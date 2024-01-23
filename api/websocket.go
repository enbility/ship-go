package api

/* WebsocketConnection */

// interface for handling the actual remote device data connection
//
// implemented by websocketConnection, used by ShipConnection
type WebsocketDataWriterInterface interface {
	// initialize data processing
	InitDataProcessing(WebsocketDataReaderInterface)

	// send data via the connection to the remote device
	WriteMessageToWebsocketConnection([]byte) error

	// close the data connection
	CloseDataConnection(closeCode int, reason string)

	// report if the data connection is closed and the error if availab le
	IsDataConnectionClosed() (bool, error)
}

// interface for handling incoming data
//
// implemented by shipConnection, used by websocketConnection
type WebsocketDataReaderInterface interface {
	// called for each incoming message
	HandleIncomingWebsocketMessage([]byte)

	// called if the data connection is closed unsafe
	// e.g. due to connection issues
	ReportConnectionError(error)
}

const ShipWebsocketSubProtocol = "ship" // SHIP 10.2: sub protocol is required for websocket connections
