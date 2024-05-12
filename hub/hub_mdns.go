package hub

import (
	"net"
	"sort"
	"strings"

	"github.com/enbility/ship-go/api"
)

var _ api.MdnsReportInterface = (*Hub)(nil)

// Process reported mDNS services
func (h *Hub) ReportMdnsEntries(entries map[string]*api.MdnsEntry) {
	h.muxMdns.Lock()
	defer h.muxMdns.Unlock()

	var mdnsEntries []*api.MdnsEntry

	for ski, entry := range entries {
		mdnsEntries = append(mdnsEntries, entry)

		// check if this ski is already connected
		if h.isSkiConnected(ski) {
			continue
		}

		// Check if the remote service is paired or queued for connection
		service := h.ServiceForSKI(ski)
		if !h.IsRemoteServiceForSKIPaired(ski) &&
			service.ConnectionStateDetail().State() != api.ConnectionStateQueued {
			continue
		}

		service.SetAutoAccept(entry.Register)

		// patch the addresses list if an IPv4 address was provided
		if service.IPv4() != "" {
			if ip := net.ParseIP(service.IPv4()); ip != nil {
				entry.Addresses = []net.IP{ip}
			}
		}

		h.coordinateConnectionInitations(ski, entry)
	}

	sort.Slice(mdnsEntries, func(i, j int) bool {
		item1 := mdnsEntries[i]
		item2 := mdnsEntries[j]
		a := strings.ToLower(item1.Brand + item1.Model + item1.Ski)
		b := strings.ToLower(item2.Brand + item2.Model + item2.Ski)
		return a < b
	})

	h.knownMdnsEntries = mdnsEntries

	var remoteServices []api.RemoteService

	for _, entry := range entries {
		remoteService := api.RemoteService{
			Name:       entry.Name,
			Ski:        entry.Ski,
			Identifier: entry.Identifier,
			Brand:      entry.Brand,
			Type:       entry.Type,
			Model:      entry.Model,
		}

		remoteServices = append(remoteServices, remoteService)
	}

	h.hubReader.VisibleRemoteServicesUpdated(remoteServices)
}
