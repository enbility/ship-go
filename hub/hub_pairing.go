package hub

import (
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/enbility/ship-go/logging"
	"github.com/enbility/ship-go/model"
	"github.com/enbility/ship-go/pairing"
	"github.com/enbility/ship-go/util"
)

// AutoTrustEstablishmentRequest contains the required parameters for establishing auto trust via pairing
type AutoTrustEstablishmentRequest struct {
	SKI         string
	ShipID      string
	Fingerprint string
	Context     string // for logging
}

// AutoTrustEstablishmentResult contains the result of establishing auto trust via pairing
type AutoTrustEstablishmentResult struct {
	Success        bool
	ServiceCreated bool // true if service was created, false if updated existing
	Error          error
}

// Provide the current pairing state for a ServiceIdentity
func (h *Hub) PairingDetailFor(identity api.ServiceIdentity) *api.ConnectionStateDetail {
	// Convert ServiceIdentity to ServiceDetails for service-based lookup
	serviceForLookup, err := identity.ToServiceDetails()
	if err != nil {
		return nil
	}
	if conn := h.connectionForService(serviceForLookup); conn != nil {
		shipState, shipError := conn.ShipHandshakeState()
		state := h.mapShipMessageExchangeState(shipState, identity.SKI)
		return api.NewConnectionStateDetail(state, shipError)
	}

	service := h.serviceFor(identity)
	if service == nil {
		return nil
	}
	return service.ConnectionStateDetail()
}

// maps ShipMessageExchangeState to PairingState
func (h *Hub) mapShipMessageExchangeState(state model.ShipMessageExchangeState, _ string) api.ConnectionState {
	var connState api.ConnectionState

	// map the SHIP states to a public ConnectionState
	switch state {
	case model.CmiStateInitStart:
		connState = api.ConnectionStateQueued
	case model.CmiStateClientSend, model.CmiStateClientWait, model.CmiStateClientEvaluate,
		model.CmiStateServerWait, model.CmiStateServerEvaluate:
		connState = api.ConnectionStateInitiated
	case model.SmeHelloStateReadyInit, model.SmeHelloStateReadyListen, model.SmeHelloStateReadyTimeout,
		model.SmeHelloStatePendingInit, model.SmeHelloStatePendingTimeout:
		connState = api.ConnectionStateInProgress
	case model.SmeHelloStatePendingListen:
		connState = api.ConnectionStateReceivedPairingRequest
	case model.SmeHelloStateOk:
		connState = api.ConnectionStateTrusted
	case model.SmeHelloStateAbort, model.SmeHelloStateAbortDone:
		connState = api.ConnectionStateNone
	case model.SmeHelloStateRemoteAbortDone, model.SmeHelloStateRejected:
		connState = api.ConnectionStateRemoteDeniedTrust
	case model.SmePinStateCheckInit, model.SmePinStateCheckListen, model.SmePinStateCheckError,
		model.SmePinStateCheckBusyInit, model.SmePinStateCheckBusyWait, model.SmePinStateCheckOk,
		model.SmePinStateAskInit, model.SmePinStateAskProcess, model.SmePinStateAskRestricted,
		model.SmePinStateAskOk:
		connState = api.ConnectionStatePin
	case model.SmeAccessMethodsRequest, model.SmeStateApproved:
		connState = api.ConnectionStateInProgress
	case model.SmeStateComplete:
		connState = api.ConnectionStateCompleted
	case model.SmeStateError:
		connState = api.ConnectionStateError
	default:
		connState = api.ConnectionStateInProgress
	}

	return connState
}

func (h *Hub) SetAutoAccept(autoaccept bool) {
	h.muxReg.Lock()
	h.autoaccept = autoaccept
	h.muxReg.Unlock()

	h.mdns.SetAutoAccept(autoaccept)
}

// check if auto accept is true
func (h *Hub) IsAutoAcceptEnabled() bool {
	h.muxReg.RLock()
	defer h.muxReg.RUnlock()

	return h.autoaccept
}

func (h *Hub) checkHasStarted() bool {
	h.muxStarted.RLock()
	defer h.muxStarted.RUnlock()
	return h.hasStarted
}

// Pair a remote service using ServiceIdentity
func (h *Hub) RegisterRemoteService(identity api.ServiceIdentity) {
	if identity.IsZero() {
		return
	}

	candidate, err := api.NewServiceDetails(identity.SKI, identity.Fingerprint, identity.ShipID)
	if err != nil {
		logging.Log().Error("RegisterRemoteService: invalid identity", "error", err)
		return
	}
	candidate.SetPairingType(identity.PairingType)
	candidate.SetIPv4(identity.IPv4)
	// Mark trust on the candidate so the merge propagates it onto the
	// canonical entry — including the case where a previously-untrusted
	// entry created from an incoming connection already exists.
	candidate.SetTrusted(true)

	service, err := h.mergeOrAddService(candidate)
	if err != nil {
		logging.Log().Error("RegisterRemoteService: identifier conflict with existing entry", "error", err)
		return
	}

	// if the hub has not started, simply add it
	if !h.checkHasStarted() {
		h.checkAutoReannounce()
		return
	}

	// Registering expresses fresh connection intent, so drop an accumulated
	// retry backoff: the delay ranges grow to 10-20s and would otherwise defer
	// the attempt far beyond what a caller waiting for the connection expects.
	h.cancelConnectionDelayTimer(identity.SKI)
	h.removeConnectionAttemptCounter(identity.SKI)

	// Pairing spec §4.3: registering trust in an addCu device expresses the
	// pairing intent, so processing of addCu-requests must deactivate now —
	// not only once the connection completes (HandleShipHandshakeStateUpdate).
	// Otherwise the listener stays armed (indefinitely if the device is
	// offline) and a third devZ announcing a valid request would immediately
	// replace the trust registered here, since no replacement window is armed
	// on this path.
	isTrustedAddCu := service.Trusted() && service.PairingType() == api.PairingTypeAddCu && service.ShipID() != ""
	if isTrustedAddCu {
		h.stopPairingListener()
	}

	// if the hub has started, trigger a search and connection attempt
	conn := h.connectionForService(service)
	// remotely initiated?
	if conn != nil {
		conn.ApprovePendingHandshake()
		return
	}

	// The addCu device has no live connection, so it is offline. Arm the
	// §4.3 1.a replacement timer — the only sanctioned automatic reactivation —
	// so that after 15 minutes of no SHIP Message Exchange the listener
	// reopens. This mirrors startAddCuReplacementTimersForOfflineDevices on the
	// startup path: without it, stopping the listener above would leave an
	// offline devZ deactivated indefinitely, recoverable only by manual removal
	// (UnregisterRemoteService). A completed connection cancels the timer
	// (StopAddCuReplacementTimer in HandleShipHandshakeStateUpdate).
	if isTrustedAddCu {
		h.addCuReplacementTracker.StartTimer(service.ShipID(), h.handleAddCuReplacementTimeout)
	}

	h.mdns.RequestMdnsEntries()
}

// Remove pairing using ServiceIdentity
func (h *Hub) UnregisterRemoteService(identity api.ServiceIdentity) {
	if service := h.serviceFor(identity); service != nil {
		// The stored entry carries the authoritative pairing type — the
		// caller-supplied identity may have been built from the SKI alone.
		isAddCu := service.PairingType() == api.PairingTypeAddCu
		shipID := service.ShipID()
		service.SetTrusted(false)
		service.ConnectionStateDetail().SetState(api.ConnectionStateNone)
		h.hubReader.ServicePairingDetailUpdate(identity, service.ConnectionStateDetail())
		h.removeServiceEntry(service)
		if isAddCu {
			// Pairing spec §4.3 item 2.b / §10.4 *3: user removal of the
			// trusted addCu device reactivates processing of addCu-requests
			// immediately — the 15-minute wait applies only to the automatic
			// reactivation path, so any pending replacement timer is obsolete.
			h.addCuReplacementTracker.StopTimer(shipID)
			h.reactivatePairingListener("Control Unit removed")
		}
	}

	h.cancelConnectionDelayTimer(identity.SKI)
	h.removeConnectionAttemptCounter(identity.SKI)

	// Convert ServiceIdentity to ServiceDetails for service-based lookup
	serviceForLookup, _ := identity.ToServiceDetails()
	if existingC := h.connectionForService(serviceForLookup); existingC != nil {
		existingC.CloseConnection(true, 4500, "User close")
	}
}

// Disconnect a connection using ServiceIdentity, used by a service implementation
// e.g. if heartbeats go wrong
func (h *Hub) DisconnectService(identity api.ServiceIdentity, reason string) {
	// Convert ServiceIdentity to ServiceDetails for service-based lookup
	serviceForLookup, _ := identity.ToServiceDetails()
	con := h.connectionForService(serviceForLookup)
	if con == nil {
		return
	}

	con.CloseConnection(true, 0, reason)
}

// Cancels the pairing process using ServiceIdentity
func (h *Hub) CancelPairing(identity api.ServiceIdentity) {
	h.removeConnectionAttemptCounter(identity.SKI)

	// Convert ServiceIdentity to ServiceDetails for service-based lookup
	serviceForLookup, _ := identity.ToServiceDetails()
	if existingC := h.connectionForService(serviceForLookup); existingC != nil {
		existingC.AbortPendingHandshake()
	}

	if service := h.serviceFor(identity); service != nil {
		service.ConnectionStateDetail().SetState(api.ConnectionStateNone)
		service.SetTrusted(false)

		h.hubReader.ServicePairingDetailUpdate(identity, service.ConnectionStateDetail())
	}
}

/* SHIP Pairing Service Integration - Composition Pattern */

// PairingService returns the pairing service if available (implements HubInterface)
func (h *Hub) PairingService() api.ShipPairingServiceInterface {
	h.muxPairing.RLock()
	defer h.muxPairing.RUnlock()

	return h.pairingService
}

// SetPairingService configures the optional pairing service (called during Hub construction)
func (h *Hub) SetPairingService(service api.ShipPairingServiceInterface) error {
	h.muxPairing.Lock()
	defer h.muxPairing.Unlock()

	if h.hasStarted {
		return api.ErrServiceAlreadyStarted
	}

	h.pairingService = service
	return nil
}

/* Pairing package integration */

// initializePairingServiceWithConfig creates pairing service with provided configuration
func (h *Hub) initializePairingServiceWithConfig(config *api.PairingConfig) error {
	if config.Mode == api.PairingModeOff {
		return nil // No pairing service needed
	}

	// Avoid double initialization
	h.muxPairing.RLock()
	if h.pairingService != nil {
		h.muxPairing.RUnlock()
		return nil
	}
	h.muxPairing.RUnlock()

	h.pairingConfig = config

	// Create crypto provider
	cryptoProvider := pairing.NewHMACCalculator()

	// Check if mDNS supports pairing (type assertion)
	mdnsPairing, ok := h.mdns.(api.MdnsPairingInterface)
	if !ok {
		return fmt.Errorf("mDNS interface does not support pairing operations")
	}

	// Create ring buffer history provider from persistence interface
	// Use default ring buffer size of 10 entries (SHIP spec minimum is 10)
	var historyProvider *pairing.RingBufferHistoryProvider
	if h.ringBufferPersistence != nil {
		ringBufferProvider, err := pairing.NewRingBufferHistoryProvider(10, h.ringBufferPersistence)
		if err != nil {
			return fmt.Errorf("failed to create ring buffer history provider: %w", err)
		}
		historyProvider = ringBufferProvider
	}

	// Create pairing service with all dependencies
	service, err := pairing.NewService(
		mdnsPairing,     // mDNS pairing interface
		cryptoProvider,  // Crypto for HMAC
		historyProvider, // Ring buffer history provider
		h,               // Hub as PairingHubInterface
		h.certificate,   // Certificate
		h.localService.ShipID(),
	)
	if err != nil {
		return fmt.Errorf("failed to create pairing service: %w", err)
	}

	h.pairingService = service

	return nil
}

// enablePairingListener starts ship pairing listener
func (h *Hub) enablePairingListener(config *api.PairingConfig) error {
	if h.pairingService == nil {
		return fmt.Errorf("pairing service not available")
	}

	// Note: Secret validation already done in PairingConfig.Validate() during Hub creation
	// But we need to validate secret exists for autonomous operation
	if len(config.Secret) == 0 {
		return fmt.Errorf("pairing secret required for autonomous listener")
	}
	if !config.Secret.IsValidLength() {
		return api.ErrInvalidSecret
	}

	// Thread-safe check and create listener (double-checked locking pattern)
	h.muxPairingListener.Lock()
	defer h.muxPairingListener.Unlock()

	var listener api.PairingListenerInterface
	if h.activePairingListener != nil {
		// Reuse existing listener
		listener = h.activePairingListener
	} else {
		// Create new listener through pairing service
		listener = h.pairingService.CreateListener()
		if listener == nil {
			return fmt.Errorf("failed to create pairing listener")
		}

		// Store the listener for future reuse
		h.activePairingListener = listener
	}

	// Start listening automatically with configured secret
	ctx := h.pairingCtx // Use Hub's context for proper lifecycle management

	if err := listener.StartListening(ctx, config.Secret); err != nil {
		if errors.Is(err, api.ErrListenerAlreadyActive) {
			return nil
		}
		return fmt.Errorf("failed to start autonomous listener: %w", err)
	}

	return nil
}

// stopPairingListener stops the active pairing listener if one is running.
// This should be called when an AddCu device reconnects.
func (h *Hub) stopPairingListener() {
	h.muxPairingListener.Lock()
	defer h.muxPairingListener.Unlock()

	if h.activePairingListener == nil {
		return
	}

	if err := h.activePairingListener.StopListening(); err != nil {
		logging.Log().Trace("pairing listener already stopped or not active", "error", err)
	}
}

// StartAnnouncementTo starts announcing pairing to a specific target device
func (h *Hub) StartAnnouncementTo(target api.PairingTarget) error {
	if target.ShipID == "" {
		return fmt.Errorf("target SHIP ID cannot be empty")
	}

	if len(target.Secret) == 0 {
		return fmt.Errorf("target secret cannot be empty")
	}

	if !api.PairingSecret(target.Secret).IsValidLength() {
		return api.ErrInvalidSecret
	}

	// devZ must already trust devA before announcing SHIP pairing.
	trustedTarget := h.serviceForTrustedShipID(target.ShipID)
	if trustedTarget == nil {
		return fmt.Errorf("%w: target SHIP ID %s", api.ErrNotPaired, target.ShipID)
	}

	if target.SKI != "" && trustedTarget.SKI() != "" && util.NormalizeSKI(target.SKI) != trustedTarget.SKI() {
		return fmt.Errorf("target identifier mismatch for SHIP ID %s: SKI does not match trusted device", target.ShipID)
	}

	if target.Fingerprint != "" && trustedTarget.Fingerprint() != "" && target.Fingerprint != trustedTarget.Fingerprint() {
		return fmt.Errorf("target identifier mismatch for SHIP ID %s: fingerprint does not match trusted device", target.ShipID)
	}

	// check if we are already connected to the target
	service, err := api.NewServiceDetails(target.SKI, target.Fingerprint, target.ShipID)
	if err != nil {
		return fmt.Errorf("invalid pairing target: %w", err)
	}
	conn := h.connectionForService(service)
	if conn != nil {
		if connState, err := conn.ShipHandshakeState(); err == nil && connState == model.SmeStateComplete {
			return nil
		}
	}

	h.muxPairing.RLock()
	pairingService := h.pairingService
	h.muxPairing.RUnlock()

	if pairingService == nil {
		return fmt.Errorf("pairing service not available")
	}

	h.muxAnnouncements.Lock()
	defer h.muxAnnouncements.Unlock()

	// Check if already announcing to this target
	if _, exists := h.activeAnnouncements[target.ShipID]; exists {
		return fmt.Errorf("already announcing to device: %s", target.ShipID)
	}

	// Create announcer for this target
	announcer := pairingService.CreateAnnouncer()
	if announcer == nil {
		return fmt.Errorf("failed to create pairing announcer")
	}

	// Create announcement state
	state := announcementState{
		target:    target,
		announcer: announcer,
		startTime: time.Now(),
	}

	// Enable the announcer with the target's secret
	concreteAnnouncer, ok := announcer.(*pairing.PairingAnnouncer)
	if !ok {
		return fmt.Errorf("failed to cast announcer to concrete type")
	}

	// Configure announcer with target's secret
	pairingConfig := &pairing.PairingConfiguration{
		Mode:    pairing.PairingModeAnnouncer,
		Secret:  target.Secret,
		Enabled: true,
	}

	if err := concreteAnnouncer.EnablePairingService(pairingConfig); err != nil {
		return fmt.Errorf("failed to enable announcer: %w", err)
	}

	// Start the announcement - this starts the mDNS announcement and returns immediately
	// The announcement will remain active until explicitly stopped
	if err := announcer.Announce(target); err != nil {
		return fmt.Errorf("failed to start announcement: %w", err)
	}

	// Store the announcement state
	h.activeAnnouncements[target.ShipID] = state

	logging.Log().Debug("started announcement to", target.ShipID)
	return nil
}

// StopAnnouncementTo stops announcing pairing to a specific target device
func (h *Hub) StopAnnouncementTo(shipID string) error {
	if shipID == "" {
		return fmt.Errorf("SHIP ID cannot be empty")
	}

	h.muxAnnouncements.Lock()
	defer h.muxAnnouncements.Unlock()

	state, exists := h.activeAnnouncements[shipID]
	if !exists {
		return fmt.Errorf("no active announcement for device: %s", shipID)
	}

	// Stop the announcer
	if state.announcer != nil {
		if err := state.announcer.StopAnnouncement(); err != nil {
			logging.Log().Error("error stopping announcement to", shipID, ":", err)
		}
	}

	// Remove from active announcements
	delete(h.activeAnnouncements, shipID)

	logging.Log().Debug("stopped announcement to", shipID)
	return nil
}

// GetActiveAnnouncements returns the list of SHIP IDs currently being announced to
func (h *Hub) GetActiveAnnouncements() []string {
	h.muxAnnouncements.RLock()
	defer h.muxAnnouncements.RUnlock()

	shipIDs := make([]string, 0, len(h.activeAnnouncements))
	for shipID := range h.activeAnnouncements {
		shipIDs = append(shipIDs, shipID)
	}

	return shipIDs
}

// IsAnnouncingTo checks if currently announcing to a specific target device
func (h *Hub) IsAnnouncingTo(shipID string) bool {
	h.muxAnnouncements.RLock()
	defer h.muxAnnouncements.RUnlock()

	_, exists := h.activeAnnouncements[shipID]
	return exists
}

/* PairingHubInterface Implementation */

var _ api.PairingHubInterface = (*Hub)(nil)

// OnPairingSuccess handles successful pairing validation (implements PairingHubInterface)
func (h *Hub) OnPairingSuccess(remoteShipID, remoteFingerprint string) {
	// Validate inputs - reject empty fingerprints for security
	if strings.TrimSpace(remoteFingerprint) == "" {
		logging.Log().Error("Pairing rejected: empty fingerprint provided for ShipID:", remoteShipID)
		return
	}

	// Pairing spec §4.3.1.b.i and §10.3: a successful addCu-request authorises
	// devA to untrust the prior devZ and trust the new one. The transfer is
	// mandatory whenever the prior devZ's fingerprint differs from the new
	// request — independent of whether the SHIP ID is the same (legitimate
	// cert rotation under a stable SHIP ID per SHIP §12.2.1) or different
	// (operator replaced the device).
	var replacedService *api.ServiceDetails = h.GetTrustedAddCuDevice()

	if replacedService != nil && replacedService.ShipID() != "" && replacedService.Fingerprint() != remoteFingerprint {
		if h.addCuReplacementTracker.IsInReplacementWindow() {
			// §4.3.1.a 15-minute window still active - defer; the announcement
			// will be re-evaluated when the timer expires.
			logging.Log().Debug("Ignoring pairing announcement during replacement window",
				"existingShipID", replacedService.ShipID(), "newShipID", remoteShipID)
			return
		}
		replacedService.SetTrusted(false)
		h.removeServiceEntry(replacedService)
		h.addCuReplacementTracker.StopTimer(replacedService.ShipID())
	}

	// Build a candidate carrying everything we just learned and let the
	// merge fold it into any existing entry that matches by fingerprint OR
	// by ShipID (e.g. an untrusted SKI+FP entry left behind by an earlier
	// rejected incoming connection).
	candidate, err := api.NewServiceDetails("", remoteFingerprint, remoteShipID)
	if err != nil {
		logging.Log().Error("OnPairingSuccess: invalid identifiers", "shipID", remoteShipID, "fingerprint", remoteFingerprint, "error", err)
		return
	}
	candidate.SetTrusted(true)
	candidate.SetPairingType(api.PairingTypeAddCu)

	service, err := h.mergeOrAddService(candidate)
	if err != nil {
		// Hard conflict: e.g. fingerprint already associated with a
		// different ShipID — possible impersonation attempt.
		logging.Log().Error("OnPairingSuccess rejected: identifier conflict",
			"shipID", remoteShipID, "fingerprint", remoteFingerprint, "error", err)
		return
	}

	logging.Log().Trace("Pairing success - trust established immediately - ShipID:", remoteShipID, "Fingerprint:", remoteFingerprint)

	// Handle replacement callback before success callback
	if replacedService != nil {
		callbackReason := "Device replaced by new AddCu device during pairing"
		h.callDeviceAutoTrustRemovedCallback(replacedService, callbackReason)
	}

	// Direct callbacks following existing Hub patterns - call PairingServiceReaderInterface if available
	if pairingReader, ok := h.hubReader.(api.PairingServiceReaderInterface); ok {
		// Convert ServiceDetails to ServiceIdentity - thread-safe, no Copy() needed
		identity := service.ToServiceIdentity()
		pairingReader.ServiceAutoTrusted(identity)
	}

	// The accepted request may have been withdrawn right after evaluation or
	// replayed from the mDNS cache, so a SHIP connection may never
	// materialise. Arm the replacement timer so the spec §4.3 1.a recovery
	// window runs from trust establishment; it is stopped as soon as the
	// SHIP connection completes.
	if conn := h.connectionForService(service); conn == nil {
		h.addCuReplacementTracker.StartTimer(remoteShipID, h.handleAddCuReplacementTimeout)
	}

	// we have to initiate checking the mds records again, to trigger a connection
	h.mdns.RequestMdnsEntries()
}

// OnPairingFailure handles pairing validation failure (implements PairingHubInterface)
func (h *Hub) OnPairingFailure(remoteShipID, remoteFingerprint string, reason error) {
	// Use ServiceForFingerprint for consistent service management and SKI normalization
	service, err := api.NewServiceDetails("", remoteFingerprint, remoteShipID)
	if err != nil {
		logging.Log().Error("OnPairingFailure: invalid identifiers", "shipID", remoteShipID, "fingerprint", remoteFingerprint, "error", err)
		return
	}
	service.ConnectionStateDetail().SetState(api.ConnectionStateError)
	service.ConnectionStateDetail().SetError(reason)

	// Direct callbacks following existing Hub patterns
	if pairingReader, ok := h.hubReader.(api.PairingServiceReaderInterface); ok {
		// Convert ServiceDetails to ServiceIdentity - thread-safe, no Copy() needed
		identity := service.ToServiceIdentity()
		pairingReader.ServiceAutoTrustFailed(identity, reason)
	}
}

// GetLocalCertificateFingerprint calculates SHA-256 fingerprint of Hub's certificate
func (h *Hub) GetLocalCertificateFingerprint() (string, error) {
	if len(h.certificate.Certificate) == 0 {
		return "", api.ErrInvalidCertificate
	}

	// Parse DER certificate
	certificate, err := x509.ParseCertificate(h.certificate.Certificate[0])
	if err != nil {
		return "", api.ErrInvalidCertificate
	}

	// Calculate SHA-256 fingerprint using cert package
	return cert.FingerprintFromCertificate(certificate)
}

// GeneratePairingQR generates a QR code string for pairing
// When there is no pairing service, or secret key is not defined: generates standard SHIP QR format
// When there is pairing service in listener mode with secret key provided: generates SHIP Pairing Service QR format
func (h *Hub) GeneratePairingQR() (string, error) {
	if h.pairingConfig == nil || len(h.pairingConfig.Secret) == 0 {
		// Generate standard SHIP QR format: SHIP;SKI:<ski>;ID:<identifier>;<optionals>ENDSHIP;
		return h.generateStandardShipQR()
	}

	// Generate SHIP Pairing Service QR format
	return h.generatePairingServiceQR(h.pairingConfig.Secret)
}

// generateStandardShipQR generates the standard SHIP QR format
func (h *Hub) generateStandardShipQR() (string, error) {
	ski := h.localService.SKI()
	identifier := h.localService.ShipID()
	optionals := h.buildOptionalMetadata()

	qrcode := fmt.Sprintf("SHIP;SKI:%s;ID:%s;%sENDSHIP;", ski, identifier, optionals)
	return qrcode, nil
}

// generatePairingServiceQR generates the SHIP Pairing Service QR format
func (h *Hub) generatePairingServiceQR(secret api.PairingSecret) (string, error) {
	// Validate secret is exactly 16 bytes
	if len(secret) < 16 {
		return "", api.ErrSecretTooShort
	}
	if len(secret) > 16 {
		return "", api.ErrInvalidSecret
	}

	// Get certificate fingerprint
	fingerprint, err := h.GetLocalCertificateFingerprint()
	if err != nil {
		return "", err
	}

	// Get required fields
	ski := h.localService.SKI()
	shipID := h.localService.ShipID()

	// Encode secret as uppercase hex
	secretHex := strings.ToUpper(hex.EncodeToString(secret))

	// Get optional device metadata
	optionals := h.buildOptionalMetadata()

	// Generate SHIP Pairing Service QR format per SHIP spec Annex A.1:
	// SHIP;SKI:xxx;ID:xxx;BRAND:xxx;TYPE:xxx;MODEL:xxx;SERIAL:xxx;CAT:xxx;FPH256:xxx;SPSEC:xxx;ENDSHIP;
	qrString := fmt.Sprintf("SHIP;SKI:%s;ID:%s;%sFPH256:%s;SPSEC:%s;ENDSHIP;",
		ski, shipID, optionals, fingerprint, secretHex)

	return qrString, nil
}

// buildOptionalMetadata builds the optional metadata string for QR codes
func (h *Hub) buildOptionalMetadata() string {
	var optionals string

	// Get device metadata from mDNS interface
	if brand := h.mdns.DeviceBrand(); len(brand) > 0 {
		optionals += h.safeQRCodeKeyValue("BRAND", brand)
	}

	if deviceType := h.mdns.DeviceType(); len(deviceType) > 0 {
		optionals += h.safeQRCodeKeyValue("TYPE", deviceType)
	}

	if model := h.mdns.DeviceModel(); len(model) > 0 {
		optionals += h.safeQRCodeKeyValue("MODEL", model)
	}

	if serial := h.mdns.DeviceSerial(); len(serial) > 0 {
		optionals += h.safeQRCodeKeyValue("SERIAL", serial)
	}

	if categories := h.mdns.DeviceCategories(); categories != nil {
		optionals += h.safeQRCodeKeyValue("CAT", h.deviceCategoriesString(categories))
	}

	return optionals
}

// safeQRCodeKeyValue returns a safe to use key value pair for the QR code text in the proper format
// according to SHIP Requirements for Installation Process V1.0.0
func (h *Hub) safeQRCodeKeyValue(key, value string) string {
	if len(value) > 0 {
		// make sure the value contains no ; chars
		value = strings.ReplaceAll(value, ";", "")

		// make sure the keys are all uppercase
		key = strings.ToUpper(key)
		return fmt.Sprintf("%s:%s;", key, value)
	}

	return ""
}

// deviceCategoriesString returns the device categories as a string, with categories separated by commas
func (h *Hub) deviceCategoriesString(categories []api.DeviceCategoryType) string {
	var cat string
	for _, category := range categories {
		if len(cat) > 0 {
			cat += ","
		}
		cat += fmt.Sprintf("%d", category)
	}
	return cat
}
