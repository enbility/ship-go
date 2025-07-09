package hub

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConnectFoundServiceCertificateValidation tests certificate validation in connectFoundService
func TestConnectFoundServiceCertificateValidation(t *testing.T) {
	t.Run("empty_peer_certificates", func(t *testing.T) {
		hub := setupTestHubForTimer(t)

		// Create a test server that accepts WebSocket connections
		// but provides no certificates in TLS handshake
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			upgrader := websocket.Upgrader{
				CheckOrigin: func(r *http.Request) bool { return true },
			}
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Logf("Failed to upgrade: %v", err)
				return
			}
			defer conn.Close()

			// Keep connection open briefly
			time.Sleep(100 * time.Millisecond)
		}))
		defer server.Close()

		// Override the server's TLS config to not send certificates
		server.TLS.Certificates = nil

		// Extract host and port from test server URL
		serverURL := server.URL
		// Parse the URL to get host and port separately
		// URL format is https://127.0.0.1:port
		parts := strings.Split(serverURL[8:], ":")
		host := parts[0]
		port := parts[1]

		service := api.NewServiceDetails("test-ski")
		service.SetShipID("test-ship-id")

		// Attempt connection - should fail due to no peer certificates
		err := hub.connectFoundService(service, host, port, "")

		// The error might be from TLS handshake or from our check
		// Either way, connection should fail
		assert.Error(t, err)
	})

	t.Run("certificate_without_subject_key_id", func(t *testing.T) {
		hub := setupTestHubForTimer(t)

		// Create a certificate without SubjectKeyId
		template := &x509.Certificate{
			SerialNumber: big.NewInt(1),
			NotBefore:    time.Now(),
			NotAfter:     time.Now().Add(365 * 24 * time.Hour),
			// Intentionally omit SubjectKeyId
		}

		// Generate key and certificate
		priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)

		certDER, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
		require.NoError(t, err)

		tlsCert := tls.Certificate{
			Certificate: [][]byte{certDER},
			PrivateKey:  priv,
		}

		// Create test server with certificate that has no SKI
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			upgrader := websocket.Upgrader{
				CheckOrigin:  func(r *http.Request) bool { return true },
				Subprotocols: []string{api.ShipWebsocketSubProtocol},
			}
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Logf("Failed to upgrade: %v", err)
				return
			}
			defer conn.Close()

			// Keep connection open briefly
			time.Sleep(100 * time.Millisecond)
		}))

		server.TLS = &tls.Config{
			Certificates: []tls.Certificate{tlsCert},
		}
		server.StartTLS()
		defer server.Close()

		// Extract host and port from test server URL
		serverURL := server.URL
		// Parse the URL to get host and port separately
		parts := strings.Split(serverURL[8:], ":")
		host := parts[0]
		port := parts[1]

		service := api.NewServiceDetails("test-ski")
		service.SetShipID("test-ship-id")

		// Attempt connection - should fail due to missing SubjectKeyId
		err = hub.connectFoundService(service, host, port, "")

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no SKI in certificate")
	})

	t.Run("valid_certificate_wrong_ski", func(t *testing.T) {
		hub := setupTestHubForTimer(t)

		// Create a valid certificate with SKI
		validCert, err := cert.CreateCertificate("test", "org", "DE", "test-server")
		require.NoError(t, err)

		// Parse to get the SKI
		parsedCert, err := x509.ParseCertificate(validCert.Certificate[0])
		require.NoError(t, err)
		actualSKI := fmt.Sprintf("%0x", parsedCert.SubjectKeyId)

		// Create test server with valid certificate
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			upgrader := websocket.Upgrader{
				CheckOrigin:  func(r *http.Request) bool { return true },
				Subprotocols: []string{api.ShipWebsocketSubProtocol},
			}
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Logf("Failed to upgrade: %v", err)
				return
			}
			defer conn.Close()

			// Keep connection open briefly
			time.Sleep(100 * time.Millisecond)
		}))

		server.TLS = &tls.Config{
			Certificates: []tls.Certificate{validCert},
		}
		server.StartTLS()
		defer server.Close()

		// Extract host and port from test server URL
		serverURL := server.URL
		// Parse the URL to get host and port separately
		parts := strings.Split(serverURL[8:], ":")
		host := parts[0]
		port := parts[1]

		// Create service with different SKI than the certificate
		service := api.NewServiceDetails("different-ski-12345")
		service.SetShipID("test-ship-id")

		// Attempt connection - should fail due to SKI mismatch
		err = hub.connectFoundService(service, host, port, "")

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "certificate SKI mismatch")
		t.Logf("Certificate has SKI: %s, service expects: %s", actualSKI, service.SKI())
	})
}
