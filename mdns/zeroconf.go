package mdns

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/logging"
	"github.com/enbility/zeroconf/v2"
)

// ZeroconfServerInterface abstracts zeroconf.Server for testing
type ZeroconfServerInterface interface {
	Shutdown()
	TTL(uint32)
}

// ZeroconfFactoryInterface abstracts zeroconf.Register for testing
type ZeroconfFactoryInterface interface {
	Register(serviceName, serviceType, domain string, port int, txt []string, ifaces []net.Interface) (ZeroconfServerInterface, error)
}

// DefaultZeroconfFactory implements ZeroconfFactoryInterface using real zeroconf
type DefaultZeroconfFactory struct{}

func (f *DefaultZeroconfFactory) Register(serviceName, serviceType, domain string, port int, txt []string, ifaces []net.Interface) (ZeroconfServerInterface, error) {
	return zeroconf.Register(serviceName, serviceType, domain, port, txt, ifaces, zeroconf.TTL(120))
}

type ZeroconfProvider struct {
	ifaces []net.Interface

	ctx    context.Context
	cancel context.CancelFunc

	// One server per service instance - fixes architectural flaw
	instanceServers map[string]ZeroconfServerInterface // instanceID -> dedicated server

	// Instance management for the new interface
	instanceCounter  int
	serviceInstances map[string]*zeroconfInstanceData // instanceID -> service data

	// Track if the provider is already started to prevent duplicate goroutines
	isStarted bool

	// Factory for creating ZeroconfServerInterface instances (injectable for testing)
	serverFactory ZeroconfFactoryInterface

	mux sync.Mutex
}

// zeroconfInstanceData holds service instance information for cleanup
type zeroconfInstanceData struct {
	ServiceType string
	ServiceName string
	Port        int
	Txt         []string
}

func NewZeroconfProvider(ifaces []net.Interface) *ZeroconfProvider {
	return &ZeroconfProvider{
		ifaces:           ifaces,
		instanceServers:  make(map[string]ZeroconfServerInterface),
		instanceCounter:  0,
		serviceInstances: make(map[string]*zeroconfInstanceData),
		serverFactory:    &DefaultZeroconfFactory{},
	}
}

// ZeroconfProvider implements the standard MdnsProviderInterface
// For multi-service support, wrap with MultiServiceAdapter
var _ api.MdnsProviderInterface = (*ZeroconfProvider)(nil)

func (z *ZeroconfProvider) Start(pairingMode api.PairingMode, autoReconnect bool, cb api.MdnsResolveCB) bool {
	z.mux.Lock()
	defer z.mux.Unlock()

	// Prevent duplicate Start() calls from creating multiple goroutines
	if z.isStarted {
		logging.Log().Debug("mdns: ZeroconfProvider already started, ignoring duplicate Start() call")
		return true
	}

	logging.Log().Debug("mdns: using zeroconf")

	z.isStarted = true
	go z.chanListener(pairingMode, cb)

	return true
}

func (z *ZeroconfProvider) Shutdown() {
	// Unannounce all service instances
	z.mux.Lock()
	instancesToRemove := make([]string, 0, len(z.serviceInstances))
	for instanceID := range z.serviceInstances {
		instancesToRemove = append(instancesToRemove, instanceID)
	}
	z.mux.Unlock()

	for _, instanceID := range instancesToRemove {
		_ = z.UnannounceService(instanceID)
	}

	z.mux.Lock()
	defer z.mux.Unlock()

	if z.cancel != nil {
		z.cancel()
		z.cancel = nil
	}

	// Reset the started flag so the provider can be restarted if needed
	z.isStarted = false
}

/* Enhanced Provider Interface Implementation - TDD Stubs */

// AnnounceService announces a specific service type and returns an instance ID
func (z *ZeroconfProvider) AnnounceService(serviceType, serviceName string, port int, txt []string) (string, error) {
	z.mux.Lock()
	defer z.mux.Unlock()

	// Determine domain based on service type
	domain := "local."
	if serviceType == shipZeroConfServiceType {
		domain = shipZeroConfDomain
	}

	// Generate unique instance ID first
	z.instanceCounter++
	instanceID := strconv.Itoa(z.instanceCounter)

	// Create dedicated server for this specific instance
	server, err := z.serverFactory.Register(serviceName, serviceType, domain, port, txt, z.ifaces)
	if err != nil {
		return "", fmt.Errorf("failed to register %s service: %w", serviceType, err)
	}

	// Store dedicated server for this instance - no sharing
	z.instanceServers[instanceID] = server

	// Store instance data for cleanup
	z.serviceInstances[instanceID] = &zeroconfInstanceData{
		ServiceType: serviceType,
		ServiceName: serviceName,
		Port:        port,
		Txt:         txt,
	}

	return instanceID, nil
}

// UnannounceService removes a service instance by its instance ID
func (z *ZeroconfProvider) UnannounceService(instanceID string) error {
	z.mux.Lock()
	defer z.mux.Unlock()

	// Look up instance data
	_, exists := z.serviceInstances[instanceID]
	if !exists {
		return api.ErrPairingNotActive
	}

	// Shutdown the dedicated server for this instance
	if server, serverExists := z.instanceServers[instanceID]; serverExists {
		server.Shutdown()
		delete(z.instanceServers, instanceID)
	}

	// Clean up instance data
	delete(z.serviceInstances, instanceID)

	return nil
}

func (z *ZeroconfProvider) chanListener(pairingMode api.PairingMode, cb api.MdnsResolveCB) {
	zcEntries := make(chan *zeroconf.ServiceEntry)
	zcRemoved := make(chan *zeroconf.ServiceEntry)

	// Separate channels for pairing services
	zcPairingEntries := make(chan *zeroconf.ServiceEntry)
	zcPairingRemoved := make(chan *zeroconf.ServiceEntry)

	z.mux.Lock()
	// for Zeroconf we need a context
	z.ctx, z.cancel = context.WithCancel(context.Background())
	z.mux.Unlock()

	// Browse for _ship._tcp services
	go func() {
		_ = zeroconf.Browse(z.ctx, shipZeroConfServiceType, shipZeroConfDomain, zcEntries, zcRemoved, zeroconf.SelectIfaces(z.ifaces))
	}()

	// Also browse for _shippairing._tcp services
	if pairingMode == api.PairingModeListener || pairingMode == api.PairingModeBoth {
		go func() {
			_ = zeroconf.Browse(z.ctx, shipPairingZeroConfServiceType, shipZeroConfDomain, zcPairingEntries, zcPairingRemoved, zeroconf.SelectIfaces(z.ifaces))
		}()
	}

	for {
		select {
		case <-z.ctx.Done():
			return
		case service := <-zcRemoved:
			// Zeroconf has issues with merging mDNS data and sometimes reports incomplete records
			if service == nil || len(service.Text) == 0 {
				continue
			}

			elements := parseTxt(service.Text)

			addresses := service.AddrIPv4
			cb(elements, service.Instance, service.HostName, service.Service, addresses, service.Port, true)

		case service := <-zcEntries:
			// Zeroconf has issues with merging mDNS data and sometimes reports incomplete records
			if service == nil || len(service.Text) == 0 {
				continue
			}

			elements := parseTxt(service.Text)

			addresses := service.AddrIPv4
			addresses = append(addresses, service.AddrIPv6...)
			cb(elements, service.Instance, service.HostName, service.Service, addresses, service.Port, false)

		case service := <-zcPairingRemoved:
			// Handle removed pairing services
			if service == nil || len(service.Text) == 0 {
				continue
			}

			elements := parseTxt(service.Text)
			addresses := service.AddrIPv4
			// Pass _shippairing._tcp as service type to ensure proper routing
			cb(elements, service.Instance, service.HostName, shipPairingZeroConfServiceType, addresses, service.Port, true)

		case service := <-zcPairingEntries:
			// Handle discovered pairing services
			if service == nil || len(service.Text) == 0 {
				continue
			}

			elements := parseTxt(service.Text)
			addresses := service.AddrIPv4
			addresses = append(addresses, service.AddrIPv6...)
			// Pass _shippairing._tcp as service type to ensure proper routing
			cb(elements, service.Instance, service.HostName, shipPairingZeroConfServiceType, addresses, service.Port, false)
		}
	}
}
