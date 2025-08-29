package api

/* Status Types */

/* Constants */

// Algorithm and parameter constants per SHIP Pairing Service specification
const (
	AlgorithmHMACSHA256  = "hmacSha256"
	ParTypeFPSHA256      = "fpSha256"
	CommandTypeAddCU     = "addCu"
	CurveSecp256r1       = "secp256r1"
	CurveBrainpoolP256r1 = "brainpoolP256r1"
	CurveBrainpoolP384r1 = "brainpoolP384r1"
)

/* TXT Record Structure */

// ShipPairingTXT represents the TXT record structure for _shippairing._tcp service
// Per SHIP Pairing Service specification Table 1
type ShipPairingTXT struct {
	TxtVers    string // "1" - Version number of TXT format
	ParType    string // "fpSha256" - Parameter type (fingerprint SHA-256)
	ForId      string // devA SHIP ID
	ForPar     string // SHA-256 fingerprint of devA certificate
	TrustId    string // devZ SHIP ID
	TrustPar   string // SHA-256 fingerprint of devZ certificate
	TrustCurve string // "secp256r1" | "brainpoolP256r1" | "brainpoolP384r1"
	Type       string // "addCu" - Command type
	TrustNonce string // devZ nonce (128-bit hex)
	Alg        string // "hmacSha256" - Algorithm
	Digest     string // HMAC result (256-bit hex)
}

// ToMap converts the ShipPairingTXT structure to a map[string]string for mDNS TXT records.
// This method is used when publishing SHIP pairing announcements via mDNS, as TXT records
// are represented as key-value string pairs. The field names match exactly those specified
// in SHIP Pairing Service specification Table 1.
//
// Returns:
//   - A map containing all TXT record fields as string key-value pairs
//   - Keys are lowercase field names (e.g., "txtvers", "parType", "forId")
//   - All values are strings, with binary data hex-encoded
//
// Usage:
//
//	This is typically called by mDNS providers when announcing pairing services
func (sp *ShipPairingTXT) ToMap() map[string]string {
	return map[string]string{
		"txtvers":    sp.TxtVers,
		"parType":    sp.ParType,
		"forId":      sp.ForId,
		"forPar":     sp.ForPar,
		"trustId":    sp.TrustId,
		"trustPar":   sp.TrustPar,
		"trustCurve": sp.TrustCurve,
		"type":       sp.Type,
		"trustNonce": sp.TrustNonce,
		"alg":        sp.Alg,
		"digest":     sp.Digest,
	}
}

// FromMap populates the ShipPairingTXT structure from a map[string]string received from mDNS TXT records.
// This method is used when parsing SHIP pairing announcements discovered via mDNS. It validates
// that all required fields are present and that the txtvers field has the expected value.
//
// Parameters:
//   - txtMap: Map of TXT record key-value pairs received from mDNS discovery
//
// Returns:
//   - nil if parsing succeeds and all required fields are present and valid
//   - PairingValidationError if any required field is missing or txtvers is invalid
//
// Validation:
//   - Checks for presence of all 11 required fields per SHIP spec Table 1
//   - Validates txtvers field equals "1" (current TXT record format version)
//   - Does not validate field content (use Validate() method for that)
//
// Usage:
//
//	This is typically called by mDNS listeners when processing discovered pairing announcements
func (sp *ShipPairingTXT) FromMap(txtMap map[string]string) error {
	// Validate required fields per SHIP spec Table 1
	required := []string{"txtvers", "parType", "forId", "forPar", "trustId",
		"trustPar", "trustCurve", "type", "trustNonce", "alg", "digest"}

	for _, field := range required {
		if _, exists := txtMap[field]; !exists {
			return NewPairingValidationError("missing required TXT field: " + field)
		}
	}

	// Validate txtvers is first and equals "1"
	if txtMap["txtvers"] != "1" {
		return NewPairingValidationError("invalid txtvers, expected '1'")
	}

	// Populate struct fields
	sp.TxtVers = txtMap["txtvers"]
	sp.ParType = txtMap["parType"]
	sp.ForId = txtMap["forId"]
	sp.ForPar = txtMap["forPar"]
	sp.TrustId = txtMap["trustId"]
	sp.TrustPar = txtMap["trustPar"]
	sp.TrustCurve = txtMap["trustCurve"]
	sp.Type = txtMap["type"]
	sp.TrustNonce = txtMap["trustNonce"]
	sp.Alg = txtMap["alg"]
	sp.Digest = txtMap["digest"]

	return nil
}

// Validate checks if all TXT record field values conform to SHIP Pairing Service specification.
// This method validates field content after the structure has been populated (typically after
// calling FromMap). It ensures that enum values are within supported ranges and algorithms
// are supported by this implementation.
//
// Returns:
//   - nil if all field values are valid per SHIP specification
//   - PairingValidationError if any field contains an unsupported or invalid value
//
// Validation checks:
//   - Algorithm must be "hmacSha256" (only supported HMAC algorithm)
//   - Parameter type must be "fpSha256" (fingerprint SHA-256)
//   - Command type must be "addCu" (only supported pairing command)
//   - Trust curve must be one of: secp256r1, brainpoolP256r1, brainpoolP384r1
//
// Usage:
//
//	Call this method after FromMap() to ensure received pairing data is processable
func (sp *ShipPairingTXT) Validate() error {
	// Check required algorithm
	if sp.Alg != AlgorithmHMACSHA256 {
		return NewPairingValidationError("unsupported algorithm: " + sp.Alg)
	}

	// Check parameter type
	if sp.ParType != ParTypeFPSHA256 {
		return NewPairingValidationError("unsupported parameter type: " + sp.ParType)
	}

	// Check command type
	if sp.Type != CommandTypeAddCU {
		return NewPairingValidationError("unsupported command type: " + sp.Type)
	}

	// Check trust curve
	validCurves := []string{CurveSecp256r1, CurveBrainpoolP256r1, CurveBrainpoolP384r1}
	valid := false
	for _, curve := range validCurves {
		if sp.TrustCurve == curve {
			valid = true
			break
		}
	}
	if !valid {
		return NewPairingValidationError("unsupported trust curve: " + sp.TrustCurve)
	}

	return nil
}

/* Error Definitions */

// Custom error types for better error handling
type PairingValidationError struct {
	Field   string
	Message string
}

func (e *PairingValidationError) Error() string {
	if e.Field != "" {
		return "pairing validation error [" + e.Field + "]: " + e.Message
	}
	return "pairing validation error: " + e.Message
}

// NewPairingValidationError creates a new PairingValidationError with a general validation message.
// This constructor is used for validation errors that don't relate to a specific field.
//
// Parameters:
//   - message: Human-readable description of the validation error
//
// Returns:
//   - A new PairingValidationError instance with the specified message
//
// Example usage:
//
//	return NewPairingValidationError("unsupported algorithm: " + alg)
func NewPairingValidationError(message string) *PairingValidationError {
	return &PairingValidationError{Message: message}
}

// NewPairingFieldValidationError creates a new PairingValidationError for a specific field.
// This constructor is used when the validation error relates to a particular field in the
// pairing data structure, providing more context for debugging and error handling.
//
// Parameters:
//   - field: The name of the field that failed validation (e.g., "trustCurve", "digest")
//   - message: Human-readable description of why the field validation failed
//
// Returns:
//   - A new PairingValidationError instance with field-specific error information
//
// Example usage:
//
//	return NewPairingFieldValidationError("trustNonce", "nonce must be 32 hex characters")
func NewPairingFieldValidationError(field, message string) *PairingValidationError {
	return &PairingValidationError{Field: field, Message: message}
}
