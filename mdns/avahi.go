package mdns

import (
	"fmt"
	"net"
	"sync"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/logging"
	"github.com/godbus/dbus/v5"
	"github.com/holoplot/go-avahi"
)

type AvahiProvider struct {
	ifaceIndexes []int32

	avServer     *avahi.Server
	avEntryGroup *avahi.EntryGroup

	shutdownOnce sync.Once

	// Used to store the service elements for each service, so that we can recall them when a service is removed
	serviceElements map[string]map[string]string

	shutdownChan chan struct{}

	mux sync.Mutex
}

func NewAvahiProvider(ifaceIndexes []int32) *AvahiProvider {
	return &AvahiProvider{
		ifaceIndexes:    ifaceIndexes,
		shutdownChan:    make(chan struct{}),
		serviceElements: make(map[string]map[string]string),
	}
}

var _ api.MdnsProviderInterface = (*AvahiProvider)(nil)

func (a *AvahiProvider) CheckAvailability() bool {
	a.mux.Lock()
	defer a.mux.Unlock()

	dbusConn, err := dbus.SystemBus()
	if err != nil {
		return false
	}

	a.avServer, err = avahi.ServerNew(dbusConn)
	if err != nil {
		return false
	}

	if _, err := a.avServer.GetAPIVersion(); err != nil {
		return false
	}

	avBrowser, err := a.avServer.ServiceBrowserNew(avahi.InterfaceUnspec, avahi.ProtoUnspec, shipZeroConfServiceType, shipZeroConfDomain, 0)
	if err != nil {
		return false
	}

	if avBrowser != nil {
		a.avServer.ServiceBrowserFree(avBrowser)
		return true
	}

	return false
}

func (a *AvahiProvider) Shutdown() {
	a.mux.Lock()
	defer a.mux.Unlock()

	a.shutdownOnce.Do(func() {
		if a.avServer == nil {
			return
		}

		close(a.shutdownChan)

		a.avServer.Close()
		a.avServer = nil
		a.avEntryGroup = nil
	})
}

func (a *AvahiProvider) Announce(serviceName string, port int, txt []string) error {
	a.mux.Lock()
	defer a.mux.Unlock()

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
		err = entryGroup.AddService(iface, avahi.ProtoUnspec, 0, serviceName, shipZeroConfServiceType, shipZeroConfDomain, "", uint16(port), btxt)
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
		return fmt.Errorf("error resolving service: %s error: %s", service.Name, err)
	}

	return a.processAddedService(resolved, cb)
}

func (a *AvahiProvider) processRemovedService(service avahi.Service, cb api.MdnsResolveCB) error {

	// get the elements for the service
	elements := a.serviceElements[getServiceUniqueKey(service)]

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

	address := net.ParseIP(service.Address)
	// if the address can not be used, ignore the entry
	if address == nil || address.IsUnspecified() {
		return fmt.Errorf("service provides unusable address: %s", service.Name)
	}

	// Ignore IPv6 addresses for now
	if address.To4() == nil {
		return fmt.Errorf("no IPv4 addresses available %s", service.Name)
	}

	// add the elements to the map
	a.serviceElements[getServiceUniqueKey(service)] = elements

	cb(elements, service.Name, service.Host, []net.IP{address}, int(service.Port), false)

	return nil
}

// Create a unique key for a ship service
func getServiceUniqueKey(service avahi.Service) string {
	return fmt.Sprintf("%s-%s-%s-%d-%d", service.Name, service.Type, service.Domain, service.Protocol, service.Interface)
}
