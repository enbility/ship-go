package mdns

import (
	"context"
	"net"
	"sync"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/logging"
	"github.com/enbility/zeroconf/v2"
)

type ZeroconfProvider struct {
	ifaces []net.Interface

	zc *zeroconf.Server

	ctx    context.Context
	cancel context.CancelFunc

	shutdownOnce sync.Once

	mux sync.Mutex
}

func NewZeroconfProvider(ifaces []net.Interface) *ZeroconfProvider {
	return &ZeroconfProvider{
		ifaces: ifaces,
	}
}

var _ api.MdnsProviderInterface = (*ZeroconfProvider)(nil)

func (z *ZeroconfProvider) CheckAvailability() bool {
	return true
}

func (z *ZeroconfProvider) Shutdown() {
	z.shutdownOnce.Do(func() {
		z.Unannounce()

		z.mux.Lock()
		defer z.mux.Unlock()

		if z.cancel != nil {
			z.cancel()
		}
	})
}

func (z *ZeroconfProvider) Announce(serviceName string, port int, txt []string) error {
	logging.Log().Debug("mdns: using zeroconf")

	// use Zeroconf library if avahi is not available
	// Set TTL to 2 minutes as defined in SHIP chapter 7
	mDNSServer, err := zeroconf.Register(serviceName, shipZeroConfServiceType, shipZeroConfDomain, port, txt, z.ifaces, zeroconf.TTL(120))
	if err != nil {
		return err
	}

	z.mux.Lock()
	defer z.mux.Unlock()

	z.zc = mDNSServer

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

func (z *ZeroconfProvider) ResolveEntries(callback api.MdnsResolveCB) {
	zcEntries := make(chan *zeroconf.ServiceEntry)
	zcRemoved := make(chan *zeroconf.ServiceEntry)

	z.mux.Lock()
	// for Zeroconf we need a context
	z.ctx, z.cancel = context.WithCancel(context.Background())
	z.mux.Unlock()

	go func() {
		_ = zeroconf.Browse(z.ctx, shipZeroConfServiceType, shipZeroConfDomain, zcEntries, zcRemoved)
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
			callback(elements, service.Instance, service.HostName, addresses, service.Port, true)

		case service := <-zcEntries:
			// Zeroconf has issues with merging mDNS data and sometimes reports incomplete records
			if service == nil || len(service.Text) == 0 {
				continue
			}

			elements := parseTxt(service.Text)

			addresses := service.AddrIPv4
			// Only use IPv4 for now
			// addresses = append(addresses, service.AddrIPv6...)
			callback(elements, service.Instance, service.HostName, addresses, service.Port, false)
		}
	}
}
