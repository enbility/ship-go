package hub

import (
	"net"
	"sort"
	"strings"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/logging"
)

var _ api.MdnsReportInterface = (*Hub)(nil)

// Process reported mDNS services
func (h *Hub) ReportMdnsEntries(entries map[string]*api.MdnsEntry, newEntries bool) {
	// Clean up connection attempts for SKIs that are no longer in mDNS
	h.cleanupRemovedMdnsEntries(entries)

	var mdnsEntries []*api.MdnsEntry

	for _, entry := range entries {
		mdnsEntries = append(mdnsEntries, entry)

		// check if this ski is already connected
		if h.isSkiConnected(entry.Ski) {
			continue
		}

		// Check if the remote service is paired or queued for connection
		service := h.ServiceForSKI(entry.Ski)
		if !h.IsRemoteServiceForSKIPaired(entry.Ski) &&
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

		h.coordinateConnectionInitations(entry.Ski, entry)
	}

	sort.Slice(mdnsEntries, func(i, j int) bool {
		item1 := mdnsEntries[i]
		item2 := mdnsEntries[j]
		a := strings.ToLower(item1.Brand + item1.Model + item1.Ski)
		b := strings.ToLower(item2.Brand + item2.Model + item2.Ski)
		return a < b
	})

	if newEntries {
		h.muxMdns.Lock()
		h.knownMdnsEntries = mdnsEntries
		h.muxMdns.Unlock()
	}

	var remoteServices []api.RemoteService

	for _, entry := range entries {
		remoteService := api.RemoteService{
			Name:       entry.Name,
			Ski:        entry.Ski,
			Identifier: entry.Identifier,
			Brand:      entry.Brand,
			Type:       entry.Type,
			Model:      entry.Model,
			Serial:     entry.Serial,
			Categories: entry.Categories,
		}

		remoteServices = append(remoteServices, remoteService)
	}

	h.hubReader.VisibleRemoteServicesUpdated(remoteServices)
}

// cleanupRemovedMdnsEntries cancels connection attempts for SKIs no longer visible in mDNS
func (h *Hub) cleanupRemovedMdnsEntries(currentEntries map[string]*api.MdnsEntry) {
	h.muxMdns.Lock()
	previousEntries := h.knownMdnsEntries
	h.muxMdns.Unlock()

	// Create a set of current SKIs for efficient lookup
	currentSKIs := make(map[string]bool)
	for _, entry := range currentEntries {
		currentSKIs[entry.Ski] = true
	}

	// Check each previous entry to see if it's still present
	for _, prevEntry := range previousEntries {
		if !currentSKIs[prevEntry.Ski] {
			// SKI is no longer in mDNS - cancel connection attempts immediately
			logging.Log().Debugf("hub: cleaning up connection attempts for SKI %s (no longer in mDNS)", prevEntry.Ski)
			h.cancelConnectionDelayTimer(prevEntry.Ski)
			h.removeConnectionAttemptCounter(prevEntry.Ski)
		}
	}
}
