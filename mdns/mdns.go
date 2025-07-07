package mdns

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"

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

	isAnnounced bool

	// the currently available mDNS entries with the SKI as the key in the map
	entries map[string]*api.MdnsEntry

	// the registered callback, only connectionsHub is using this
	report api.MdnsReportInterface

	mdnsProvider api.MdnsProviderInterface

	// testProvider is used to inject mock providers for testing
	testProvider api.MdnsProviderInterface

	// providerFactory creates provider instances, can be overridden for testing
	providerFactory *ProviderFactory

	shutdownOnce sync.Once

	providerSelection MdnsProviderSelection

	// Signal handler management
	signalHandler    chan os.Signal
	signalHandlerMux sync.Mutex
	signalOnce       sync.Once

	mux,
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

func (m *MdnsManager) Start(cb api.MdnsReportInterface) error {
	ifaces, ifaceIndexes, err := m.interfaces()
	if err != nil {
		return err
	}

	// assign the cb before mDNS is initialised, so that we don't miss any found services
	m.report = cb

	// If a test provider is injected, use it instead of creating a real provider
	if m.testProvider != nil {
		m.mdnsProvider = m.testProvider
	} else {
		// Validate provider factory is available
		if m.providerFactory == nil {
			return errors.New("mDNS provider factory not initialized")
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
		return errors.New("failed to initialize any mDNS provider")
	}

	// on startup always start mDNS announcement
	if err := m.AnnounceMdnsEntry(); err != nil {
		return err
	}

	// Set up signal handler only once
	m.signalOnce.Do(func() {
		m.signalHandlerMux.Lock()
		m.signalHandler = make(chan os.Signal, 1)
		signal.Notify(m.signalHandler, os.Interrupt, syscall.SIGTERM)
		signalChan := m.signalHandler // capture for goroutine
		m.signalHandlerMux.Unlock()

		go func() {
			<-signalChan // wait for signal
			m.Shutdown()
		}()
	})

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

		// Clean up signal handler
		m.signalHandlerMux.Lock()
		if m.signalHandler != nil {
			signal.Stop(m.signalHandler)
			close(m.signalHandler)
			m.signalHandler = nil
		}
		m.signalHandlerMux.Unlock()
	})
}

// Announces the service to the network via mDNS
// A CEM service should always invoke this on startup
// Any other service should only invoke this whenever it is not connected to a CEM service
func (m *MdnsManager) AnnounceMdnsEntry() error {
	if m.mdnsProvider == nil {
		return errors.New("cannot announce mDNS entry: no provider available")
	}

	// Validate required fields
	if len(m.identifier) == 0 {
		return errors.New("cannot announce mDNS entry: service identifier is empty")
	}
	if len(m.ski) == 0 {
		return errors.New("cannot announce mDNS entry: SKI is empty")
	}
	if len(m.serviceName) == 0 {
		return errors.New("cannot announce mDNS entry: service name is empty")
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

	if err := m.mdnsProvider.Announce(serviceName, m.port, txt); err != nil {
		logging.Log().Debug("mdns: failure announcing service", err)
		return err
	}

	m.mux.Lock()
	defer m.mux.Unlock()

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
	m.mdnsProvider.Unannounce()

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

	// Update the announcement as autoaccept changed
	if err := m.AnnounceMdnsEntry(); err != nil {
		logging.Log().Debug("mdns: changing mdns entry failed", err)
	}
}

// SetTestProvider injects a mock provider for testing purposes
func (m *MdnsManager) SetTestProvider(provider api.MdnsProviderInterface) {
	m.testProvider = provider
}

// SetProviderFactory injects a custom provider factory for testing purposes
func (m *MdnsManager) SetProviderFactory(factory *ProviderFactory) {
	m.providerFactory = factory
}

// Returns a safe to use key value pair for the QR code text in the proper format
// according to SHIP Requirements for Installation Process V1.0.0
func (m *MdnsManager) safeQRCodeKeyValue(key, value string) string {
	if len(value) > 0 {
		// make sure the value contains no ; chars
		value = strings.ReplaceAll(value, ";", "")

		// make sure the keys are all uppercase
		key = strings.ToUpper(key)
		return fmt.Sprintf("%s:%s;", key, value)
	}

	return ""
}

// Returns the device categories as a string, with categories separated by commas
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

// Returns the QR code text for the service
// as defined in SHIP Requirements for Installation Process V1.0.0
func (m *MdnsManager) QRCodeText() string {
	var optionals string

	if len(m.deviceBrand) > 0 {
		optionals += m.safeQRCodeKeyValue("BRAND", m.deviceBrand)
	}

	if len(m.deviceType) > 0 {
		optionals += m.safeQRCodeKeyValue("TYPE", m.deviceType)
	}

	if len(m.deviceModel) > 0 {
		optionals += m.safeQRCodeKeyValue("MODEL", m.deviceModel)
	}

	if len(m.deviceSerial) > 0 {
		optionals += m.safeQRCodeKeyValue("SERIAL", m.deviceSerial)
	}

	if m.deviceCategories != nil {
		optionals += m.safeQRCodeKeyValue("CAT", m.deviceCategoriesString(m.deviceCategories))
	}

	qrcode := fmt.Sprintf("SHIP;SKI:%s;ID:%s;%sENDSHIP;", m.ski, m.identifier, optionals)

	return qrcode
}

func (m *MdnsManager) mdnsEntries() map[string]*api.MdnsEntry {
	m.mux.Lock()
	defer m.mux.Unlock()

	return m.entries
}

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

func (m *MdnsManager) mdnsEntry(ski string) (*api.MdnsEntry, bool) {
	m.mux.Lock()
	defer m.mux.Unlock()

	entry, ok := m.entries[ski]
	return entry, ok
}

func (m *MdnsManager) setMdnsEntry(ski string, entry *api.MdnsEntry) {
	m.mux.Lock()
	defer m.mux.Unlock()

	m.entries[ski] = entry
}

func (m *MdnsManager) removeMdnsEntry(ski string) {
	m.mux.Lock()
	defer m.mux.Unlock()

	delete(m.entries, ski)
}

// process an mDNS entry and manage mDNS entries map
func (m *MdnsManager) processMdnsEntry(elements map[string]string, name, host string, addresses []net.IP, port int, remove bool) {
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

	register := elements["register"]
	// register has to be a boolean
	if register != "true" && register != "false" {
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

	entry, exists := m.mdnsEntry(ski)

	if remove && exists {
		updated = true
		// remove
		// there will be a remove for each address with avahi, but we'll delete it right away
		m.removeMdnsEntry(ski)

		logging.Log().Debug("mdns: remove - ski:", ski, "name:", name, "brand:", brand, "model:", model, "typ:", deviceType, "serial:", serial, "categories:", categoriesStr, "identifier:", identifier, "register:", register, "host:", host, "port:", port, "addresses:", addresses)
	} else if exists {
		// avahi sends an item for each network address, merge them

		// we assume only network addresses are added
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
			m.setMdnsEntry(ski, entry)

			logging.Log().Debug("mdns: update - ski:", ski, "name:", name, "brand:", brand, "model:", model, "typ:", deviceType, "serial:", serial, "categories:", categoriesStr, "identifier:", identifier, "register:", register, "host:", host, "port:", port, "addresses:", addresses)
		}
	} else if !exists && !remove {
		updated = true
		// new
		newEntry := &api.MdnsEntry{
			Name:       name,
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
		m.setMdnsEntry(ski, newEntry)

		logging.Log().Debug("mdns: new - ski:", ski, "name:", name, "brand:", brand, "model:", model, "typ:", deviceType, "serial:", serial, "categories:", categoriesStr, "identifier:", identifier, "register:", register, "host:", host, "port:", port, "addresses:", addresses)
	}

	if m.report == nil || !updated {
		return
	}

	entries := m.copyMdnsEntries()
	go m.report.ReportMdnsEntries(entries, true)
}

func (m *MdnsManager) RequestMdnsEntries() {
	if m.report == nil {
		return
	}

	entries := m.copyMdnsEntries()
	go m.report.ReportMdnsEntries(entries, false)
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

	return errors.New("no mDNS provider available - both Avahi and Zeroconf failed to initialize")
}

// initializeAvahiProvider creates and starts an Avahi provider
func (m *MdnsManager) initializeAvahiProvider(ifaceIndexes []int32, autoReconnect bool) error {
	if m.providerFactory.NewAvahi == nil {
		return errors.New("Avahi provider factory function not available")
	}

	provider := m.providerFactory.NewAvahi(ifaceIndexes)
	if provider == nil {
		return errors.New("failed to create Avahi provider instance")
	}

	if !provider.Start(autoReconnect, m.processMdnsEntry) {
		// Clean up failed provider
		provider.Shutdown()
		return errors.New("Avahi provider failed to start")
	}

	m.mdnsProvider = provider
	return nil
}

// initializeZeroconfProvider creates and starts a Zeroconf provider  
func (m *MdnsManager) initializeZeroconfProvider(ifaces []net.Interface, autoReconnect bool) error {
	if m.providerFactory.NewZeroconf == nil {
		return errors.New("Zeroconf provider factory function not available")
	}

	provider := m.providerFactory.NewZeroconf(ifaces)
	if provider == nil {
		return errors.New("failed to create Zeroconf provider instance")
	}

	if !provider.Start(autoReconnect, m.processMdnsEntry) {
		// Clean up failed provider
		provider.Shutdown()
		return errors.New("Zeroconf provider failed to start")
	}

	m.mdnsProvider = provider
	return nil
}
