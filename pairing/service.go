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
	history api.PairingHistoryProviderInterface
	hub     api.PairingHubInterface

	// Local certificate (simplified design - no interface needed)
	localCert *x509.Certificate

	// State management
	running bool
	mux     sync.RWMutex
}

// NewService creates a new SHIP pairing service
// The certificate parameter is the hub's tls.Certificate containing the local certificate
func NewService(
	mdns api.MdnsPairingInterface,
	crypto api.PairingCryptoInterface,
	history api.PairingHistoryProviderInterface,
	hub api.PairingHubInterface,
	certificate tls.Certificate,
) (*Service, error) {
	// Extract X.509 certificate from TLS certificate
	if len(certificate.Certificate) == 0 {
		return nil, api.ErrInvalidCertificate
	}

	x509Cert, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate: %w", err)
	}

	return &Service{
		mdns:      mdns,
		crypto:    crypto,
		history:   history,
		hub:       hub,
		localCert: x509Cert,
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

// PairingStateFor returns pairing state for given service (ServiceDetails-centric)
func (s *Service) PairingStateFor(service *api.ServiceDetails) (*api.PairingStateDetail, error) {
	if service == nil {
		return nil, api.ErrServiceNil
	}

	s.mux.RLock()
	defer s.mux.RUnlock()

	if !s.running {
		return nil, api.ErrServiceNotStarted
	}

	return api.NewPairingStateDetail(api.PairingStateNone, nil), nil
}

// GetPairingStatus returns overall pairing service status (implements ShipPairingServiceInterface)
func (s *Service) GetPairingStatus() *api.PairingServiceStatus {
	s.mux.RLock()
	defer s.mux.RUnlock()

	status := &api.PairingServiceStatus{
		Running:         s.running,
		ListenerActive:  false,
		AnnouncerActive: false,
		LastError:       nil,
	}


	// Get listener status if available
	if s.listener != nil {
		listenerStatus := s.listener.GetPairingServiceStatus()
		if listenerStatus != nil {
			status.ListenerActive = listenerStatus.ListenerActive
			if listenerStatus.LastError != nil {
				status.LastError = listenerStatus.LastError
			}
		}
	}

	return status
}

// CreateAnnouncer creates a configured announcer component
func (s *Service) CreateAnnouncer(localService *api.ServiceDetails) api.PairingAnnouncerInterface {
	s.mux.RLock()
	defer s.mux.RUnlock()

	if !s.running {
		return nil
	}

	// Stateless factory: create and return new announcer (no tracking, no interference)
	return NewPairingAnnouncer(s.mdns, s.crypto, s.localCert, s.history, localService)
}

// CreateListener creates a configured listener component
func (s *Service) CreateListener(localService *api.ServiceDetails) api.PairingListenerInterface {
	s.mux.Lock()
	defer s.mux.Unlock()

	// Stop any existing listener before creating a new one
	if s.listener != nil {
		// Use the public interface which provides proper mutex protection
		_ = s.listener.StopListening()
	}

	// Create and track the new listener
	s.listener = NewPairingListener(s.mdns, s.crypto, s.history, s.hub, localService)

	// Note: Pairing notifications happen through hub.OnPairingSuccess
	// which calls the application's PairingServiceReaderInterface
	return s.listener
}
