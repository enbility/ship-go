package model

type ShipState struct {
	State ShipMessageExchangeState
	Error error
}

type ShipMessageExchangeState uint

const (
	// Connection Mode Initialisation (CMI) SHIP 13.4.3
	CmiStateInitStart ShipMessageExchangeState = iota
	CmiStateClientSend
	CmiStateClientWait
	CmiStateClientEvaluate
	CmiStateServerWait
	CmiStateServerEvaluate
	// Connection Data Preparation SHIP 13.4.4
	SmeHelloState
	SmeHelloStateReadyInit
	SmeHelloStateReadyListen
	SmeHelloStateReadyTimeout
	SmeHelloStatePendingInit
	SmeHelloStatePendingListen
	SmeHelloStatePendingTimeout
	SmeHelloStateOk
	SmeHelloStateAbort           // Sent abort to remote
	SmeHelloStateAbortDone       // Sending abort to remote is done
	SmeHelloStateRemoteAbortDone // Received abort from remote
	SmeHelloStateRejected        // Connection closed after remote pending: "4452: Node rejected by application"

	// Connection State Protocol Handhsake SHIP 13.4.4.2
	SmeProtHStateServerInit
	SmeProtHStateClientInit
	SmeProtHStateServerListenProposal
	SmeProtHStateServerListenConfirm
	SmeProtHStateClientListenChoice
	SmeProtHStateTimeout
	SmeProtHStateClientOk
	SmeProtHStateServerOk
	// Connection PIN State 13.4.5
	SmePinStateCheckInit
	SmePinStateCheckListen
	SmePinStateCheckError
	SmePinStateCheckBusyInit
	SmePinStateCheckBusyWait
	SmePinStateCheckOk
	SmePinStateAskInit
	SmePinStateAskProcess
	SmePinStateAskRestricted
	SmePinStateAskOk
	// ConnectionAccess Methods Identification 13.4.6
	SmeAccessMethodsRequest

	// Handshake approved on both ends
	SmeStateApproved

	// Handshake process is successfully completed
	SmeStateComplete

	// Handshake ended with an error
	SmeStateError
)

var ShipInit []byte = []byte{MsgTypeInit, 0x00}
