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

// announcedPairing holds the full state for one announced _shippairing._tcp service.
// The logicalID (the map key in announcedPairings) is stable and returned to callers.
// providerID is transient and updated transparently when interfaces change.
type announcedPairing struct {
	serviceName string              // mDNS service name — stable across re-announcements
	txtRecord   *api.ShipPairingTXT // TXT record data
	providerID  string              // current provider-side instance ID (changes on re-announcement)
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

	// which pairing mode
	pairingMode api.PairingMode

	isAnnounced bool // State for _ship._tcp service

	// announcedPairings tracks all pairing services this device has announced.
	// The map key is a stable logical ID returned to — and held by — callers.
	// Callers use this logical ID to stop an announcement via UnannouncePairingService.
	//
	// The logical ID never changes across interface-change re-announcements.
	// Only the internal providerID field changes when a new provider instance is
	// created during re-announcement. This keeps caller-held IDs valid indefinitely.
	announcedPairings    map[string]*announcedPairing
	announcedPairingsMux sync.RWMutex
	instanceCounter      int // monotonic counter for stable logical ID and service name generation

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

	providerSelection MdnsProviderSelection

	// Track if the manager has been started to prevent redundant operations
	isStarted bool

	// Unique instance ID retrieved from the ship service announcement
	instanceID string

	// Interface refresh state for continuous monitoring
	currentIfaces   []string            // Currently resolved interface names
	missingIfaces   map[string]struct{} // Interfaces not resolved
	refreshTicker   *time.Ticker        // Periodic retry timer
	refreshStopChan chan struct{}       // Signal to stop refresh goroutine
	refreshDone     chan struct{}       // Closed when refreshLoop exits
	refreshMux      sync.Mutex          // Protects refresh operations

	mux,
	muxReport,
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
		pairingEntries:    make(map[string]*api.ShipPairingTXT),
		announcedPairings: make(map[string]*announcedPairing),
		instanceCounter:   0,
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
	if err != nil && !errors.Is(err, ErrNoInterfacesAvailable) {
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
	pairingWasAnnounced := m.IsPairingServiceAnnounced()

	if !wasAnnounced {
		logging.Log().Info("mdns: making first announcement now that interfaces are available")
	}
	if !pairingWasAnnounced {
		logging.Log().Info("mdns: making first pairing announcement now that interfaces are available")
	}

	// Capture the live instance so it can be released after the new one is up.
	oldInstanceID := m.instanceID

	// Re-resolve interfaces (will pick up newly available ones)
	ifaces, ifaceIndexes, err := m.resolveInterfaces()
	if err != nil {
		if errors.Is(err, ErrNoInterfacesAvailable) {
			logging.Log().Debug("mdns: still no interfaces available during refresh")
			if wasAnnounced {
				m.UnannounceMdnsEntry()
			}
			if pairingWasAnnounced {
				// Tear down provider-side instances only. announcedPairings (the logical entries)
				// are preserved so services are re-announced when interfaces reappear.
				m.announcedPairingsMux.Lock()
				for _, entry := range m.announcedPairings {
					_ = m.mdnsProvider.UnannounceService(entry.providerID)
					entry.providerID = "" // mark as not currently active
				}
				m.announcedPairingsMux.Unlock()
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
	m.setIsServiceAnnounce(false) // bypass AnnounceMdnsEntry's already-announced early-return

	if err := m.AnnounceMdnsEntry(); err != nil {
		logging.Log().Debug("mdns: announcement failed:", err)
		// the previous announcement is still live, keep the state consistent
		m.setIsServiceAnnounce(wasAnnounced)
		return
	}

	// Create-then-swap: the new instance is live, release the stale one. Without
	// this the provider-side object (an avahi EntryGroup) is leaked on every
	// interface change until the daemon's per-client object limit is hit.
	if oldInstanceID != "" && oldInstanceID != m.instanceID {
		_ = m.mdnsProvider.UnannounceService(oldInstanceID)
	}

	// Re-announce all pairing services with the updated interface list.
	// announcedPairings is the authoritative source: logical IDs are stable, service names
	// are reused (remote devices see the same service), only the internal provider ID changes.
	//
	// Create-then-swap: new provider instance is live before the old one is torn down.
	m.announcedPairingsMux.Lock()
	for _, entry := range m.announcedPairings {
		txtArray := m.convertPairingTXTToArray(entry.txtRecord)
		newProviderID, err := m.mdnsProvider.AnnounceService(shipPairingZeroConfServiceType, entry.serviceName, m.port, txtArray)
		if err != nil {
			m.announcedPairingsMux.Unlock()
			logging.Log().Debug("mdns: pairing re-announcement failed:", err)
			return
		}
		oldProviderID := entry.providerID
		entry.providerID = newProviderID // atomic swap under lock
		if oldProviderID != "" {
			_ = m.mdnsProvider.UnannounceService(oldProviderID)
		}
	}
	m.announcedPairingsMux.Unlock()

	if !wasAnnounced {
		logging.Log().Info("mdns: successfully made first announcement")
	} else {
		logging.Log().Info("mdns: successfully re-announced with new interfaces")
	}
	if !pairingWasAnnounced {
		logging.Log().Info("mdns: successfully made first pairing announcement")
	} else {
		logging.Log().Info("mdns: successfully re-announced pairing with new interfaces")
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

	instanceID, err := m.mdnsProvider.AnnounceService(shipZeroConfServiceType, serviceName, m.port, txt)
	if err != nil {
		logging.Log().Debug("mdns: failure announcing service", err)
		return err
	}
	m.instanceID = instanceID

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
	if err := m.mdnsProvider.UnannounceService(m.instanceID); err != nil {
		logging.Log().Debug("mdns: stop announcement failed: %v", err.Error())
		return
	}

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

	// Create-then-swap: announce a new instance with the updated TXT record
	// before tearing down the old one. Mirrors reannounceWithNewInterfaces and
	// avoids goodbye packets that disrupt remote devices (see dev PR #76).
	oldInstanceID := m.instanceID
	m.setIsServiceAnnounce(false) // bypass AnnounceMdnsEntry's already-announced early-return

	if err := m.AnnounceMdnsEntry(); err != nil {
		logging.Log().Debug("mdns: changing mdns entry failed", err)
		return
	}

	if oldInstanceID != "" && oldInstanceID != m.instanceID {
		_ = m.mdnsProvider.UnannounceService(oldInstanceID)
	}
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
	// Compare case insensitively per RFC 6763
	switch strings.ToLower(serviceType) {
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

// processShipPairingMdnsEntry processes a _shippairing._tcp mDNS entry.
//
// elements is expected to use RFC 6763 canonical (lowercase) key names —
// parseTxt folds keys to lowercase before the manager sees them. SHIP Pairing
// TS §5.4 documents the keys in camelCase as wire convention; since RFC 6763
// makes key comparison case-insensitive, mapItems retains the spec spelling
// for audit clarity while lookups go through the lowercased form.
func (m *MdnsManager) processShipPairingMdnsEntry(elements map[string]string, serviceName string, remove bool) {
	// A removal is identified by its service instance name alone. mDNS remove
	// events often carry no TXT data (avahi can only echo elements cached at
	// add time, zeroconf reports incomplete records), so removal must be
	// handled before any TXT validation.
	if remove {
		if _, exists := m.pairingMdnsEntry(serviceName); exists {
			m.removePairingMdnsEntry(serviceName)
			logging.Log().Debug("mdns: remove shippairing", serviceName)
		}
		return
	}

	// check for mandatory text elements
	mapItems := []string{"txtvers", "parType", "forId", "forPar", "trustId", "trustPar", "trustCurve", "type", "trustNonce", "alg", "digest"}
	for _, item := range mapItems {
		if _, ok := elements[strings.ToLower(item)]; !ok {
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

	parType := elements["partype"]
	forId := elements["forid"]
	forPar := elements["forpar"]
	trustId := elements["trustid"]
	trustPar := elements["trustpar"]
	trustCurve := elements["trustcurve"]
	elType := elements["type"]
	trustNonce := elements["trustnonce"]
	alg := elements["alg"]
	digest := elements["digest"]

	logString := fmt.Sprintf(" - forId: %s, forPar: %s, trustId: %s, trustPar: %s, trustCurve: %s, type: %s, trustNonce: %s, alg: %s, digest: %s",
		forId, forPar, trustId, trustPar, trustCurve, elType, trustNonce, alg, digest)
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

	if err := validatePairingTXTStrict(newEntry); err != nil {
		logging.Log().Debug("mdns: pairing - invalid TXT record", err, serviceName)
		return
	}

	// A re-report of a known instance name with identical content is a TTL
	// refresh — ignore it. Different content means devZ corrected its request
	// reusing the same instance name (pairing spec §5.5 option 2), or the
	// goodbye that should have preceded the new announcement was lost; §4.2
	// devA rule 2 requires the evaluation to begin again in both cases, so
	// update the cache and re-deliver.
	if cached, exists := m.pairingMdnsEntry(serviceName); exists {
		if *cached == *newEntry {
			return
		}
		logging.Log().Debug("mdns: changed", logString)
	} else {
		logging.Log().Debug("mdns: new", logString)
	}

	m.setPairingMdnsEntry(serviceName, newEntry)

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

func validatePairingTXTStrict(txt *api.ShipPairingTXT) error {
	if err := txt.Validate(); err != nil {
		return err
	}

	if strings.TrimSpace(txt.ForId) == "" || strings.TrimSpace(txt.TrustId) == "" {
		return api.ErrInvalidTXTRecord
	}

	if !isUpperHexOfLength(txt.TrustNonce, 32) {
		return api.ErrInvalidTXTRecord
	}

	if !isUpperHexOfLength(txt.Digest, 64) {
		return api.ErrInvalidTXTRecord
	}

	return nil
}

func isUpperHexOfLength(v string, n int) bool {
	if len(v) != n {
		return false
	}

	for i := 0; i < len(v); i++ {
		c := v[i]
		if (c >= '0' && c <= '9') || (c >= 'A' && c <= 'F') {
			continue
		}
		return false
	}

	return true
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

// AnnouncePairingService announces _shippairing._tcp service (implements MdnsPairingInterface).
//
// Returns a stable logical ID. The caller must hold this ID and pass it to
// UnannouncePairingService to stop the announcement. The logical ID remains valid
// across interface-change re-announcements — the manager transparently replaces the
// underlying provider instance without invalidating caller-held IDs.
func (m *MdnsManager) AnnouncePairingService(txtRecord *api.ShipPairingTXT) (string, error) {
	provider := m.mdnsProvider
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

	// Generate stable logical ID and a matching mDNS service name.
	// Both are based on the same counter so the service name is deterministic
	// and reused on re-announcement (avoids remote devices seeing a new service).
	m.announcedPairingsMux.Lock()
	m.instanceCounter++
	logicalID := strconv.Itoa(m.instanceCounter)
	serviceName := m.serviceName + "-pairing#" + logicalID
	m.announcedPairingsMux.Unlock()

	txtArray := m.convertPairingTXTToArray(txtRecord)

	providerID, err := provider.AnnounceService(shipPairingZeroConfServiceType, serviceName, m.port, txtArray)
	if err != nil {
		logging.Log().Debug("mdns: AnnouncePairingService - provider.AnnounceService failed:", err)
		return "", fmt.Errorf("failed to announce pairing service: %w", err)
	}

	m.announcedPairingsMux.Lock()
	m.announcedPairings[logicalID] = &announcedPairing{
		serviceName: serviceName,
		txtRecord:   txtRecord,
		providerID:  providerID,
	}
	m.announcedPairingsMux.Unlock()

	return logicalID, nil
}

// UnannouncePairingService removes a _shippairing._tcp announcement (implements MdnsPairingInterface).
// logicalID is the value returned by AnnouncePairingService; it remains valid even after
// interface-change re-announcements.
func (m *MdnsManager) UnannouncePairingService(logicalID string) error {
	provider := m.mdnsProvider
	if provider == nil {
		return api.ErrPairingNotActive
	}

	m.announcedPairingsMux.Lock()
	entry, exists := m.announcedPairings[logicalID]
	if !exists {
		m.announcedPairingsMux.Unlock()
		return fmt.Errorf("pairing logical ID %s not found", logicalID)
	}
	providerID := entry.providerID
	delete(m.announcedPairings, logicalID)
	m.announcedPairingsMux.Unlock()

	if err := provider.UnannounceService(providerID); err != nil {
		return err
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

// RequestPairingEntries returns a snapshot of the currently-live
// _shippairing._tcp records from the local browse cache; it performs no
// network operation (implements MdnsPairingInterface)
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

// IsPairingServiceAnnounced returns true if at least one pairing service has been announced
// and not yet unannounced. Returns true even during interface-loss events when the provider-side
// announcement is temporarily down, because the logical entry still exists and will be
// re-announced when interfaces reappear.
func (m *MdnsManager) IsPairingServiceAnnounced() bool {
	m.announcedPairingsMux.RLock()
	has := len(m.announcedPairings) > 0
	m.announcedPairingsMux.RUnlock()
	return has
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

// SimulatePairingDiscovery simulates the discovery of a pairing service for testing.
// Routes the TXT record through the same wire-encode → parseTxt pipeline that
// production providers use, so the simulator delivers identically normalized
// input to processShipPairingMdnsEntry.
func (m *MdnsManager) SimulatePairingDiscovery(txtRecord *api.ShipPairingTXT) {
	if txtRecord == nil {
		return
	}

	elements, ok := parseTxt(m.convertPairingTXTToArray(txtRecord))
	if !ok {
		return
	}

	// Process the simulated discovery
	m.processShipPairingMdnsEntry(elements, "servicename", false)
}
