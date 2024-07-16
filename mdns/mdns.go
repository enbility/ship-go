package mdns

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/logging"
	"github.com/enbility/ship-go/util"
	"github.com/holoplot/go-avahi"
)

const shipWebsocketPath = "/ship/"

type MdnsProviderSelection uint

const (
	MdnsProviderSelectionAll            MdnsProviderSelection = iota // Automatically use avahi if available, otherwise use Go native Zeroconf, default
	MdnsProviderSelectionAvahiOnly                                   // Only use avahi
	MdnsProviderSelectionGoZeroConfOnly                              // Only us Go native zeroconf
)

type MdnsManager struct {
	ski string

	// The deviceBrand of the device
	deviceBrand string

	// The device model
	deviceModel string

	// device type
	deviceType string

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

	shutdownOnce sync.Once

	providerSelection MdnsProviderSelection

	mux sync.Mutex
}

func NewMDNS(
	ski, deviceBrand, deviceModel, deviceType, shipIdentifier, serviceName string,
	port int,
	ifaces []string,
	providerSelection MdnsProviderSelection) *MdnsManager {
	m := &MdnsManager{
		ski:               ski,
		deviceBrand:       deviceBrand,
		deviceModel:       deviceModel,
		deviceType:        deviceType,
		identifier:        shipIdentifier,
		serviceName:       serviceName,
		port:              port,
		ifaces:            ifaces,
		providerSelection: providerSelection,
		entries:           make(map[string]*api.MdnsEntry),
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
			ifaceIndexes[i] = int32(iface.Index)
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

	switch m.providerSelection {
	case MdnsProviderSelectionAll:
		// First try avahi, if not available use zerconf
		m.mdnsProvider = NewAvahiProvider(ifaceIndexes)
		if !m.mdnsProvider.CheckAvailability() {
			m.mdnsProvider.Shutdown()

			// Avahi is not availble, use Zeroconf
			m.mdnsProvider = NewZeroconfProvider(ifaces)
			if !m.mdnsProvider.CheckAvailability() {
				return errors.New("No mDNS provider available")
			}
		}
	case MdnsProviderSelectionAvahiOnly:
		// Only use Avahi
		m.mdnsProvider = NewAvahiProvider(ifaceIndexes)
		if !m.mdnsProvider.CheckAvailability() {
			m.mdnsProvider.Shutdown()
			return errors.New("Avahi mDNS provider not available")
		}
	case MdnsProviderSelectionGoZeroConfOnly:
		// Only use Zeroconf
		m.mdnsProvider = NewZeroconfProvider(ifaces)
	}

	// on startup always start mDNS announcement
	if err := m.AnnounceMdnsEntry(); err != nil {
		return err
	}

	m.report = cb

	logging.Log().Debug("mdns: start search")
	go m.mdnsProvider.ResolveEntries(m.processMdnsEntry)

	// catch signals
	go func() {
		signalC := make(chan os.Signal, 1)
		signal.Notify(signalC, os.Interrupt, syscall.SIGTERM)

		<-signalC // wait for signal

		m.Shutdown()
	}()

	return nil
}

// Shutdown all of mDNS
func (m *MdnsManager) Shutdown() {
	m.shutdownOnce.Do(func() {
		m.UnannounceMdnsEntry()

		if m.mdnsProvider == nil {
			return
		}

		m.mdnsProvider.Shutdown()
		m.mdnsProvider = nil
	})
}

// Announces the service to the network via mDNS
// A CEM service should always invoke this on startup
// Any other service should only invoke this whenever it is not connected to a CEM service
func (m *MdnsManager) AnnounceMdnsEntry() error {
	if m.mdnsProvider == nil {
		return nil
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

	logging.Log().Debug("mdns: announce")

	serviceName := m.serviceName

	if err := m.mdnsProvider.Announce(serviceName, m.port, txt); err != nil {
		logging.Log().Debug("mdns: failure announcing service", err)
		return err
	}

	m.isAnnounced = true

	return nil
}

// Stop the mDNS announcement on the network
func (m *MdnsManager) UnannounceMdnsEntry() {
	if !m.isAnnounced || m.mdnsProvider == nil {
		return
	}

	m.mdnsProvider.Unannounce()
	logging.Log().Debug("mdns: stop announcement")

	m.isAnnounced = false
}

func (m *MdnsManager) SetAutoAccept(accept bool) {
	m.autoaccept = accept

	// if announcement is off, don't enforce a new announcement
	if !m.isAnnounced {
		return
	}

	// Update the announcement as autoaccept changed
	if err := m.AnnounceMdnsEntry(); err != nil {
		logging.Log().Debug("mdns: changing mdns entry failed", err)
	}
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
		util.DeepCopy[*api.MdnsEntry](v, newEntry)
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
			return
		}
	}

	txtvers := elements["txtvers"]
	// value of mandatory txtvers has to be 1 or the response be ignored: SHIP 7.3.2
	if txtvers != "1" {
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
		return
	}

	var deviceType, model, brand string

	if _, ok := elements["brand"]; ok {
		brand = elements["brand"]
	}
	if _, ok := elements["type"]; ok {
		deviceType = elements["type"]
	}
	if _, ok := elements["model"]; ok {
		model = elements["model"]
	}

	updated := false

	entry, exists := m.mdnsEntry(ski)

	if remove && exists {
		updated = true
		// remove
		// there will be a remove for each address with avahi, but we'll delete it right away
		m.removeMdnsEntry(ski)
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
			Host:       host,
			Port:       port,
			Addresses:  addresses,
		}
		m.setMdnsEntry(ski, newEntry)

		logging.Log().Debug("ski:", ski, "name:", name, "brand:", brand, "model:", model, "typ:", deviceType, "identifier:", identifier, "register:", register, "host:", host, "port:", port, "addresses:", addresses)
	}

	if m.report == nil || !updated {
		return
	}

	entries := m.copyMdnsEntries()
	go m.report.ReportMdnsEntries(entries)
}

func (m *MdnsManager) RequestMdnsEntries() {
	if m.report == nil {
		return
	}

	entries := m.copyMdnsEntries()
	go m.report.ReportMdnsEntries(entries)
}
