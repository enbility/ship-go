package mdns

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/logging"
	"github.com/enbility/zeroconf/v3"
	zapi "github.com/enbility/zeroconf/v3/api"
)

type ZeroconfProvider struct {
	ifaces            []net.Interface
	connFactory       zapi.ConnectionFactory
	interfaceProvider zapi.InterfaceProvider

	zc *zeroconf.Server

	ctx    context.Context
	cancel context.CancelFunc

	isStarted    bool
	listenerDone chan struct{} // closed when chanListener exits

	mux sync.Mutex
}

// The connection factory and interface provider can be replaced with mocks for testing
//
// For normal operation no special connection factory or interface provider need to be passed (simply pass nil)
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
		ifaces: ifaces,
		connFactory: zConnFactory,
		interfaceProvider: zInterfaceProvider,
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

var _ api.MdnsProviderInterface = (*ZeroconfProvider)(nil)

func (z *ZeroconfProvider) Start(autoReconnect bool, cb api.MdnsResolveCB) bool {
	z.mux.Lock()
	if z.isStarted {
		z.mux.Unlock()
		logging.Log().Debug("mdns: ZeroconfProvider already started, ignoring duplicate Start()")
		return true
	}
	z.isStarted = true
	z.listenerDone = make(chan struct{})
	z.mux.Unlock()

	go z.chanListener(cb)

	return true
}

func (z *ZeroconfProvider) Shutdown() {
	z.Unannounce()

	z.mux.Lock()
	if z.cancel != nil {
		z.cancel()
	}

	done := z.listenerDone
	z.isStarted = false
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

func (z *ZeroconfProvider) Announce(serviceName string, port int, txt []string) error {
	logging.Log().Debug("mdns: using zeroconf")
	
	// use Zeroconf library if avahi is not available
	// Set TTL to 2 minutes as defined in SHIP chapter 7
	ifaces := z.getIfaces()  
	opts := []zeroconf.ServerOption{zeroconf.TTL(120), zeroconf.WithServerConnFactory(z.connFactory), zeroconf.WithServerInterfaceProvider(z.interfaceProvider)}
	mDNSServer, err := zeroconf.Register(serviceName, shipZeroConfServiceType, shipZeroConfDomain, port, txt, ifaces, opts...)

	if err != nil {
		return err
	}

	z.mux.Lock()
	oldServer := z.zc
	z.zc = mDNSServer
	z.mux.Unlock()

	// Shut down the old server AFTER the new one is running.
	// This avoids a window where the service is completely unannounced,
	// which would cause remote devices to think we left the network.
	if oldServer != nil {
		oldServer.Shutdown()
	}

	return nil
}

func (z *ZeroconfProvider) Unannounce() {
	z.mux.Lock()
	defer z.mux.Unlock()

	if z.zc == nil {
		return
	}

	z.zc.Shutdown()
	z.zc = nil
}

func (z *ZeroconfProvider) chanListener(cb api.MdnsResolveCB) {
	defer func() {
		z.mux.Lock()
		if z.listenerDone != nil {
			close(z.listenerDone)
		}
		z.mux.Unlock()
	}()

	// Buffered channels prevent the Browse goroutine from deadlocking on shutdown.
	// When ctx is cancelled, chanListener exits and stops reading. Without a buffer,
	// Browse's mainloop blocks forever on a channel send and leaks. With a buffer,
	// the pending send completes into the buffer, mainloop loops back to its select,
	// picks ctx.Done(), and cleans up normally.
	zcEntries := make(chan *zeroconf.ServiceEntry, 2)
	zcRemoved := make(chan *zeroconf.ServiceEntry, 2)

	z.mux.Lock()
	z.ctx, z.cancel = context.WithCancel(context.Background())
	z.mux.Unlock()

  // Get a thread-safe copy of interfaces
	ifaces := z.getIfaces()
	opts := []zeroconf.ClientOption{zeroconf.SelectIfaces(ifaces), zeroconf.WithClientConnFactory(z.connFactory), zeroconf.WithClientInterfaceProvider(z.interfaceProvider)}
	go func() {
		_ = zeroconf.Browse(z.ctx, shipZeroConfServiceType, shipZeroConfDomain, zcEntries, zcRemoved, opts...)

	}()

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
			cb(elements, service.Instance, service.HostName, addresses, service.Port, true)

		case service := <-zcEntries:
			// Zeroconf has issues with merging mDNS data and sometimes reports incomplete records
			if service == nil || len(service.Text) == 0 {
				continue
			}

			elements := parseTxt(service.Text)

			addresses := service.AddrIPv4
			addresses = append(addresses, service.AddrIPv6...)
			cb(elements, service.Instance, service.HostName, addresses, service.Port, false)
		}
	}
}
