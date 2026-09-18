package api

import (
	"github.com/enbility/ship-go/model"
)

/* ShipConnection */

// ShipConnectionInterface provides access to SHIP protocol connections.
//
// This interface represents an active SHIP connection between local and remote devices,
// handling the SHIP handshake protocol and providing access to remote service information.
// Connections are created by the Hub when devices establish WebSocket connections.
//
// Implemented by: ShipConnection (ship package)
// Used by: Hub for connection management and lifecycle coordination
type ShipConnectionInterface interface {
	// DataHandler returns the WebSocket data writer for sending SHIP messages.
	//
	// The data handler provides low-level WebSocket communication capabilities
	// for sending SHIP protocol messages and SPINE payloads.
	//
	// Returns:
	// - WebsocketDataWriterInterface: Writer for sending data over the connection
	DataHandler() WebsocketDataWriterInterface

	// CloseConnection closes the SHIP connection with optional close code and reason.
	//
	// This method gracefully terminates the SHIP connection, sending appropriate
	// close frames and cleaning up resources.
	//
	// Parameters:
	// - safe: true for graceful close, false for immediate termination
	// - code: WebSocket close code (e.g., 1000 for normal closure)
	// - reason: Human-readable reason for closing the connection
	CloseConnection(safe bool, code int, reason string)

	// RemoteSKI returns the SKI of the remote service.
	//
	// Returns:
	// - string: The SKI of the remote service
	//
	// Example:
	//   ski := connection.RemoteSKI()
	RemoteSKI() string

	// ApprovePendingHandshake approves a handshake that is waiting for user confirmation.
	//
	// This method is called when a user manually approves a connection request
	// from an untrusted device. It continues the SHIP handshake process that
	// was paused for user intervention.
	//
	// Used when: Connection is in pending state waiting for trust confirmation
	ApprovePendingHandshake()

	// AbortPendingHandshake rejects a handshake that is waiting for user confirmation.
	//
	// This method is called when a user manually rejects a connection request
	// from an untrusted device. It terminates the SHIP handshake process and
	// closes the connection.
	//
	// Used when: Connection is in pending state and user rejects the request
	AbortPendingHandshake()

	// ShipHandshakeState returns the current SHIP protocol handshake state.
	//
	// The SHIP handshake progresses through multiple states (Init, Hello, Protocol,
	// PIN) before entering connection data exchange. Access methods identification
	// runs in parallel to data exchange, and SmeStateComplete is reached once it
	// verified the remote's SHIP ID. This method provides visibility into the
	// current handshake progress.
	//
	// Returns:
	// - model.ShipMessageExchangeState: Current handshake state
	// - error: Any error that occurred during handshake, nil if successful
	//
	// Example:
	//   state, err := connection.ShipHandshakeState()
	//   if err != nil {
	//       // Handle handshake error
	//   }
	//   if state == model.SmeStateComplete {
	//       // Handshake completed successfully
	//   }
	ShipHandshakeState() (model.ShipMessageExchangeState, error)
}

// ShipConnectionInfoProviderInterface provides service information and coordination for SHIP connections.
//
// This interface enables SHIP connections to interact with the Hub's service management
// system during the connection lifecycle. It provides trust validation, state reporting,
// and device setup coordination based on the SHIP Connections unique SKI.
//
// Implemented by: Hub
// Used by: ShipConnection during handshake and connection lifecycle events
type ShipConnectionInfoProviderInterface interface {
	// IsRemoteServiceForSKIPaired checks if a remote service is trusted and should be allowed to connect.
	//
	// This method is called during SHIP handshake to determine if the remote device
	// is already paired/trusted. Services can be trusted through manual pairing,
	// SHIP Pairing Service, or auto-accept configuration.
	//
	// Parameters:
	// - ski: The SKI of the remote service
	//
	// Returns:
	// - bool: true if service is trusted, false if trust confirmation required
	//
	// Used during: SHIP handshake Hello phase for trust validation
	IsRemoteServiceForSKIPaired(ski string) bool

	// IsAutoAcceptEnabled returns whether the Hub is configured to automatically accept connections.
	//
	// This method indicates if the Hub will automatically trust new connection
	// requests without user confirmation. Used during handshake to determine
	// if manual approval is required.
	//
	// Returns:
	// - bool: true if auto-accept is enabled, false if manual approval required
	IsAutoAcceptEnabled() bool

	// HandleConnectionClosed reports that a SHIP connection has been closed.
	//
	// This method is called when a SHIP connection is terminated, either gracefully
	// or due to error conditions. It allows the Hub to clean up connection state
	// and potentially trigger reconnection attempts.
	//
	// Parameters:
	// - ShipConnectionInterface: The connection that was closed
	// - bool: true if handshake completed before closure, false otherwise
	//
	// Used when: Connection terminates for any reason
	HandleConnectionClosed(ShipConnectionInterface, bool)

	// ReportServiceShipID notifies about the reported shipid, if it was unknown before.
	//
	// This method is called when SHIP ID is provided by the remote service and it
	// wasn't known before the ship connection.
	//
	// Parameters:
	// - ski: SKI of the remote service
	// - shipID: SHIP ID of the remote service
	//
	// Used during: SHIP handshake when service information is discovered/updated
	ReportServiceShipID(ski string, shipID string)

	// AllowWaitingForTrust determines if the connection should continue waiting for user trust confirmation.
	//
	// This method is called periodically during connection establishment when a
	// remote service is not automatically trusted and requires user confirmation.
	// It allows applications to implement timeout policies or cancel pending connections.
	//
	// Parameters:
	// - ski: SKI of the remote service
	//
	// Returns:
	// - bool: true to continue waiting, false to abort the connection
	//
	// Used during: SHIP handshake when waiting for manual trust confirmation
	//
	// Example implementation:
	//   return time.Since(connectionStart) < 30*time.Second  // 30-second timeout
	AllowWaitingForTrust(ski string) bool

	// HandleServiceHandshakeStateUpdate reports SHIP handshake state changes.
	//
	// This method is called whenever the SHIP handshake progresses through its
	// various states, providing applications with real-time handshake monitoring
	// and error reporting capabilities.
	//
	// Parameters:
	// - ski: SKI of the remote service
	// - state: Current SHIP handshake state information
	//
	// Used during: All phases of SHIP handshake for progress monitoring
	//
	// Example states: CmiStateInitStart, HelloStateOk, ProtocolStateOk, AccessStateOk
	HandleShipHandshakeStateUpdate(ski string, state model.ShipState)

	// SetupRemoteService sets up communication with a newly connected remote device.
	//
	// This method is called exactly once per connection, when it enters SHIP connection
	// data exchange (SHIP 13.4.5) after PIN verification succeeded. From then on the remote
	// device may send SPINE messages, and they are passed to the returned reader immediately
	// (SHIP IG Transport and Connectivity 2.1). writeI can be used immediately as well.
	//
	// The access methods exchange runs in parallel and has not completed at this point. It
	// verifies the remote's SHIP ID, so for a remote whose SHIP ID was not known before the
	// connection, the SHIP ID is not available yet. If the verification fails, or the remote
	// does not answer within 60 seconds, the connection is terminated and HandleConnectionClosed
	// reports it without a completed handshake.
	//
	// Parameters:
	// - ski: SKI of the remote device
	// - writeI: Writer interface for sending messages to the remote device
	//
	// Returns:
	// - ShipConnectionDataReaderInterface: Reader interface for receiving messages, or nil if
	//   the application does not process SPINE messages on this connection
	//
	// Used when: The connection enters SHIP connection data exchange
	SetupRemoteService(ski string, writeI ShipConnectionDataWriterInterface) ShipConnectionDataReaderInterface
}

// Used to pass an outgoing SPINE message from a DeviceLocal to the SHIP connection
//
// Implemented by ShipConnection, used by spine DeviceLocal
type ShipConnectionDataWriterInterface interface {
	WriteShipMessageWithPayload(message []byte)
}

// Used to pass an incoming SPINE message from a SHIP connection to the proper DeviceRemote
//
// Implemented by spine DeviceRemote, used by ShipConnection
type ShipConnectionDataReaderInterface interface {
	HandleShipPayloadMessage(message []byte)
}
