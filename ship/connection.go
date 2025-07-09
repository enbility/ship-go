package ship

import (
	"sync"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/model"
)

// A ShipConnection handles the data connection and coordinates SHIP and SPINE messages i/o
type ShipConnection struct {
	// The ship connection mode of this connection
	role shipRole

	// The remote SKI
	remoteSKI string

	// the remote SHIP Id
	remoteShipID string

	// The local SHIP ID
	localShipID string

	// data provider
	infoProvider api.ShipConnectionInfoProviderInterface

	// Where to pass incoming SPINE messages to
	dataReader api.ShipConnectionDataReaderInterface

	// the (web socket) handler for sending messages
	dataWriter api.WebsocketDataWriterInterface

	// The current SHIP state
	smeState model.ShipMessageExchangeState

	// the current error value if SHIP state is in error
	smeError error

	// handles timeouts for the various states
	//
	// WaitForReady SHIP 13.4.4.1.3: The communication partner must send its "READY" state (or request for prolongation") before the timer expires.
	//
	// SendProlongationRequest SHIP 13.4.4.1.3: Local timer to request for prolongation at the communication partner in time (i.e. before the communication partner's Wait-For-Ready-Timer expires).
	//
	// ProlongationRequestReply SHIP 13.4.4.1.3: Detection of response timeout on prolongation request.
	handshakeTimer        *time.Timer
	handshakeTimerType    timeoutTimerType
	handshakeTimerMux     sync.Mutex
	handshakeTimerDone    chan struct{} // Signals when timer goroutine has completed
	handshakeTimerRunning bool          // For test assertions only

	lastReceivedWaitingValue time.Duration // required for Prolong-Request-Reply-Timer

	shutdownOnce sync.Once

	// buffer for SPINE messages that came in before the handshake was completed
	spineBuffer [][]byte

	mux       sync.Mutex
	bufferMux sync.Mutex
}

var _ api.ShipConnectionInterface = (*ShipConnection)(nil)