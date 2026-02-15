package mdns

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/enbility/go-avahi"
	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/logging"
	"github.com/enbility/ship-go/util"
)

const shipWebsocketPath = "/ship/"

type MdnsProviderSelection uint

const (
	MdnsProviderSelectionAll            MdnsProviderSelection = iota // Automatically use avahi if available, otherwise use Go native Zeroconf, default
	MdnsProviderSelectionAvahiOnly                                   // Only use avahi
	MdnsProviderSelectionGoZeroConfOnly                              // Only us Go native zeroconf
	MdnsProviderSelectionTestSetup                                   // Skip provider creation, use pre-set provider via SetMdnsProvider
)

// ProviderFactory defines functions for creating mDNS providers
type ProviderFactory struct {
	NewAvahi    func([]int32) api.MdnsProviderInterface
	NewZeroconf func([]net.Interface) api.MdnsProviderInterface
}

// DefaultProviderFactory returns the standard provider factory
func DefaultProviderFactory() *ProviderFactory {
	return &ProviderFactory{
		NewAvahi:    func(ifaceIndexes []int32) api.MdnsProviderInterface { return NewAvahiProvider(ifaceIndexes) },
		NewZeroconf: func(ifaces []net.Interface) api.MdnsProviderInterface { return NewZeroconfProvider(ifaces) },
	}
}

type MdnsManager struct {
	// The certificates SKI
	ski string

	// The deviceBrand of the device
	deviceBrand string

	// The device model
	deviceModel string

	// The device serial number
	deviceSerial string

	// device type
	deviceType string

	// the device categories
	deviceCategories []api.DeviceCategoryType

	// the identifier to be used for mDNS and SHIP ID
	identifier string

	// the name to be used as the mDNS service name
	serviceName string

	// Network interface to use for the service
	// Optional, if not set all detected interfaces will be used
	ifaces []string

	// The port address of the websocket server
	port int

	// Wether remote devices should be automatically accepted
	autoaccept bool

	// which pairing mode
	pairingMode api.PairingMode

	isAnnounced        bool // State for _ship._tcp service
	isPairingAnnounced bool // State for _shippairing._tcp service

	// Multiple pairing instance tracking
	pairingInstances    map[string]*api.ShipPairingTXT // instanceID -> txtRecord
	pairingInstancesMux sync.RWMutex
	instanceCounter     int

	// the currently available mDNS entries with the serviceName as the key in the map
	entries map[string]*api.MdnsEntry
	// the currently available mDNS entries with the serviceName as the key in the map
	pairingEntries map[string]*api.ShipPairingTXT

	// the registered callback, only connectionsHub is using this
	report api.MdnsReportInterface

	// callback for pairing service discoveries
	pairingCallback func(*api.ShipPairingTXT) bool

	mdnsProvider api.MdnsProviderInterface

	// providerFactory creates provider instances, can be overridden for testing
	providerFactory *ProviderFactory

	shutdownOnce sync.Once

	providerSelection MdnsProviderSelection

	// Track if the manager has been started to prevent redundant operations
	isStarted bool

	// Signal handler management - DISABLED
	// Libraries should not register signal handlers - that's the application's responsibility
	// signalHandler    chan os.Signal
	// signalHandlerMux sync.Mutex
	// signalOnce       sync.Once

	mux,
	muxReport,
	muxAnnounced sync.Mutex
}

func shortenString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

// Create a new mDNS manager
//
// Parameters:
//   - ski: the SKI of certificate
//   - deviceBrand: the brand of the device (max 32 byte of UTF8)
//   - deviceModel: the model of the device (max 32 byte of UTF8)
//   - deviceType: the type of the device (max 32 byte of UTF8)
//   - deviceSerial: the serial number of the device (max 32 byte of UTF8)
//   - deviceCategories: the categories of the device
//   - shipIdentifier: the identifier to be used for SHIP ID
//   - serviceName: the name to be used as the mDNS service name
//   - port: the port address of the websocket server
//   - ifaces: the network interfaces to use for the service or empty if a all to be used
//   - providerSelection: the mDNS provider selection
func NewMDNS(
	ski, deviceBrand, deviceModel, deviceType, deviceSerial string,
	deviceCategories []api.DeviceCategoryType,
	shipIdentifier, serviceName string,
	port int,
	ifaces []string,
	providerSelection MdnsProviderSelection) *MdnsManager {
	m := &MdnsManager{
		ski:               ski,
		deviceBrand:       shortenString(deviceBrand, 32),
		deviceModel:       shortenString(deviceModel, 32),
		deviceType:        shortenString(deviceType, 32),
		deviceSerial:      shortenString(deviceSerial, 32),
		deviceCategories:  deviceCategories,
		identifier:        shipIdentifier,
		serviceName:       serviceName,
		port:              port,
		ifaces:            ifaces,
		providerSelection: providerSelection,
		entries:           make(map[string]*api.MdnsEntry),
		pairingEntries:    make(map[string]*api.ShipPairingTXT),
		pairingInstances:  make(map[string]*api.ShipPairingTXT),
		instanceCounter:   0,
		providerFactory:   DefaultProviderFactory(),
	}

	return m
}

// Return allowed interfaces for mDNS
func (m *MdnsManager) interfaces() ([]net.Interface, []int32, error) {
	var ifaces []net.Interface
	var ifaceIndexes []int32

	if len(m.ifaces) > 0 {
		ifaces = make([]net.Interface, len(m.ifaces))
		ifaceIndexes = make([]int32, len(m.ifaces))
		for i, ifaceName := range m.ifaces {
			iface, err := net.InterfaceByName(ifaceName)
			if err != nil {
				return nil, nil, err
			}
			ifaces[i] = *iface
			// conversion is safe, as the index is always positive and not higher than int32
			ifaceIndexes[i] = int32(iface.Index) // #nosec G115
		}
	}

	if len(ifaces) == 0 {
		ifaces = nil
		ifaceIndexes = []int32{avahi.InterfaceUnspec}
	}

	return ifaces, ifaceIndexes, nil
}

var _ api.MdnsInterface = (*MdnsManager)(nil)
var _ api.MdnsPairingInterface = (*MdnsManager)(nil)

func (m *MdnsManager) reportInterface() api.MdnsReportInterface {
	m.muxReport.Lock()
	defer m.muxReport.Unlock()
	return m.report
}

func (m *MdnsManager) setReportInterface(report api.MdnsReportInterface) {
	m.muxReport.Lock()
	defer m.muxReport.Unlock()
	m.report = report
}

func (m *MdnsManager) Start(pairingMode api.PairingMode, cb api.MdnsReportInterface) error {
	// Always update the callback, even on subsequent calls
	m.setReportInterface(cb)

	m.pairingMode = pairingMode

	// Check if already started to avoid duplicate initialization
	if m.isStarted {
		// on subsequent calls, just make sure mDNS announcement is active
		if err := m.AnnounceMdnsEntry(); err != nil {
			return err
		}
		return nil
	}

	ifaces, ifaceIndexes, err := m.interfaces()
	if err != nil {
		return err
	}

	// If a test provider is injected, use it instead of creating a real provider
	// Handle provider selection
	switch m.providerSelection {
	case MdnsProviderSelectionTestSetup:
		// For test setup, provider should already be set - validate and continue
		if m.mdnsProvider == nil {
			return fmt.Errorf("test provider must be set before starting with MdnsProviderSelectionTestSetup")
		}
		// Start the test provider only once
		if !m.mdnsProvider.Start(pairingMode, true, m.processMdnsEntry) {
			return fmt.Errorf("test provider failed to start")
		}
	default:
		// Validate provider factory is available for non-test selections
		if m.providerFactory == nil {
			return fmt.Errorf("mDNS provider factory not initialized for provider selection %d", m.providerSelection)
		}

		var err error
		switch m.providerSelection {
		case MdnsProviderSelectionAll:
			err = m.initializeProviderWithFallback(ifaceIndexes, ifaces)
		case MdnsProviderSelectionAvahiOnly:
			err = m.initializeAvahiProvider(ifaceIndexes, true)
		case MdnsProviderSelectionGoZeroConfOnly:
			err = m.initializeZeroconfProvider(ifaces, true)
		default:
			return fmt.Errorf("invalid mDNS provider selection: %d", m.providerSelection)
		}

		if err != nil {
			return err
		}
	}

	// Validate that a provider was successfully set
	if m.mdnsProvider == nil {
		return fmt.Errorf("failed to initialize any mDNS provider (selection: %d)", m.providerSelection)
	}

	// Signal handler removed - libraries should not register signal handlers
	// The application using this library is responsible for calling Shutdown()
	// when appropriate (e.g., on SIGINT/SIGTERM)

	m.isStarted = true

	// on startup always start mDNS announcement
	if err := m.AnnounceMdnsEntry(); err != nil {
		return err
	}

	return nil
}

// Shutdown all of mDNS
func (m *MdnsManager) Shutdown() {
	m.shutdownOnce.Do(func() {
		logging.Log().Debug("mdns: shutting down mDNS manager")

		// Safely unannounce the service
		func() {
			defer func() {
				if r := recover(); r != nil {
					logging.Log().Debug("mdns: panic during unannounce:", r)
				}
			}()
			m.UnannounceMdnsEntry()
		}()

		// Safely shutdown provider
		if m.mdnsProvider != nil {
			func() {
				defer func() {
					if r := recover(); r != nil {
						logging.Log().Debug("mdns: panic during provider shutdown:", r)
					}
				}()
				m.mdnsProvider.Shutdown()
			}()
			m.mdnsProvider = nil
		}

		// Signal handler cleanup removed - no longer needed

		// Clear the report interface to prevent goroutines from accessing it after shutdown
		m.setReportInterface(nil)

		// Clear mDNS entries to prevent contamination between runs
		m.mux.Lock()
		m.entries = make(map[string]*api.MdnsEntry)
		m.pairingEntries = make(map[string]*api.ShipPairingTXT)
		// Clear pairing callback to prevent contamination between runs
		m.pairingCallback = nil
		m.mux.Unlock()

		// Reset the start state to allow restarting after shutdown
		m.isStarted = false
		// Note: We cannot reset sync.Once, but the isStarted flag handles restart logic
	})
}

// Announces the service to the network via mDNS
// A CEM service should always invoke this on startup
// Any other service should only invoke this whenever it is not connected to a CEM service
func (m *MdnsManager) AnnounceMdnsEntry() error {
	if m.mdnsProvider == nil {
		return fmt.Errorf("cannot announce mDNS entry: no provider available (selection: %d)", m.providerSelection)
	}

	// no need to announce if it is already, can happen when call via hub
	if m.isServiceAnnounced() {
		return nil
	}

	// Validate required fields
	if len(m.identifier) == 0 {
		return fmt.Errorf("cannot announce mDNS entry: service identifier is empty (SKI: %s)", m.ski)
	}
	if len(m.ski) == 0 {
		return fmt.Errorf("cannot announce mDNS entry: SKI is empty (identifier: %s)", m.identifier)
	}
	if len(m.serviceName) == 0 {
		return fmt.Errorf("cannot announce mDNS entry: service name is empty (SKI: %s, identifier: %s)", m.ski, m.identifier)
	}
	if m.port <= 0 || m.port > 65535 {
		return fmt.Errorf("cannot announce mDNS entry: invalid port %d", m.port)
	}

	serviceIdentifier := m.identifier

	txt := []string{ // SHIP 7.3.2
		"txtvers=1",
		"path=" + shipWebsocketPath,
		"id=" + serviceIdentifier,
		"ski=" + m.ski,
		"brand=" + m.deviceBrand,
		"model=" + m.deviceModel,
		"type=" + m.deviceType,
		"register=" + fmt.Sprintf("%v", m.autoaccept),
	}

	// SHIP Requirements for Installation Process V1.0.0
	if len(m.deviceSerial) > 0 {
		txt = append(txt, "serial="+m.deviceSerial)
	}

	categories := m.deviceCategoriesString(m.deviceCategories)
	if len(categories) > 0 {
		txt = append(txt, "cat="+categories)
	}

	logging.Log().Debug("mdns: announce")

	serviceName := m.serviceName

	_, err := m.mdnsProvider.AnnounceService(shipZeroConfServiceType, serviceName, m.port, txt)
	if err != nil {
		logging.Log().Debug("mdns: failure announcing service", err)
		return err
	}

	m.setIsServiceAnnounce(true)

	return nil
}

// Stop the mDNS announcement on the network
func (m *MdnsManager) UnannounceMdnsEntry() {
	if !m.isServiceAnnounced() {
		return
	}

	if m.mdnsProvider == nil {
		return
	}

	logging.Log().Debug("mdns: stop announcement")
	_ = m.mdnsProvider.UnannounceService(shipZeroConfServiceType)

	m.setIsServiceAnnounce(false)
}

func (m *MdnsManager) isServiceAnnounced() bool {
	m.muxAnnounced.Lock()
	defer m.muxAnnounced.Unlock()

	return m.isAnnounced
}

func (m *MdnsManager) setIsServiceAnnounce(value bool) {
	m.muxAnnounced.Lock()
	defer m.muxAnnounced.Unlock()

	m.isAnnounced = value
}

func (m *MdnsManager) SetAutoAccept(accept bool) {
	m.autoaccept = accept

	// if announcement is off, don't enforce a new announcement
	if !m.isServiceAnnounced() {
		return
	}

	m.UnannounceMdnsEntry()

	// Update the announcement as autoaccept changed
	err := m.AnnounceMdnsEntry()

	if err == nil {
		return
	}

	logging.Log().Debug("mdns: changing mdns entry failed", err)

	m.setIsServiceAnnounce(false)
}

// SetMdnsProvider sets the mDNS provider for the manager
// mainly used for testing
func (m *MdnsManager) SetMdnsProvider(provider api.MdnsProviderInterface) {
	if provider == nil {
		logging.Log().Debug("mdns: cannot set nil provider")
		return
	}

	m.mdnsProvider = provider
}

// SetProviderFactory injects a custom provider factory for testing purposes
func (m *MdnsManager) SetProviderFactory(factory *ProviderFactory) {
	m.providerFactory = factory
}

// Device metadata getters for QR code generation
func (m *MdnsManager) DeviceBrand() string {
	return m.deviceBrand
}

func (m *MdnsManager) DeviceModel() string {
	return m.deviceModel
}

func (m *MdnsManager) DeviceSerial() string {
	return m.deviceSerial
}

func (m *MdnsManager) DeviceType() string {
	return m.deviceType
}

func (m *MdnsManager) DeviceCategories() []api.DeviceCategoryType {
	return m.deviceCategories
}

// deviceCategoriesString returns the device categories as a string, with categories separated by commas
// This is used internally for mDNS announcements
func (m *MdnsManager) deviceCategoriesString(categories []api.DeviceCategoryType) string {
	var cat string
	for _, category := range categories {
		if len(cat) > 0 {
			cat += ","
		}
		cat += fmt.Sprintf("%d", category)
	}
	return cat
}

/* MdnsEntry helper */
func (m *MdnsManager) mdnsEntries() map[string]*api.MdnsEntry {
	m.mux.Lock()
	defer m.mux.Unlock()

	return m.entries
}

// copyMdnsEntries returns a copy of all mDNS entries
// Internal: returns entries keyed by serviceName for integrity
func (m *MdnsManager) copyMdnsEntries() map[string]*api.MdnsEntry {
	m.mux.Lock()
	defer m.mux.Unlock()

	mdnsEntries := make(map[string]*api.MdnsEntry)
	for k, v := range m.entries {
		newEntry := &api.MdnsEntry{}
		util.DeepCopy(v, newEntry)
		mdnsEntries[k] = newEntry
	}

	return mdnsEntries
}

func (m *MdnsManager) mdnsEntry(serviceName string) (*api.MdnsEntry, bool) {
	m.mux.Lock()
	defer m.mux.Unlock()

	entry, ok := m.entries[serviceName]
	return entry, ok
}

func (m *MdnsManager) setMdnsEntry(serviceName string, entry *api.MdnsEntry) {
	m.mux.Lock()
	defer m.mux.Unlock()

	m.entries[serviceName] = entry
}

func (m *MdnsManager) removeMdnsEntry(serviceName string) {
	m.mux.Lock()
	defer m.mux.Unlock()

	delete(m.entries, serviceName)
}

/* MdnsPairingEntry helper */

func (m *MdnsManager) pairingMdnsEntry(serviceName string) (*api.ShipPairingTXT, bool) {
	m.mux.Lock()
	defer m.mux.Unlock()

	entry, ok := m.pairingEntries[serviceName]
	return entry, ok
}

func (m *MdnsManager) setPairingMdnsEntry(serviceName string, entry *api.ShipPairingTXT) {
	m.mux.Lock()
	defer m.mux.Unlock()

	m.pairingEntries[serviceName] = entry
}

func (m *MdnsManager) removePairingMdnsEntry(serviceName string) {
	m.mux.Lock()
	defer m.mux.Unlock()

	delete(m.pairingEntries, serviceName)
}

// RegisterPairingCallback registers a callback for pairing service discoveries
func (m *MdnsManager) RegisterPairingCallback(callback func(*api.ShipPairingTXT) bool) {
	m.mux.Lock()
	defer m.mux.Unlock()
	m.pairingCallback = callback
}

// UnregisterPairingCallback removes the registered pairing callback
func (m *MdnsManager) UnregisterPairingCallback() {
	m.mux.Lock()
	defer m.mux.Unlock()
	m.pairingCallback = nil
}

/* Generic mDNS */

// detectServiceType determines the service type based on the serviceType parameter
func (m *MdnsManager) detectServiceType(serviceType string) ServiceType {
	// Check the serviceType parameter with exact matching
	switch serviceType {
	case shipPairingZeroConfServiceType: // "_shippairing._tcp"
		return ServiceTypeShipPairing
	case shipZeroConfServiceType: // "_ship._tcp"
		return ServiceTypeShip
	default:
		return ServiceTypeUnknown
	}
}

// processMdnsEntry is the main entry point from providers - routes to appropriate handler
func (m *MdnsManager) processMdnsEntry(elements map[string]string, serviceName, host, serviceType string, addresses []net.IP, port int, remove bool) {
	detectedType := m.detectServiceType(serviceType)

	switch detectedType {
	case ServiceTypeShip:
		m.processShipMdnsEntry(elements, serviceName, host, addresses, port, remove)
	case ServiceTypeShipPairing:
		m.processShipPairingMdnsEntry(elements, serviceName, remove)
	default:
		// Unknown service types are ignored
		logging.Log().Debug("mdns: ignoring unknown service type", serviceType, serviceName)
	}
}

// processShipPairingMdnsEntry processes a _shippairing._tcp mDNS entry
func (m *MdnsManager) processShipPairingMdnsEntry(elements map[string]string, serviceName string, remove bool) {
	// check for mandatory text elements
	mapItems := []string{"txtvers", "parType", "forId", "forPar", "trustId", "trustPar", "trustCurve", "type", "trustNonce", "alg", "digest"}
	for _, item := range mapItems {
		if _, ok := elements[item]; !ok {
			logging.Log().Debug("mdns: pairing - missing mandatory element", item, serviceName)
			return
		}
	}

	txtvers := elements["txtvers"]
	// Validate txtvers value (must be "1" per SHIP spec)
	if txtvers != "1" {
		logging.Log().Debug("mdns: pairing - invalid txtvers value", txtvers, serviceName)
		return
	}

	parType := elements["parType"]
	forId := elements["forId"]
	forPar := elements["forPar"]
	trustId := elements["trustId"]
	trustPar := elements["trustPar"]
	trustCurve := elements["trustCurve"]
	elType := elements["type"]
	trustNonce := elements["trustNonce"]
	alg := elements["alg"]
	digest := elements["digest"]

	logString := fmt.Sprintf(" - forId: %s, forPar: %s, trustId: %s, trustPar: %s, trustCurve: %s, type: %s, trustNonce: %s, alg: %s, digest: %s",
		forId, forPar, trustId, trustPar, trustCurve, elType, trustNonce, alg, digest)

	_, exists := m.pairingMdnsEntry(serviceName)

	if remove && exists {
		// remove
		// there will be a remove for each address with avahi, but we'll delete it right away
		m.removePairingMdnsEntry(serviceName)

		logging.Log().Debug("mdns: remove", logString)
		return
	}

	if remove || exists {
		return
	}

	// new
	newEntry := &api.ShipPairingTXT{
		TxtVers:    txtvers,
		ParType:    parType,
		ForId:      forId,
		ForPar:     forPar,
		TrustId:    trustId,
		TrustPar:   trustPar,
		TrustCurve: trustCurve,
		Type:       elType,
		TrustNonce: trustNonce,
		Alg:        alg,
		Digest:     digest,
	}

	m.setPairingMdnsEntry(serviceName, newEntry)

	logging.Log().Debug("mdns: new", logString)

	m.mux.Lock()
	callback := m.pairingCallback
	m.mux.Unlock()

	if callback == nil {
		logging.Log().Debug("mdns: pairing entry received but no callback registered")
		return
	}

	// Invoke callback with pairing data
	// Return value: true = continue searching, false = stop searching (pairing accepted)
	continueSearching := callback(newEntry)
	if !continueSearching {
		logging.Log().Debug("mdns: not searching shippairing")
	}
}

// processShipMdnsEntry processes a standard _ship._tcp mDNS entry (original processMdnsEntry logic)
func (m *MdnsManager) processShipMdnsEntry(elements map[string]string, serviceName, host string, addresses []net.IP, port int, remove bool) {
	// check for mandatory text elements
	mapItems := []string{"txtvers", "id", "path", "ski", "register"}
	for _, item := range mapItems {
		if _, ok := elements[item]; !ok {
			logging.Log().Debug("mdns: txt - missing mandatory element", item)
			return
		}
	}

	txtvers := elements["txtvers"]
	// value of mandatory txtvers has to be 1 or the response be ignored: SHIP 7.3.2
	if txtvers != "1" {
		logging.Log().Debug("mdns: txt - unknown txtvers", txtvers)
		return
	}

	identifier := elements["id"]
	path := elements["path"]
	ski := elements["ski"]

	// ignore own service
	if ski == m.ski {
		return
	}

	trueValue := "true"
	falseValue := "false"

	register := elements["register"]
	// register has to be a boolean
	if register != trueValue && register != falseValue {
		logging.Log().Debug("mdns: txt - register value is not a text boolean", register)
		return
	}

	// remove IPv6 local link addresses
	var newAddresses []net.IP
	for _, address := range addresses {
		if address.To4() == nil && address.IsLinkLocalUnicast() {
			continue
		}
		newAddresses = append(newAddresses, address)
	}
	addresses = newAddresses

	var deviceType, model, brand, serial string

	if value, ok := elements["brand"]; ok {
		brand = value
	}
	if value, ok := elements["type"]; ok {
		deviceType = value
	}
	if value, ok := elements["model"]; ok {
		model = value
	}
	if value, ok := elements["serial"]; ok {
		serial = value
	}

	var categories []api.DeviceCategoryType
	var categoriesStr string
	if value, ok := elements["cat"]; ok {
		categoriesStr = value
		// Device categories according to SHIP Requirements for Installation Process V1.0.0
		for _, item := range strings.Split(value, ",") {
			category, err := strconv.ParseUint(item, 10, 32)
			if err != nil {
				logging.Log().Debug("mdns: txt - invalid category", item)
				continue
			}
			categories = append(categories, api.DeviceCategoryType(category))
		}
	}

	updated := false

	entry, exists := m.mdnsEntry(serviceName)

	if remove && exists {
		updated = true
		// remove
		// there will be a remove for each address with avahi, but we'll delete it right away
		m.removeMdnsEntry(serviceName)

		logging.Log().Debug("mdns: remove - ski:", ski, "name:", serviceName, "brand:", brand, "model:", model, "typ:", deviceType, "serial:", serial, "categories:", categoriesStr, "identifier:", identifier, "register:", register, "host:", host, "port:", port, "addresses:", addresses)
	} else if exists {
		// Update existing entry with new metadata and merge addresses

		// Update all metadata fields (they may have changed)
		if entry.Brand != brand || entry.Type != deviceType || entry.Model != model ||
			entry.Serial != serial || entry.Identifier != identifier ||
			entry.Path != path || entry.Register != (register == trueValue) ||
			entry.Host != host || entry.Port != port ||
			len(entry.Categories) != len(categories) {
			updated = true
		}

		// Check if categories changed
		if !updated && len(entry.Categories) == len(categories) {
			for i, cat := range entry.Categories {
				if i >= len(categories) || cat != categories[i] {
					updated = true
					break
				}
			}
		}

		// Update metadata
		entry.Identifier = identifier
		entry.Path = path
		entry.Register = register == trueValue
		entry.Brand = brand
		entry.Type = deviceType
		entry.Model = model
		entry.Serial = serial
		entry.Categories = categories
		entry.Host = host
		entry.Port = port

		// Merge addresses (avahi sends an item for each network address)
		for _, address := range addresses {
			// only add if it is not added yet
			isNewElement := true

			for _, item := range entry.Addresses {
				if item.String() == address.String() {
					isNewElement = false
					break
				}
			}

			if isNewElement {
				entry.Addresses = append(entry.Addresses, address)
				updated = true
			}
		}

		if updated {
			m.setMdnsEntry(serviceName, entry)

			logging.Log().Debug("mdns: update - ski:", ski, "name:", serviceName, "brand:", brand, "model:", model, "typ:", deviceType, "serial:", serial, "categories:", categoriesStr, "identifier:", identifier, "register:", register, "host:", host, "port:", port, "addresses:", addresses)
		}
	} else if !exists && !remove {
		updated = true
		// new
		newEntry := &api.MdnsEntry{
			Name:       serviceName,
			Ski:        ski,
			Identifier: identifier,
			Path:       path,
			Register:   register == "true",
			Brand:      brand,
			Type:       deviceType,
			Model:      model,
			Serial:     serial,
			Categories: categories,
			Host:       host,
			Port:       port,
			Addresses:  addresses,
		}
		m.setMdnsEntry(serviceName, newEntry)

		logging.Log().Debug("mdns: new - ski:", ski, "name:", serviceName, "brand:", brand, "model:", model, "typ:", deviceType, "serial:", serial, "categories:", categoriesStr, "identifier:", identifier, "register:", register, "host:", host, "port:", port, "addresses:", addresses)
	}

	reportIntf := m.reportInterface()
	if reportIntf == nil || !updated {
		return
	}

	// Report entries keyed by SKI for Hub compatibility
	entries := m.copyMdnsEntries()
	go reportIntf.ReportMdnsEntries(entries, true)
}

func (m *MdnsManager) RequestMdnsEntries() {
	reportIntf := m.reportInterface()
	if reportIntf == nil {
		return
	}

	// Report entries keyed by SKI for Hub compatibility
	entries := m.copyMdnsEntries()
	go reportIntf.ReportMdnsEntries(entries, false)
}

// initializeProviderWithFallback attempts to initialize Avahi first, then falls back to Zeroconf
func (m *MdnsManager) initializeProviderWithFallback(ifaceIndexes []int32, ifaces []net.Interface) error {
	// Try Avahi first
	if err := m.initializeAvahiProvider(ifaceIndexes, false); err == nil {
		return nil
	} else {
		logging.Log().Debug("mdns: Avahi provider failed, attempting Zeroconf fallback:", err)
	}

	// Fallback to Zeroconf
	if err := m.initializeZeroconfProvider(ifaces, false); err == nil {
		return nil
	} else {
		logging.Log().Debug("mdns: Zeroconf provider also failed:", err)
	}

	return fmt.Errorf("no mDNS provider available - both Avahi and Zeroconf failed to initialize (interfaces: %d)", len(ifaces))
}

// initializeAvahiProvider creates and starts an Avahi provider
func (m *MdnsManager) initializeAvahiProvider(ifaceIndexes []int32, autoReconnect bool) error {
	if m.providerFactory.NewAvahi == nil {
		return fmt.Errorf("avahi provider factory function not available (interfaces: %d)", len(ifaceIndexes))
	}

	provider := m.providerFactory.NewAvahi(ifaceIndexes)
	if provider == nil {
		return fmt.Errorf("failed to create Avahi provider instance (interfaces: %d)", len(ifaceIndexes))
	}

	if !provider.Start(m.pairingMode, autoReconnect, m.processMdnsEntry) {
		// Clean up failed provider
		provider.Shutdown()
		return fmt.Errorf("avahi provider failed to start (interfaces: %d, autoReconnect: %v)", len(ifaceIndexes), autoReconnect)
	}

	m.mdnsProvider = provider
	return nil
}

// initializeZeroconfProvider creates and starts a Zeroconf provider
func (m *MdnsManager) initializeZeroconfProvider(ifaces []net.Interface, autoReconnect bool) error {
	if m.providerFactory.NewZeroconf == nil {
		return fmt.Errorf("zeroconf provider factory function not available (interfaces: %d)", len(ifaces))
	}

	provider := m.providerFactory.NewZeroconf(ifaces)
	if provider == nil {
		return fmt.Errorf("failed to create Zeroconf provider instance (interfaces: %d)", len(ifaces))
	}

	if !provider.Start(m.pairingMode, autoReconnect, m.processMdnsEntry) {
		// Clean up failed provider
		provider.Shutdown()
		return fmt.Errorf("zeroconf provider failed to start (interfaces: %d, autoReconnect: %v)", len(ifaces), autoReconnect)
	}

	m.mdnsProvider = provider
	return nil
}

/* SHIP Pairing Service Extension - TDD Stubs */

// AnnouncePairingService announces _shippairing._tcp service (implements MdnsPairingInterface)
func (m *MdnsManager) AnnouncePairingService(txtRecord *api.ShipPairingTXT) (string, error) {
	// Get active provider (testProvider takes precedence for testing)
	provider := m.mdnsProvider

	// Critical validation
	if provider == nil {
		logging.Log().Debug("mdns: AnnouncePairingService - no provider available")
		return "", fmt.Errorf("cannot announce pairing service: no provider available")
	}
	if txtRecord == nil {
		logging.Log().Debug("mdns: AnnouncePairingService - txtRecord is nil")
		return "", fmt.Errorf("txtRecord cannot be nil")
	}
	if err := txtRecord.Validate(); err != nil {
		logging.Log().Debug("mdns: AnnouncePairingService - invalid TXT record:", err)
		return "", fmt.Errorf("invalid TXT record: %w", err)
	}

	// Generate unique service name
	m.pairingInstancesMux.Lock()
	m.instanceCounter++
	// Generate service name following SHIP-compliant naming
	// All instances use #N suffix, starting from #1 (per interface test requirements)
	serviceName := m.serviceName + "-pairing#" + strconv.Itoa(m.instanceCounter)
	m.pairingInstancesMux.Unlock()

	// Convert ShipPairingTXT to string array
	txtArray := m.convertPairingTXTToArray(txtRecord)

	// Use provider's enhanced AnnounceService method
	// Use the same port as the SHIP server (m.port) since pairing connects to the same WebSocket endpoint
	providerInstanceID, err := m.mdnsProvider.AnnounceService(shipPairingZeroConfServiceType, serviceName, m.port, txtArray)
	if err != nil {
		logging.Log().Debug("mdns: AnnouncePairingService - provider.AnnounceService failed:", err)
		return "", fmt.Errorf("failed to announce pairing service: %w", err)
	}

	// Store instance using provider's actual instance ID for proper cleanup
	m.pairingInstancesMux.Lock()
	m.pairingInstances[providerInstanceID] = txtRecord
	m.pairingInstancesMux.Unlock()

	// Update state tracking after successful announcement
	m.setIsPairingServiceAnnounced(true)

	// Return provider's instance ID so it can be used for cleanup
	return providerInstanceID, nil
}

// UnannouncePairingService removes _shippairing._tcp announcement (implements MdnsPairingInterface)
func (m *MdnsManager) UnannouncePairingService(providerInstanceID string) error {
	// Get active provider (testProvider takes precedence for testing)
	provider := m.mdnsProvider
	if provider == nil {
		return api.ErrPairingNotActive
	}

	// Validate providerInstanceID and remove from instances map
	m.pairingInstancesMux.Lock()
	_, exists := m.pairingInstances[providerInstanceID]
	if !exists {
		m.pairingInstancesMux.Unlock()
		return fmt.Errorf("provider instance ID %s not found", providerInstanceID)
	}
	delete(m.pairingInstances, providerInstanceID)
	m.pairingInstancesMux.Unlock()

	// Use provider's enhanced UnannounceService method with provider's own instance ID
	if err := provider.UnannounceService(providerInstanceID); err != nil {
		return err
	}

	// Update state tracking - only set to false if no instances remain
	m.pairingInstancesMux.RLock()
	hasInstances := len(m.pairingInstances) > 0
	m.pairingInstancesMux.RUnlock()

	if !hasInstances {
		m.setIsPairingServiceAnnounced(false)
	}
	return nil
}

// SearchPairingServices searches for _shippairing._tcp services (implements MdnsPairingInterface)
func (m *MdnsManager) SearchPairingServices(callback func(*api.ShipPairingTXT) bool) error {
	// Validate callback
	if callback == nil {
		return fmt.Errorf("callback cannot be nil")
	}

	// Ensure the mDNS manager is started
	// If not started, we need to start it to begin browsing
	if !m.isStarted {
		// SearchPairingServices requires a provider to be available
		// The caller should have called Start() first
		return fmt.Errorf("mDNS manager not started: call Start() before SearchPairingServices")
	}

	// Validate provider is available
	if m.mdnsProvider == nil {
		return api.ErrMDNSSearchFailed
	}

	// Register the pairing callback
	// This will be invoked when processShipPairingMdnsEntry is called via the routing mechanism
	m.RegisterPairingCallback(callback)

	// The provider infrastructure (both Avahi and Zeroconf) has been enhanced to browse
	// for both _ship._tcp and _shippairing._tcp services simultaneously.
	// When a _shippairing._tcp service is discovered, it will be routed through:
	// processMdnsEntry → detectServiceType → processShipPairingMdnsEntry → callback
	//
	// Note: The providers must be explicitly configured to browse for _shippairing._tcp
	// This is done in avahi.go and zeroconf.go with dual browser instances

	logging.Log().Debug("mdns: pairing service search activated")
	return nil
}

// RequestPairingEntries triggers an immediate discovery scan for SHIP Pairing Services (implements MdnsPairingInterface)
func (m *MdnsManager) RequestPairingEntries() (map[string]*api.ShipPairingTXT, error) {
	// Ensure the mDNS manager is started
	if !m.isStarted {
		return nil, fmt.Errorf("mDNS manager not started: call Start() before RequestPairingEntries")
	}

	// Return a copy of current pairing entries to avoid race conditions
	m.mux.Lock()
	defer m.mux.Unlock()

	result := make(map[string]*api.ShipPairingTXT)
	for name, entry := range m.pairingEntries {
		// Create a copy of the ShipPairingTXT to avoid external modification
		entryCopy := *entry
		result[name] = &entryCopy
	}

	logging.Log().Debug("mdns: returning current pairing entries", "count", len(result))
	return result, nil
}

// IsPairingServiceAnnounced checks if pairing service is currently announced (implements MdnsPairingInterface)
func (m *MdnsManager) IsPairingServiceAnnounced() bool {
	m.pairingInstancesMux.RLock()
	hasInstances := len(m.pairingInstances) > 0
	m.pairingInstancesMux.RUnlock()
	return hasInstances
}

// setIsPairingServiceAnnounced updates pairing service announcement state (internal helper)
func (m *MdnsManager) setIsPairingServiceAnnounced(announced bool) {
	m.muxAnnounced.Lock()
	defer m.muxAnnounced.Unlock()
	m.isPairingAnnounced = announced
}

// convertPairingTXTToArray converts ShipPairingTXT to string array for provider (fixed per sub-agent review)
func (m *MdnsManager) convertPairingTXTToArray(txtRecord *api.ShipPairingTXT) []string {
	// Fixed ordering per SHIP spec section 7.4
	fieldOrder := []string{"txtvers", "parType", "forId", "forPar", "trustId",
		"trustPar", "trustCurve", "type", "trustNonce", "alg", "digest"}

	txtMap := txtRecord.ToMap()
	var txtArray []string // Dynamic array instead of fixed size

	// Only include non-empty values (fixes mDNS compatibility issue)
	for _, field := range fieldOrder {
		if value, exists := txtMap[field]; exists && value != "" {
			txtArray = append(txtArray, field+"="+value)
		}
	}

	return txtArray
}

// SimulatePairingDiscovery simulates the discovery of a pairing service for testing
// This method is intended for integration tests to simulate mDNS discovery
func (m *MdnsManager) SimulatePairingDiscovery(txtRecord *api.ShipPairingTXT) {
	if txtRecord == nil {
		return
	}

	// Create elements map from TXT record
	elements := map[string]string{
		"txtvers":    txtRecord.TxtVers,
		"parType":    txtRecord.ParType,
		"forId":      txtRecord.ForId,
		"forPar":     txtRecord.ForPar,
		"trustId":    txtRecord.TrustId,
		"trustPar":   txtRecord.TrustPar,
		"trustCurve": txtRecord.TrustCurve,
		"type":       txtRecord.Type,
		"trustNonce": txtRecord.TrustNonce,
		"alg":        txtRecord.Alg,
		"digest":     txtRecord.Digest,
	}

	// Process the simulated discovery
	m.processShipPairingMdnsEntry(elements, "servicename", false)
}
