package mdns

import (
	"fmt"
	"math/rand"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/enbility/go-avahi"
	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/logging"
)

type mdnsServiceData struct {
	// the service name
	Name string
	// the service port
	Port int
	// the service txt
	Txt []string
}

// instanceData holds service instance information for cleanup
type instanceData struct {
	ServiceType string
	ServiceName string
	Port        int
	Txt         []string
}

type AvahiProvider struct {
	ifaceIndexes []int32

	avServer         avahi.ServerInterface
	avBrowser        avahi.ServiceBrowserInterface // For _ship._tcp browsing
	avPairingBrowser avahi.ServiceBrowserInterface // For _shippairing._tcp browsing

	autoReconnect   bool
	manualShutdown  bool
	setupSuccessful bool
	listenerRunning bool

	pairingMode     api.PairingMode // The pairing mode
	mdnsServiceData *mdnsServiceData

	resolveCB api.MdnsResolveCB

	// Used to store the service elements for each service, so that we can recall them when a service is removed
	serviceElements map[string]map[string]string

	shutdownChan                      chan struct{}
	addServiceChan, removeServiceChan chan avahi.Service

	// One EntryGroup per service instance - fixes architectural flaw
	instanceEntryGroups map[string]avahi.EntryGroupInterface // instanceID -> dedicated EntryGroup
	instanceStates      map[string]*mdnsServiceData          // instanceID -> service data

	// Instance management for the new interface
	instanceCounter  int
	serviceInstances map[string]*instanceData // instanceID -> service data

	mux sync.Mutex
	// muxIface guards ifaceIndexes only, deliberately not mux.
	//
	// Announce, Start, Shutdown and attemptReconnect all hold mux across blocking D-Bus
	// round trips, and chanListener needs the interface indexes for every browsed service.
	// With one shared lock that closes a deadlock cycle (#78): the announcing goroutine
	// holds mux inside EntryGroupNew, chanListener waits for mux in getIfaceIndexes, and
	// the Avahi signal loop is parked sending into the browse channel chanListener would
	// drain - so the D-Bus reply that would release mux can never be delivered. Keeping
	// the indexes on their own lock takes chanListener out of that cycle.
	muxIface sync.RWMutex
	muxEl    sync.RWMutex // used for serviceElements

	// Prevent multiple reconnection goroutines
	reconnectInProgress bool
	reconnectMux        sync.Mutex
}

func NewAvahiProvider(ifaceIndexes []int32) *AvahiProvider {
	return &AvahiProvider{
		avServer:            avahi.ServerNew(),
		setupSuccessful:     false,
		ifaceIndexes:        ifaceIndexes,
		serviceElements:     make(map[string]map[string]string),
		instanceEntryGroups: make(map[string]avahi.EntryGroupInterface), // One per instance
		instanceStates:      make(map[string]*mdnsServiceData),          // One per instance
		instanceCounter:     0,
		serviceInstances:    make(map[string]*instanceData),
	}
}

// UpdateInterfaces updates the interface indexes in a thread-safe manner.
// AvahiProvider uses ifaceIndexes; ifaces is ignored.
func (a *AvahiProvider) UpdateInterfaces(_ []net.Interface, ifaceIndexes []int32) {
	a.muxIface.Lock()
	defer a.muxIface.Unlock()
	a.ifaceIndexes = ifaceIndexes
}

// getIfaceIndexes returns a copy of the interface indexes in a thread-safe manner
func (a *AvahiProvider) getIfaceIndexes() []int32 {
	a.muxIface.RLock()
	defer a.muxIface.RUnlock()
	// Return a copy to avoid race conditions
	indexesCopy := make([]int32, len(a.ifaceIndexes))
	copy(indexesCopy, a.ifaceIndexes)
	return indexesCopy
}

// AvahiProvider implements the standard MdnsProviderInterface
// For multi-service support, wrap with MultiServiceAdapter
var _ api.MdnsProviderInterface = (*AvahiProvider)(nil)

func (a *AvahiProvider) Start(pairingMode api.PairingMode, autoReconnect bool, cb api.MdnsResolveCB) bool {
	a.mux.Lock()
	defer a.mux.Unlock()

	a.pairingMode = pairingMode
	a.autoReconnect = autoReconnect
	a.resolveCB = cb
	a.manualShutdown = false

	err := a.avServer.Setup(a.avahiCallback)
	if err != nil {
		return false
	}
	a.setupSuccessful = true
	if a.shutdownChan == nil {
		// Buffered (capacity 1) so Shutdown()'s send below never blocks
		// while holding a.mux. If chanListener is busy inside
		// processService (e.g. parked in a dBus call like ResolveService,
		// or contending for a.mux via getIfaceIndexes), an unbuffered
		// send would create a lock/channel deadlock cycle: Shutdown holds
		// a.mux and waits for the send to complete, while chanListener
		// cannot return to its select to receive until it either finishes
		// processService or acquires a.mux -- both of which Shutdown is
		// blocking.
		a.shutdownChan = make(chan struct{}, 1)
	}
	if a.addServiceChan == nil {
		a.addServiceChan = make(chan avahi.Service)
	}
	if a.removeServiceChan == nil {
		a.removeServiceChan = make(chan avahi.Service)
	}

	a.avServer.Start()

	if _, err := a.avServer.GetAPIVersion(); err != nil {
		a.avServer.Shutdown()
		return false
	}

	// instead of limiting search on specific allowed interfaces, we allow all and filter the results
	// Browse for _ship._tcp services
	avBrowser, err := a.avServer.ServiceBrowserNew(a.addServiceChan, a.removeServiceChan, avahi.InterfaceUnspec, avahi.ProtoUnspec, shipZeroConfServiceType, shipZeroConfDomain, 0)
	if err != nil || avBrowser == nil {
		a.avServer.Shutdown()
		return false
	}
	a.avBrowser = avBrowser

	if pairingMode == api.PairingModeListener || pairingMode == api.PairingModeBoth {
		// Also browse for _shippairing._tcp services
		avPairingBrowser, err := a.avServer.ServiceBrowserNew(a.addServiceChan, a.removeServiceChan, avahi.InterfaceUnspec, avahi.ProtoUnspec, shipPairingZeroConfServiceType, shipZeroConfDomain, 0)
		if err != nil || avPairingBrowser == nil {
			// If pairing browser fails, log but don't fail completely
			logging.Log().Debug("mdns: avahi - failed to create pairing browser, pairing discovery disabled", err)
			// Continue without pairing support
		} else {
			a.avPairingBrowser = avPairingBrowser
		}
	}

	// autoReconnect is only called with false if the systems does not know if
	// avahi should be used in the first place.
	// but if it was found and therefor being used, it should automatically reconnect once disconnected
	if !autoReconnect {
		a.autoReconnect = true
	}

	if !a.listenerRunning {
		a.listenerRunning = true
		// Capture channels for the goroutine so it does not read the
		// provider fields directly. This avoids a data race with
		// Shutdown() nilling a.shutdownChan / a.addServiceChan /
		// a.removeServiceChan after closing them.
		go a.chanListener(cb, a.shutdownChan, a.addServiceChan, a.removeServiceChan)
	}

	return true
}

func (a *AvahiProvider) Shutdown() {
	a.mux.Lock()
	a.manualShutdown = true

	if !a.setupSuccessful {
		a.mux.Unlock()
		return
	}

	// when shutting down on purpose, do not try to reconnect
	a.autoReconnect = false
	if a.avBrowser != nil {
		a.avServer.ServiceBrowserFree(a.avBrowser)
		a.avBrowser = nil

		if a.listenerRunning {
			// stop the currently running resolve
			a.shutdownChan <- struct{}{}
		}
	}
	// Also free the pairing browser if it exists
	if a.avPairingBrowser != nil {
		a.avServer.ServiceBrowserFree(a.avPairingBrowser)
		a.avPairingBrowser = nil
	}
	a.listenerRunning = false
	if a.shutdownChan != nil {
		close(a.shutdownChan)
		a.shutdownChan = nil
	}
	if a.addServiceChan != nil {
		close(a.addServiceChan)
		a.addServiceChan = nil
	}
	if a.removeServiceChan != nil {
		close(a.removeServiceChan)
		a.removeServiceChan = nil
	}
	a.mux.Unlock()

	// Wait for any reconnection goroutine to stop
	for {
		a.reconnectMux.Lock()
		inProgress := a.reconnectInProgress
		a.reconnectMux.Unlock()

		if !inProgress {
			break
		}

		logging.Log().Debug("mdns: avahi - waiting for reconnection goroutine to stop")
		time.Sleep(100 * time.Millisecond)
	}

	// Unannounce all service instances
	a.mux.Lock()
	instancesToRemove := make([]string, 0, len(a.serviceInstances))
	for instanceID := range a.serviceInstances {
		instancesToRemove = append(instancesToRemove, instanceID)
	}
	a.mux.Unlock()

	for _, instanceID := range instancesToRemove {
		_ = a.UnannounceService(instanceID)
	}

	a.mux.Lock()
	defer a.mux.Unlock()

	a.avServer.Shutdown()
}

func (a *AvahiProvider) avahiCallback(event avahi.Event) {
	a.mux.Lock()
	// if there is a manual shutdown, we do not want to reconnect
	if a.manualShutdown || !a.autoReconnect || event != avahi.Disconnected {
		a.mux.Unlock()
		return
	}

	logging.Log().Debug("mdns: avahi - disconnected")

	// the server was shutdown, set it to nil so we don't try to call free functions
	// on shutting down a currently running resolve
	cb := a.resolveCB
	var serviceData *mdnsServiceData
	if a.mdnsServiceData != nil {
		serviceData = a.mdnsServiceData
	}

	// Keep instanceEntryGroups: the stale entry group objects will be freed in
	// attemptReconnect after the new entry group is committed (create-then-swap).
	// Freeing them here (before reconnect) would be premature and could race with
	// the reconnect goroutine.

	a.mux.Unlock()

	// Prevent multiple reconnection goroutines
	a.reconnectMux.Lock()
	if a.reconnectInProgress {
		a.reconnectMux.Unlock()
		logging.Log().Debug("mdns: avahi - reconnection already in progress")
		return
	}
	a.reconnectInProgress = true
	a.reconnectMux.Unlock()

	// try to reconnect until successull
	go a.attemptReconnect(cb, serviceData)
}

// attempt to reconnect to the avahi daemon with exponential backoff
func (a *AvahiProvider) attemptReconnect(cb api.MdnsResolveCB, serviceData *mdnsServiceData) {
	defer func() {
		// Clear the reconnection flag when done
		a.reconnectMux.Lock()
		a.reconnectInProgress = false
		a.reconnectMux.Unlock()
	}()

	baseDelay := time.Second
	maxDelay := 30 * time.Second // Maximum 30 seconds between attempts
	currentDelay := baseDelay
	attempt := 0

	for {
		a.mux.Lock()
		if a.manualShutdown {
			a.mux.Unlock()
			return
		}
		a.mux.Unlock()

		// Wait with exponential backoff
		time.Sleep(currentDelay)
		attempt++

		logging.Log().Tracef("mdns: avahi - reconnection attempt %d (delay: %v)", attempt, currentDelay)

		if !a.Start(a.pairingMode, true, cb) {
			// Exponential backoff with jitter
			currentDelay = currentDelay * 2
			// Add jitter (±10%)
			// Using math/rand is appropriate here for non-cryptographic timing jitter
			jitter := time.Duration(float64(currentDelay) * 0.1 * (2*rand.Float64() - 1)) //nolint:gosec
			currentDelay = currentDelay + jitter
			if currentDelay > maxDelay {
				currentDelay = maxDelay
			}
			continue
		}

		logging.Log().Debug("mdns: avahi - reconnected successfully")

		// Restore all services from serviceStates
		a.mux.Lock()
		statesToRestore := make(map[string]*mdnsServiceData)
		for instanceID, serviceState := range a.instanceStates {
			statesToRestore[instanceID] = serviceState
		}
		a.mux.Unlock()

		// restoredShipServices tracks service names already re-announced for the
		// legacy compatibility check below (keyed by service name).
		restoredShipServices := make(map[string]bool)

		for instanceID, serviceState := range statesToRestore {
			// Capture service type before any map deletions below
			serviceType := "_shippairing._tcp" // Default fallback
			if instanceInfo, exists := a.serviceInstances[instanceID]; exists {
				serviceType = instanceInfo.ServiceType
			}
			if _, err := a.AnnounceService(serviceType, serviceState.Name, serviceState.Port, serviceState.Txt); err != nil {
				logging.Log().Debugf("mdns: avahi - error re-announcing service %s: %v", serviceState.Name, err)
				continue
			}
			// create-then-swap: new entry group is now committed; release the stale
			// (disconnected-server) entry group and remove the old instance ID.
			// AnnounceService has already registered a new instance ID for this service.
			a.mux.Lock()
			if oldEG, ok := a.instanceEntryGroups[instanceID]; ok {
				a.avServer.EntryGroupFree(oldEG)
				delete(a.instanceEntryGroups, instanceID)
			}
			delete(a.instanceStates, instanceID)
			delete(a.serviceInstances, instanceID)
			a.mux.Unlock()

			if serviceType == shipZeroConfServiceType {
				restoredShipServices[serviceState.Name] = true
			}
		}

		// Legacy compatibility: also restore from serviceData if available and not already restored
		if serviceData != nil && !restoredShipServices[serviceData.Name] {
			if _, err := a.AnnounceService(shipZeroConfServiceType, serviceData.Name, serviceData.Port, serviceData.Txt); err != nil {
				logging.Log().Debug("mdns: avahi - error re-announcing legacy service:", err)
			}
		}

		return
	}
}

// listen to service changes and shutdown.
// shutdownChan, addServiceChan and removeServiceChan are passed in so this
// goroutine never reads the provider fields directly -- Shutdown() is free
// to close and nil those fields without racing against this select.
func (a *AvahiProvider) chanListener(
	cb api.MdnsResolveCB,
	shutdownChan chan struct{},
	addServiceChan, removeServiceChan chan avahi.Service,
) {
	for {
		select {
		case <-shutdownChan:
			return
		case service := <-addServiceChan:
			if err := a.processService(service, false, cb); err != nil {
				logging.Log().Debug("mdns: avahi -", err)
			}
		case service := <-removeServiceChan:
			if err := a.processService(service, true, cb); err != nil {
				logging.Log().Debug("mdns: avahi -", err)
			}
		}
	}
}

// process an avahi mDNS service
// as avahi returns a service per interface, we need to combine them
func (a *AvahiProvider) processService(service avahi.Service, remove bool, cb api.MdnsResolveCB) error {
	// check if the service is within the allowed list
	// Get a thread-safe copy of interface indexes
	ifaceIndexes := a.getIfaceIndexes()
	allow := false
	if len(ifaceIndexes) == 1 && ifaceIndexes[0] == avahi.InterfaceUnspec {
		allow = true
	} else {
		for _, iface := range ifaceIndexes {
			if service.Interface == iface {
				allow = true
				break
			}
		}
	}

	if !allow {
		return fmt.Errorf("ignoring service as its interface is not in the allowed list: %s", service.Name)
	}

	if remove {
		return a.processRemovedService(service, cb)
	}

	// resolve the new service
	resolved, err := a.avServer.ResolveService(service.Interface, service.Protocol, service.Name, service.Type, service.Domain, avahi.ProtoUnspec, 0)
	if err != nil {
		return fmt.Errorf("error resolving service: %s error: %w", service.Name, err)
	}

	return a.processAddedService(resolved, cb)
}

func (a *AvahiProvider) processRemovedService(service avahi.Service, cb api.MdnsResolveCB) error {
	logging.Log().Tracef("mdns: avahi - process remove service: %v", service)

	// get the elements for the service
	a.muxEl.RLock()
	elements := a.serviceElements[getServiceUniqueKey(service)]
	a.muxEl.RUnlock()

	cb(elements, service.Name, service.Host, service.Type, nil, -1, true)

	return nil
}

func (a *AvahiProvider) processAddedService(service avahi.Service, cb api.MdnsResolveCB) error {
	// convert [][]byte to []string manually
	var txt []string
	for _, element := range service.Txt {
		txt = append(txt, string(element))
	}
	elements, uniqueKeys := parseTxt(txt)
	if !uniqueKeys {
		return fmt.Errorf("duplicate keys in txt record: %v", txt)
	}

	if !validateTxtversOrder(txt) {
		return fmt.Errorf("invalid order - must lead with txtvers: %v", txt)
	}

	logging.Log().Trace("mdns: avahi - process add service:", service.Name, service.Type, service.Domain, service.Host, service.Address, service.Port, elements)

	address := net.ParseIP(service.Address)
	// if the address can not be used, ignore the entry
	if address == nil || address.IsUnspecified() {
		return fmt.Errorf("service provides unusable address: %s", service.Name)
	}

	// add the elements to the map
	a.muxEl.Lock()
	a.serviceElements[getServiceUniqueKey(service)] = elements
	a.muxEl.Unlock()

	cb(elements, service.Name, service.Host, service.Type, []net.IP{address}, int(service.Port), false)

	return nil
}

// Create a unique key for a ship service
func getServiceUniqueKey(service avahi.Service) string {
	return fmt.Sprintf("%s-%s-%s-%d-%d", service.Name, service.Type, service.Domain, service.Protocol, service.Interface)
}

/* Enhanced Provider Interface Implementation - TDD Stubs */

// AnnounceService announces a specific service type and returns an instance ID
func (a *AvahiProvider) AnnounceService(serviceType, serviceName string, port int, txt []string) (string, error) {
	// Use existing announcement logic but with configurable service type
	// This extends the current Announce() method to support different service types

	a.mux.Lock()
	defer a.mux.Unlock()

	if a.avServer == nil {
		return "", api.ErrServiceNotStarted
	}

	// Generate unique instance ID first
	a.instanceCounter++
	instanceID := strconv.Itoa(a.instanceCounter)

	// Create dedicated EntryGroup for this instance - no sharing
	entryGroup, err := a.avServer.EntryGroupNew()
	if err != nil {
		return "", fmt.Errorf("failed to create entry group for instance %s: %w", instanceID, err)
	}
	a.instanceEntryGroups[instanceID] = entryGroup

	// Convert TXT records to Avahi format ([][]byte)
	btxt := make([][]byte, len(txt))
	for i, t := range txt {
		btxt[i] = []byte(t)
	}

	// Add service to the dedicated EntryGroup
	// Note: For _shippairing._tcp, we use the same port as the SHIP server since pairing connects to the same WebSocket endpoint
	for _, iface := range a.getIfaceIndexes() {
		err := entryGroup.AddService(iface, avahi.ProtoUnspec, 0, serviceName, serviceType, shipZeroConfDomain, "", uint16(port), btxt) // #nosec G115
		if err != nil {
			// Clean up the EntryGroup we just created since AddService failed
			a.avServer.EntryGroupFree(entryGroup)
			delete(a.instanceEntryGroups, instanceID)
			return "", fmt.Errorf("failed to add %s service: %w", serviceType, err)
		}
	}

	// Commit the EntryGroup
	if err := entryGroup.Commit(); err != nil {
		// Clean up the EntryGroup we just created since Commit failed
		a.avServer.EntryGroupFree(entryGroup)
		delete(a.instanceEntryGroups, instanceID)
		return "", fmt.Errorf("failed to commit %s service: %w", serviceType, err)
	}

	// Store service data for this instance only after successful commit
	a.instanceStates[instanceID] = &mdnsServiceData{
		Name: serviceName,
		Port: port,
		Txt:  txt,
	}

	// Store instance mapping for cleanup
	a.serviceInstances[instanceID] = &instanceData{
		ServiceType: serviceType,
		ServiceName: serviceName,
		Port:        port,
		Txt:         txt,
	}

	// For _ship._tcp, also store legacy reconnection data for compatibility
	if serviceType == shipZeroConfServiceType {
		a.mdnsServiceData = &mdnsServiceData{
			Name: serviceName,
			Port: port,
			Txt:  txt,
		}
	}

	return instanceID, nil
}

// UnannounceService removes a service instance by its instance ID
func (a *AvahiProvider) UnannounceService(instanceID string) error {
	a.mux.Lock()
	defer a.mux.Unlock()

	// Look up instance data
	_, exists := a.serviceInstances[instanceID]
	if !exists {
		return api.ErrPairingNotActive
	}

	// Shutdown the dedicated EntryGroup for this instance
	if entryGroup, entryExists := a.instanceEntryGroups[instanceID]; entryExists {
		a.avServer.EntryGroupFree(entryGroup)
		delete(a.instanceEntryGroups, instanceID)
	}

	// Clean up instance state
	delete(a.instanceStates, instanceID)

	// Clean up instance mapping
	delete(a.serviceInstances, instanceID)

	return nil
}
