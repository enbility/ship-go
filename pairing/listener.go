package pairing

import (
	"context"
	"encoding/hex"
	"strings"
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
	history      PairingHistoryProviderInterface
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
	history PairingHistoryProviderInterface,
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
	if !secret.IsValidLength() {
		return api.ErrInvalidSecret
	}

	// Check if already listening
	if l.listening {
		return api.ErrListenerAlreadyActive
	}

	// Setup context and secret
	ctx, l.cancel = context.WithCancel(ctx)
	l.ctx = ctx
	l.secret = make(api.PairingSecret, len(secret))
	copy(l.secret, secret)
	l.startTime = time.Now()
	l.listening = true

	// Start mDNS search
	go func() {
		<-ctx.Done()
		l.mux.Lock()
		if l.listening && l.ctx == ctx {
			l.stopListeningInternal()
		}
		l.mux.Unlock()
	}()

	err := l.mdns.SearchPairingServices(l.handleMdnsDiscovery)
	if err != nil {
		l.stopListeningInternal()
		return err
	}

	// A browse callback fires only once per service instance; announcements
	// that arrived while no listener was active were dropped and will not be
	// re-delivered, so evaluate the currently-live records now. The order is
	// load-bearing: the callback above is registered first, then the snapshot
	// is read — every record is in at least one of the two deliveries, and a
	// duplicate delivery is benign (an accepted request stops the listener, a
	// rejected one re-fails identically). Runs on its own goroutine because
	// evaluation re-enters both l.mux and the hub, and callers may hold locks
	// across StartListening.
	go l.evaluateCurrentEntries()

	return nil
}

// evaluateCurrentEntries evaluates the pairing records currently live in the
// local mDNS browse cache through the regular discovery pipeline.
func (l *PairingListener) evaluateCurrentEntries() {
	entries, err := l.mdns.RequestPairingEntries()
	if err != nil {
		// The subscription for future announcements is already in place;
		// failing to read the snapshot must not tear the listener down.
		logging.Log().Error("pairing listener: could not read current pairing records", "error", err)
		return
	}

	if err := l.ProcessPendingEntries(entries); err != nil {
		logging.Log().Error("pairing listener: failed to evaluate current pairing records", "error", err)
	}
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
func (l *PairingListener) GetListenerStatus() api.ListenerStatus {
	l.mux.RLock()
	defer l.mux.RUnlock()

	return api.ListenerStatus{
		Active:       l.listening,
		StartTime:    l.startTime,
		RequestsSeen: l.requestsSeen,
		LastRequest:  l.lastRequest,
		Error:        l.lastError,
	}
}

// ProcessPendingEntries processes a batch of pairing entries (implements PairingListenerInterface)
//
// Deprecated: currently-live records are evaluated automatically when
// StartListening succeeds; external callers no longer need to replay them.
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

	// The request must target our certificate identity.
	if !strings.EqualFold(strings.TrimSpace(txtRecord.ForPar), strings.TrimSpace(l.localService.Fingerprint())) {
		logging.Log().Tracef("pairing listener: announcement not for our certificate in handlePairingRequest - wanted: %s, got: %s",
			l.localService.Fingerprint(), txtRecord.ForPar)
		l.notifyFailure(txtRecord.TrustId, txtRecord.TrustPar, api.ErrInvalidTXTRecord)
		return true
	}

	// Runtime capability check: this implementation only supports secp256r1.
	if txtRecord.TrustCurve != api.CurveSecp256r1 {
		logging.Log().Tracef("pairing listener: unsupported trust curve in handlePairingRequest - supported: %s, got: %s",
			api.CurveSecp256r1, txtRecord.TrustCurve)
		l.notifyFailure(txtRecord.TrustId, txtRecord.TrustPar, api.ErrUnsupportedTrustCurve)
		return true
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
	params := api.HMACParams{
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
