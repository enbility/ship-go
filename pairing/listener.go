package pairing

import (
	"context"
	"encoding/hex"
	"sync"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/logging"
)

// PairingListener implements devA (listener) functionality for SHIP Pairing Service
type PairingListener struct {
	// Dependencies
	mdns         api.MdnsPairingInterface
	crypto       api.PairingCryptoInterface
	history      api.PairingHistoryProviderInterface
	hub          api.PairingHubInterface
	localService *api.ServiceDetails

	// State management
	listening    bool
	secret       api.PairingSecret
	startTime    time.Time
	requestsSeen int
	lastRequest  time.Time
	lastError    error

	// Concurrency control
	ctx    context.Context
	cancel context.CancelFunc

	// Thread safety
	mux sync.RWMutex
}

// NewPairingListener creates a new pairing listener
func NewPairingListener(
	mdns api.MdnsPairingInterface,
	crypto api.PairingCryptoInterface,
	history api.PairingHistoryProviderInterface,
	hub api.PairingHubInterface,
	localService *api.ServiceDetails,
) *PairingListener {
	return &PairingListener{
		mdns:         mdns,
		crypto:       crypto,
		history:      history,
		hub:          hub,
		localService: localService,
	}
}

// StartListening starts listening for pairing requests (implements PairingListenerInterface)
func (l *PairingListener) StartListening(ctx context.Context, secret api.PairingSecret) error {
	l.mux.Lock()
	defer l.mux.Unlock()

	// Validate secret
	if len(secret) < 16 {
		return api.ErrInvalidSecret
	}
	if len(secret) > 128 {
		return api.ErrInvalidSecret
	}

	// Check if already listening
	if l.listening {
		return api.ErrListenerAlreadyActive
	}

	// Setup context and secret
	l.ctx, l.cancel = context.WithCancel(ctx)
	l.secret = make(api.PairingSecret, len(secret))
	copy(l.secret, secret)
	l.startTime = time.Now()
	l.listening = true

	// Start mDNS search
	go func() {
		<-ctx.Done()
		l.mux.Lock()
		if l.listening {
			l.stopListeningInternal()
		}
		l.mux.Unlock()
	}()

	err := l.mdns.SearchPairingServices(l.handleMdnsDiscovery)
	if err != nil {
		l.stopListeningInternal()
		return err
	}

	return nil
}

// StopListening stops listening for pairing requests (implements PairingListenerInterface)
func (l *PairingListener) StopListening() error {
	l.mux.Lock()
	defer l.mux.Unlock()

	if !l.listening {
		return api.ErrPairingNotActive
	}

	l.stopListeningInternal()
	return nil
}

// GetListenerStatus returns current listener status (implements PairingListenerInterface)
func (l *PairingListener) GetListenerStatus() *api.ListenerStatus {
	l.mux.RLock()
	defer l.mux.RUnlock()

	return &api.ListenerStatus{
		Active:       l.listening,
		StartTime:    l.startTime,
		RequestsSeen: l.requestsSeen,
		LastRequest:  l.lastRequest,
		Error:        l.lastError,
	}
}

// ProcessPendingEntries processes a batch of pairing entries (implements PairingListenerInterface)
func (l *PairingListener) ProcessPendingEntries(entries map[string]*api.ShipPairingTXT) error {
	if len(entries) == 0 {
		return nil
	}

	logging.Log().Debug("Processing pending pairing entries", "count", len(entries))

	for _, txtRecord := range entries {
		// Reuse existing validation logic - this will handle all the validation,
		// trust decisions, and listener state management
		shouldContinue := l.handleMdnsDiscovery(txtRecord)
		if !shouldContinue {
			// Listener stopped (successful pairing occurred) - stop processing remaining entries
			logging.Log().Debug("Stopping pending entry processing due to successful pairing")
			break
		}
	}

	return nil
}

// GetPairingServiceStatus returns current pairing service status
func (l *PairingListener) GetPairingServiceStatus() *PairingServiceStatus {
	l.mux.RLock()
	defer l.mux.RUnlock()

	return &PairingServiceStatus{
		Enabled:         true, // Always true when listener exists
		Mode:            PairingModeListener,
		ListenerActive:  l.listening,
		AnnouncerActive: false,
		LastError:       l.lastError,
	}
}

// handlePairingRequest processes incoming pairing requests (internal method for testing)
func (l *PairingListener) handlePairingRequest(txtRecord *api.ShipPairingTXT) bool {
	l.mux.Lock()
	l.requestsSeen++
	l.lastRequest = time.Now()
	l.mux.Unlock()

	// Validate TXT record structure
	if err := txtRecord.Validate(); err != nil {
		logging.Log().Tracef("pairing listener: TXT record validation failed: %v", err)
		l.notifyFailure(txtRecord.TrustId, txtRecord.TrustPar, err)
		return true // Continue listening
	}

	// Check if announcement is for us
	if txtRecord.ForId != l.localService.ShipID() {
		logging.Log().Tracef("pairing listener: announcement not for us in handlePairingRequest - wanted: %s, got: %s",
			l.localService.ShipID(), txtRecord.ForId)
		return true // Continue listening - not for us
	}

	// Parse nonce and digest from hex
	nonce, err := hex.DecodeString(txtRecord.TrustNonce)
	if err != nil {
		logging.Log().Tracef("pairing listener: failed to parse nonce: %v", err)
		l.notifyFailure(txtRecord.TrustId, txtRecord.TrustPar, api.ErrInvalidTXTRecord)
		return true
	}

	expectedDigest, err := hex.DecodeString(txtRecord.Digest)
	if err != nil {
		logging.Log().Tracef("pairing listener: failed to parse digest: %v", err)
		l.notifyFailure(txtRecord.TrustId, txtRecord.TrustPar, api.ErrInvalidHMACDigest)
		return true
	}

	// Validate HMAC digest
	params := &api.HMACParams{
		Algorithm: txtRecord.Alg,
		Nonce:     nonce,
		TxtRecord: txtRecord,
	}

	l.mux.RLock()
	secret := make(api.PairingSecret, len(l.secret))
	copy(secret, l.secret)
	l.mux.RUnlock()

	if err := l.crypto.ValidateDigest(secret, params, expectedDigest); err != nil {
		logging.Log().Tracef("pairing listener: HMAC validation failed: %v", err)
		l.notifyFailure(txtRecord.TrustId, txtRecord.TrustPar, err)
		return true // Continue listening
	}

	// Check replay protection
	if l.history.HasSeenDigest(txtRecord.Alg, txtRecord.Digest) {
		logging.Log().Trace("pairing listener: replay attack detected - digest already seen")
		l.notifyFailure(txtRecord.TrustId, txtRecord.TrustPar, api.ErrReplayAttackDetected)
		return true // Continue listening
	}

	// SHIP Pairing Service autonomous operation per specification section 4.2:
	// "devA SHALL trust" after successful evaluation - no user interaction required

	// Record successful pairing in ring buffer per SHIP spec section 11
	l.history.RecordPairing(txtRecord.Alg, txtRecord.Digest)

	// Establish trust automatically per SHIP spec (no conditions)
	// The hub will store this as pending pairing trust until SKI is resolved via mDNS
	l.hub.OnPairingSuccess(txtRecord.TrustId, txtRecord.TrustPar)

	// Stop listening after acceptance per SHIP spec section 4.3
	l.mux.Lock()
	l.stopListeningInternal()
	l.mux.Unlock()

	return false // Stop searching
}

// handleMdnsDiscovery processes mDNS discoveries (internal method for testing)
func (l *PairingListener) handleMdnsDiscovery(txtRecord *api.ShipPairingTXT) bool {
	l.mux.RLock()
	listening := l.listening
	l.mux.RUnlock()

	if !listening {
		return false // Stop processing if listener stopped
	}

	// Filter to only process announcements for our device
	if txtRecord.ForId != l.localService.ShipID() {
		return true // Continue searching
	}

	return l.handlePairingRequest(txtRecord)
}

// Note: handleAutoTrust and handleUserAccept removed - SHIP Pairing Service is purely autonomous
// Valid HMAC authentication IS the authorization per specification section 4.2

// notifyFailure notifies about pairing failures
func (l *PairingListener) notifyFailure(shipid, fingerprint string, err error) {
	l.mux.Lock()
	l.lastError = err
	l.mux.Unlock()

	// Use hub's OnPairingFailure which will notify the application
	l.hub.OnPairingFailure(shipid, fingerprint, err)
}

// stopListeningInternal handles internal cleanup (must be called with mutex locked)
func (l *PairingListener) stopListeningInternal() {
	if l.cancel != nil {
		l.cancel()
		l.cancel = nil
	}

	if l.secret != nil {
		l.secret.Clear()
		l.secret = nil
	}

	l.listening = false
}
