package hub

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1" // #nosec G505 // SHIP 12.2: the SKI is defined as SHA-1 of the public key
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/enbility/ship-go/mocks"
	"github.com/enbility/ship-go/util"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Tests for TC_SHIP_SEC_001 (EEBus SHIP TestSpecification V1.0.0 §4.4.1) and TC_SHIP_SEC_002
// (§4.4.2), i.e. requirements SHIP-TS-SEC-01 and SHIP-TS-SEC-02:
//
//	"A SHIP node SHALL compute SHA-1 of the public key extracted from a received certificate
//	 and compare it against the trusted SKI of the peer. If they do not match, the node SHALL
//	 reject the certificate and abort the TLS handshake."
//
// The tests below cover the client role of the DUT (SEC_001 step 2 / SEC_002 step 3): the test
// tool acts as a pure server and presents a spoofed certificate. The server role of the DUT
// (SEC_001 step 1 / SEC_002 step 2) is covered by TestVerifyPeerCertificate in
// hub_connections_unit_test.go.
//
// The client role verifies the trusted fingerprint during the handshake as well: after SHIP
// Pairing (parType=fpSha256) the trusted entry holds a SHIP ID and a fingerprint, and the SKI is
// taken from the unauthenticated mDNS announcement (hub_mdns.go), so only the fingerprint
// authenticates the peer.

// spoofedCertificate builds the test tool's spoofed certificate.
//
// It generates a fresh key pair (PUB_T2/PRV_T2 in the wording of TestSpec §4.4.2) and writes
// forgedSKI into the SubjectKeyId field instead of SHA-1(PUB_T2), so the certificate parses
// fine but violates the SKI = SHA-1(public key) rule of SHIP 12.2.
//
// forgedSKI nil is the TC_SHIP_SEC_001 variant: there is no prior pairing and therefore no
// particular SKI worth forging, so the certificate's own SKI is corrupted instead.
// A non-nil forgedSKI is the TC_SHIP_SEC_002 variant: SKI_T1, which the DUT already trusts.
func spoofedCertificate(t *testing.T, forgedSKI []byte) tls.Certificate {
	t.Helper()

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	ski := forgedSKI
	if ski == nil {
		ecdhKey, err := privateKey.PublicKey.ECDH()
		require.NoError(t, err)
		realSKI := sha1.Sum(ecdhKey.Bytes()) // #nosec G401 // SHIP 12.2 defines SHA-1 here

		ski = make([]byte, len(realSKI))
		copy(ski, realSKI[:])
		ski[0] = ^ski[0] // flip a byte so SKI != SHA-1(public key)
	}
	require.Len(t, ski, 20, "a SHIP SKI is 20 bytes")

	template := x509.Certificate{
		SignatureAlgorithm:    x509.ECDSAWithSHA256,
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "spoofed-test-tool"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour * 24 * 365),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		SubjectKeyId:          ski,
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	require.NoError(t, err)

	return tls.Certificate{
		Certificate:                  [][]byte{certBytes},
		PrivateKey:                   privateKey,
		SupportedSignatureAlgorithms: []tls.SignatureScheme{tls.ECDSAWithP256AndSHA256},
	}
}

// legitimateCertificate returns a spec conformant certificate together with its SKI.
func legitimateCertificate(t *testing.T, commonName string) (tls.Certificate, string, []byte) {
	t.Helper()

	tlsCert, err := cert.CreateCertificate("unit", "org", "DE", commonName)
	require.NoError(t, err)

	parsed, err := x509.ParseCertificate(tlsCert.Certificate[0])
	require.NoError(t, err)

	ski, err := cert.SkiFromCertificate(parsed)
	require.NoError(t, err)

	return tlsCert, util.NormalizeSKI(ski), parsed.SubjectKeyId
}

// certificateFingerprint returns the SHA-256 fingerprint of a certificate, the identifier SHIP
// Pairing trusts a peer by.
func certificateFingerprint(t *testing.T, certificate tls.Certificate) string {
	t.Helper()

	parsed, err := x509.ParseCertificate(certificate.Certificate[0])
	require.NoError(t, err)

	fingerprint, err := cert.FingerprintFromCertificate(parsed)
	require.NoError(t, err)

	return fingerprint
}

// newRemoteService returns the DUT's entry for the peer it dials, holding the identifiers the
// DUT trusts for it.
func newRemoteService(t *testing.T, ski, fingerprint, shipID string) *api.ServiceDetails {
	t.Helper()

	service, err := api.NewServiceDetails(ski, fingerprint, shipID)
	require.NoError(t, err)

	return service
}

// newSecTestHub builds a hub with a real local certificate.
//
// setupTestHubForTimer deliberately uses an empty tls.Certificate{}, which is fine for tests
// that never complete a handshake. Here it would make the TLS handshake fail with "tls: no
// certificates configured" against the test tool's ClientAuth: RequireAnyClientCert - an
// unrelated failure that would mask whether the certificate check actually fired.
func newSecTestHub(t *testing.T) *Hub {
	t.Helper()

	mdns := mocks.NewMdnsInterface(t)
	hubReader := mocks.NewHubReaderInterface(t)

	hubReader.EXPECT().RemoteServiceConnected(mock.AnythingOfType("api.ShipConnectionInterface")).Maybe()
	hubReader.EXPECT().RemoteServiceDisconnected(mock.AnythingOfType("string")).Maybe()
	hubReader.EXPECT().ServiceUpdated(mock.AnythingOfType("api.ServiceIdentity")).Maybe()
	hubReader.EXPECT().ServicePairingDetailUpdate(mock.AnythingOfType("string"), mock.AnythingOfType("*api.ConnectionStateDetail")).Maybe()
	hubReader.EXPECT().AllowWaitingForTrust(mock.AnythingOfType("string")).Return(false).Maybe()
	mdns.EXPECT().Shutdown().Maybe()

	localCert, localSKI, _ := legitimateCertificate(t, "device-under-test")

	localService, err := api.NewServiceDetails(localSKI, "", "")
	require.NoError(t, err)
	localService.SetShipID("sec-test-dut")

	hub, err := newTestHub(hubReader, mdns, 0, localCert, localService, nil)
	require.NoError(t, err)

	return hub
}

// countingListener counts accepted TCP connections, so a test can tell how many TLS
// handshakes a single dial attempt caused.
type countingListener struct {
	net.Listener
	accepts atomic.Int32
}

func (l *countingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err == nil {
		l.accepts.Add(1)
	}
	return conn, err
}

// certificateTestServer is the test tool acting as a pure SHIP server that presents a given
// certificate. It records whether the DUT got as far as the HTTP request, and how many TCP
// connections - one per TLS handshake - the DUT opened.
type certificateTestServer struct {
	host, port string
	handlerHit atomic.Bool
	listener   *countingListener
}

func startCertificateTestServer(t *testing.T, certificate tls.Certificate) *certificateTestServer {
	t.Helper()

	s := &certificateTestServer{}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reaching this point means the TLS handshake completed and the DUT sent
		// "GET /ship/" - exactly the behaviour the test lab rejected.
		s.handlerHit.Store(true)

		upgrader := websocket.Upgrader{
			CheckOrigin:  func(r *http.Request) bool { return true },
			Subprotocols: []string{api.ShipWebsocketSubProtocol},
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
	}))

	s.listener = &countingListener{Listener: server.Listener}
	server.Listener = s.listener
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{certificate},
		// SHIP 9: client authentication is required
		ClientAuth: tls.RequireAnyClientCert,
		// SHIP 9.1: the ciphers are reported insecure but are defined to be used by SHIP
		CipherSuites: cert.CipherSuites, // #nosec G402
		MinVersion:   tls.VersionTLS12,  // SHIP 9: mandatory TLS version
	}
	server.StartTLS()
	t.Cleanup(server.Close)

	var err error
	s.host, s.port, err = net.SplitHostPort(server.Listener.Addr().String())
	require.NoError(t, err)

	return s
}

// TestSpoofedServerCertificateAbortsTlsHandshake is the regression test for the finding that
// the DUT completed the WebSocket upgrade (GET /ship/ -> 101 Switching Protocols) against a
// test tool presenting a spoofed server certificate, and only closed afterwards with 1006.
//
// TestSpec §4.4.1 step 2 / §4.4.2 step 3 require the handshake to be terminated as soon as the
// server certificate has been received, so the peer must never see an HTTP request at all.
func TestSpoofedServerCertificateAbortsTlsHandshake(t *testing.T) {
	tests := []struct {
		name string
		// setup returns the certificate the test tool presents and the DUT's entry for the peer
		setup       func(t *testing.T) (tls.Certificate, *api.ServiceDetails)
		errContains string
	}{
		{
			// TC_SHIP_SEC_001: no prior pairing, so the certificate's own SKI is corrupted. The
			// peer is still identified by fingerprint and SHIP ID, which is what a
			// discovery-only entry looks like.
			name: "sec_001_no_prior_pairing",
			setup: func(t *testing.T) (tls.Certificate, *api.ServiceDetails) {
				spoofed := spoofedCertificate(t, nil)
				return spoofed, newRemoteService(t, "", certificateFingerprint(t, spoofed), "spoofed-test-tool")
			},
			errContains: "invalid SKI",
		},
		{
			// TC_SHIP_SEC_002: the SKI of a previously trusted certificate (SKI_T1) is forged
			// onto a different key pair.
			name: "sec_002_forged_trusted_ski",
			setup: func(t *testing.T) (tls.Certificate, *api.ServiceDetails) {
				_, skiT1, rawSKIT1 := legitimateCertificate(t, "trusted-peer")
				return spoofedCertificate(t, rawSKIT1), newRemoteService(t, skiT1, "", "")
			},
			errContains: "invalid SKI",
		},
		{
			// A genuine certificate, but not the one of the peer whose SKI is trusted.
			name: "genuine_certificate_of_another_ski",
			setup: func(t *testing.T) (tls.Certificate, *api.ServiceDetails) {
				_, trustedSKI, _ := legitimateCertificate(t, "trusted-peer")
				impostor, _, _ := legitimateCertificate(t, "impostor")
				return impostor, newRemoteService(t, trustedSKI, "", "")
			},
			errContains: "SKI mismatch",
		},
		{
			// SHIP Pairing leaves a trusted entry with a SHIP ID and a fingerprint. hub_mdns.go
			// takes the SKI from the mDNS announcement of that SHIP ID, so an impostor announcing
			// it gets its own SKI into the entry, and only the fingerprint is left to reject it.
			name: "ship_pairing_impostor_announcing_trusted_ship_id",
			setup: func(t *testing.T) (tls.Certificate, *api.ServiceDetails) {
				pairedCU, _, _ := legitimateCertificate(t, "paired-cu")
				impostor, impostorSKI, _ := legitimateCertificate(t, "impostor")

				service := newRemoteService(t, "", certificateFingerprint(t, pairedCU), "paired-cu-ship-id")
				service.SetSKI(impostorSKI) // as hub_mdns.go does with the announced SKI

				return impostor, service
			},
			errContains: "fingerprint mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hub := newSecTestHub(t)
			certificate, service := tt.setup(t)
			server := startCertificateTestServer(t, certificate)

			err := hub.connectFoundService(service, server.host, server.port, "/ship/")

			require.Error(t, err, "the server certificate must fail the connection")
			assert.ErrorIs(t, err, errCertificateRejected)
			assert.Contains(t, err.Error(), tt.errContains,
				"the error should name the check that rejected the certificate")

			// The actual requirement of SHIP-TS-SEC-01/02: the handshake is aborted while the
			// server certificate is being processed, so no HTTP request is ever written and
			// the peer never answers 101 Switching Protocols.
			assert.False(t, server.handlerHit.Load(),
				"TLS handshake must be aborted before the websocket upgrade - no GET /ship/, no 101")

			// One rejected certificate must not cause a second, equally doomed handshake via
			// the path-less fallback URL.
			assert.EqualValues(t, 1, server.listener.accepts.Load(),
				"a rejected certificate must not be retried without the path")
		})
	}

	// Control: the trusted peer's own certificate reaches the same server, so the rejections
	// above are the certificate check's doing and not a broken test server.
	t.Run("genuine_certificate_of_the_trusted_peer", func(t *testing.T) {
		hub := newSecTestHub(t)
		trusted, trustedSKI, _ := legitimateCertificate(t, "trusted-peer")
		service := newRemoteService(t, trustedSKI, certificateFingerprint(t, trusted), "")
		server := startCertificateTestServer(t, trusted)

		conn, err := hub.establishWebSocketConnection(server.host, server.port, "/ship/", nil, service)
		require.NoError(t, err)
		_ = conn.Close()

		assert.True(t, server.handlerHit.Load())
		assert.EqualValues(t, 1, server.listener.accepts.Load())
	})
}

// TestVerifyServerCertificateHook drives the VerifyPeerCertificate hook exactly as crypto/tls
// does mid-handshake, through the real dialer so the wiring is covered as well.
func TestVerifyServerCertificateHook(t *testing.T) {
	hub := newSecTestHub(t)

	validCert, validSKI, _ := legitimateCertificate(t, "valid-peer")
	otherCert, otherSKI, _ := legitimateCertificate(t, "other-peer")
	_, skiT1, rawSKIT1 := legitimateCertificate(t, "trusted-peer")

	spoofedSelf := spoofedCertificate(t, nil)
	spoofedForged := spoofedCertificate(t, rawSKIT1)

	validFingerprint := certificateFingerprint(t, validCert)
	otherFingerprint := certificateFingerprint(t, otherCert)

	tests := []struct {
		name                string
		rawCerts            [][]byte
		expectedSKI         string
		expectedFingerprint string
		errContains         string
	}{
		{
			// TC_SHIP_SEC_001: no prior pairing, so no trusted SKI - the certificate is still
			// rejected because its SKI is not SHA-1 of its own public key.
			name:                "sec_001_spoofed_no_prior_pairing",
			rawCerts:            spoofedSelf.Certificate,
			expectedFingerprint: certificateFingerprint(t, spoofedSelf),
			errContains:         "invalid SKI",
		},
		{
			// TC_SHIP_SEC_002: the forged SKI is one the DUT trusts from an earlier pairing.
			name:        "sec_002_spoofed_forged_trusted_ski",
			rawCerts:    spoofedForged.Certificate,
			expectedSKI: skiT1,
			errContains: "invalid SKI",
		},
		{
			name:        "valid_cert_matching_trusted_ski",
			rawCerts:    validCert.Certificate,
			expectedSKI: validSKI,
		},
		{
			name:        "valid_cert_wrong_trusted_ski",
			rawCerts:    validCert.Certificate,
			expectedSKI: otherSKI,
			errContains: "SKI mismatch",
		},
		{
			// SHIP Pairing (parType=fpSha256): the trusted entry has a fingerprint, but no SKI yet.
			name:                "valid_cert_matching_trusted_fingerprint_no_ski",
			rawCerts:            validCert.Certificate,
			expectedFingerprint: validFingerprint,
		},
		{
			name:                "valid_cert_wrong_trusted_fingerprint_no_ski",
			rawCerts:            validCert.Certificate,
			expectedFingerprint: otherFingerprint,
			errContains:         "fingerprint mismatch",
		},
		{
			// The SKI matches - e.g. one taken from mDNS - but the certificate is not the one
			// trusted by fingerprint.
			name:                "valid_cert_matching_ski_wrong_fingerprint",
			rawCerts:            validCert.Certificate,
			expectedSKI:         validSKI,
			expectedFingerprint: otherFingerprint,
			errContains:         "fingerprint mismatch",
		},
		{
			name:        "empty_raw_certs",
			rawCerts:    nil,
			expectedSKI: validSKI,
			errContains: "no SKI in certificate",
		},
		{
			name:        "unparseable_der",
			rawCerts:    [][]byte{{0x00, 0x01, 0x02}},
			expectedSKI: validSKI,
			errContains: "x509",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := newRemoteService(t, tt.expectedSKI, tt.expectedFingerprint, "peer-ship-id")
			verify := hub.createWebSocketDialer(nil, service).TLSClientConfig.VerifyPeerCertificate
			require.NotNil(t, verify, "the dialer must install the handshake-time certificate check")

			err := verify(tt.rawCerts, nil)

			if tt.errContains == "" {
				assert.NoError(t, err)
				return
			}

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errContains)
			assert.ErrorIs(t, err, errCertificateRejected,
				"a rejected certificate must be recognisable via errCertificateRejected")
		})
	}

	// Without a trusted SKI or fingerprint there is nothing to verify the peer against, so no
	// certificate - not even a genuine one - may pass.
	t.Run("no_remote_service", func(t *testing.T) {
		verify := hub.createWebSocketDialer(nil, nil).TLSClientConfig.VerifyPeerCertificate
		require.NotNil(t, verify, "the dialer must install the handshake-time certificate check")

		err := verify(validCert.Certificate, nil)
		assert.ErrorIs(t, err, errCertificateRejected)
	})
}
