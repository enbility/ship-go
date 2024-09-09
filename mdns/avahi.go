package mdns

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/enbility/go-avahi"
	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/logging"
)

type AvahiProvider struct {
	ifaceIndexes []int32

	avServer     *avahi.Server
	avEntryGroup *avahi.EntryGroup

	autoReconnect  bool
	manualShutdown bool

	announceServiceName string
	announcePort        int
	announceTxt         []string

	resolveCB api.MdnsResolveCB

	// Used to store the service elements for each service, so that we can recall them when a service is removed
	serviceElements map[string]map[string]string

	shutdownChan chan struct{}

	mux   sync.Mutex
	muxEl sync.RWMutex // used for serviceElements
}

func NewAvahiProvider(ifaceIndexes []int32) *AvahiProvider {
	return &AvahiProvider{
		ifaceIndexes:    ifaceIndexes,
		serviceElements: make(map[string]map[string]string),
	}
}

var _ api.MdnsProviderInterface = (*AvahiProvider)(nil)

func (a *AvahiProvider) Start(autoReconnect bool) bool {
	a.mux.Lock()
	defer a.mux.Unlock()

	a.shutdownChan = make(chan struct{})
	a.autoReconnect = autoReconnect
	a.manualShutdown = false

	var err error
	a.avServer, err = avahi.ServerNew(a.avahiCallback)
	if err != nil {
		return false
	}
	a.avServer.Start()

	if _, err := a.avServer.GetAPIVersion(); err != nil {
		a.avServer.Shutdown()
		return false
	}

	avBrowser, err := a.avServer.ServiceBrowserNew(avahi.InterfaceUnspec, avahi.ProtoUnspec, shipZeroConfServiceType, shipZeroConfDomain, 0)
	if err != nil {
		a.avServer.Shutdown()
		return false
	}

	if avBrowser != nil {
		a.avServer.ServiceBrowserFree(avBrowser)

		// autoReconnect is only called with false if the systems does not know if
		// avahi should be used in the first place.
		// but if it was found and therefor being used, it should automatically reconnect once disconnected
		if !autoReconnect {
			a.autoReconnect = true
		}

		return true
	}

	a.avServer.Shutdown()
	return false
}

func (a *AvahiProvider) avahiCallback(event avahi.Event) {
	logging.Log().Debug("mdns: avahi - disconnected")

	a.mux.Lock()
	// if there is a manual shutdown, we do not want to reconnect
	if a.manualShutdown || !a.autoReconnect || event != avahi.Disconnected {
		a.mux.Unlock()
		return
	}

	// the server was shutdown, set it to nil so we don't try to call free functions
	// on shutting down a currently running resolve
	a.avServer = nil
	cb := a.resolveCB
	serviceName := a.announceServiceName
	port := a.announcePort
	txt := a.announceTxt
	a.mux.Unlock()

	if cb != nil {
		// stop the currently running resolve
		a.shutdownChan <- struct{}{}
	}

	// try to reconnect until successull
	go func() {
		for {
			<-time.After(time.Second)

			if !a.Start(true) {
				continue
			}

			logging.Log().Debug("mdns: avahi - reconnected")

			if serviceName != "" {
				if err := a.Announce(serviceName, port, txt); err != nil {
					logging.Log().Debug("mdns: avahi - error re-announcing service:", err)
				}
			}

			if cb != nil {
				// restart the resolve
				a.ResolveEntries(cb)
			}

			return
		}
	}()
}

func (a *AvahiProvider) Shutdown() {
	a.mux.Lock()
	a.manualShutdown = true

	if a.avServer == nil {
		a.mux.Unlock()
		return
	}

	// when shutting down on purpose, do not try to reconnect
	a.autoReconnect = false
	cb := a.resolveCB
	a.mux.Unlock()

	if cb != nil {
		a.shutdownChan <- struct{}{}
	}
	close(a.shutdownChan)

	// Unannounce the service
	a.Unannounce()

	a.mux.Lock()
	defer a.mux.Unlock()

	a.avServer.Shutdown()
	a.avServer = nil
	a.avEntryGroup = nil
}

func (a *AvahiProvider) Announce(serviceName string, port int, txt []string) error {
	a.mux.Lock()
	defer a.mux.Unlock()

	// store the data for reconnection
	a.announceServiceName = serviceName
	a.announcePort = port
	a.announceTxt = txt

	logging.Log().Debug("mdns: using avahi")

	var btxt [][]byte
	for _, t := range txt {
		btxt = append(btxt, []byte(t))
	}

	entryGroup, err := a.avServer.EntryGroupNew()
	if err != nil {
		return err
	}

	for _, iface := range a.ifaceIndexes {
		// conversion is safe, as port values are always positive
		err = entryGroup.AddService(iface, avahi.ProtoUnspec, 0, serviceName, shipZeroConfServiceType, shipZeroConfDomain, "", uint16(port), btxt) // #nosec G115
		if err != nil {
			return err
		}
	}

	err = entryGroup.Commit()
	if err != nil {
		return err
	}

	a.avEntryGroup = entryGroup

	return nil
}

func (a *AvahiProvider) Unannounce() {
	a.mux.Lock()
	defer a.mux.Unlock()

	// clean up the reconnection data
	a.announceServiceName = ""
	a.announcePort = 0
	a.announceTxt = nil

	if a.avEntryGroup == nil {
		return
	}

	a.avServer.EntryGroupFree(a.avEntryGroup)
	a.avEntryGroup = nil
}

func (a *AvahiProvider) ResolveEntries(callback api.MdnsResolveCB) {
	a.mux.Lock()

	var err error

	var avBrowser *avahi.ServiceBrowser

	if a.avServer == nil {
		a.mux.Unlock()
		return
	}

	// instead of limiting search on specific allowed interfaces, we allow all and filter the results
	if avBrowser, err = a.avServer.ServiceBrowserNew(avahi.InterfaceUnspec, avahi.ProtoUnspec, shipZeroConfServiceType, shipZeroConfDomain, 0); err != nil {
		logging.Log().Debug("mdns: error setting up avahi browser:", err)
		a.mux.Unlock()
		return
	}

	if avBrowser == nil {
		logging.Log().Debug("mdns: avahi browser is not available")
		a.mux.Unlock()
		return
	}

	// cache the callback for reconnection
	a.resolveCB = callback

	a.mux.Unlock()

	defer func() {
		a.mux.Lock()

		if a.avServer != nil {
			a.avServer.ServiceBrowserFree(avBrowser)
		}

		a.mux.Unlock()
	}()

	for {
		select {
		case <-a.shutdownChan:
			return
		case service := <-avBrowser.AddChannel:
			if err := a.processService(service, false, callback); err != nil {
				logging.Log().Debug("mdns: avahi -", err)
			}
		case service := <-avBrowser.RemoveChannel:
			if err := a.processService(service, true, callback); err != nil {
				logging.Log().Debug("mdns: avahi -", err)
			}
		}
	}
}

// process an avahi mDNS service
// as avahi returns a service per interface, we need to combine them
func (a *AvahiProvider) processService(service avahi.Service, remove bool, cb api.MdnsResolveCB) error {
	// check if the service is within the allowed list
	allow := false
	if len(a.ifaceIndexes) == 1 && a.ifaceIndexes[0] == avahi.InterfaceUnspec {
		allow = true
	} else {
		for _, iface := range a.ifaceIndexes {
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
