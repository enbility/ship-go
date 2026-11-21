package hub

import (
	"net"
	"sort"
	"strings"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
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

		if !cert.IsSkiFormatValid(entry.Ski) {
			continue
		}

		// check if this ski is already connected
		if h.isSkiConnected(entry.Ski) {
			continue
		}

		// Check if the remote service is paired or queued for connection
		service := h.ServiceForIdentifier(entry.Ski, "")
		if service != nil {
			if !h.IsRemoteServiceForSKIPaired(entry.Ski) && service.Trusted() {
				continue
			}
		} else {
			// devA via SHIP Pairing does not know the SKI at this point,
			// but has the fingerprint and SHIP ID. The fingerprint is not announced via mDNS!

			// Make sure SHIP ID is not empty
			if len(entry.Identifier) == 0 {
				continue
			}

			service = h.serviceForTrustedShipID(entry.Identifier)
			if service == nil {
				continue
			}
			// Make sure SKI is not present, to match our criteiron
			if len(service.SKI()) != 0 {
				continue
			}
			// Fingerprint format validation
			if !cert.IsFingerprintFormatValid(service.Fingerprint()) {
				continue
			}
			// Fingerprint is valid, add the found SKI in our trust,
			// given that the fingerprint will be validated on handshake process
			service.SetSKI(entry.Ski)
			// Fold any other entry that now matches by the discovered SKI
			// (typically an untrusted SKI+FP entry from a prior rejected
			// incoming connection).
			if merged, err := h.mergeOrAddService(service); err == nil {
				service = merged
			} else {
				logging.Log().Error("mDNS: identifier conflict during merge",
					"ski", entry.Ski, "shipID", entry.Identifier, "error", err)
			}
		}

		service.SetAutoAccept(entry.Register)

		// patch the addresses list if an IPv4 address was provided
		if service.IPv4() != "" {
			if ip := net.ParseIP(service.IPv4()); ip != nil {
				entry.Addresses = []net.IP{ip}
			}
		}

		copyEntry := *entry
		h.coordinateConnectionInitations(copyEntry.Ski, &copyEntry)
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

	var remoteServices []api.RemoteMdnsService

	for _, entry := range entries {
		remoteService := api.RemoteMdnsService{
			Name:       entry.Name,
			Ski:        entry.Ski,
			ShipID:     entry.Identifier,
			Brand:      entry.Brand,
			Type:       entry.Type,
			Model:      entry.Model,
			Serial:     entry.Serial,
			Categories: entry.Categories,
			Addresses:  entry.Addresses,
		}

		remoteServices = append(remoteServices, remoteService)
	}

	h.hubReader.VisibleRemoteMdnsServicesUpdated(remoteServices)
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
