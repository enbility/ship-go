package hub

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1" // #nosec G505 // SHIP 12.2: the SKI is defined as SHA-1 of the public key
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
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

// TestSpoofedServerCertificateAbortsTlsHandshake is the regression test for the finding that
// the DUT completed the WebSocket upgrade (GET /ship/ -> 101 Switching Protocols) against a
// test tool presenting a spoofed server certificate, and only closed afterwards with 1006.
//
// TestSpec §4.4.1 step 2 / §4.4.2 step 3 require the handshake to be terminated as soon as the
// server certificate has been received, so the peer must never see an HTTP request at all.
func TestSpoofedServerCertificateAbortsTlsHandshake(t *testing.T) {
	tests := []struct {
		name string
		// forgeTrustedSKI mirrors TC_SHIP_SEC_002: the spoofed certificate reuses the SKI of
		// a previously trusted certificate (SKI_T1) on a different key pair.
		forgeTrustedSKI bool
	}{
		{name: "sec_001_no_prior_pairing", forgeTrustedSKI: false},
		{name: "sec_002_forged_trusted_ski", forgeTrustedSKI: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hub := newSecTestHub(t)

			// trustedSKI is what the DUT expects for this peer. For SEC_002 it is SKI_T1 from
			// the earlier, legitimate pairing; for SEC_001 nothing is known yet.
			var trustedSKI string
			var spoofed tls.Certificate
			if tt.forgeTrustedSKI {
				_, skiT1, rawSKIT1 := legitimateCertificate(t, "trusted-peer")
				trustedSKI = skiT1
				spoofed = spoofedCertificate(t, rawSKIT1)
			} else {
				spoofed = spoofedCertificate(t, nil)
			}

			var handlerHit atomic.Bool
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Reaching this point means the TLS handshake completed and the DUT sent
				// "GET /ship/" - exactly the behaviour the test lab rejected.
				handlerHit.Store(true)

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

			listener := &countingListener{Listener: server.Listener}
			server.Listener = listener
			server.TLS = &tls.Config{
				Certificates: []tls.Certificate{spoofed},
				// SHIP 9: client authentication is required
				ClientAuth: tls.RequireAnyClientCert,
				// SHIP 9.1: the ciphers are reported insecure but are defined to be used by SHIP
				CipherSuites: cert.CipherSuites, // #nosec G402
				MinVersion:   tls.VersionTLS12,  // SHIP 9: mandatory TLS version
			}
			server.StartTLS()
			defer server.Close()

			host, port, err := net.SplitHostPort(server.Listener.Addr().String())
			require.NoError(t, err)

			// With no trusted SKI (SEC_001) the peer is still identified by fingerprint and
			// SHIP ID, which is what a discovery-only entry looks like.
			fingerprint := ""
			shipID := ""
			if trustedSKI == "" {
				parsed, err := x509.ParseCertificate(spoofed.Certificate[0])
				require.NoError(t, err)
				fingerprint, err = cert.FingerprintFromCertificate(parsed)
				require.NoError(t, err)
				shipID = "spoofed-test-tool"
			}

			service, err := api.NewServiceDetails(trustedSKI, fingerprint, shipID)
			require.NoError(t, err)

			err = hub.connectFoundService(service, host, port, "/ship/")

			require.Error(t, err, "a spoofed server certificate must fail the connection")
			assert.Contains(t, err.Error(), "SKI",
				"the error should name the SKI check that rejected the certificate")

			// The actual requirement of SHIP-TS-SEC-01/02: the handshake is aborted while the
			// server certificate is being processed, so no HTTP request is ever written and
			// the peer never answers 101 Switching Protocols.
			assert.False(t, handlerHit.Load(),
				"TLS handshake must be aborted before the websocket upgrade - no GET /ship/, no 101")

			// One rejected certificate must not cause a second, equally doomed handshake via
			// the path-less fallback URL.
			assert.EqualValues(t, 1, listener.accepts.Load(),
				"a rejected certificate must not be retried without the path")
		})
	}
}

// TestVerifyServerCertificateHook drives the VerifyPeerCertificate hook exactly as crypto/tls
// does mid-handshake, through the real dialer so the wiring is covered as well.
func TestVerifyServerCertificateHook(t *testing.T) {
	hub := newSecTestHub(t)

	validCert, validSKI, _ := legitimateCertificate(t, "valid-peer")
	_, otherSKI, _ := legitimateCertificate(t, "other-peer")
	_, skiT1, rawSKIT1 := legitimateCertificate(t, "trusted-peer")

	spoofedSelf := spoofedCertificate(t, nil)
	spoofedForged := spoofedCertificate(t, rawSKIT1)

	tests := []struct {
		name        string
		rawCerts    [][]byte
		expectedSKI string
		errContains string
	}{
		{
			// TC_SHIP_SEC_001: no prior pairing, so no trusted SKI - the certificate is still
			// rejected because its SKI is not SHA-1 of its own public key.
			name:        "sec_001_spoofed_no_prior_pairing",
			rawCerts:    spoofedSelf.Certificate,
			expectedSKI: "",
			errContains: "invalid SKI",
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
			// Fingerprint-only / pairing-discovery dial: the trusted SKI is not known yet.
			name:        "valid_cert_no_trusted_ski_yet",
			rawCerts:    validCert.Certificate,
			expectedSKI: "",
		},
		{
			name:        "valid_cert_wrong_trusted_ski",
			rawCerts:    validCert.Certificate,
			expectedSKI: otherSKI,
			errContains: "SKI mismatch",
		},
		{
			name:        "empty_raw_certs",
			rawCerts:    nil,
			expectedSKI: "",
			errContains: "no SKI in certificate",
		},
		{
			name:        "unparseable_der",
			rawCerts:    [][]byte{{0x00, 0x01, 0x02}},
			expectedSKI: "",
			errContains: "x509",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verify := hub.createWebSocketDialer(nil, tt.expectedSKI).TLSClientConfig.VerifyPeerCertificate
			require.NotNil(t, verify, "the dialer must install the handshake-time certificate check")

			err := verify(tt.rawCerts, nil)

			if tt.errContains == "" {
				assert.NoError(t, err)
				return
			}

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errContains)
			assert.True(t, errors.Is(err, errCertificateRejected),
				"a rejected certificate must be recognisable via errCertificateRejected")
		})
	}
}
