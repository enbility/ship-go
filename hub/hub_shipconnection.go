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
	h.UnregisterConnectionIfMatch(remoteSki, connection)

	// connection close was after a completed handshake, so we can reset the attempt counter
	if handshakeCompleted {
		h.removeConnectionAttemptCounter(connection.RemoteSKI())
	}

	h.hubReader.RemoteSKIDisconnected(connection.RemoteSKI())

	// Do not automatically reconnect if handshake failed and not already paired
	remoteService := h.ServiceForIdentifier(connection.RemoteSKI(), "")
	if remoteService == nil || (!handshakeCompleted && !remoteService.Trusted()) {
		return
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
func (h *Hub) ReportServiceShipID(ski string, shipdID string) {
	h.hubReader.RemoteSKIConnected(ski)

	h.hubReader.ServiceShipIDUpdate(ski, shipdID)
}

// check if the user is still able to trust the connection
func (h *Hub) AllowWaitingForTrust(ski string) bool {
	if service := h.ServiceForIdentifier(ski, ""); service != nil {
		if service.Trusted() {
			return true
		}
	}

	return h.hubReader.AllowWaitingForTrust(ski)
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

	existingDetails := service.ConnectionStateDetail()
	existingState := existingDetails.State()
	if existingState != pairingState || !errors.Is(existingDetails.Error(), state.Error) {
		service.SetConnectionStateDetail(pairingDetail)

		if pairingState == api.ConnectionStateCompleted {
			// Stop AddCu replacement timer when connection successfully completes
			// Stop announcement for successfully connected device
			h.StopAddCuReplacementTimer(service)

			if shipID := service.ShipID(); shipID != "" {
				if h.IsAnnouncingTo(shipID) {
					_ = h.StopAnnouncementTo(shipID)
				}
			}
		}

		// always send a delayed update, as the processing of the new state has to be done
		// and the SHIP message has to be received by the other service before
		// acting upon the new state is safe
		go func() {
			<-time.After(time.Millisecond * 500)
			h.hubReader.ServicePairingDetailUpdate(ski, pairingDetail)
		}()
	}
}

// report an approved handshake by a remote device
func (h *Hub) SetupRemoteDevice(ski string, writeI api.ShipConnectionDataWriterInterface) api.ShipConnectionDataReaderInterface {
	return h.hubReader.SetupRemoteDevice(ski, writeI)
}
