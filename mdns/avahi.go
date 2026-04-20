package mdns

import (
	"fmt"
	"math/rand"
	"net"
	"slices"
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

type AvahiProvider struct {
	ifaceIndexes []int32

	avServer     avahi.ServerInterface
	avEntryGroup avahi.EntryGroupInterface
	avBrowser    avahi.ServiceBrowserInterface

	autoReconnect   bool
	manualShutdown  bool
	setupSuccessful bool
	listenerRunning bool

	mdnsServiceData *mdnsServiceData

	resolveCB api.MdnsResolveCB

	// Used to store the service elements for each service, so that we can recall them when a service is removed
	serviceElements map[string]map[string]string

	shutdownChan                      chan struct{}
	addServiceChan, removeServiceChan chan avahi.Service

	mux          sync.Mutex
	announceMux  sync.Mutex   // serializes Announce calls and excludes Shutdown's teardown phase
	muxEl        sync.RWMutex // used for serviceElements

	// Prevent multiple reconnection goroutines
	reconnectInProgress bool
	reconnectMux        sync.Mutex
}

func NewAvahiProvider(ifaceIndexes []int32) *AvahiProvider {
	return &AvahiProvider{
		avServer:        avahi.ServerNew(),
		setupSuccessful: false,
		ifaceIndexes:    ifaceIndexes,
		serviceElements: make(map[string]map[string]string),
	}
}

// UpdateInterfaces updates the interface indexes in a thread-safe manner.
// AvahiProvider uses ifaceIndexes; ifaces is ignored.
func (a *AvahiProvider) UpdateInterfaces(_ []net.Interface, ifaceIndexes []int32) {
	a.mux.Lock()
	defer a.mux.Unlock()
	a.ifaceIndexes = ifaceIndexes
}

// getIfaceIndexes returns a copy of the interface indexes in a thread-safe manner
func (a *AvahiProvider) getIfaceIndexes() []int32 {
	a.mux.Lock()
	defer a.mux.Unlock()
	// Return a copy to avoid race conditions
	indexesCopy := make([]int32, len(a.ifaceIndexes))
	copy(indexesCopy, a.ifaceIndexes)
	return indexesCopy
}

var _ api.MdnsProviderInterface = (*AvahiProvider)(nil)

func (a *AvahiProvider) Start(autoReconnect bool, cb api.MdnsResolveCB) bool {
	a.mux.Lock()
	defer a.mux.Unlock()

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
	avBrowser, err := a.avServer.ServiceBrowserNew(a.addServiceChan, a.removeServiceChan, avahi.InterfaceUnspec, avahi.ProtoUnspec, shipZeroConfServiceType, shipZeroConfDomain, 0)
	if err != nil || avBrowser == nil {
		a.avServer.Shutdown()
		return false
	}

	a.avBrowser = avBrowser

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

	// Wait for any in-flight Announce to finish before tearing down the
	// server. Without this, an Announce that already passed its snapshot
	// phase could re-acquire a.mux after Unannounce and store a zombie
	// entry group referencing the shut-down server.
	a.announceMux.Lock()
	defer a.announceMux.Unlock()

	// Unannounce the service
	a.unannounce()

	a.mux.Lock()
	defer a.mux.Unlock()

	a.avServer.Shutdown()
	a.avEntryGroup = nil
}

func (a *AvahiProvider) Announce(serviceName string, port int, txt []string) error {
	logging.Log().Debug("mdns: using avahi")

	// Serialize concurrent Announce calls so that only one entry group
	// is created and committed at a time. Without this, two concurrent
	// callers could each commit a group on the Avahi daemon and then
	// race to update struct state, orphaning the loser's group.
	a.announceMux.Lock()
	defer a.announceMux.Unlock()

	var btxt [][]byte
	for _, t := range txt {
		btxt = append(btxt, []byte(t))
	}

	// Compare-and-retry loop: snapshot ifaceIndexes, perform DBus calls
	// without holding a.mux (to avoid deadlock with chanListener), then
	// verify the snapshot is still current. If interfaces changed during
	// the DBus phase, discard the work and retry once with fresh data.
	// The announceMux guarantees no external Announce can interleave, so
	// a single retry suffices — further changes are left to the existing
	// reannounceWithNewInterfaces mechanism.
	for attempt := 0; ; attempt++ {
		// Snapshot ifaceIndexes under the lock, then release immediately.
		// DBus calls must not execute while holding a.mux: chanListener
		// (the goroutine that consumes the DBus signal stream delivering
		// replies to these calls) needs a.mux via processService →
		// getIfaceIndexes. Holding a.mux across a DBus round-trip blocks
		// chanListener, preventing the reply from arriving — hard deadlock.
		a.mux.Lock()
		if a.manualShutdown {
			a.mux.Unlock()
			return fmt.Errorf("mdns: avahi provider is shut down")
		}
		ifaceIndexes := make([]int32, len(a.ifaceIndexes))
		copy(ifaceIndexes, a.ifaceIndexes)
		a.mux.Unlock()

		// All DBus calls happen without holding a.mux.
		newEntryGroup, err := a.avServer.EntryGroupNew()
		if err != nil {
			return err
		}

		for _, iface := range ifaceIndexes {
			// conversion is safe, as port values are always positive
			err = newEntryGroup.AddService(iface, avahi.ProtoUnspec, 0, serviceName, shipZeroConfServiceType, shipZeroConfDomain, "", uint16(port), btxt) // #nosec G115
			if err != nil {
				a.avServer.EntryGroupFree(newEntryGroup)
				return err
			}
		}

		err = newEntryGroup.Commit()
		if err != nil {
			a.avServer.EntryGroupFree(newEntryGroup)
			return err
		}

		// Re-acquire the lock to update struct state only.
		// Only store the data for reconnection after a successful commit,
		// so avahiCallback never re-announces with parameters that were
		// never successfully committed.
		a.mux.Lock()

		// Shutdown may have set manualShutdown while we were performing
		// DBus calls without holding a.mux. Shutdown is blocked on
		// announceMux so the server is still alive, but storing a new
		// entry group is pointless — Shutdown's unannounce will free it
		// immediately. Short-circuit here to avoid the unnecessary state
		// mutation.
		if a.manualShutdown {
			a.mux.Unlock()
			a.avServer.EntryGroupFree(newEntryGroup)
			return fmt.Errorf("mdns: avahi provider is shut down")
		}

		// Only retry once to avoid infinite loops under interface churn.
		// On the second attempt we proceed regardless — the MdnsManager's
		// reannounceWithNewInterfaces cycle will reconcile any further drift.
		if attempt < 1 && !slices.Equal(ifaceIndexes, a.ifaceIndexes) {
			// Interfaces changed during DBus calls; discard and retry once
			// with fresh data.
			a.mux.Unlock()
			a.avServer.EntryGroupFree(newEntryGroup)
			continue
		}

		a.mdnsServiceData = &mdnsServiceData{
			Name: serviceName,
			Port: port,
			Txt:  txt,
		}

		// Free the old entry group AFTER the new one is committed.
		// This avoids a window where the service is completely unannounced,
		// which would cause remote devices to think we left the network.
		oldEntryGroup := a.avEntryGroup
		a.avEntryGroup = newEntryGroup

		a.mux.Unlock()

		if oldEntryGroup != nil {
			a.avServer.EntryGroupFree(oldEntryGroup)
		}

		return nil
	}
}

func (a *AvahiProvider) Unannounce() {
	// Serialize with Announce so we don't race its DBus phase.
	// Without this, Unannounce can clear avEntryGroup while Announce
	// is between Commit and storing the new group — the caller
	// thinks the service is unannounced, but Announce re-stores it.
	a.announceMux.Lock()
	defer a.announceMux.Unlock()
	a.unannounce()
}

// unannounce does the actual work without acquiring announceMux.
// Called by Unannounce (which holds announceMux) and Shutdown
// (which also holds announceMux).
func (a *AvahiProvider) unannounce() {
	a.mux.Lock()

	// clean up the reconnection data
	a.mdnsServiceData = nil

	entryGroup := a.avEntryGroup
	a.avEntryGroup = nil

	// Release the lock before freeing the entry group, as
	// EntryGroupFree may block on dBUS.
	a.mux.Unlock()

	if entryGroup != nil {
		a.avServer.EntryGroupFree(entryGroup)
	}
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
	maxDelay := 30 * time.Second  // Maximum 30 seconds between attempts
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

		logging.Log().Debugf("mdns: avahi - reconnection attempt %d (delay: %v)", attempt, currentDelay)

		if !a.Start(true, cb) {
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

		if serviceData != nil {
			if err := a.Announce(serviceData.Name, serviceData.Port, serviceData.Txt); err != nil {
				logging.Log().Debug("mdns: avahi - error re-announcing service:", err)
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

	cb(elements, service.Name, service.Host, nil, -1, true)

	return nil
}

func (a *AvahiProvider) processAddedService(service avahi.Service, cb api.MdnsResolveCB) error {
	// convert [][]byte to []string manually
	var txt []string
	for _, element := range service.Txt {
		txt = append(txt, string(element))
	}
	elements := parseTxt(txt)

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

	cb(elements, service.Name, service.Host, []net.IP{address}, int(service.Port), false)

	return nil
}

// Create a unique key for a ship service
func getServiceUniqueKey(service avahi.Service) string {
	return fmt.Sprintf("%s-%s-%s-%d-%d", service.Name, service.Type, service.Domain, service.Protocol, service.Interface)
}
