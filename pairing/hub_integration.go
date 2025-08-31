package pairing

import (
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
)

// PairingMode represents the mode of pairing service operation
type PairingMode uint

const (
	PairingModeListener  PairingMode = iota // devA mode - listen for pairing requests
	PairingModeAnnouncer                    // devZ mode - announce pairing requests
	PairingModeBoth                         // Support both modes
)

// PairingConfiguration contains configuration for pairing service
type PairingConfiguration struct {
	Mode    PairingMode
	Secret  api.PairingSecret
	Timeout time.Duration
	Enabled bool
}

// PairingServiceStatus represents the overall status of pairing service
type PairingServiceStatus struct {
	Enabled         bool
	Mode            PairingMode
	ListenerActive  bool
	AnnouncerActive bool
	LastError       error
}

// PairingAnnouncer implements the announcer (devZ) functionality for SHIP Pairing Service
type PairingAnnouncer struct {
	// Dependencies
	mdns         api.MdnsPairingInterface
	crypto       api.PairingCryptoInterface
	history      api.PairingHistoryProviderInterface
	localService *api.ServiceDetails

	// Local certificate (simplified design - no interface needed)
	localCert *x509.Certificate

	// Configuration
	config     *PairingConfiguration
	autoaccept bool
	enabled    bool

	// Current announcement state (protected by mutex)
	currentTarget     *api.PairingTarget
	announcing        bool
	currentInstanceID string // mDNS instance ID for current announcement
	mux               sync.RWMutex

	// Note: Service management removed - PairingAnnouncer focuses on devZ role only
}

// NewPairingAnnouncer creates a new pairing announcer
func NewPairingAnnouncer(
	mdns api.MdnsPairingInterface,
	crypto api.PairingCryptoInterface,
	localCert *x509.Certificate,
	history api.PairingHistoryProviderInterface,
	localService *api.ServiceDetails,
) *PairingAnnouncer {
	return &PairingAnnouncer{
		mdns:         mdns,
		crypto:       crypto,
		localCert:    localCert,
		history:      history,
		localService: localService,
		// hub will be set via dependency injection when integrated with real Hub
	}
}

// EnablePairingService enables the pairing service with given configuration
func (p *PairingAnnouncer) EnablePairingService(config *PairingConfiguration) error {
	if config == nil {
		return api.ErrPairingNoConfig
	}

	// Validate pairing mode
	if config.Mode != PairingModeAnnouncer && config.Mode != PairingModeListener && config.Mode != PairingModeBoth {
		return api.NewPairingValidationError("invalid pairing mode")
	}

	// Validate secret
	if len(config.Secret) < 16 || len(config.Secret) > 128 {
		return api.ErrInvalidSecret
	}

	p.mux.Lock()
	defer p.mux.Unlock()

	p.config = config
	p.enabled = config.Enabled

	return nil
}

// AnnounceToDevice announces pairing request to target device (devZ mode)
func (p *PairingAnnouncer) AnnounceToDevice(target *api.PairingTarget) error {
	p.mux.Lock()
	defer p.mux.Unlock()

	if !p.enabled || p.config == nil {
		return api.ErrServiceNotStarted
	}

	if p.announcing {
		return api.ErrAnnouncerAlreadyActive
	}

	// Get local certificate fingerprint
	localFingerprint, err := cert.FingerprintFromCertificate(p.localCert)
	if err != nil {
		return err
	}

	// Generate nonce
	nonce, err := p.crypto.GenerateNonce()
	if err != nil {
		return err
	}

	// Build TXT record
	txtRecord := &api.ShipPairingTXT{
		TxtVers:    "1",
		ParType:    api.ParTypeFPSHA256,
		ForId:      target.ShipID,
		ForPar:     target.Fingerprint,
		TrustId:    p.localService.ShipID(),
		TrustPar:   localFingerprint,
		TrustCurve: api.CurveSecp256r1,
		Type:       api.CommandTypeAddCU,
		TrustNonce: bytesToHex(nonce),
		Alg:        api.AlgorithmHMACSHA256,
	}

	// Calculate HMAC digest
	params := &api.HMACParams{
		Algorithm: api.AlgorithmHMACSHA256,
		Nonce:     nonce,
		TxtRecord: txtRecord,
	}

	digest, err := p.crypto.CalculateDigest(p.config.Secret, params)
	if err != nil {
		return err
	}

	txtRecord.Digest = bytesToHex(digest)

	// Announce via mDNS
	instanceID, err := p.mdns.AnnouncePairingService(txtRecord)
	if err != nil {
		// Preserve the original error for better debugging
		return fmt.Errorf("mDNS announcement failed: %w", err)
	}

	// Update state
	p.currentTarget = target
	p.currentInstanceID = instanceID
	p.announcing = true

	return nil
}

// Announce announces pairing request to target device (implements PairingAnnouncerInterface)
func (p *PairingAnnouncer) Announce(target *api.PairingTarget) error {
	return p.AnnounceToDevice(target)
}

// StopAnnouncement stops the current announcement (implements PairingAnnouncerInterface)
func (p *PairingAnnouncer) StopAnnouncement() error {
	p.mux.Lock()
	defer p.mux.Unlock()

	if !p.announcing {
		return api.ErrPairingNotActive
	}

	// Clean up mDNS announcement
	if p.mdns != nil && p.currentInstanceID != "" {
		if err := p.mdns.UnannouncePairingService(p.currentInstanceID); err != nil {
			return err
		}
	}

	// Reset state
	p.announcing = false
	p.currentTarget = nil
	p.currentInstanceID = ""

	return nil
}

// GetAnnouncementStatus returns current announcement status (implements PairingAnnouncerInterface)
func (p *PairingAnnouncer) GetAnnouncementStatus() *api.AnnouncementStatus {
	p.mux.RLock()
	defer p.mux.RUnlock()

	status := &api.AnnouncementStatus{
		Active: p.announcing,
		Target: p.currentTarget,
	}

	return status
}

// GetPairingServiceStatus returns current pairing service status
func (p *PairingAnnouncer) GetPairingServiceStatus() *PairingServiceStatus {
	p.mux.RLock()
	defer p.mux.RUnlock()

	if !p.enabled {
		return nil
	}

	return &PairingServiceStatus{
		Enabled:         p.enabled,
		Mode:            PairingModeAnnouncer,
		AnnouncerActive: p.announcing,
		ListenerActive:  false,
	}
}

// SetAutoAccept sets the auto-accept mode
func (p *PairingAnnouncer) SetAutoAccept(autoaccept bool) {
	p.autoaccept = autoaccept
}

/* Hub Integration - Connection Management Only */

// Note: ShouldAutoTrust removed from PairingHubInterface
// SHIP Pairing Service operates autonomously per specification section 4.2

// OnConnectionEstablished handles SHIP connection establishment
func (p *PairingAnnouncer) OnConnectionEstablished(ski string) {
	// Cleanup pairing announcement per SHIP spec section 4.2
	p.mux.Lock()
	defer p.mux.Unlock()

	if p.announcing && p.mdns != nil && p.currentInstanceID != "" {
		_ = p.mdns.UnannouncePairingService(p.currentInstanceID)
		p.announcing = false
		p.currentTarget = nil
		p.currentInstanceID = ""
	}
}

// Helper function for hex conversion (shared with tests)
func bytesToHex(bytes []byte) string {
	return strings.ToUpper(hex.EncodeToString(bytes))
}
