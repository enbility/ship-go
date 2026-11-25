package hub

import (
	"errors"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/logging"
	"github.com/enbility/ship-go/model"
)

var _ api.ShipConnectionInfoProviderInterface = (*Hub)(nil)

// check if the SKI is paired
func (h *Hub) IsRemoteServiceForSKIPaired(ski string) bool {
	service := h.ServiceForIdentifier(ski, "")
	if service == nil {
		return false
	}

	return service.Trusted()
}

// report closing of a connection and if handshake did complete
func (h *Hub) HandleConnectionClosed(connection api.ShipConnectionInterface, handshakeCompleted bool) {
	remoteSki := connection.RemoteSKI()

	// only remove this connection if it is the registered one for the ski!
	// as we can have double connections but only one can be registered
	// Use the new atomic method to avoid race conditions
	wasRegistered := h.UnregisterConnectionIfMatch(remoteSki, connection)

	// This connection was displaced by a more recent one to the same SKI (SHIP 12.2.2).
	// The peer is still connected, so reporting a disconnect here would tear down the
	// application state belonging to the connection that just won.
	if !wasRegistered && h.isSkiConnected(remoteSki) {
		logging.Log().Debug("superseded connection closed, peer remains connected", remoteSki)
		return
	}

	// connection close was after a completed handshake, so we can reset the attempt counter
	if handshakeCompleted {
		h.removeConnectionAttemptCounter(connection.RemoteSKI())
	}

	// Do not automatically reconnect if handshake failed and not already paired
	remoteService := h.ServiceForIdentifier(connection.RemoteSKI(), "")
	if remoteService == nil || (!handshakeCompleted && !remoteService.Trusted()) {
		// Convert SKI to ServiceIdentity for callback
		disconnectedIdentity := api.SKIToServiceIdentity(connection.RemoteSKI())
		h.hubReader.RemoteServiceDisconnected(disconnectedIdentity)
		return
	}

	disconnectedIdentity := remoteService.ToServiceIdentity()
	h.hubReader.RemoteServiceDisconnected(disconnectedIdentity)

	// Cancel any announcement lifetime timer for this device for devZ
	if h.pairingService != nil && h.pairingConfig != nil && (h.pairingConfig.Mode == api.PairingModeAnnouncer || h.pairingConfig.Mode == api.PairingModeBoth) {
		if remoteService.ShipID() != "" {
			h.announcementLifetimeTracker.CancelLifetimeTimer(remoteService.ShipID())
		}
	}

	// Start replacement tracker for AddCu devices
	if remoteService.PairingType() == api.PairingTypeAddCu && remoteService.ShipID() != "" {
		shipID := remoteService.ShipID()
		logging.Log().Trace("starting AddCu replacement timer", "shipID", shipID, "ski", remoteService.SKI(), "timeout", "15 minutes")
		h.addCuReplacementTracker.StartTimer(shipID, h.handleAddCuReplacementTimeout)
	}

	h.checkAutoReannounce()
}

// report the ship ID provided during the handshake
func (h *Hub) ReportServiceShipID(ski string, shipID string) {
	service := h.ServiceForIdentifier(ski, "")
	if service == nil {
		return
	}
	if service.ShipID() == "" {
		service.SetShipID(shipID)
		// The new ShipID may match a separate ShipID-only entry created
		// earlier (e.g. by RegisterRemoteService with no SKI yet). Merge
		// to fold them so SKI-keyed lookups stop missing the trust state.
		if merged, err := h.mergeOrAddService(service); err == nil {
			service = merged
		} else {
			logging.Log().Error("ReportServiceShipID: identifier conflict during merge",
				"ski", ski, "shipID", shipID, "error", err)
		}
	}

	connectedIdentity := service.ToServiceIdentity()
	h.hubReader.ServiceUpdated(connectedIdentity)
}

// check if the user is still able to trust the connection
func (h *Hub) AllowWaitingForTrust(ski string) bool {
	var waitingIdentity api.ServiceIdentity
	if service := h.ServiceForIdentifier(ski, ""); service != nil {
		if service.Trusted() {
			return true
		}
		waitingIdentity = service.ToServiceIdentity()
	} else {
		waitingIdentity = api.SKIToServiceIdentity(ski)
	}

	return h.hubReader.AllowWaitingForTrust(waitingIdentity)
}

// report the updated SHIP handshake state and optional error message for a SKI
func (h *Hub) HandleShipHandshakeStateUpdate(ski string, state model.ShipState) {
	service := h.ServiceForIdentifier(ski, "")
	// this should never happen, as we can't have a connection without a service added
	if service == nil {
		return
	}

	// overwrite service Paired value
	if state.State == model.SmeHelloStateOk {
		service.SetTrusted(true)
	}

	pairingState := h.mapShipMessageExchangeState(state.State, ski)
	if state.Error != nil && !errors.Is(state.Error, api.ErrConnectionNotFound) {
		pairingState = api.ConnectionStateError
	}

	pairingDetail := api.NewConnectionStateDetail(pairingState, state.Error)

	// compare-and-set under muxReg: double connections to one SKI update the same service concurrently
	h.muxReg.Lock()
	existingDetails := service.ConnectionStateDetail()
	changed := existingDetails.State() != pairingState || !errors.Is(existingDetails.Error(), state.Error)
	if changed {
		service.SetConnectionStateDetail(pairingDetail)
	}
	h.muxReg.Unlock()

	if changed {
		if pairingState == api.ConnectionStateCompleted {
			connectedIdentity := service.ToServiceIdentity()
			h.hubReader.RemoteServiceConnected(connectedIdentity)
			// Stop AddCu replacement timer when connection successfully completes
			// Stop announcement for successfully connected device
			h.StopAddCuReplacementTimer(service)

			// Stop the pairing listener we have a AddCu device successful connection
			if service.PairingType() == api.PairingTypeAddCu {
				h.stopPairingListener()
			}

			if shipID := service.ShipID(); shipID != "" {
				if h.IsAnnouncingTo(shipID) {
					// Start the lifetime timer; if the connection drops before expiry,
					// the timer is cancelled in HandleConnectionClosed.
					h.announcementLifetimeTracker.StartLifetimeTimer(shipID, func(expiredShipID string) {
						_ = h.StopAnnouncementTo(expiredShipID)
					})
				}
			}
		}

		// always send a delayed update, as the processing of the new state has to be done
		// and the SHIP message has to be received by the other service before
		// acting upon the new state is safe
		go func() {
			<-time.After(time.Millisecond * 500)
			// Convert SKI to ServiceIdentity for callback
			pairingIdentity := service.ToServiceIdentity()
			h.hubReader.ServicePairingDetailUpdate(pairingIdentity, pairingDetail)
		}()
	}
}

// report an approved handshake by a remote device
func (h *Hub) SetupRemoteService(ski string, writeI api.ShipConnectionDataWriterInterface) api.ShipConnectionDataReaderInterface {
	// Convert SKI to ServiceIdentity for callback
	var setupIdentity api.ServiceIdentity
	if service := h.ServiceForIdentifier(ski, ""); service != nil {
		setupIdentity = service.ToServiceIdentity()
	} else {
		setupIdentity = api.SKIToServiceIdentity(ski)
	}
	return h.hubReader.SetupRemoteService(setupIdentity, writeI)
}
