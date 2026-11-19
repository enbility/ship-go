package mdns

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/logging"
	"github.com/enbility/zeroconf/v3"
	zapi "github.com/enbility/zeroconf/v3/api"
)

// ZeroconfServerInterface abstracts zeroconf.Server for testing.
// Note: zeroconf/v3 no longer exposes TTL as a method on *Server (it is a
// ServerOption applied at registration time), so it is not part of this interface.
type ZeroconfServerInterface interface {
	Shutdown()
}

// ZeroconfFactoryInterface abstracts zeroconf.Register for testing
type ZeroconfFactoryInterface interface {
	Register(serviceName, serviceType, domain string, port int, txt []string, ifaces []net.Interface) (ZeroconfServerInterface, error)
}

// DefaultZeroconfFactory implements ZeroconfFactoryInterface using real zeroconf.
// The connection factory and interface provider are passed through to zeroconf so
// they can be replaced with mocks for testing.
type DefaultZeroconfFactory struct {
	connFactory       zapi.ConnectionFactory
	interfaceProvider zapi.InterfaceProvider
}

func (f *DefaultZeroconfFactory) Register(serviceName, serviceType, domain string, port int, txt []string, ifaces []net.Interface) (ZeroconfServerInterface, error) {
	// Set TTL to 2 minutes as defined in SHIP chapter 7
	opts := []zeroconf.ServerOption{
		zeroconf.TTL(120),
		zeroconf.WithServerConnFactory(f.connFactory),
		zeroconf.WithServerInterfaceProvider(f.interfaceProvider),
	}
	return zeroconf.Register(serviceName, serviceType, domain, port, txt, ifaces, opts...)
}

type ZeroconfProvider struct {
	ifaces            []net.Interface
	connFactory       zapi.ConnectionFactory
	interfaceProvider zapi.InterfaceProvider

	ctx    context.Context
	cancel context.CancelFunc

	// One server per service instance - fixes architectural flaw
	instanceServers map[string]ZeroconfServerInterface // instanceID -> dedicated server

	// Instance management for the new interface
	instanceCounter  int
	serviceInstances map[string]*zeroconfInstanceData // instanceID -> service data

	// Track if the provider is already started to prevent duplicate goroutines
	isStarted    bool
	listenerDone chan struct{} // closed when chanListener exits

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

// NewZeroconfProvider creates a ZeroconfProvider.
//
// The connection factory and interface provider can be replaced with mocks for testing.
// For normal operation no special connection factory or interface provider need to be
// passed (simply pass nil for both) and zeroconf's defaults are used.
func NewZeroconfProvider(ifaces []net.Interface, connFactory *zapi.ConnectionFactory, interfaceProvider *zapi.InterfaceProvider) *ZeroconfProvider {
	zConnFactory := zeroconf.NewConnectionFactory()
	if connFactory != nil {
		zConnFactory = *connFactory
	}

	zInterfaceProvider := zeroconf.NewInterfaceProvider()
	if interfaceProvider != nil {
		zInterfaceProvider = *interfaceProvider
	}

	return &ZeroconfProvider{
		ifaces:            ifaces,
		connFactory:       zConnFactory,
		interfaceProvider: zInterfaceProvider,
		instanceServers:   make(map[string]ZeroconfServerInterface),
		instanceCounter:   0,
		serviceInstances:  make(map[string]*zeroconfInstanceData),
		serverFactory: &DefaultZeroconfFactory{
			connFactory:       zConnFactory,
			interfaceProvider: zInterfaceProvider,
		},
	}
}

// UpdateInterfaces updates the interface list in a thread-safe manner.
// ZeroconfProvider uses ifaces; ifaceIndexes is ignored.
func (z *ZeroconfProvider) UpdateInterfaces(ifaces []net.Interface, _ []int32) {
	z.mux.Lock()
	defer z.mux.Unlock()
	z.ifaces = ifaces
}

// getIfaces returns a copy of the interface list in a thread-safe manner
func (z *ZeroconfProvider) getIfaces() []net.Interface {
	z.mux.Lock()
	defer z.mux.Unlock()
	// Return a copy to avoid race conditions
	ifacesCopy := make([]net.Interface, len(z.ifaces))
	copy(ifacesCopy, z.ifaces)
	return ifacesCopy
}

// ZeroconfProvider implements the standard MdnsProviderInterface
// For multi-service support, wrap with MultiServiceAdapter
var _ api.MdnsProviderInterface = (*ZeroconfProvider)(nil)

func (z *ZeroconfProvider) Start(pairingMode api.PairingMode, autoReconnect bool, cb api.MdnsResolveCB) bool {
	z.mux.Lock()
	if z.isStarted {
		z.mux.Unlock()
		logging.Log().Debug("mdns: ZeroconfProvider already started, ignoring duplicate Start()")
		return true
	}
	z.isStarted = true
	z.listenerDone = make(chan struct{})
	// Create the context here, under the lock, so Shutdown() can always cancel
	// it even if the goroutine hasn't started running yet.
	z.ctx, z.cancel = context.WithCancel(context.Background())
	z.mux.Unlock()

	go z.chanListener(pairingMode, cb)

	logging.Log().Debug("mdns: using zeroconf")

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
	if z.cancel != nil {
		z.cancel()
		z.cancel = nil
	}

	// Reset the started flag so the provider can be restarted if needed
	z.isStarted = false
	done := z.listenerDone
	z.mux.Unlock()

	// Wait for the chanListener goroutine to finish before returning,
	// so a subsequent Start() won't race with the old goroutine.
	if done != nil {
		select {
		case <-done:
		case <-time.After(6 * time.Second):
			logging.Log().Debug("mdns: zeroconf chanListener did not exit in time")
		}
	}
}

/* Enhanced Provider Interface Implementation - TDD Stubs */
// AnnounceService announces a specific service type and returns an instance ID
func (z *ZeroconfProvider) AnnounceService(serviceType, serviceName string, port int, txt []string) (string, error) {
	// Determine domain based on service type
	domain := "local."
	if serviceType == shipZeroConfServiceType {
		domain = shipZeroConfDomain
	}
	z.mux.Lock()
	// Generate unique instance ID first
	z.instanceCounter++
	instanceID := strconv.Itoa(z.instanceCounter)
	z.mux.Unlock()

	// Create dedicated server for this specific instance
	ifaces := z.getIfaces()
	server, err := z.serverFactory.Register(serviceName, serviceType, domain, port, txt, ifaces)
	if err != nil {
		return "", fmt.Errorf("failed to register %s service: %w", serviceType, err)
	}

	z.mux.Lock()
	defer z.mux.Unlock()

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
	// Capture the done channel and context that belong to THIS goroutine's lifecycle.
	// Doing this at entry (not inside the defer) ensures that even if a concurrent
	// Shutdown()+Start() replaces z.listenerDone or z.ctx, we always signal the right
	// channel and listen to the right context — preventing a double-close panic.
	z.mux.Lock()
	myDone := z.listenerDone
	myCtx := z.ctx
	z.mux.Unlock()

	defer func() {
		close(myDone)
	}()

	// Buffered channels prevent the Browse goroutine from deadlocking on shutdown.
	// When ctx is cancelled, chanListener exits and stops reading. Without a buffer,
	// Browse's mainloop blocks forever on a channel send and leaks. With a buffer,
	// the pending send completes into the buffer, mainloop loops back to its select,
	// picks ctx.Done(), and cleans up normally.
	zcEntries := make(chan *zeroconf.ServiceEntry, 2)
	zcRemoved := make(chan *zeroconf.ServiceEntry, 2)

	// Separate channels for pairing services
	zcPairingEntries := make(chan *zeroconf.ServiceEntry, 2)
	zcPairingRemoved := make(chan *zeroconf.ServiceEntry, 2)

	// Browse for _ship._tcp services
	// Get a thread-safe copy of interfaces
	ifaces := z.getIfaces()
	clientOpts := []zeroconf.ClientOption{
		zeroconf.SelectIfaces(ifaces),
		zeroconf.WithClientConnFactory(z.connFactory),
		zeroconf.WithClientInterfaceProvider(z.interfaceProvider),
	}
	go func() {
		_ = zeroconf.Browse(myCtx, shipZeroConfServiceType, shipZeroConfDomain, zcEntries, zcRemoved, clientOpts...)
	}()

	// Also browse for _shippairing._tcp services
	if pairingMode == api.PairingModeListener || pairingMode == api.PairingModeBoth {
		go func() {
			_ = zeroconf.Browse(myCtx, shipPairingZeroConfServiceType, shipZeroConfDomain, zcPairingEntries, zcPairingRemoved, clientOpts...)
		}()
	}

	for {
		select {
		case <-myCtx.Done():
			return
		case service := <-zcRemoved:
			// Zeroconf has issues with merging mDNS data and sometimes reports incomplete records
			if service == nil || len(service.Text) == 0 {
				continue
			}

			elements, uniqueKeys := parseTxt(service.Text)

			if !uniqueKeys {
				logging.Log().Errorf("duplicate keys in txt record: %v", service.Text)
				continue
			}

			if !validateTxtversOrder(service.Text) {
				logging.Log().Errorf("invalid order - must lead with txtvers: %v", service.Text)
				continue
			}

			addresses := service.AddrIPv4
			cb(elements, service.Instance, service.HostName, service.Service, addresses, service.Port, true)

		case service := <-zcEntries:
			// Zeroconf has issues with merging mDNS data and sometimes reports incomplete records
			if service == nil || len(service.Text) == 0 {
				continue
			}

			elements, uniqueKeys := parseTxt(service.Text)

			if !uniqueKeys {
				logging.Log().Errorf("duplicate keys in txt record: %v", service.Text)
				continue
			}

			if !validateTxtversOrder(service.Text) {
				logging.Log().Errorf("invalid order - must lead with txtvers: %v", service.Text)
				continue
			}

			addresses := service.AddrIPv4
			addresses = append(addresses, service.AddrIPv6...)
			cb(elements, service.Instance, service.HostName, service.Service, addresses, service.Port, false)

		case service := <-zcPairingRemoved:
			// Handle removed pairing services
			if service == nil {
				continue
			}

			elements, uniqueKeys := parseTxt(service.Text)

			if !uniqueKeys {
				logging.Log().Errorf("duplicate keys in txt record: %v", service.Text)
			}

			addresses := service.AddrIPv4
			// Pass _shippairing._tcp as service type to ensure proper routing
			cb(elements, service.Instance, service.HostName, shipPairingZeroConfServiceType, addresses, service.Port, true)

		case service := <-zcPairingEntries:
			// Handle discovered pairing services
			if service == nil || len(service.Text) == 0 {
				continue
			}

			elements, uniqueKeys := parseTxt(service.Text)

			if !uniqueKeys {
				logging.Log().Errorf("duplicate keys in txt record: %v", service.Text)
				continue
			}

			addresses := service.AddrIPv4
			addresses = append(addresses, service.AddrIPv6...)
			// Pass _shippairing._tcp as service type to ensure proper routing
			cb(elements, service.Instance, service.HostName, shipPairingZeroConfServiceType, addresses, service.Port, false)
		}
	}
}
