package model

type ShipState struct {
	State ShipMessageExchangeState
	Error error
}

type ShipMessageExchangeState uint

// set the values manually instead of using iota, so log data can be associated easier
const (
	// Connection Mode Initialisation (CMI) SHIP 13.4.3
	CmiStateInitStart      ShipMessageExchangeState = 0
	CmiStateClientSend     ShipMessageExchangeState = 1
	CmiStateClientWait     ShipMessageExchangeState = 2
	CmiStateClientEvaluate ShipMessageExchangeState = 3
	CmiStateServerWait     ShipMessageExchangeState = 4
	CmiStateServerEvaluate ShipMessageExchangeState = 5
	// Connection Data Preparation SHIP 13.4.4
	SmeHelloState                ShipMessageExchangeState = 6
	SmeHelloStateReadyInit       ShipMessageExchangeState = 7
	SmeHelloStateReadyListen     ShipMessageExchangeState = 8
	SmeHelloStateReadyTimeout    ShipMessageExchangeState = 9
	SmeHelloStatePendingInit     ShipMessageExchangeState = 10
	SmeHelloStatePendingListen   ShipMessageExchangeState = 11
	SmeHelloStatePendingTimeout  ShipMessageExchangeState = 12
	SmeHelloStateOk              ShipMessageExchangeState = 13
	SmeHelloStateAbort           ShipMessageExchangeState = 14 // Sent abort to remote
	SmeHelloStateAbortDone       ShipMessageExchangeState = 15 // Sending abort to remote is done
	SmeHelloStateRemoteAbortDone ShipMessageExchangeState = 16 // Received abort from remote
	SmeHelloStateRejected        ShipMessageExchangeState = 17 // Connection closed after remote pending: "4452: Node rejected by application"

	// Connection State Protocol Handhsake SHIP 13.4.4.2
	SmeProtHStateServerInit           ShipMessageExchangeState = 18
	SmeProtHStateClientInit           ShipMessageExchangeState = 19
	SmeProtHStateServerListenProposal ShipMessageExchangeState = 20
	SmeProtHStateServerListenConfirm  ShipMessageExchangeState = 21
	SmeProtHStateClientListenChoice   ShipMessageExchangeState = 22
	SmeProtHStateTimeout              ShipMessageExchangeState = 23
	SmeProtHStateClientOk             ShipMessageExchangeState = 24
	SmeProtHStateServerOk             ShipMessageExchangeState = 25
	// Connection State PIN Verification SHIP 13.4.4.3
	SmePinStateCheckInit     ShipMessageExchangeState = 26
	SmePinStateCheckListen   ShipMessageExchangeState = 27
	SmePinStateCheckError    ShipMessageExchangeState = 28
	SmePinStateCheckBusyInit ShipMessageExchangeState = 29
	SmePinStateCheckBusyWait ShipMessageExchangeState = 30
	SmePinStateCheckOk       ShipMessageExchangeState = 31
	SmePinStateAskInit       ShipMessageExchangeState = 32
	SmePinStateAskProcess    ShipMessageExchangeState = 33
	SmePinStateAskRestricted ShipMessageExchangeState = 34
	SmePinStateAskOk         ShipMessageExchangeState = 35
	// Connection data exchange (SHIP 13.4.5) is entered once PIN verification succeeded. Access
	// methods identification (SHIP 13.4.6) runs in parallel to it, so the following three states
	// are all connection data exchange.

	// Access Methods Identification SHIP 13.4.6: our access methods request is pending
	SmeAccessMethodsRequest ShipMessageExchangeState = 36

	// The remote's access methods reply was accepted
	SmeStateApproved ShipMessageExchangeState = 37

	// Connection is established: data exchange, with the remote's SHIP ID verified
	SmeStateComplete ShipMessageExchangeState = 38

	// Handshake ended with an error
	SmeStateError ShipMessageExchangeState = 39
)

var ShipInit []byte = []byte{MsgTypeInit, 0x00}
