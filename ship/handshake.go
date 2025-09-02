package ship

import (
	"fmt"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/logging"
	"github.com/enbility/ship-go/model"
)

// handle incoming SHIP messages and coordinate Handshake States
func (c *ShipConnection) handleShipMessage(timeout bool, message []byte) {
	if len(message) > 2 {
		var closeMsg model.ConnectionClose
		err := c.processShipJsonMessage(message, &closeMsg)
		if err == nil && closeMsg.ConnectionClose.Phase != "" {
			switch closeMsg.ConnectionClose.Phase {
			case model.ConnectionClosePhaseTypeAnnounce:
				// SHIP 13.4.7: Connection Termination Confirm
				closeMessage := model.ConnectionClose{
					ConnectionClose: model.ConnectionCloseType{
						Phase: model.ConnectionClosePhaseTypeConfirm,
					},
				}

				_ = c.sendShipModel(model.MsgTypeEnd, closeMessage)

				// wait a bit to let it send
				<-time.After(500 * time.Millisecond)

				//
				c.dataWriter.CloseDataConnection(4001, "close")
				c.infoProvider.HandleConnectionClosed(c, c.getState() == model.SmeStateComplete)
			case model.ConnectionClosePhaseTypeConfirm:
				// we got a confirmation so close this connection
				c.dataWriter.CloseDataConnection(4001, "close")
				c.infoProvider.HandleConnectionClosed(c, c.getState() == model.SmeStateComplete)
			}

			return
		}
	}

	c.handleState(timeout, message)
}

// set a new handshake state and handle timers if needed
func (c *ShipConnection) setState(newState model.ShipMessageExchangeState, err error) {
	// Phase 1: Update state atomically while holding lock
	var timerOp func()
	var shouldNotify bool
	var notifyState model.ShipState

	c.mux.Lock()
	oldState := c.smeState
	c.smeState = newState
	logging.Log().Trace(c.RemoteSKI(), "SHIP state changed to:", newState)

	// Determine timer operation while holding lock, but don't execute it yet
	switch newState {
	case model.SmeHelloStateReadyInit:
		timerOp = func() { c.setHandshakeTimer(timeoutTimerTypeWaitForReady, getHelloInitTimeout()) }
	case model.SmeHelloStatePendingInit:
		timerOp = func() { c.setHandshakeTimer(timeoutTimerTypeWaitForReady, getHelloInitTimeout()) }
	case model.SmeHelloStateOk:
		timerOp = func() { c.stopTimerSafe() }
	case model.SmeHelloStateAbort, model.SmeHelloStateAbortDone, model.SmeHelloStateRemoteAbortDone, model.SmeHelloStateRejected:
		timerOp = func() { c.stopTimerSafe() }
	case model.SmeProtHStateClientListenChoice:
		timerOp = func() { c.setHandshakeTimer(timeoutTimerTypeWaitForReady, getCmiTimeout()) }
	case model.SmeProtHStateClientOk:
		timerOp = func() { c.stopTimerSafe() }
	}

	c.smeError = nil
	if oldState != newState {
		c.smeError = err
		shouldNotify = true
		notifyState = model.ShipState{
			State: newState,
			Error: err,
		}
	}
	c.mux.Unlock()

	// Phase 2: Execute timer operations and notifications without holding lock
	if timerOp != nil {
		timerOp()
	}

	if shouldNotify {
		c.infoProvider.HandleShipHandshakeStateUpdate(c.remoteSKI, notifyState)
	}
}

func (c *ShipConnection) getState() model.ShipMessageExchangeState {
	c.mux.Lock()
	defer c.mux.Unlock()

	return c.smeState
}

// handle handshake state transitions
func (c *ShipConnection) handleState(timeout bool, message []byte) {
	switch c.getState() {
	case model.SmeStateError:
		logging.Log().Debug(c.RemoteSKI(), "connection is in error state")
		return

	// cmiStateInit
	case model.CmiStateInitStart:
		// triggered without a message received
		c.handshakeInit_cmiStateInitStart()

	case model.CmiStateClientWait:
		if timeout {
			c.endHandshakeWithError(fmt.Errorf("%w: SHIP client handshake with remote SKI %s (state: %v)",
				api.ErrConnectionTimeout, c.remoteSKI, model.CmiStateClientWait))
			return
		}

		c.handshakeInit_cmiStateClientWait(message)

	case model.CmiStateServerWait:
		if timeout {
			c.endHandshakeWithError(fmt.Errorf("%w: SHIP server handshake with remote SKI %s (state: %v)",
				api.ErrConnectionTimeout, c.remoteSKI, model.CmiStateServerWait))
			return
		}
		c.handshakeInit_cmiStateServerWait(message)

	// smeHello

	case model.SmeHelloState:
		// check if the service is already trusted, auto accept is true or the role is client,
		// which means it was initiated from this service usually by triggering the
		// pairing service
		// go to substate ready if so, otherwise to substate pending

		if c.infoProvider.IsRemoteServiceForSKIPaired(c.remoteSKI) ||
			c.infoProvider.IsAutoAcceptEnabled() ||
			c.role == ShipRoleClient {
			c.setState(model.SmeHelloStateReadyInit, nil)
		} else {
			c.setState(model.SmeHelloStatePendingInit, nil)
		}
		c.handleState(timeout, message)

	case model.SmeHelloStateReadyInit:
		c.handshakeHello_Init()

	case model.SmeHelloStateReadyListen:
		c.handshakeHello_ReadyListen(timeout, message)

	case model.SmeHelloStatePendingInit:
		c.handshakeHello_PendingInit()

	case model.SmeHelloStatePendingListen:
		c.handshakeHello_PendingListen(timeout, message)

	case model.SmeHelloStateOk:
		c.handshakeProtocol_Init()

	case model.SmeHelloStateAbort:
		c.handshakeHello_Abort()

	case model.SmeHelloStateAbortDone, model.SmeHelloStateRemoteAbortDone:
		go func() {
			<-time.After(tAbortDelay)
			c.CloseConnection(false, 4452, "Node rejected by application")
		}()

	// smeProtocol

	case model.SmeProtHStateServerListenProposal:
		c.handshakeProtocol_smeProtHStateServerListenProposal(message)

	case model.SmeProtHStateServerListenConfirm:
		c.handshakeProtocol_smeProtHStateServerListenConfirm(message)

	case model.SmeProtHStateClientListenChoice:
		c.stopTimerSafe()
		c.handshakeProtocol_smeProtHStateClientListenChoice(message)

	case model.SmeProtHStateClientOk:
		c.setAndHandleState(model.SmePinStateCheckInit)

	case model.SmeProtHStateServerOk:
		c.setAndHandleState(model.SmePinStateCheckInit)

	// smePinState

	case model.SmePinStateCheckInit:
		c.handshakePin_Init()

	case model.SmePinStateCheckListen:
		c.handshakePin_smePinStateCheckListen(message)

	case model.SmePinStateCheckOk:
		c.handshakeAccessMethods_Init()

	// smeAccessMethods

	case model.SmeAccessMethodsRequest:
		c.handshakeAccessMethods_Request(message)
	}
}

// set a state and trigger handling it
func (c *ShipConnection) setAndHandleState(state model.ShipMessageExchangeState) {
	c.setState(state, nil)
	c.handleState(false, nil)
}

// SHIP handshake is approved, now set the new state and the SPINE read handler
func (c *ShipConnection) approveHandshake() {
	// Report to SPINE local device about this remote device connection
	c.dataReader = c.infoProvider.SetupRemoteService(c.remoteSKI, c)
	c.stopTimerSafe()
	c.setState(model.SmeStateComplete, nil)
	c.processBufferedSpineMessages()
}

// end the handshake process because of an error
func (c *ShipConnection) endHandshakeWithError(err error) {
	c.stopTimerSafe()

	c.setState(model.SmeStateError, err)

	logging.Log().Debug(c.RemoteSKI(), "SHIP handshake error:", err)

	c.CloseConnection(true, 0, err.Error())

	state := model.ShipState{
		State: model.SmeStateError,
		Error: err,
	}
	c.infoProvider.HandleShipHandshakeStateUpdate(c.remoteSKI, state)
}

// set the handshake timer to a new duration and start the channel
func (c *ShipConnection) setHandshakeTimer(timerType timeoutTimerType, duration time.Duration) {
	c.stopHandshakeTimer()

	c.handshakeTimerMux.Lock()
	defer c.handshakeTimerMux.Unlock()

	// Create a new done channel for this timer
	c.handshakeTimerDone = make(chan struct{})
	done := c.handshakeTimerDone

	c.handshakeTimerType = timerType
	c.handshakeTimerRunning = true
	c.handshakeTimer = time.AfterFunc(duration, func() {
		defer close(done) // Signal completion when this goroutine exits

		c.handshakeTimerMux.Lock()
		// Check if this timer is still active
		if c.handshakeTimer == nil {
			c.handshakeTimerMux.Unlock()
			return
		}
		c.handshakeTimer = nil
		c.handshakeTimerRunning = false
		c.handshakeTimerMux.Unlock()

		c.handleState(true, nil)
	})
}

// stopHandshakeTimer stops the timer and returns a channel that closes when the timer goroutine completes
func (c *ShipConnection) stopHandshakeTimer() <-chan struct{} {
	c.handshakeTimerMux.Lock()
	defer c.handshakeTimerMux.Unlock()

	if c.handshakeTimer == nil {
		// No timer running, return closed channel
		ch := make(chan struct{})
		close(ch)
		return ch
	}

	// Stop the timer
	stopped := c.handshakeTimer.Stop()
	done := c.handshakeTimerDone
	c.handshakeTimer = nil
	c.handshakeTimerRunning = false

	// If we successfully stopped the timer (it hadn't fired yet),
	// we need to close the done channel ourselves since the timer
	// function won't run to close it
	if stopped && done != nil {
		// Use a goroutine to avoid potential deadlock if someone is waiting on done
		go func() {
			// Ensure we don't panic on double close (defensive programming)
			defer func() { recover() }()
			close(done)
		}()
	}

	return done
}

func (c *ShipConnection) getHandshakeTimerRunning() bool {
	c.handshakeTimerMux.Lock()
	defer c.handshakeTimerMux.Unlock()

	return c.handshakeTimerRunning
}

func (c *ShipConnection) setHandshakeTimerType(timerType timeoutTimerType) {
	c.handshakeTimerMux.Lock()
	defer c.handshakeTimerMux.Unlock()

	c.handshakeTimerType = timerType
}

func (c *ShipConnection) getHandshakeTimerType() timeoutTimerType {
	c.handshakeTimerMux.Lock()
	defer c.handshakeTimerMux.Unlock()

	return c.handshakeTimerType
}

// stopTimerSafe atomically stops the handshake timer if it's running
// Returns true if the timer was stopped, false if it wasn't running
//
// This method addresses a race condition in timer management where the timer
// running state was checked without holding the lock continuously through the
// operation. The atomic implementation prevents concurrent timer operations
// from conflicting with each other.
func (c *ShipConnection) stopTimerSafe() bool {
	c.handshakeTimerMux.Lock()
	wasRunning := c.handshakeTimerRunning
	c.handshakeTimerMux.Unlock()

	if wasRunning {
		c.stopHandshakeTimer()
	}

	return wasRunning
}

// startHandshakeTimer starts the handshake timer (exposed for testing)
func (c *ShipConnection) startHandshakeTimer(duration time.Duration, timerType timeoutTimerType) {
	c.setHandshakeTimer(timerType, duration)
}
