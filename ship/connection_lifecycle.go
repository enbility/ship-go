package ship

import (
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/model"
	"github.com/enbility/ship-go/util"
)

// NewConnectionHandler creates a new SHIP connection handler
func NewConnectionHandler(
	dataProvider api.ShipConnectionInfoProviderInterface,
	dataHandler api.WebsocketDataWriterInterface,
	role shipRole,
	localShipID,
	remoteSki,
	remoteShipId string) *ShipConnection {
	ship := &ShipConnection{
		infoProvider: dataProvider,
		dataWriter:   dataHandler,
		role:         role,
		localShipID:  localShipID,
		remoteSKI:    remoteSki,
		remoteShipID: remoteShipId,
		smeState:     model.CmiStateInitStart,
		smeError:     nil,
	}

	ship.handshakeTimerDone = make(chan struct{})

	if dataHandler != nil {
		dataHandler.InitDataProcessing(ship)
	}

	return ship
}

// RemoteSKI returns the remote SKI
func (c *ShipConnection) RemoteSKI() string {
	return c.remoteSKI
}

// DataHandler returns the WebSocket data writer
func (c *ShipConnection) DataHandler() api.WebsocketDataWriterInterface {
	return c.dataWriter
}

// Run starts SHIP communication
func (c *ShipConnection) Run() {
	c.handleShipMessage(false, nil)
}

// ShipHandshakeState provides the current ship state and error value if the state is in error
func (c *ShipConnection) ShipHandshakeState() (model.ShipMessageExchangeState, error) {
	c.mux.Lock()
	defer c.mux.Unlock()
	return c.smeState, c.smeError
}

// CloseConnection closes this ship connection
func (c *ShipConnection) CloseConnection(safe bool, code int, reason string) {
	c.shutdownOnce.Do(func() {
		// Stop timer and wait for completion with a reasonable timeout
		done := c.stopHandshakeTimer()
		select {
		case <-done:
			// Timer stopped cleanly
		case <-time.After(100 * time.Millisecond):
			// Timeout waiting for timer
		}

		// handshake is completed if approved or aborted
		state := c.getState()
		handshakeEnd := state == model.SmeStateComplete ||
			state == model.SmeHelloStateAbortDone ||
			state == model.SmeHelloStateRemoteAbortDone ||
			state == model.SmeHelloStateRejected

		// this may not be used for Connection Data Exchange is entered!
		if safe && state == model.SmeStateComplete {
			// SHIP 13.4.7: Connection Termination Announce
			closeMessage := model.ConnectionClose{
				ConnectionClose: model.ConnectionCloseType{
					Phase:   model.ConnectionClosePhaseTypeAnnounce,
					MaxTime: util.Ptr(uint(500)),
					Reason:  util.Ptr(model.ConnectionCloseReasonType(reason)),
				},
			}

			_ = c.sendShipModel(model.MsgTypeEnd, closeMessage)

			go func() {
				// wait a bit to let it send
				<-time.After(100 * time.Millisecond)

				c.dataWriter.CloseDataConnection(4001, "close")
				c.infoProvider.HandleConnectionClosed(c, handshakeEnd)
			}()
			return
		}

		closeCode := 4001
		if code != 0 {
			closeCode = code
		}
		c.dataWriter.CloseDataConnection(closeCode, reason)

		c.infoProvider.HandleConnectionClosed(c, handshakeEnd)
	})
}
