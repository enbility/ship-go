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

	// The connection was closed
	closeChannel chan struct{}

	// The ship write channel for outgoing SHIP messages
	shipWriteChannel chan []byte

	// Flush requests: the write pump answers one once every SHIP message queued so far is written
	flushChannel chan chan struct{}

	// A close is flushing the queued SHIP messages, so no new ones are accepted. Guarded by muxShipWrite.
	closing bool

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
	w.closeChannel = make(chan struct{}, 1)                        // Listen to close events
	w.flushChannel = make(chan chan struct{})                      // Flush requests before a close

	w.pumpsWg.Add(2)
	go w.readShipPump()
	go w.writeShipPump()
}

// writePump pumps messages from the SPINE and SHIP writeChannels to the websocket connection
func (w *WebsocketConnection) writeShipPump() {
	defer w.pumpsWg.Done()
	defer func() {
		if r := recover(); r != nil {
			logging.Log().Debug(w.remoteSki, "panic in writeShipPump:", r)
			w.close()
		}
	}()
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

			if !w.writeMessage(websocket.BinaryMessage, message) {
				return
			}

			text := w.textFromMessage(message)
			logging.Log().Trace("Send:", w.remoteSki, text)

		case ack := <-w.flushChannel:
			// a close is waiting: write everything still queued before acknowledging
			w.drainShipWriteChannel()
			close(ack)

		case <-ticker.C:
			w.handlePing()
		}
	}
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

		// Then signal the pumps to stop
		close(w.closeChannel)

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
	// Deliver the SHIP messages queued before the close first. Callers queue a final message and
	// close in the same breath - a protocol handshake abort, a CMI rejection - and close()
	// discards whatever the write pump has not written yet. The close frame below is written
	// directly, so without the flush it would overtake those messages too.
	w.flushShipWrites(flushTimeout)

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
		err = fmt.Errorf("%w for remote SKI %s", api.ErrConnectionClosed, w.remoteSki)
	}

	return isClosed, err
}

func (w *WebsocketConnection) closeShipWriteChannel() {
	w.muxShipWrite.Lock()
	defer w.muxShipWrite.Unlock()
	close(w.shipWriteChannel)
}

// drainShipWriteChannel writes every SHIP message still queued. It runs on the write pump, the only
// writer, so once it returns no message is held between leaving the queue and reaching the socket.
func (w *WebsocketConnection) drainShipWriteChannel() {
	for {
		select {
		case message, ok := <-w.shipWriteChannel:
			if !ok || !w.writeMessage(websocket.BinaryMessage, message) {
				return
			}
		default:
			return
		}
	}
}

// flushShipWrites stops accepting SHIP messages and waits, at most timeout, until the write pump has
// written every message queued before that point. It returns immediately if the connection is
// already closed or was never started, e.g. after a read or write error.
func (w *WebsocketConnection) flushShipWrites(timeout time.Duration) {
	w.muxShipWrite.Lock()
	if w.closing || w.shipWriteChannel == nil || w.isConnClosed() {
		w.muxShipWrite.Unlock()
		return
	}
	w.closing = true
	w.muxShipWrite.Unlock()

	ack := make(chan struct{})
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	select {
	case w.flushChannel <- ack:
	case <-w.closeChannel:
		return
	case <-deadline.C:
		logging.Log().Debug(w.remoteSki, "timeout requesting a flush of queued SHIP messages")
		return
	}

	select {
	case <-ack:
	case <-w.closeChannel:
	case <-deadline.C:
		logging.Log().Debug(w.remoteSki, "timeout flushing queued SHIP messages before close")
	}
}
