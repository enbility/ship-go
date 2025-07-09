package ship

import (
	"github.com/enbility/ship-go/logging"
	"github.com/enbility/ship-go/model"
)

// ApprovePendingHandshake invoked when pairing for a pending request is approved
func (c *ShipConnection) ApprovePendingHandshake() {
	// Use the new atomic method to avoid race conditions
	c.ApproveIfPending()
}

// AbortPendingHandshake invoked when pairing for a pending request is denied
func (c *ShipConnection) AbortPendingHandshake() {
	// Use the new atomic method to avoid race conditions
	c.AbortIfPending()
}

// ApproveIfPending atomically approves the handshake if in pending state
// Returns true if the approval was successful, false otherwise
//
// This method addresses a race condition where concurrent approve/abort operations
// could result in inconsistent state. The previous implementation had a time-of-check-time-of-use
// (TOCTOU) bug where the state was read without holding the lock, then actions were taken
// based on that potentially stale state.
//
// The atomic implementation ensures that the state check and update happen together
// while holding the lock, preventing race conditions.
func (c *ShipConnection) ApproveIfPending() bool {
	c.mux.Lock()

	if c.smeState != model.SmeHelloStatePendingListen {
		c.mux.Unlock()
		return false
	}

	// Update state while holding lock
	c.smeState = model.SmeHelloStateReadyInit
	c.mux.Unlock()

	// Now handle state transitions without lock to avoid deadlock
	c.stopTimerSafe()
	c.handleState(false, nil)

	// Validate connection health and trust state before completing handshake
	if !c.validateConnectionBeforeApproval() {
		// Validation failed, abort the handshake
		c.setAndHandleState(model.SmeHelloStateAbort)
		return false
	}

	c.setAndHandleState(model.SmeHelloStateOk)

	return true
}

// validateConnectionBeforeApproval performs security and connection health checks
// before transitioning from SmeHelloStateReadyInit to SmeHelloStateOk
func (c *ShipConnection) validateConnectionBeforeApproval() bool {
	// Check if the WebSocket connection is still active
	if isClosed, err := c.dataWriter.IsDataConnectionClosed(); isClosed {
		if err != nil {
			logging.Log().Debug(c.RemoteSKI(), "Connection validation failed: WebSocket closed with error:", err)
		} else {
			logging.Log().Debug(c.RemoteSKI(), "Connection validation failed: WebSocket connection closed")
		}
		return false
	}

	// Verify that the remote service is still paired/trusted
	if !c.infoProvider.IsRemoteServiceForSKIPaired(c.remoteSKI) {
		logging.Log().Debug(c.RemoteSKI(), "Connection validation failed: Remote service no longer paired")
		return false
	}

	// Check if the trust state is still valid (user hasn't revoked trust)
	if !c.infoProvider.AllowWaitingForTrust(c.remoteSKI) {
		logging.Log().Debug(c.RemoteSKI(), "Connection validation failed: Trust revoked")
		return false
	}

	// All validations passed
	return true
}

// AbortIfPending atomically aborts the handshake if in pending or ready state
// Returns true if the abort was successful, false otherwise
//
// This method accepts both SmeHelloStatePendingListen and SmeHelloStateReadyListen states
// because the handshake can be aborted at any point before reaching SmeHelloStateOk.
// The ReadyListen state is included to handle the case where an approve operation
// transitions the state from PendingListen through ReadyInit to ReadyListen, but
// the handshake should still be abortable until fully complete.
//
// Similar to ApproveIfPending, this method prevents race conditions during
// concurrent handshake operations by atomically checking and updating the state.
func (c *ShipConnection) AbortIfPending() bool {
	c.mux.Lock()

	if c.smeState != model.SmeHelloStatePendingListen && c.smeState != model.SmeHelloStateReadyListen {
		c.mux.Unlock()
		return false
	}

	// Update state while holding lock
	c.smeState = model.SmeHelloStateAbort
	c.mux.Unlock()

	// Now handle state transitions without lock to avoid deadlock
	c.stopTimerSafe()
	c.handleState(false, nil)

	return true
}
