package pairing

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/enbility/ship-go/mocks"
)

// TestHistoryProvider implements PairingHistoryProviderInterface for pairing tests
type TestHistoryProvider struct {
	seen map[string]bool
	last *api.DigestEntry
}

func NewTestHistoryProvider() *TestHistoryProvider {
	return &TestHistoryProvider{
		seen: make(map[string]bool),
	}
}

func (t *TestHistoryProvider) HasSeenDigest(alg, digest string) bool {
	key := alg + ":" + digest
	return t.seen[key]
}

func (t *TestHistoryProvider) RecordPairing(alg, digest string) {
	key := alg + ":" + digest
	t.seen[key] = true
	
	// Track most recent entry for GetCurrentEntry
	t.last = &api.DigestEntry{
		Algorithm: alg,
		Digest:    digest,
		Timestamp: time.Now(),
	}
}

// GetCurrentEntry returns the most recent entry (simplified for testing)
func (t *TestHistoryProvider) GetCurrentEntry() (*api.DigestEntry, error) {
	if t.last == nil {
		return nil, fmt.Errorf("no current entry")
	}
	return t.last, nil
}

// SpecificationIntegrationTestSuite provides end-to-end integration tests using exact
// test vectors from SHIP Pairing Service specification Annex A
type SpecificationIntegrationTestSuite struct {
	suite.Suite

	// Test certificates from specification Annex A
	devACert tls.Certificate
	devZCert tls.Certificate

	// Service components with mocks
	announcerService *Service
	listenerService  *Service

	// Service details from specification
	devAService *api.ServiceDetails
	devZService *api.ServiceDetails

	// Specification test data
	devASecret     api.PairingSecret
	devASecretHex  string
	devZNonceHex   string
	expectedDigest string

	// Mocks
	mockAnnouncerMdns *mocks.MdnsPairingInterface
	mockListenerMdns  *mocks.MdnsPairingInterface
	mockAnnouncerHub  *mocks.PairingHubInterface
	mockListenerHub   *mocks.PairingHubInterface
}

func TestSpecificationIntegrationTestSuite(t *testing.T) {
	suite.Run(t, new(SpecificationIntegrationTestSuite))
}

func (suite *SpecificationIntegrationTestSuite) SetupTest() {
	// Load exact test certificates from SHIP specification Annex A
	suite.setupSpecificationCertificates()

	// Setup test data from specification
	suite.setupSpecificationTestData()

	// Create service components
	suite.setupServices()
}

func (suite *SpecificationIntegrationTestSuite) setupSpecificationCertificates() {
	// devA certificate from SHIP spec Annex A.1
	devAPEM := `-----BEGIN CERTIFICATE-----
MIICJjCCAcugAwIBAgIURCTotMcqCC1Dt8VqkoywEXiXX2QwCgYIKoZIzj0EAwIw
aDELMAkGA1UEBhMCREUxDDAKBgNVBAgMA05SVzEQMA4GA1UEBwwHQ29sb2duZTEV
MBMGA1UECgwMRVhBTVBMRUJSQU5EMSIwIAYDVQQDDBlFRUIwMWRldkE4MTQtQzgy
NzdIMDA4Ri0zMB4XDTI1MDgxMTE2NTQwOVoXDTI5MDgxMTE2NTQwOVowaDELMAkG
A1UEBhMCREUxDDAKBgNVBAgMA05SVzEQMA4GA1UEBwwHQ29sb2duZTEVMBMGA1UE
CgwMRVhBTVBMRUJSQU5EMSIwIAYDVQQDDBlFRUIwMWRldkE4MTQtQzgyNzdIMDA4
Ri0zMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEsKOrknwvjOfO+ihEu9EIIwhM
rJraXone1kWiRA1DMVZK7A1k0rlEmCVL1XBOM7/wcloIrXnYX7iCqN3oBuarSKNT
MFEwHQYDVR0OBBYEFKgFVqJD9Xyhew1Xelm3EbeetIVsMB8GA1UdIwQYMBaAFKgF
VqJD9Xyhew1Xelm3EbeetIVsMA8GA1UdEwEB/wQFMAMBAf8wCgYIKoZIzj0EAwID
SQAwRgIhAIc7Is5jXiphSqXqK0V4yu5H2D+JxHBuLWDb74TnsKXOAiEAi0yLNr4z
YyfuxNevdBQr+PkpeskDrpJaAaUPg81JlQg=
-----END CERTIFICATE-----`

	// devZ certificate from SHIP spec Annex A.2
	devZPEM := `-----BEGIN CERTIFICATE-----
MIICLjCCAdOgAwIBAgIUN8jFQuYTDfjInHKhQQQkNVMkHRAwCgYIKoZIzj0EAwIw
bDELMAkGA1UEBhMCREUxDDAKBgNVBAgMA05SVzEQMA4GA1UEBwwHQ29sb2duZTEa
MBgGA1UECgwRT1RIRVJFWEFNUExFQlJBTkQxITAfBgNVBAMMGEVFQjAyc3RiNTQt
NDM2NTJiay0yLWd0MTAeFw0yNTA4MTExNTQ4MjlaFw0yOTA4MTExNTQ4MjlaMGwx
CzAJBgNVBAYTAkRFMQwwCgYDVQQIDANOUlcxEDAOBgNVBAcMB0NvbG9nbmUxGjAY
BgNVBAoMEU9USEVSRVhBTVBMRUJSQU5EMSEwHwYDVQQDDBhFRUIwMnN0YjU0LTQz
NjUyYmstMi1ndDEwWTATBgcqhkjOPQIBBggqhkjOPQMBBwNCAARB4qKF7gkB0dpc
p7aGTT3YnAX2Im5HE3mtqKzDOuPoSl+YG19hpumso11J4T6iwjX2oORjrxFPmR3P
Pd1TAOX7o1MwUTAdBgNVHQ4EFgQUvehlF5Rzk0Fr3BVbMzgiQyN3FRcwHwYDVR0j
BBgwFoAUvehlF5Rzk0Fr3BVbMzgiQyN3FRcwDwYDVR0TAQH/BAUwAwEB/zAKBggq
hkjOPQQDAgNJADBGAiEAmFBWkuUyLe9546+5uwpyuOVi7fk6ZI3ahNhAs+FQJJMC
IQCqkzJAxrjNHJ3dcyQGsP5CSskTrVtTbqnTR3DbxvxAQQ==
-----END CERTIFICATE-----`

	// Parse certificates
	var err error
	suite.devACert, err = parsePEMToCert(devAPEM)
	require.NoError(suite.T(), err)

	suite.devZCert, err = parsePEMToCert(devZPEM)
	require.NoError(suite.T(), err)
}

func (suite *SpecificationIntegrationTestSuite) setupSpecificationTestData() {
	// Test data from SHIP specification Annex A
	suite.devASecretHex = "7A37DCF81BDB50F8E92CFA4160CCB3DE"                                  // devA secret from spec
	suite.devZNonceHex = "BDCEE427FA7208DF3C1F2A749BA6F4D4"                                   // devZ nonce from spec
	suite.expectedDigest = "BCBB62B2176DA2CEE545784CEB1F2A55E049451B12A549C98E8CA213F001DA25" // Expected digest from spec

	// Convert secret to bytes
	secretBytes, err := hexToBytes(suite.devASecretHex)
	require.NoError(suite.T(), err)
	suite.devASecret = api.PairingSecret(secretBytes)

	// Extract certificate information for service details
	devAX509, err := x509.ParseCertificate(suite.devACert.Certificate[0])
	require.NoError(suite.T(), err)

	devZX509, err := x509.ParseCertificate(suite.devZCert.Certificate[0])
	require.NoError(suite.T(), err)

	// Get SKIs
	devASKI, err := cert.SkiFromCertificate(devAX509)
	require.NoError(suite.T(), err)

	devZSKI, err := cert.SkiFromCertificate(devZX509)
	require.NoError(suite.T(), err)

	// Get fingerprints
	devAFingerprint, err := cert.FingerprintFromCertificate(devAX509)
	require.NoError(suite.T(), err)

	devZFingerprint, err := cert.FingerprintFromCertificate(devZX509)
	require.NoError(suite.T(), err)

	// Verify fingerprints match specification
	assert.Equal(suite.T(), "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943", devAFingerprint, "devA fingerprint should match spec")
	assert.Equal(suite.T(), "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4", devZFingerprint, "devZ fingerprint should match spec")

	// Create service details from specification data
	suite.devAService = api.NewServiceDetails(devASKI, devAFingerprint, "")
	suite.devAService.SetShipID("i:983327_u:C8277H008F-3") // devA SHIP ID from spec

	suite.devZService = api.NewServiceDetails(devZSKI, devZFingerprint, "")
	suite.devZService.SetShipID("i:46925_u:43652bk-2-gt1") // devZ SHIP ID from spec
}

func (suite *SpecificationIntegrationTestSuite) setupServices() {
	// Create mocks
	suite.mockAnnouncerMdns = mocks.NewMdnsPairingInterface(suite.T())
	suite.mockListenerMdns = mocks.NewMdnsPairingInterface(suite.T())
	suite.mockAnnouncerHub = mocks.NewPairingHubInterface(suite.T())
	suite.mockListenerHub = mocks.NewPairingHubInterface(suite.T())

	// Create crypto implementations (real, not mocked)
	announcerCrypto := NewHMACCalculator()
	listenerCrypto := NewHMACCalculator()

	// Create history providers (real, not mocked for integration test)
	announcerHistory := NewTestHistoryProvider()
	listenerHistory := NewTestHistoryProvider()

	// Create services
	var err error
	suite.announcerService, err = NewService(
		suite.mockAnnouncerMdns,
		announcerCrypto,
		announcerHistory,
		suite.mockAnnouncerHub,
		suite.devZCert, // devZ uses its own cert
	)
	require.NoError(suite.T(), err)

	suite.listenerService, err = NewService(
		suite.mockListenerMdns,
		listenerCrypto,
		listenerHistory,
		suite.mockListenerHub,
		suite.devACert, // devA uses its own cert
	)
	require.NoError(suite.T(), err)
}

// TestSpecificationCompleteFlow tests the complete pairing flow using exact specification data
func (suite *SpecificationIntegrationTestSuite) TestSpecificationCompleteFlow() {
	// Start both services
	err := suite.announcerService.Start()
	require.NoError(suite.T(), err)
	defer suite.announcerService.Shutdown()

	err = suite.listenerService.Start()
	require.NoError(suite.T(), err)
	defer suite.listenerService.Shutdown()

	// Setup devZ (announcer) to announce to devA
	announcerInterface := suite.announcerService.CreateAnnouncer(suite.devZService)
	require.NotNil(suite.T(), announcerInterface)
	
	// Cast to concrete type to access EnablePairingService
	announcer, ok := announcerInterface.(*PairingAnnouncer)
	require.True(suite.T(), ok)

	// Configure announcer with specification secret
	config := &PairingConfiguration{
		Mode:    PairingModeAnnouncer,
		Secret:  suite.devASecret, // devZ knows devA's secret (from QR code)
		Enabled: true,
	}
	err = announcer.EnablePairingService(config)
	require.NoError(suite.T(), err)

	// Create pairing target from specification data
	target := &api.PairingTarget{
		SKI:         suite.devAService.SKI(),
		Fingerprint: suite.devAService.Fingerprint(),
		ShipID:      suite.devAService.ShipID(),
		Secret:      []byte(suite.devASecret), // Convert to bytes for target
	}

	// Mock the mDNS announcement with exact specification TXT record
	expectedTXT := &api.ShipPairingTXT{
		TxtVers:    "1",
		ParType:    "fpSha256",
		ForId:      "i:983327_u:C8277H008F-3",                                          // devA SHIP ID from spec
		ForPar:     "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943", // devA fingerprint from spec
		TrustId:    "i:46925_u:43652bk-2-gt1",                                          // devZ SHIP ID from spec
		TrustPar:   "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4", // devZ fingerprint from spec
		TrustCurve: "secp256r1",                                                        // Curve from spec
		Type:       "addCu",                                                            // Command type from spec
		TrustNonce: suite.devZNonceHex,                                                 // devZ nonce from spec (will be set by mock)
		Alg:        "hmacSha256",                                                       // Algorithm from spec
		Digest:     suite.expectedDigest,                                               // Expected digest from spec (will be set by mock)
	}

	// Set up mock expectations for mDNS announcement
	suite.mockAnnouncerMdns.EXPECT().AnnouncePairingService(
		mock.MatchedBy(func(txt *api.ShipPairingTXT) bool {
			// Verify TXT record structure matches specification
			return txt.TxtVers == expectedTXT.TxtVers &&
				txt.ParType == expectedTXT.ParType &&
				txt.ForId == expectedTXT.ForId &&
				txt.ForPar == expectedTXT.ForPar &&
				txt.TrustId == expectedTXT.TrustId &&
				txt.TrustPar == expectedTXT.TrustPar &&
				txt.TrustCurve == expectedTXT.TrustCurve &&
				txt.Type == expectedTXT.Type &&
				txt.Alg == expectedTXT.Alg &&
				len(txt.TrustNonce) == 32 && // 128-bit nonce as hex
				len(txt.Digest) == 64 // 256-bit digest as hex
		})).Return("test-announcement-instance", nil).Once()

	// Execute announcement
	err = announcer.Announce(target)
	require.NoError(suite.T(), err)

	// Verify announcement status
	status := announcer.GetAnnouncementStatus()
	assert.True(suite.T(), status.Active, "announcement should be active")
	assert.Equal(suite.T(), target, status.Target, "target should match")

	// Simulate devA (listener) discovering and validating the announcement
	// In a real scenario, this would be triggered by mDNS discovery
	listener := suite.listenerService.CreateListener(suite.devAService)
	require.NotNil(suite.T(), listener)

	// Create the exact TXT record that would be received via mDNS
	// Using the specification test vectors to simulate what devA would receive
	discoveredTXT := &api.ShipPairingTXT{
		TxtVers:    "1",
		ParType:    "fpSha256",
		ForId:      "i:983327_u:C8277H008F-3",                                          // devA SHIP ID
		ForPar:     "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943", // devA fingerprint
		TrustId:    "i:46925_u:43652bk-2-gt1",                                          // devZ SHIP ID
		TrustPar:   "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4", // devZ fingerprint
		TrustCurve: "secp256r1",                                                        // Curve
		Type:       "addCu",                                                            // Command type
		TrustNonce: suite.devZNonceHex,                                                 // devZ nonce from spec
		Alg:        "hmacSha256",                                                       // Algorithm
		Digest:     suite.expectedDigest,                                               // Expected digest from spec
	}

	// Test devA validation logic directly using specification data
	suite.testDevAValidation(discoveredTXT)

	// Test cleanup
	suite.mockAnnouncerMdns.EXPECT().UnannouncePairingService("test-announcement-instance").Return(nil).Once()

	err = announcer.StopAnnouncement()
	assert.NoError(suite.T(), err, "should stop announcement successfully")

	// Verify cleanup
	finalStatus := announcer.GetAnnouncementStatus()
	assert.False(suite.T(), finalStatus.Active, "announcement should be stopped")
	assert.Nil(suite.T(), finalStatus.Target, "target should be cleared")
}

func (suite *SpecificationIntegrationTestSuite) testDevAValidation(txtRecord *api.ShipPairingTXT) {
	// Create HMAC calculator for devA (listener)
	crypto := NewHMACCalculator()

	// Convert nonce from hex
	nonce, err := hexToBytes(txtRecord.TrustNonce)
	require.NoError(suite.T(), err)

	// Create HMAC parameters
	params := &api.HMACParams{
		Algorithm: txtRecord.Alg,
		Nonce:     nonce,
		TxtRecord: txtRecord,
	}

	// Calculate digest using devA's secret
	calculatedDigest, err := crypto.CalculateDigest(suite.devASecret, params)
	require.NoError(suite.T(), err, "devA should be able to calculate digest")

	// Convert to hex for comparison
	calculatedDigestHex := bytesToHex(calculatedDigest)

	// Verify digest matches specification
	assert.Equal(suite.T(), suite.expectedDigest, calculatedDigestHex,
		"calculated digest should match specification test vector")

	// Test validation function
	expectedDigestBytes, err := hexToBytes(suite.expectedDigest)
	require.NoError(suite.T(), err)

	err = crypto.ValidateDigest(suite.devASecret, params, expectedDigestBytes)
	assert.NoError(suite.T(), err, "digest validation should succeed with correct secret and data")

	// Test validation with wrong secret (should fail)
	wrongSecret := api.PairingSecret([]byte("wrong-secret-1234"))
	err = crypto.ValidateDigest(wrongSecret, params, expectedDigestBytes)
	assert.Error(suite.T(), err, "digest validation should fail with wrong secret")
	assert.Equal(suite.T(), api.ErrInvalidHMACDigest, err)

	// Test validation with tampered digest (should fail)
	tamperedDigest := make([]byte, len(expectedDigestBytes))
	copy(tamperedDigest, expectedDigestBytes)
	tamperedDigest[0] ^= 0xFF // Flip bits

	err = crypto.ValidateDigest(suite.devASecret, params, tamperedDigest)
	assert.Error(suite.T(), err, "digest validation should fail with tampered digest")
	assert.Equal(suite.T(), api.ErrInvalidHMACDigest, err)
}

// TestSpecificationHMACKeyConstruction tests key construction per SHIP spec section 7.3
func (suite *SpecificationIntegrationTestSuite) TestSpecificationHMACKeyConstruction() {
	// Test data from specification
	devASecret := suite.devASecretHex     // "7A37DCF81BDB50F8E92CFA4160CCB3DE"
	devZNonce := suite.devZNonceHex       // "BDCEE427FA7208DF3C1F2A749BA6F4D4"
	expectedKey := devASecret + devZNonce // Direct concatenation per spec section 7.3

	// Convert to bytes
	secretBytes, err := hexToBytes(devASecret)
	require.NoError(suite.T(), err)

	nonceBytes, err := hexToBytes(devZNonce)
	require.NoError(suite.T(), err)

	// Create calculator and construct key
	calculator := NewHMACCalculator()
	constructedKey := calculator.constructKey(api.PairingSecret(secretBytes), nonceBytes)

	// Verify key construction matches specification
	constructedKeyHex := bytesToHex(constructedKey)
	assert.Equal(suite.T(), expectedKey, constructedKeyHex,
		"key construction should match SHIP specification section 7.3")

	// Verify key length
	assert.Len(suite.T(), constructedKey, 32, "key should be 32 bytes (16 + 16)")
}

// TestSpecificationMessageConstruction tests message construction per SHIP spec section 7.4
func (suite *SpecificationIntegrationTestSuite) TestSpecificationMessageConstruction() {
	// Create TXT record from specification data
	txtRecord := &api.ShipPairingTXT{
		TxtVers:    "1",
		ParType:    "fpSha256",
		ForId:      "i:983327_u:C8277H008F-3",                                          // devA SHIP ID from spec
		ForPar:     "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943", // devA fingerprint from spec
		TrustId:    "i:46925_u:43652bk-2-gt1",                                          // devZ SHIP ID from spec
		TrustPar:   "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4", // devZ fingerprint from spec
		TrustCurve: "secp256r1",                                                        // Curve from spec
		Type:       "addCu",                                                            // Command type from spec
		TrustNonce: suite.devZNonceHex,                                                 // devZ nonce from spec
		Alg:        "hmacSha256",                                                       // Algorithm from spec
	}

	// Expected message from SHIP specification Annex A.3
	expectedMessage := "txtvers=1;parType=fpSha256;forId=i:983327_u:C8277H008F-3;forPar=C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943;trustId=i:46925_u:43652bk-2-gt1;trustPar=2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4;trustCurve=secp256r1;type=addCu;trustNonce=BDCEE427FA7208DF3C1F2A749BA6F4D4;alg=hmacSha256;"

	// Create calculator and construct message
	calculator := NewHMACCalculator()
	constructedMessage := calculator.constructMessage(txtRecord)

	// Verify message construction matches specification
	assert.Equal(suite.T(), expectedMessage, constructedMessage,
		"message construction should exactly match SHIP specification Annex A.3")
}

// TestSpecificationDigestCalculation tests complete digest calculation using exact spec data
func (suite *SpecificationIntegrationTestSuite) TestSpecificationDigestCalculation() {
	// Test the complete digest calculation pipeline using specification test vectors

	// Convert specification data to bytes
	secretBytes, err := hexToBytes(suite.devASecretHex)
	require.NoError(suite.T(), err)

	nonceBytes, err := hexToBytes(suite.devZNonceHex)
	require.NoError(suite.T(), err)

	// Create TXT record from specification
	txtRecord := &api.ShipPairingTXT{
		TxtVers:    "1",
		ParType:    "fpSha256",
		ForId:      "i:983327_u:C8277H008F-3",
		ForPar:     "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
		TrustId:    "i:46925_u:43652bk-2-gt1",
		TrustPar:   "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4",
		TrustCurve: "secp256r1",
		Type:       "addCu",
		TrustNonce: suite.devZNonceHex,
		Alg:        "hmacSha256",
	}

	// Create HMAC parameters
	params := &api.HMACParams{
		Algorithm: "hmacSha256",
		Nonce:     nonceBytes,
		TxtRecord: txtRecord,
	}

	// Calculate digest
	calculator := NewHMACCalculator()
	digest, err := calculator.CalculateDigest(api.PairingSecret(secretBytes), params)
	require.NoError(suite.T(), err, "digest calculation should succeed")

	// Convert to hex and compare with specification
	digestHex := bytesToHex(digest)
	assert.Equal(suite.T(), suite.expectedDigest, digestHex,
		"calculated digest should exactly match SHIP specification Annex A.3")

	// Verify digest length
	assert.Len(suite.T(), digest, 32, "digest should be 32 bytes (SHA-256)")
}

// TestSpecificationReplayProtection tests replay protection using ring buffer
func (suite *SpecificationIntegrationTestSuite) TestSpecificationReplayProtection() {
	// Create history provider
	history := NewTestHistoryProvider()

	// First pairing should succeed
	hasSeenBefore := history.HasSeenDigest("hmacSha256", suite.expectedDigest)
	assert.False(suite.T(), hasSeenBefore, "digest should not be seen initially")

	// Record the pairing
	history.RecordPairing("hmacSha256", suite.expectedDigest)

	// Second attempt should be detected as replay
	hasSeenAfter := history.HasSeenDigest("hmacSha256", suite.expectedDigest)
	assert.True(suite.T(), hasSeenAfter, "digest should be seen after recording")

	// Verify current entry
	entry, err := history.GetCurrentEntry()
	require.NoError(suite.T(), err)
	assert.Equal(suite.T(), "hmacSha256", entry.Algorithm)
	assert.Equal(suite.T(), suite.expectedDigest, entry.Digest)
}

// TestSpecificationTXTRecordValidation tests TXT record validation with spec data
func (suite *SpecificationIntegrationTestSuite) TestSpecificationTXTRecordValidation() {
	// Create TXT record map from specification
	txtMap := map[string]string{
		"txtvers":    "1",
		"parType":    "fpSha256",
		"forId":      "i:983327_u:C8277H008F-3",
		"forPar":     "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
		"trustId":    "i:46925_u:43652bk-2-gt1",
		"trustPar":   "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4",
		"trustCurve": "secp256r1",
		"type":       "addCu",
		"trustNonce": suite.devZNonceHex,
		"alg":        "hmacSha256",
		"digest":     suite.expectedDigest,
	}

	// Parse and validate
	txtRecord := &api.ShipPairingTXT{}
	err := txtRecord.FromMap(txtMap)
	require.NoError(suite.T(), err, "should parse specification TXT record")

	// Validate structure
	err = txtRecord.Validate()
	assert.NoError(suite.T(), err, "specification TXT record should be valid")

	// Test round-trip conversion
	convertedMap := txtRecord.ToMap()
	for key, expectedValue := range txtMap {
		actualValue, exists := convertedMap[key]
		assert.True(suite.T(), exists, "key %s should exist after conversion", key)
		assert.Equal(suite.T(), expectedValue, actualValue, "value for key %s should match", key)
	}
}

// TestSpecificationErrorConditions tests error conditions with specification data
func (suite *SpecificationIntegrationTestSuite) TestSpecificationErrorConditions() {
	// Test various error conditions using specification as baseline

	// 1. Test wrong algorithm
	txtRecord := &api.ShipPairingTXT{
		TxtVers:    "1",
		ParType:    "fpSha256",
		ForId:      "i:983327_u:C8277H008F-3",
		ForPar:     "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
		TrustId:    "i:46925_u:43652bk-2-gt1",
		TrustPar:   "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4",
		TrustCurve: "secp256r1",
		Type:       "addCu",
		TrustNonce: suite.devZNonceHex,
		Alg:        "sha1", // Wrong algorithm
	}

	err := txtRecord.Validate()
	assert.Error(suite.T(), err, "should reject wrong algorithm")
	assert.Contains(suite.T(), err.Error(), "unsupported algorithm")

	// 2. Test wrong parameter type
	txtRecord.Alg = "hmacSha256"
	txtRecord.ParType = "md5"
	err = txtRecord.Validate()
	assert.Error(suite.T(), err, "should reject wrong parameter type")
	assert.Contains(suite.T(), err.Error(), "unsupported parameter type")

	// 3. Test wrong command type
	txtRecord.ParType = "fpSha256"
	txtRecord.Type = "removeCu"
	err = txtRecord.Validate()
	assert.Error(suite.T(), err, "should reject wrong command type")
	assert.Contains(suite.T(), err.Error(), "unsupported command type")

	// 4. Test wrong curve
	txtRecord.Type = "addCu"
	txtRecord.TrustCurve = "invalidCurve"
	err = txtRecord.Validate()
	assert.Error(suite.T(), err, "should reject wrong curve")
	assert.Contains(suite.T(), err.Error(), "unsupported trust curve")
}

/* Helper Functions */

func parsePEMToCert(pemData string) (tls.Certificate, error) {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return tls.Certificate{}, api.ErrInvalidCertificate
	}

	_, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return tls.Certificate{}, api.ErrInvalidCertificate
	}

	// For testing purposes, we only need the certificate (private key not used in pairing tests)
	// In real usage, proper private keys would be loaded
	return tls.Certificate{
		Certificate: [][]byte{block.Bytes},
		PrivateKey:  nil, // Not needed for pairing validation tests
	}, nil
}
