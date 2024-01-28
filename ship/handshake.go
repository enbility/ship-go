package ship

import (
	"errors"
	"time"

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
	c.mux.Lock()

	oldState := c.smeState

	c.smeState = newState
	logging.Log().Trace(c.RemoteSKI(), "SHIP state changed to:", newState)

	switch newState {
	case model.SmeHelloStateReadyInit:
		c.setHandshakeTimer(timeoutTimerTypeWaitForReady, tHelloInit)
	case model.SmeHelloStatePendingInit:
		c.setHandshakeTimer(timeoutTimerTypeWaitForReady, tHelloInit)
	case model.SmeHelloStateOk:
		c.stopHandshakeTimer()
	case model.SmeHelloStateAbort, model.SmeHelloStateAbortDone, model.SmeHelloStateRemoteAbortDone, model.SmeHelloStateRejected:
		c.stopHandshakeTimer()
	case model.SmeProtHStateClientListenChoice:
		c.setHandshakeTimer(timeoutTimerTypeWaitForReady, cmiTimeout)
	case model.SmeProtHStateClientOk:
		c.stopHandshakeTimer()
	}

	c.smeError = nil
	if oldState != newState {
		c.smeError = err
		state := model.ShipState{
			State: newState,
			Error: err,
		}
		c.mux.Unlock()
		c.infoProvider.HandleShipHandshakeStateUpdate(c.remoteSKI, state)
		return
	}
	c.mux.Unlock()
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
			c.endHandshakeWithError(errors.New("ship client handshake timeout"))
			return
		}

		c.handshakeInit_cmiStateClientWait(message)

	case model.CmiStateServerWait:
		if timeout {
			c.endHandshakeWithError(errors.New("ship server handshake timeout"))
			return
		}
		c.handshakeInit_cmiStateServerWait(message)

	// smeHello

	case model.SmeHelloState:
		// check if the service is already trusted or the role is client,
		// which means it was initiated from this service usually by triggering the
		// pairing service
		// go to substate ready if so, otherwise to substate pending

		if c.infoProvider.IsRemoteServiceForSKIPaired(c.remoteSKI) || c.role == ShipRoleClient {
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
			time.Sleep(time.Second)
			c.CloseConnection(false, 4452, "Node rejected by application")
		}()

	// smeProtocol

	case model.SmeProtHStateServerListenProposal:
		c.handshakeProtocol_smeProtHStateServerListenProposal(message)

	case model.SmeProtHStateServerListenConfirm:
		c.handshakeProtocol_smeProtHStateServerListenConfirm(message)

	case model.SmeProtHStateClientListenChoice:
		c.stopHandshakeTimer()
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
	c.dataReader = c.infoProvider.SetupRemoteDevice(c.remoteSKI, c)
	c.stopHandshakeTimer()
	c.setState(model.SmeStateComplete, nil)
	c.processBufferedSpineMessages()
}

// end the handshake process because of an error
func (c *ShipConnection) endHandshakeWithError(err error) {
	c.stopHandshakeTimer()

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

	c.setHandshakeTimerRunning(true)
	c.setHandshakeTimerType(timerType)

	go func() {
		select {
		case <-c.handshakeTimerStopChan:
			return
		case <-time.After(duration):
			c.setHandshakeTimerRunning(false)
			c.handleState(true, nil)
			return
		}
	}()
}

// stop the handshake timer and close the channel
func (c *ShipConnection) stopHandshakeTimer() {
	if !c.getHandshakeTimerRunning() {
		return
	}

	select {
	case c.handshakeTimerStopChan <- struct{}{}:
	default:
	}
	c.setHandshakeTimerRunning(false)
}

func (c *ShipConnection) setHandshakeTimerRunning(value bool) {
	c.handshakeTimerMux.Lock()
	defer c.handshakeTimerMux.Unlock()

	c.handshakeTimerRunning = value
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
