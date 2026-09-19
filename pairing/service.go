package pairing

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"sync"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
)

// Service implements ShipPairingServiceInterface as an orchestrator for pairing components
type Service struct {
	// Component registry (concrete types for simplicity)
	listener *PairingListener

	// Dependencies
	mdns    api.MdnsPairingInterface
	crypto  api.PairingCryptoInterface
	history PairingHistoryProviderInterface
	hub     api.PairingHubInterface

	// Local certificate (simplified design - no interface needed)
	localCert *x509.Certificate

	localService *api.ServiceDetails

	// State management
	running bool
	mux     sync.RWMutex
}

// NewService creates a new SHIP pairing service
// The certificate parameter is the hub's tls.Certificate containing the local certificate
func NewService(
	mdns api.MdnsPairingInterface,
	crypto api.PairingCryptoInterface,
	history PairingHistoryProviderInterface,
	hub api.PairingHubInterface,
	certificate tls.Certificate,
	shipID string,
) (*Service, error) {
	// Extract X.509 certificate from TLS certificate
	if len(certificate.Certificate) == 0 {
		return nil, api.ErrInvalidCertificate
	}

	x509Cert, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate: %w", err)
	}
	fingerprint, err := cert.FingerprintFromCertificate(x509Cert)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate fingerprint: %w", err)
	}

	svc, err := api.NewServiceDetails("", fingerprint, shipID)
	if err != nil {
		return nil, fmt.Errorf("failed to create service details: %w", err)
	}

	return &Service{
		mdns:         mdns,
		crypto:       crypto,
		history:      history,
		hub:          hub,
		localCert:    x509Cert,
		localService: svc,
	}, nil
}

// GetLocalFingerprint returns the SHA-256 fingerprint of the local certificate
// This is a convenience method that wraps the cert package function
func (s *Service) GetLocalFingerprint() (string, error) {
	if s.localCert == nil {
		return "", api.ErrInvalidCertificate
	}
	return cert.FingerprintFromCertificate(s.localCert)
}

// ValidateRemoteFingerprint validates a remote certificate against expected fingerprint
// This is a convenience method that wraps the cert package function
func (s *Service) ValidateRemoteFingerprint(remoteCert *x509.Certificate, expectedFingerprint string) error {
	return cert.ValidateFingerprint(remoteCert, expectedFingerprint)
}

// Start starts the pairing service (implements ShipPairingServiceInterface)
func (s *Service) Start() error {
	s.mux.Lock()
	defer s.mux.Unlock()

	if s.running {
		return api.ErrServiceAlreadyStarted
	}

	s.running = true
	return nil
}

// Shutdown shuts down the pairing service (implements ShipPairingServiceInterface)
func (s *Service) Shutdown() {
	s.mux.Lock()
	defer s.mux.Unlock()

	if !s.running {
		return
	}

	// Shutdown listener if it's actively listening
	if s.listener != nil {
		// Stop the listener gracefully using the public interface
		// which provides proper mutex protection
		_ = s.listener.StopListening()
	}

	s.running = false
}

// IsServiceRunning returns overall pairing service status (implements ShipPairingServiceInterface)
func (s *Service) IsServiceRunning() bool {
	s.mux.RLock()
	defer s.mux.RUnlock()

	return s.running
}

// Listener returns the active pairing listener instance, if any.
func (s *Service) Listener() api.PairingListenerInterface {
	s.mux.RLock()
	defer s.mux.RUnlock()

	return s.listener
}

// CreateAnnouncer creates a configured announcer component
func (s *Service) CreateAnnouncer() api.PairingAnnouncerInterface {
	s.mux.RLock()
	defer s.mux.RUnlock()

	if !s.running {
		return nil
	}

	// Stateless factory: create and return new announcer (no tracking, no interference)
	return NewPairingAnnouncer(s.mdns, s.crypto, s.localCert, s.history, s.localService.Copy())
}

// CreateListener creates a configured listener component
func (s *Service) CreateListener() api.PairingListenerInterface {
	s.mux.Lock()
	defer s.mux.Unlock()

	// Stop any existing listener before creating a new one
	if s.listener != nil {
		// Use the public interface which provides proper mutex protection
		_ = s.listener.StopListening()
	}

	// Create and track the new listener
	s.listener = NewPairingListener(s.mdns, s.crypto, s.history, s.hub, s.localService.Copy())

	// Note: Pairing notifications happen through hub.OnPairingSuccess
	// which calls the application's PairingServiceReaderInterface
	return s.listener
}
