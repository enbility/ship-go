package ws

import (
	"bytes"
	"fmt"
	"sync"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/logging"
	"github.com/enbility/ship-go/model"
	"github.com/gorilla/websocket"
)

// Handling of the actual websocket connection to a remote device
type WebsocketConnection struct {
	// The actual websocket connection
	conn *websocket.Conn

	// The implementation handling message processing
	dataProcessing api.WebsocketDataReaderInterface

	// The ship write channel for outgoing SHIP messages
	shipWriteChannel chan []byte

	// Closed exactly once, by writeShipPump itself, right before it returns.
	writeDone chan struct{}

	// A close is underway, so no new SHIP messages are accepted. Guarded by muxShipWrite.
	closing bool

	// The close code/reason writeShipPump writes as a close frame after draining shipWriteChannel.
	// Set at most once, guarded by muxShipWrite. An empty reason means no close frame is written.
	closeCode   int
	closeReason string

	// internal handling of closed connections
	connectionClosed bool

	// the error message received for the closed connection
	connectionClosedError error

	remoteSki string

	// Goroutine lifecycle management
	pumpsWg sync.WaitGroup

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
	w.shipWriteChannel = make(chan []byte, DefaultWriteBufferSize) // Send outgoing ship messages
	w.writeDone = make(chan struct{})

	w.pumpsWg.Add(2)
	go w.readShipPump()
	go w.writeShipPump()
}

// writePump pumps messages from the SPINE and SHIP writeChannels to the websocket connection
func (w *WebsocketConnection) writeShipPump() {
	defer w.pumpsWg.Done()
	defer close(w.writeDone)
	defer func() {
		if r := recover(); r != nil {
			logging.Log().Debug(w.remoteSki, "panic in writeShipPump:", r)
			w.close()
		}
	}()
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case message, ok := <-w.shipWriteChannel:
			if w.isConnClosed() {
				return
			}

			if !ok {
				// The channel was closed with everything queued before that point already
				// drained above - write the close frame, if any, from here too, so it can
				// never overtake a still-queued SHIP message.
				logging.Log().Debug(w.remoteSki, "ship write channel closed")
				w.writeCloseFrame()
				return
			}

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

// writeCloseFrame writes the close frame requested by CloseDataConnection, if any. It runs on the
// write pump, the only writer, right after draining shipWriteChannel, so it can't overtake or be
// overtaken by a queued SHIP message.
func (w *WebsocketConnection) writeCloseFrame() {
	w.muxShipWrite.Lock()
	code, reason := w.closeCode, w.closeReason
	w.muxShipWrite.Unlock()

	if reason == "" {
		return
	}

	_ = w.writeMessage(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason))
}

func (w *WebsocketConnection) handlePing() {
	if w.isConnClosed() {
		return
	}

	_ = w.writeMessage(websocket.PingMessage, nil)
}

func (w *WebsocketConnection) closeWithError(err error, reason string) {
	logging.Log().Debug(w.remoteSki, reason, err)
	w.setConnClosedError(err)
	w.dataProcessing.ReportConnectionError(err)
}

// readShipPump checks for messages from the websocket connection
func (w *WebsocketConnection) readShipPump() {
	defer w.pumpsWg.Done()
	defer func() {
		if r := recover(); r != nil {
			logging.Log().Debug(w.remoteSki, "panic in readShipPump:", r)
			w.close()
		}
	}()
	_ = w.conn.SetReadDeadline(time.Now().Add(pongWait))
	w.conn.SetPongHandler(func(string) error { _ = w.conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })

	for {
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
		return nil, fmt.Errorf("%w for remote SKI %s", api.ErrConnectionNotInitialized, w.remoteSki)
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
		return fmt.Errorf("websocket message from %s is not binary (type: %d)", w.remoteSki, msgType)
	}

	if len(data) < 2 {
		return fmt.Errorf("websocket message from %s too short: %d bytes (minimum 2)", w.remoteSki, len(data))
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

		// First close the websocket connection to unblock any pending reads
		if w.conn != nil {
			_ = w.conn.Close()
		}

		// Then unblock the write pump too, in case it's not already stopping via CloseDataConnection
		w.stopShipWrites(0, "")

		// Wait for pumps to finish with a timeout
		done := make(chan struct{})
		go func() {
			w.pumpsWg.Wait()
			close(done)
		}()

		select {
		case <-done:
			// Pumps exited cleanly
		case <-time.After(500 * time.Millisecond):
			// Timeout waiting for pumps
			logging.Log().Debug(w.remoteSki, "timeout waiting for pump goroutines to exit")
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

	if w.closing || w.isConnClosed() || w.shipWriteChannel == nil {
		return fmt.Errorf("%w for remote SKI %s", api.ErrConnectionClosed, w.remoteSki)
	}

	select {
	case w.shipWriteChannel <- message:
	default:
		// too many messages are pending, this doesn't look good
		return fmt.Errorf("%w: websocket message for remote SKI %s (buffer size: %d)", 
			api.ErrBufferFull, w.remoteSki, DefaultWriteBufferSize)
	}

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
		return fmt.Errorf("%w for remote SKI %s", api.ErrConnectionClosed, w.remoteSki)
	}

	w.muxConWrite.Lock()
	defer w.muxConWrite.Unlock()

	// Every write needs its own deadline. A deadline stays armed on the socket after the
	// write it was set for, so a connection that has been idle for longer than writeWait
	// fails its next write with "i/o timeout" - and gorilla latches write errors, which
	// takes the connection down for good. Setting it here rather than at each call site
	// keeps that guarantee for pings and close frames too, not just SHIP messages.
	_ = w.conn.SetWriteDeadline(time.Now().Add(writeWait))

	return w.conn.WriteMessage(messageType, data)
}

// shutdown the connection and all internals
func (w *WebsocketConnection) CloseDataConnection(closeCode int, reason string) {
	// Callers queue a final message and close in the same breath - a protocol handshake abort, a
	// CMI rejection - and close() discards whatever the write pump has not written yet. Stopping
	// the write pump here, rather than letting close() do it, hands it the close frame to write
	// too, so both happen from the one goroutine that owns the socket, in queue order.
	w.stopShipWrites(closeCode, reason)

	select {
	case <-w.writeDone:
		// the write pump drained the queue and wrote the close frame, if any
	case <-time.After(flushTimeout):
		logging.Log().Debug(w.remoteSki, "timeout waiting for queued SHIP messages to flush before close")
	}

	w.close()
}

// return if the connection is closed
func (w *WebsocketConnection) IsDataConnectionClosed() (bool, error) {
	isClosed := w.isConnClosed()
	err := w.connClosedError()

	if isClosed && err == nil {
		err = fmt.Errorf("%w for remote SKI %s", api.ErrConnectionClosed, w.remoteSki)
	}

	return isClosed, err
}

// stopShipWrites stops accepting new SHIP messages and closes shipWriteChannel exactly once. That
// wakes writeShipPump's !ok branch, which drains whatever was queued before this point and, if
// reason is non-empty, writes it as the close frame - both before returning. A no-op if a close is
// already underway, so CloseDataConnection and close() can both call it safely.
func (w *WebsocketConnection) stopShipWrites(closeCode int, reason string) {
	w.muxShipWrite.Lock()
	defer w.muxShipWrite.Unlock()

	if w.closing || w.shipWriteChannel == nil {
		return
	}

	w.closing = true
	w.closeCode = closeCode
	w.closeReason = reason
	close(w.shipWriteChannel)
}
