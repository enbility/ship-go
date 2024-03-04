package hub

import (
	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/logging"
	"github.com/enbility/ship-go/model"
)

// Provide the current pairing state for a SKI
//
// returns:
//
//	ErrNotPaired if the SKI is not in the (to be) paired list
//	ErrNoConnectionFound if no connection for the SKI was found
func (h *Hub) PairingDetailForSki(ski string) *api.ConnectionStateDetail {
	service := h.ServiceForSKI(ski)

	if conn := h.connectionForSKI(ski); conn != nil {
		shipState, shipError := conn.ShipHandshakeState()
		state := h.mapShipMessageExchangeState(shipState, ski)
		return api.NewConnectionStateDetail(state, shipError)
	}

	return service.ConnectionStateDetail()
}

// maps ShipMessageExchangeState to PairingState
func (h *Hub) mapShipMessageExchangeState(state model.ShipMessageExchangeState, _ string) api.ConnectionState {
	var connState api.ConnectionState

	// map the SHIP states to a public ConnectionState
	switch state {
	case model.CmiStateInitStart:
		connState = api.ConnectionStateQueued
	case model.CmiStateClientSend, model.CmiStateClientWait, model.CmiStateClientEvaluate,
		model.CmiStateServerWait, model.CmiStateServerEvaluate:
		connState = api.ConnectionStateInitiated
	case model.SmeHelloStateReadyInit, model.SmeHelloStateReadyListen, model.SmeHelloStateReadyTimeout,
		model.SmeHelloStatePendingInit, model.SmeHelloStatePendingTimeout:
		connState = api.ConnectionStateInProgress
	case model.SmeHelloStatePendingListen:
		connState = api.ConnectionStateReceivedPairingRequest
	case model.SmeHelloStateOk:
		connState = api.ConnectionStateTrusted
	case model.SmeHelloStateAbort, model.SmeHelloStateAbortDone:
		connState = api.ConnectionStateNone
	case model.SmeHelloStateRemoteAbortDone, model.SmeHelloStateRejected:
		connState = api.ConnectionStateRemoteDeniedTrust
	case model.SmePinStateCheckInit, model.SmePinStateCheckListen, model.SmePinStateCheckError,
		model.SmePinStateCheckBusyInit, model.SmePinStateCheckBusyWait, model.SmePinStateCheckOk,
		model.SmePinStateAskInit, model.SmePinStateAskProcess, model.SmePinStateAskRestricted,
		model.SmePinStateAskOk:
		connState = api.ConnectionStatePin
	case model.SmeAccessMethodsRequest, model.SmeStateApproved:
		connState = api.ConnectionStateInProgress
	case model.SmeStateComplete:
		connState = api.ConnectionStateCompleted
	case model.SmeStateError:
		connState = api.ConnectionStateError
	default:
		connState = api.ConnectionStateInProgress
	}

	return connState
}

// Sets the SKI as being paired or not
// Should be used for services which completed the pairing process and
// which were stored as having the process completed
func (h *Hub) RegisterRemoteSKI(ski string, enable bool) {
	// this should only be invoked before start is invoked
	h.muxStarted.Lock()
	if h.hasStarted {
		logging.Log().Error("RegisterRemoteSKI should only be called before the service started!")
		return
	}
	h.muxStarted.Unlock()

	service := h.ServiceForSKI(ski)
	service.SetTrusted(enable)

	if enable {
		h.checkAutoReannounce()
		return
	}

	h.removeConnectionAttemptCounter(ski)

	service.ConnectionStateDetail().SetState(api.ConnectionStateNone)

	h.hubReader.ServicePairingDetailUpdate(ski, service.ConnectionStateDetail())

	if existingC := h.connectionForSKI(ski); existingC != nil {
		existingC.CloseConnection(true, 4500, "User close")
	}
}

// Disconnect a connection to an SKI, used by a service implementation
// e.g. if heartbeats go wrong
func (h *Hub) DisconnectSKI(ski string, reason string) {

	con := h.connectionForSKI(ski)
	if con == nil {
		return
	}

	con.CloseConnection(true, 0, reason)
}

// Triggers the pairing process for a SKI
func (h *Hub) InitiateOrApprovePairingWithSKI(ski string) {
	conn := h.connectionForSKI(ski)

	// remotely initiated
	if conn != nil {
		conn.ApprovePendingHandshake()

		return
	}

	// locally initiated
	service := h.ServiceForSKI(ski)
	service.ConnectionStateDetail().SetState(api.ConnectionStateQueued)

	h.hubReader.ServicePairingDetailUpdate(ski, service.ConnectionStateDetail())

	h.mdns.RequestMdnsEntries()
}

// Cancels the pairing process for a SKI
func (h *Hub) CancelPairingWithSKI(ski string) {
	h.removeConnectionAttemptCounter(ski)

	if existingC := h.connectionForSKI(ski); existingC != nil {
		existingC.AbortPendingHandshake()
	}

	service := h.ServiceForSKI(ski)
	service.ConnectionStateDetail().SetState(api.ConnectionStateNone)
	service.SetTrusted(false)

	h.hubReader.ServicePairingDetailUpdate(ski, service.ConnectionStateDetail())
}
