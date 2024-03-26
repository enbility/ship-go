package hub

import (
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/model"
)

var _ api.ShipConnectionInfoProviderInterface = (*Hub)(nil)

// check if the SKI is paired
func (h *Hub) IsRemoteServiceForSKIPaired(ski string) bool {
	service := h.ServiceForSKI(ski)

	return service.Trusted()
}

// report closing of a connection and if handshake did complete
func (h *Hub) HandleConnectionClosed(connection api.ShipConnectionInterface, handshakeCompleted bool) {
	remoteSki := connection.RemoteSKI()

	// only remove this connection if it is the registered one for the ski!
	// as we can have double connections but only one can be registered
	if existingC := h.connectionForSKI(remoteSki); existingC != nil {
		if existingC.DataHandler() == connection.DataHandler() {
			h.muxCon.Lock()
			delete(h.connections, connection.RemoteSKI())
			h.muxCon.Unlock()
		}

		// connection close was after a completed handshake, so we can reset the attetmpt counter
		if handshakeCompleted {
			h.removeConnectionAttemptCounter(connection.RemoteSKI())
		}
	}

	h.hubReader.RemoteSKIDisconnected(connection.RemoteSKI())

	// Do not automatically reconnect if handshake failed and not already paired
	remoteService := h.ServiceForSKI(connection.RemoteSKI())
	if !handshakeCompleted && !remoteService.Trusted() {
		return
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
	if service := h.ServiceForSKI(ski); service != nil {
		if service.Trusted() {
			return true
		}
	}

	return h.hubReader.AllowWaitingForTrust(ski)
}

// report the updated SHIP handshake state and optional error message for a SKI
func (h *Hub) HandleShipHandshakeStateUpdate(ski string, state model.ShipState) {
	// overwrite service Paired value
	if state.State == model.SmeHelloStateOk {
		service := h.ServiceForSKI(ski)
		service.SetTrusted(true)
	}

	pairingState := h.mapShipMessageExchangeState(state.State, ski)
	if state.Error != nil && state.Error != api.ErrConnectionNotFound {
		pairingState = api.ConnectionStateError
	}

	pairingDetail := api.NewConnectionStateDetail(pairingState, state.Error)

	service := h.ServiceForSKI(ski)

	existingDetails := service.ConnectionStateDetail()
	existingState := existingDetails.State()
	if existingState != pairingState || existingDetails.Error() != state.Error {
		service.SetConnectionStateDetail(pairingDetail)

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
