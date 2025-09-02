package hub

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/enbility/ship-go/mocks"
	"github.com/enbility/ship-go/model"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"
)

func TestHubConnectionsServerSuite(t *testing.T) {
	suite.Run(t, new(HubConnectionsServerSuite))
}

type HubConnectionsServerSuite struct {
	suite.Suite

	hubReader      *mocks.MockHubReaderInterface
	mdnsService    *mocks.MockMdnsInterface
	shipConnection *mocks.ShipConnectionInterface
	wsDataWriter   *mocks.WebsocketDataWriterInterface
	remoteSki      string
	sut            *Hub
}

func (s *HubConnectionsServerSuite) BeforeTest(suiteName, testName string) {
	s.remoteSki = "remotetestski"

	ctrl := gomock.NewController(s.T())

	s.hubReader = mocks.NewMockHubReaderInterface(ctrl)
	s.hubReader.EXPECT().RemoteServiceConnected(gomock.Any()).Return().AnyTimes()
	s.hubReader.EXPECT().RemoteServiceDisconnected(gomock.Any()).Return().AnyTimes()
	s.hubReader.EXPECT().ServiceUpdated(gomock.Any()).Return().AnyTimes()
	s.hubReader.EXPECT().ServicePairingDetailUpdate(gomock.Any(), gomock.Any()).Return().AnyTimes()
	s.hubReader.EXPECT().AllowWaitingForTrust(gomock.Any()).Return(false).AnyTimes()

	s.mdnsService = mocks.NewMockMdnsInterface(ctrl)
	s.mdnsService.EXPECT().AnnounceMdnsEntry().Return(nil).AnyTimes()
	s.mdnsService.EXPECT().UnannounceMdnsEntry().Return().AnyTimes()
	s.mdnsService.EXPECT().RequestMdnsEntries().Return().AnyTimes()

	s.wsDataWriter = mocks.NewWebsocketDataWriterInterface(s.T())

	s.shipConnection = mocks.NewShipConnectionInterface(s.T())
	s.shipConnection.EXPECT().CloseConnection(mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	s.shipConnection.EXPECT().RemoteSKI().Return(s.remoteSki).Maybe()
	s.shipConnection.EXPECT().ApprovePendingHandshake().Return().Maybe()
	s.shipConnection.EXPECT().AbortPendingHandshake().Return().Maybe()
	s.shipConnection.EXPECT().DataHandler().Return(s.wsDataWriter).Maybe()
	s.shipConnection.EXPECT().ShipHandshakeState().Return(model.SmeStateComplete, nil).Maybe()

	localService := api.NewServiceDetails("localSKI", "", "")
	certificate, _ := cert.CreateCertificate("unit", "org", "DE", "CN")
	var err error
	s.sut, err = newTestHub(s.hubReader, s.mdnsService, 4567, certificate, localService, nil)
	assert.NoError(s.T(), err)
}

func (s *HubConnectionsServerSuite) AfterTest(suiteName, testName string) {
	s.mdnsService.EXPECT().Shutdown().AnyTimes()
	s.sut.Shutdown()
}

func (s *HubConnectionsServerSuite) Test_SendWSCloseMessage() {
	req := httptest.NewRequest("GET", "http://example.com/foo", nil)
	w := httptest.NewRecorder()
	s.sut.ServeHTTP(w, req)

	server := httptest.NewServer(s.sut)
	wsURL := strings.ReplaceAll(server.URL, "http://", "ws://")

	// Connect to the server
	con, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	assert.Nil(s.T(), err)

	ski := "12af9e"
	localService := api.NewServiceDetails(ski, "", "")

	hub, err := newTestHub(s.hubReader, s.mdnsService, 4567, tls.Certificate{}, localService, nil)
	assert.NotNil(s.T(), hub)
	assert.NoError(s.T(), err)

	hub.sendWSCloseMessage(con)

	resp.Body.Close()
	_ = con.Close()
	server.CloseClientConnections()
	server.Close()

	time.Sleep(time.Second)
}

func (s *HubConnectionsServerSuite) Test_ServeHTTP_01() {
	req := httptest.NewRequest("GET", "http://example.com/foo", nil)
	w := httptest.NewRecorder()
	s.sut.ServeHTTP(w, req)

	server := httptest.NewServer(s.sut)
	wsURL := strings.ReplaceAll(server.URL, "http://", "ws://")

	// Connect to the server
	con, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	assert.Nil(s.T(), err)
	resp.Body.Close()
	_ = con.Close()

	dialer := &websocket.Dialer{
		Subprotocols: []string{api.ShipWebsocketSubProtocol},
	}
	con, resp, err = dialer.Dial(wsURL, nil)
	assert.Nil(s.T(), err)

	resp.Body.Close()
	_ = con.Close()
	server.CloseClientConnections()
	server.Close()

	time.Sleep(time.Second)
}

func (s *HubConnectionsServerSuite) Test_ServeHTTP_02() {
	server := httptest.NewUnstartedServer(s.sut)
	server.TLS = &tls.Config{
		Certificates:       []tls.Certificate{s.sut.certificate},
		ClientAuth:         tls.RequireAnyClientCert,
		CipherSuites:       cert.CipherSuites, // #nosec G402
		InsecureSkipVerify: true,              // #nosec G402
	}
	server.StartTLS()
	wsURL := strings.ReplaceAll(server.URL, "https://", "wss://")

	invalidCert, _ := createInvalidCertificate("unit", "org", "DE", "CN")
	dialer := &websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 5 * time.Second,
		TLSClientConfig: &tls.Config{
			Certificates:       []tls.Certificate{invalidCert},
			InsecureSkipVerify: true,              // #nosec G402
			CipherSuites:       cert.CipherSuites, // #nosec G402
		},
		Subprotocols: []string{api.ShipWebsocketSubProtocol},
	}
	con, resp, err := dialer.Dial(wsURL, nil)
	assert.Nil(s.T(), err)

	resp.Body.Close()
	_ = con.Close()

	validCert, _ := cert.CreateCertificate("unit", "org", "DE", "CN")
	dialer = &websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 5 * time.Second,
		TLSClientConfig: &tls.Config{
			Certificates:       []tls.Certificate{validCert},
			InsecureSkipVerify: true,              // #nosec G402
			CipherSuites:       cert.CipherSuites, // #nosec G402
		},
		Subprotocols: []string{api.ShipWebsocketSubProtocol},
	}
	con, resp, err = dialer.Dial(wsURL, nil)
	assert.Nil(s.T(), err)

	resp.Body.Close()
	_ = con.Close()
	server.CloseClientConnections()
	server.Close()

	time.Sleep(time.Second)
}

func (s *HubConnectionsServerSuite) Test_VerifyPeerCertificate() {
	testCert, _ := cert.CreateCertificate("unit", "org", "DE", "CN")
	var rawCerts [][]byte
	rawCerts = append(rawCerts, testCert.Certificate...)
	err := s.sut.verifyPeerCertificate(rawCerts, nil)
	assert.Nil(s.T(), err)

	rawCerts = nil
	rawCerts = append(rawCerts, []byte{100})
	err = s.sut.verifyPeerCertificate(rawCerts, nil)
	assert.NotNil(s.T(), err)

	rawCerts = nil
	invalidCert, _ := createInvalidCertificate("unit", "org", "DE", "CN")
	rawCerts = append(rawCerts, invalidCert.Certificate...)

	err = s.sut.verifyPeerCertificate(rawCerts, nil)
	assert.NotNil(s.T(), err)
}

func createInvalidCertificate(organizationalUnit, organization, country, commonName string) (tls.Certificate, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}

	// Create the EEBUS service SKI using the private key
	asn1, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	// SHIP 12.2: Required to be created according to RFC 3280 4.2.1.2
	// #nosec G401
	ski := sha1.Sum(asn1)

	subject := pkix.Name{
		OrganizationalUnit: []string{organizationalUnit},
		Organization:       []string{organization},
		Country:            []string{country},
		CommonName:         commonName,
	}

	// Create a random serial big int value
	maxValue := new(big.Int)
	maxValue.Exp(big.NewInt(2), big.NewInt(130), nil).Sub(maxValue, big.NewInt(1))
	serialNumber, err := rand.Int(rand.Reader, maxValue)
	if err != nil {
		return tls.Certificate{}, err
	}

	template := x509.Certificate{
		SignatureAlgorithm:    x509.ECDSAWithSHA256,
		SerialNumber:          serialNumber,
		Subject:               subject,
		NotBefore:             time.Now(),                                // Valid starting now
		NotAfter:              time.Now().Add(time.Hour * 24 * 365 * 10), // Valid for 10 years
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		SubjectKeyId:          ski[:19],
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return tls.Certificate{}, err
	}

	tlsCertificate := tls.Certificate{
		Certificate:                  [][]byte{certBytes},
		PrivateKey:                   privateKey,
		SupportedSignatureAlgorithms: []tls.SignatureScheme{tls.ECDSAWithP256AndSHA256},
	}

	return tlsCertificate, nil
}
