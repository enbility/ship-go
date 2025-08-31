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

	// ErrServiceNil indicates a nil service was provided
	ErrServiceNil = errors.New("service is not initialized")

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
	// Lifecycle errors
	ErrServiceNotStarted     = errors.New("pairing service not started")
	ErrServiceAlreadyStarted = errors.New("pairing service already started")
	ErrServiceNotStopped     = errors.New("pairing service not stopped properly")
	ErrPairingNotEnabled     = errors.New("pairing service not enabled")
	ErrPairingNoConfig       = errors.New("pairing service configuration not set")

	// Configuration errors
	ErrInvalidSecret            = errors.New("invalid pairing secret")
	ErrInvalidTargetSKI         = errors.New("invalid target SKI")
	ErrInvalidTargetFingerprint = errors.New("invalid target fingerprint")

	// Operation errors
	ErrPairingAlreadyActive   = errors.New("pairing already active for target")
	ErrPairingNotActive       = errors.New("no active pairing for target")
	ErrListenerAlreadyActive  = errors.New("pairing listener already active")
	ErrAnnouncerAlreadyActive = errors.New("pairing announcer already active")
	ErrOperationCancelled     = errors.New("pairing operation cancelled")
	ErrDeviceAlreadyPaired    = errors.New("device is already paired/trusted")

	// Validation errors (used by validation functions)
	ErrInvalidHMACDigest     = errors.New("invalid HMAC digest")
	ErrReplayAttackDetected  = errors.New("replay attack detected - digest already seen")
	ErrInvalidTXTRecord      = errors.New("invalid TXT record structure")
	ErrUnsupportedTrustCurve = errors.New("unsupported trust curve")
	ErrUnsupportedAlgorithm  = errors.New("unsupported HMAC algorithm")

	// Provider interface errors
	ErrHistoryProviderFailed     = errors.New("pairing history provider failed")
	ErrHistoryProviderNotSet     = errors.New("pairing history provider not configured")
	ErrHubProviderNotSet         = errors.New("hub provider not configured")
	ErrMdnsProviderNotSet        = errors.New("mDNS provider not configured")
	ErrCertificateProviderNotSet = errors.New("certificate provider not configured")
	ErrCryptoProviderNotSet      = errors.New("crypto provider not configured")

	// Network and mDNS errors
	ErrMDNSAnnouncementFailed  = errors.New("mDNS pairing announcement failed")
	ErrMDNSSearchFailed        = errors.New("mDNS pairing search failed")
	ErrNetworkTimeout          = errors.New("network operation timeout")
	ErrConnectionEstablishment = errors.New("connection establishment failed")

	// Security errors
	ErrSecretTooShort        = errors.New("pairing secret too short (minimum 16 bytes)")
	ErrSecretTooLong         = errors.New("pairing secret too long (maximum 128 bytes)")
	ErrNonceGenerationFailed = errors.New("failed to generate cryptographic nonce")
	ErrHMACCalculationFailed = errors.New("HMAC calculation failed")
	ErrTimingAttackDetected  = errors.New("potential timing attack detected")
)
