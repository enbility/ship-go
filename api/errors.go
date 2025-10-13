package api

import "errors"

// Common errors that can occur across the ship-go library.
// These are sentinel errors that can be checked with errors.Is()

// Connection errors
var (
	// ErrConnectionNotInitialized indicates a connection operation was attempted before initialization
	ErrConnectionNotInitialized = errors.New("connection not initialized")

	// ErrConnectionClosed indicates an operation was attempted on a closed connection
	ErrConnectionClosed = errors.New("connection closed")

	// ErrConnectionTimeout indicates a connection operation timed out
	ErrConnectionTimeout = errors.New("connection timeout")

	// ErrBufferFull indicates a message buffer is full
	ErrBufferFull = errors.New("buffer full")
)

// Certificate and authentication errors
var (
	// ErrInvalidCertificate indicates certificate validation failed
	ErrInvalidCertificate = errors.New("invalid certificate")

	// ErrInvalidSKI indicates an invalid or missing SKI
	ErrInvalidSKI = errors.New("invalid SKI")

	// ErrNotPaired indicates the remote service is not paired
	ErrNotPaired = errors.New("remote service not paired")
)

// Protocol errors
var (
	// ErrInvalidHandshake indicates a handshake protocol violation
	ErrInvalidHandshake = errors.New("invalid handshake")

	// ErrInvalidProtocolMessage indicates a malformed protocol message
	ErrInvalidProtocolMessage = errors.New("invalid protocol message")

	// ErrUnsupportedPinState indicates an unsupported PIN state was received
	ErrUnsupportedPinState = errors.New("unsupported PIN state")
)

// SHIP Pairing Service errors following ship-go error pattern
var (
	// Configuration errors
	ErrInvalidTargetFingerprint = errors.New("invalid target fingerprint")
)
