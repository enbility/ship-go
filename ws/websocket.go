package ws

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/logging"
	"github.com/enbility/ship-go/model"
	"github.com/gorilla/websocket"
)

const connIsClosedError string = "connection is closed"

// Handling of the actual websocket connection to a remote device
type WebsocketConnection struct {
	// The actual websocket connection
	conn *websocket.Conn

	// The implementation handling message processing
	dataProcessing api.WebsocketDataReaderInterface

	// The connection was closed
	closeChannel chan struct{}

	// The ship write channel for outgoing SHIP messages
	shipWriteChannel chan []byte

	// internal handling of closed connections
	connectionClosed bool

	// the error message received for the closed connection
	connectionClosedError error

	remoteSki string

	muxConnClosed sync.Mutex
	muxShipWrite  sync.Mutex
	muxConWrite   sync.Mutex
	shutdownOnce  sync.Once
}

// create a new websocket based shipDataProcessing implementation
func NewWebsocketConnection(conn *websocket.Conn, remoteSki string) *WebsocketConnection {
	return &WebsocketConnection{
		conn:                  conn,
		remoteSki:             remoteSki,
		connectionClosedError: nil,
	}
}

// sets the error message for the closed connection
func (w *WebsocketConnection) setConnClosedError(err error) {
	w.muxConnClosed.Lock()
	defer w.muxConnClosed.Unlock()

	w.connectionClosed = true

	if err != nil {
		w.connectionClosedError = err
	}
}

func (w *WebsocketConnection) connClosedError() error {
	w.muxConnClosed.Lock()
	defer w.muxConnClosed.Unlock()

	return w.connectionClosedError
}

// check if the websocket connection is closed
func (w *WebsocketConnection) isConnClosed() bool {
	w.muxConnClosed.Lock()
	defer w.muxConnClosed.Unlock()

	return w.connectionClosed
}

func (w *WebsocketConnection) run() {
	w.shipWriteChannel = make(chan []byte, 1) // Send outgoing ship messages
	w.closeChannel = make(chan struct{}, 1)   // Listen to close events

	go w.readShipPump()
	go w.writeShipPump()
}

// writePump pumps messages from the SPINE and SHIP writeChannels to the websocket connection
func (w *WebsocketConnection) writeShipPump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		w.closeShipWriteChannel()
	}()

	for {
		select {
		case <-w.closeChannel:
			return

		case message, ok := <-w.shipWriteChannel:
			if w.isConnClosed() {
				return
			}

			if !ok {
				logging.Log().Debug(w.remoteSki, "ship write channel closed")
				// The write channel has been closed
				_ = w.writeMessage(websocket.CloseMessage, []byte{})
				return
			}

			w.muxConWrite.Lock()
			_ = w.conn.SetWriteDeadline(time.Now().Add(writeWait))
			w.muxConWrite.Unlock()

			if !w.writeMessage(websocket.BinaryMessage, message) {
				return
			}

			text := w.textFromMessage(message)
			logging.Log().Trace("Send:", w.remoteSki, text)

		case <-ticker.C:
			w.handlePing()
		}
	}
}

func (w *WebsocketConnection) handlePing() {
	if w.isConnClosed() {
		return
	}

	w.muxConWrite.Lock()
	_ = w.conn.SetWriteDeadline(time.Now().Add(writeWait))
	w.muxConWrite.Unlock()
	_ = w.writeMessage(websocket.PingMessage, nil)
}

func (w *WebsocketConnection) closeWithError(err error, reason string) {
	logging.Log().Debug(w.remoteSki, reason, err)
	w.setConnClosedError(err)
	w.dataProcessing.ReportConnectionError(err)
}

// readShipPump checks for messages from the websocket connection
func (w *WebsocketConnection) readShipPump() {
	_ = w.conn.SetReadDeadline(time.Now().Add(pongWait))
	w.conn.SetPongHandler(func(string) error { _ = w.conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })

	for {
		select {
		case <-w.closeChannel:
			return

		default:
			if w.isConnClosed() {
				return
			}

			message, err := w.readWebsocketMessage()
			// ignore read errors if the connection got closed
			if w.isConnClosed() {
				return
			}

			if err != nil {
				logging.Log().Debug(w.remoteSki, "websocket read error: ", err)
				w.close()
				w.setConnClosedError(err)
				w.dataProcessing.ReportConnectionError(err)
				return
			}

			text := w.textFromMessage(message)
			logging.Log().Trace("Recv:", w.remoteSki, text)

			w.dataProcessing.HandleIncomingWebsocketMessage(message)
		}
	}
}

func (w *WebsocketConnection) textFromMessage(msg []byte) string {
	text := "unknown single byte"
	if len(msg) > 2 {
		text = string(msg[1:])
	} else if bytes.Equal(msg, model.ShipInit) {
		text = "ship init"
	}

	return text
}

// read a message from the websocket connection
func (w *WebsocketConnection) readWebsocketMessage() ([]byte, error) {
	if w.conn == nil {
		return nil, errors.New("connection is not initialized")
	}

	msgType, b, err := w.conn.ReadMessage()
	if err != nil {
		return nil, err
	}

	if err := w.checkWebsocketMessage(msgType, b); err != nil {
		return nil, err
	}

	return b, nil
}

func (w *WebsocketConnection) checkWebsocketMessage(msgType int, data []byte) error {
	if msgType != websocket.BinaryMessage {
		return errors.New("message is not a binary message")
	}

	if len(data) < 2 {
		return fmt.Errorf("invalid ship message length")
	}

	return nil
}

// close the current websocket connection
func (w *WebsocketConnection) close() {
	w.shutdownOnce.Do(func() {
		if w.isConnClosed() {
			return
		}

		w.setConnClosedError(nil)

		close(w.closeChannel)

		if w.conn != nil {
			_ = w.conn.Close()
		}
	})
}

var _ api.WebsocketDataWriterInterface = (*WebsocketConnection)(nil)

func (w *WebsocketConnection) InitDataProcessing(dataProcessing api.WebsocketDataReaderInterface) {
	w.dataProcessing = dataProcessing

	w.run()
}

// write a message to the websocket connection
func (w *WebsocketConnection) WriteMessageToWebsocketConnection(message []byte) error {
	w.muxShipWrite.Lock()
	defer w.muxShipWrite.Unlock()

	if w.isConnClosed() || w.shipWriteChannel == nil {
		return errors.New(connIsClosedError)
	}

	w.shipWriteChannel <- message
	return nil
}

// make sure websocket Write is only called once at a time
func (w *WebsocketConnection) writeMessage(messageType int, data []byte) bool {
	if w.isConnClosed() {
		return false
	}

	err := w.writeMessageWithoutErrorHandling(messageType, data)
	if err != nil {
		// ignore write errors if the connection got closed
		w.closeWithError(err, "error writing to websocket: ")
		logging.Log().Debug("WRITE ERROR: ", err)
		return false
	}

	return true
}

// make sure websocket Write is only called once at a time
func (w *WebsocketConnection) writeMessageWithoutErrorHandling(messageType int, data []byte) error {
	if w.isConnClosed() {
		return errors.New(connIsClosedError)
	}

	w.muxConWrite.Lock()
	defer w.muxConWrite.Unlock()

	return w.conn.WriteMessage(messageType, data)
}

// shutdown the connection and all internals
func (w *WebsocketConnection) CloseDataConnection(closeCode int, reason string) {
	// send a close message to the remote side if we have a reason
	if reason != "" {
		_ = w.writeMessageWithoutErrorHandling(websocket.CloseMessage, websocket.FormatCloseMessage(closeCode, reason))
	}

	w.close()
}

// return if the connection is closed
func (w *WebsocketConnection) IsDataConnectionClosed() (bool, error) {
	isClosed := w.isConnClosed()
	err := w.connClosedError()

	if isClosed && err == nil {
		err = errors.New("connection is closed")
	}

	return isClosed, err
}

func (w *WebsocketConnection) closeShipWriteChannel() {
	w.muxShipWrite.Lock()
	defer w.muxShipWrite.Unlock()
	close(w.shipWriteChannel)
}
