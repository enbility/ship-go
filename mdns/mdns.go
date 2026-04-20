package mdns

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/enbility/go-avahi"
	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/logging"
	"github.com/enbility/ship-go/util"
)

var ErrNoInterfacesAvailable = errors.New("none of the configured interfaces are available")

const shipWebsocketPath = "/ship/"

// interfaceRefreshInterval is the interval at which the mDNS manager
// checks for interface availability changes
const interfaceRefreshInterval = 15 * time.Second

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

	// Whether remote devices should be automatically accepted
	autoaccept atomic.Bool

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

	providerSelection MdnsProviderSelection

	// Interface refresh state for continuous monitoring
	currentIfaces   []string            // Currently resolved interface names
	missingIfaces   map[string]struct{} // Interfaces not resolved
	refreshTicker   *time.Ticker        // Periodic retry timer
	refreshStopChan chan struct{}        // Signal to stop refresh goroutine
	refreshDone     chan struct{}        // Closed when refreshLoop exits
	refreshMux      sync.Mutex          // Protects refresh operations

	mux,
	muxAnnounced,
	shutdownMux sync.Mutex
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
	ifaces, ifaceIndexes, err := m.resolveInterfaces()
	if err != nil && !errors.Is(err, ErrNoInterfacesAvailable) {
		return nil, nil, err
	}

	if len(m.ifaces) == 0 {
		return ifaces, ifaceIndexes, nil
	}

	// Reset and rebuild tracking state (protected by refreshMux since
	// these fields are also read/written by attemptResolveMapping)
	m.refreshMux.Lock()
	m.missingIfaces = make(map[string]struct{})
	m.currentIfaces = make([]string, 0, len(m.ifaces))

	resolvedSet := make(map[string]struct{}, len(ifaces))
	for _, iface := range ifaces {
		resolvedSet[iface.Name] = struct{}{}
		m.currentIfaces = append(m.currentIfaces, iface.Name)
	}

	for _, ifaceName := range m.ifaces {
		if _, ok := resolvedSet[ifaceName]; !ok {
			m.missingIfaces[ifaceName] = struct{}{}
			logging.Log().Debugf("mdns: interface %s not available or not usable", ifaceName)
		}
	}
	m.refreshMux.Unlock()

	if errors.Is(err, ErrNoInterfacesAvailable) {
		logging.Log().Infof("mdns: none of the %d required interfaces are available, will retry", len(m.ifaces))
		return nil, nil, ErrNoInterfacesAvailable
	}

	return ifaces, ifaceIndexes, nil
}

// resolveInterfaces returns currently usable interfaces without modifying
// tracking state (currentIfaces/missingIfaces). Used by reannounceWithNewInterfaces
// to avoid resetting the change-detection trackers managed by attemptResolveMapping.
func (m *MdnsManager) resolveInterfaces() ([]net.Interface, []int32, error) {
	if len(m.ifaces) == 0 {
		return nil, []int32{avahi.InterfaceUnspec}, nil
	}

	var ifaces []net.Interface
	var ifaceIndexes []int32

	for _, ifaceName := range m.ifaces {
		iface, usable := getUsableInterface(ifaceName)
		if !usable {
			continue
		}
		ifaces = append(ifaces, *iface)
		ifaceIndexes = append(ifaceIndexes, int32(iface.Index)) // #nosec G115
	}

	if len(ifaces) == 0 {
		return nil, nil, ErrNoInterfacesAvailable
	}

	return ifaces, ifaceIndexes, nil
}

// isInterfaceUsable checks if a network interface is usable for mDNS
func isInterfaceUsable(iface *net.Interface) bool {
	// Must be UP
	if iface.Flags&net.FlagUp == 0 {
		return false
	}
	// Must not be loopback
	if iface.Flags&net.FlagLoopback != 0 {
		return false
	}
	// Must have at least one address
	addrs, err := iface.Addrs()
	if err != nil || len(addrs) == 0 {
		return false
	}
	return true
}

// getUsableInterface attempts to get an interface by name and checks if it's usable.
// Returns the interface and true if found and usable, nil and false otherwise.
func getUsableInterface(ifaceName string) (*net.Interface, bool) {
	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		return nil, false
	}

	if !isInterfaceUsable(iface) {
		return nil, false
	}

	return iface, true
}

var _ api.MdnsInterface = (*MdnsManager)(nil)

func (m *MdnsManager) Start(cb api.MdnsReportInterface) error {
	ifaces, ifaceIndexes, err := m.interfaces()
	if err != nil && !errors.Is(err, ErrNoInterfacesAvailable) {
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

	// Only announce if we have interfaces available
	if ifaces != nil || ifaceIndexes != nil {
		// on startup start mDNS announcement
		if err := m.AnnounceMdnsEntry(); err != nil {
			return err
		}
	} else {
		logging.Log().Info("mdns: no interfaces available, skipping initial announcement")
	}

	// Start interface monitoring if specific interfaces are configured
	if len(m.ifaces) > 0 {
		logging.Log().Debug("mdns: starting interface monitoring")
		m.startInterfaceRefresh()
	}

	return nil
}

// startInterfaceRefresh starts the background goroutine for monitoring interface changes
func (m *MdnsManager) startInterfaceRefresh() {
	m.refreshMux.Lock()
	defer m.refreshMux.Unlock()

	if m.refreshTicker != nil {
		return // Already running
	}

	m.refreshStopChan = make(chan struct{})
	m.refreshDone = make(chan struct{})
	m.refreshTicker = time.NewTicker(interfaceRefreshInterval)

	// Capture channels for goroutine to avoid race conditions
	stopChan := m.refreshStopChan
	tickChan := m.refreshTicker.C
	done := m.refreshDone

	go m.refreshLoop(stopChan, tickChan, done)
}

// refreshLoop is the background goroutine that periodically checks for interface changes
func (m *MdnsManager) refreshLoop(stopChan <-chan struct{}, tickChan <-chan time.Time, done chan struct{}) {
	defer close(done)

	for {
		select {
		case <-stopChan:
			return
		case <-tickChan:
			m.attemptResolveMapping()
		}
	}
}

// attemptResolveMapping checks for interface changes and triggers re-announcement if needed
func (m *MdnsManager) attemptResolveMapping() {
	m.refreshMux.Lock()

	// Build current state: which interfaces are usable NOW
	currentlyAvailable := make(map[string]bool)
	for _, ifaceName := range m.ifaces {
		if _, usable := getUsableInterface(ifaceName); usable {
			currentlyAvailable[ifaceName] = true
		}
	}

	// Detect changes from last known state
	var appeared []string
	var disappeared []string

	// Check for newly appeared interfaces
	for ifaceName := range currentlyAvailable {
		if _, wasMissing := m.missingIfaces[ifaceName]; wasMissing {
			appeared = append(appeared, ifaceName)
			delete(m.missingIfaces, ifaceName)
		}
	}

	// Check for disappeared interfaces
	for _, ifaceName := range m.currentIfaces {
		if !currentlyAvailable[ifaceName] {
			disappeared = append(disappeared, ifaceName)
			m.missingIfaces[ifaceName] = struct{}{}
		}
	}

	// Update current state
	m.currentIfaces = make([]string, 0, len(currentlyAvailable))
	for ifaceName := range currentlyAvailable {
		m.currentIfaces = append(m.currentIfaces, ifaceName)
	}

	hasChanges := len(appeared) > 0 || len(disappeared) > 0

	m.refreshMux.Unlock()

	if hasChanges {
		if len(appeared) > 0 {
			logging.Log().Infof("mdns: interfaces appeared: %v", appeared)
		}
		if len(disappeared) > 0 {
			logging.Log().Infof("mdns: interfaces disappeared: %v", disappeared)
		}
		m.reannounceWithNewInterfaces()
	}
}

// reannounceWithNewInterfaces re-announces the service with the updated interface list.
//
// This function intentionally does NOT call UnannounceMdnsEntry() before re-announcing.
// The providers handle the transition internally by creating the new announcement before
// tearing down the old one (create-then-swap). This avoids sending mDNS goodbye packets
// that would cause remote devices to believe this service has left the network, which
// could break existing EEBUS/SHIP connections on interfaces that are still operational.
func (m *MdnsManager) reannounceWithNewInterfaces() {
	wasAnnounced := m.isServiceAnnounced()

	if !wasAnnounced {
		logging.Log().Info("mdns: making first announcement now that interfaces are available")
	}

	// Re-resolve interfaces (will pick up newly available ones)
	ifaces, ifaceIndexes, err := m.resolveInterfaces()
	if err != nil {
		if errors.Is(err, ErrNoInterfacesAvailable) {
			logging.Log().Debug("mdns: still no interfaces available during refresh")
			if wasAnnounced {
				m.UnannounceMdnsEntry()
			}
			return
		}
		// handle unexpected errors
		logging.Log().Debugf("mdns: error resolving interfaces during refresh: %s", err)
		return
	}

	// Update provider with new interface list
	m.updateProviderInterfaces(ifaces, ifaceIndexes)

	// Announce (or re-announce). The providers handle the transition seamlessly
	// by creating the new server/entry group before shutting down the old one.
	if err := m.AnnounceMdnsEntry(); err != nil {
		logging.Log().Debug("mdns: announcement failed:", err)
		return
	}

	if !wasAnnounced {
		logging.Log().Info("mdns: successfully made first announcement")
	} else {
		logging.Log().Info("mdns: successfully re-announced with new interfaces")
	}
}

// mdnsProviderInterfaceUpdater is implemented by providers that support
// dynamic interface updates at runtime.
type mdnsProviderInterfaceUpdater interface {
	UpdateInterfaces(ifaces []net.Interface, ifaceIndexes []int32)
}

// updateProviderInterfaces updates the provider's interface list
func (m *MdnsManager) updateProviderInterfaces(ifaces []net.Interface, ifaceIndexes []int32) {
	if m.mdnsProvider == nil {
		return
	}

	if updater, ok := m.mdnsProvider.(mdnsProviderInterfaceUpdater); ok {
		updater.UpdateInterfaces(ifaces, ifaceIndexes)
	}
}

// stopInterfaceRefresh stops the interface monitoring goroutine and waits
// for it to exit before returning. This ensures no goroutine is still
// accessing shared state (e.g. mdnsProvider) after this call returns.
func (m *MdnsManager) stopInterfaceRefresh() {
	m.refreshMux.Lock()

	if m.refreshStopChan != nil {
		close(m.refreshStopChan)
		m.refreshStopChan = nil
	}

	if m.refreshTicker != nil {
		m.refreshTicker.Stop()
		m.refreshTicker = nil
	}

	done := m.refreshDone
	m.refreshMux.Unlock()

	// Wait for the goroutine to exit. This must happen outside the lock
	// because the goroutine may be in attemptResolveMapping which also
	// acquires refreshMux.
	if done != nil {
		<-done
	}
}

// Shutdown all of mDNS.
// Safe to call multiple times; only the first call after each Start() performs cleanup.
func (m *MdnsManager) Shutdown() {
	m.shutdownMux.Lock()
	defer m.shutdownMux.Unlock()

	// Stop interface refresh goroutine first
	m.stopInterfaceRefresh()

	// Idempotency: if provider is already nil, nothing to shut down
	if m.mdnsProvider == nil {
		return
	}

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
	func() {
		defer func() {
			if r := recover(); r != nil {
				logging.Log().Debug("mdns: panic during provider shutdown:", r)
			}
		}()
		m.mdnsProvider.Shutdown()
	}()
	m.mdnsProvider = nil

	// Always reset announced state so a subsequent Start() begins clean,
	// even if UnannounceMdnsEntry above panicked before clearing it.
	m.setIsServiceAnnounce(false)
}

// Announces the service to the network via mDNS
// A CEM service should always invoke this on startup
// Any other service should only invoke this whenever it is not connected to a CEM service
func (m *MdnsManager) AnnounceMdnsEntry() error {
	if m.mdnsProvider == nil {
		return fmt.Errorf("cannot announce mDNS entry: no provider available (selection: %d)", m.providerSelection)
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
		"register=" + fmt.Sprintf("%v", m.autoaccept.Load()),
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
	m.autoaccept.Store(accept)

	if !m.isServiceAnnounced() || m.mdnsProvider == nil {
		return
	}

	if err := m.AnnounceMdnsEntry(); err != nil {
		logging.Log().Debug("mdns: changing mdns entry failed", err)
		m.setIsServiceAnnounce(false)
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

// process an mDNS entry and manage mDNS entries map
func (m *MdnsManager) processMdnsEntry(elements map[string]string, serviceName, host string, addresses []net.IP, port int, remove bool) {
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

		logging.Log().Debug("mdns: remove - ski:", ski, "serviceName:", serviceName, "brand:", brand, "model:", model, "typ:", deviceType, "serial:", serial, "categories:", categoriesStr, "identifier:", identifier, "register:", register, "host:", host, "port:", port, "addresses:", addresses)
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

			logging.Log().Debug("mdns: update - ski:", ski, "serviceName:", serviceName, "brand:", brand, "model:", model, "typ:", deviceType, "serial:", serial, "categories:", categoriesStr, "identifier:", identifier, "register:", register, "host:", host, "port:", port, "addresses:", addresses)
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

		logging.Log().Debug("mdns: new - ski:", ski, "serviceName:", serviceName, "brand:", brand, "model:", model, "typ:", deviceType, "serial:", serial, "categories:", categoriesStr, "identifier:", identifier, "register:", register, "host:", host, "port:", port, "addresses:", addresses)
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

	if !provider.Start(autoReconnect, m.processMdnsEntry) {
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

	if !provider.Start(autoReconnect, m.processMdnsEntry) {
		// Clean up failed provider
		provider.Shutdown()
		return fmt.Errorf("zeroconf provider failed to start (interfaces: %d, autoReconnect: %v)", len(ifaces), autoReconnect)
	}

	m.mdnsProvider = provider
	return nil
}
