package ship

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/mocks"
	"github.com/enbility/ship-go/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// accessMethodsTimeoutFixture wires a ShipConnection to mocks that record, in order, every frame
// written to the websocket and the websocket close, so a test can check how a connection ended.
type accessMethodsTimeoutFixture struct {
	t      *testing.T
	sut    *ShipConnection
	info   *mocks.ShipConnectionInfoProviderInterface
	ws     *mocks.WebsocketDataWriterInterface
	reader *mocks.ShipConnectionDataReaderInterface

	mux    sync.Mutex
	events []string // "write:<SHIP message type>" and "close:<code>", in order
	frames [][]byte
	states []model.ShipState
}

func newAccessMethodsTimeoutFixture(t *testing.T) *accessMethodsTimeoutFixture {
	f := &accessMethodsTimeoutFixture{t: t}

	f.ws = mocks.NewWebsocketDataWriterInterface(t)
	f.ws.EXPECT().InitDataProcessing(mock.Anything).Return().Maybe()
	f.ws.EXPECT().IsDataConnectionClosed().Return(false, nil).Maybe()
	f.ws.EXPECT().WriteMessageToWebsocketConnection(mock.Anything).
		RunAndReturn(func(msg []byte) error {
			f.mux.Lock()
			defer f.mux.Unlock()

			f.events = append(f.events, fmt.Sprintf("write:%d", msg[0]))
			f.frames = append(f.frames, append([]byte(nil), msg...))
			return nil
		}).Maybe()

	f.info = mocks.NewShipConnectionInfoProviderInterface(t)
	f.info.EXPECT().HandleShipHandshakeStateUpdate(mock.Anything, mock.Anything).
		Run(func(_ string, state model.ShipState) {
			f.mux.Lock()
			defer f.mux.Unlock()

			f.states = append(f.states, state)
		}).Return().Maybe()

	// the reader has no expectations: SPINE data reaching it fails the test
	f.reader = mocks.NewShipConnectionDataReaderInterface(t)
	f.info.EXPECT().SetupRemoteService(mock.Anything, mock.Anything).Return(f.reader).Once()

	f.sut = NewConnectionHandler(f.info, f.ws, ShipRoleClient, "LocalShipID", "RemoteSKI", "RemoteShipID")

	return f
}

func (f *accessMethodsTimeoutFixture) record(event string) {
	f.mux.Lock()
	defer f.mux.Unlock()

	f.events = append(f.events, event)
}

func (f *accessMethodsTimeoutFixture) eventsSnapshot() []string {
	f.mux.Lock()
	defer f.mux.Unlock()

	return append([]string(nil), f.events...)
}

func (f *accessMethodsTimeoutFixture) lastFrame() []byte {
	f.mux.Lock()
	defer f.mux.Unlock()

	if len(f.frames) == 0 {
		return nil
	}
	return f.frames[len(f.frames)-1]
}

// reported tells whether the info provider was told about the given state with the given error
func (f *accessMethodsTimeoutFixture) reported(state model.ShipMessageExchangeState, target error) bool {
	f.mux.Lock()
	defer f.mux.Unlock()

	for _, s := range f.states {
		if s.State == state && errors.Is(s.Error, target) {
			return true
		}
	}
	return false
}

// enterDataExchange drives the connection from successful PIN verification into connection data
// exchange, which sends our access methods request and arms the wait for the reply
func (f *accessMethodsTimeoutFixture) enterDataExchange() {
	f.sut.setState(model.SmePinStateCheckOk, nil)
	f.sut.handleState(false, nil)

	require.Equal(f.t, model.SmeAccessMethodsRequest, f.sut.getState())
	require.True(f.t, f.sut.getHandshakeTimerRunning(), "entering data exchange must arm the access methods timer")

	require.Equal(f.t, []string{fmt.Sprintf("write:%d", model.MsgTypeControl)}, f.eventsSnapshot())
	_, data := f.sut.parseMessage(f.lastFrame(), true)
	msgType, err := detectAccessMethodsMessageType(data)
	require.Nil(f.t, err)
	require.Equal(f.t, "request", msgType, "the only message sent so far must be our accessMethodsRequest")
}

// shortenAccessMethodsTimer re-arms the timer that entering data exchange armed with a shorter
// duration. CI runs without -tags=test, where that timer is the production 60s; the firing
// mechanism and its callback stay the production ones.
func (f *accessMethodsTimeoutFixture) shortenAccessMethodsTimer(d time.Duration) {
	f.sut.setHandshakeTimer(timeoutTimerTypeWaitForReady, d)
}

// SHIP 13.4.6.2.1: the recipient of our access methods request SHALL reply, and if no reply arrives
// in time we may close the connection. The connection is already in connection data exchange by
// then, so it must be terminated per SHIP 13.4.7 - the termination announced before the websocket
// is closed - and reported as a timeout of a connection that never completed.
func TestAccessMethodsTimeout_NoResponse_TerminatesConnection(t *testing.T) {
	assert.Equal(t, 60*time.Second, accessMethodsTimeout, "production wait for the access methods reply (SHIP 13.4.6.2.1)")

	f := newAccessMethodsTimeoutFixture(t)

	f.ws.EXPECT().CloseDataConnection(mock.Anything, mock.Anything).
		Run(func(code int, _ string) {
			f.record(fmt.Sprintf("close:%d", code))
		}).Return().Once()

	type closedReport struct {
		conn               api.ShipConnectionInterface
		handshakeCompleted bool
	}
	closed := make(chan closedReport, 1)
	f.info.EXPECT().HandleConnectionClosed(mock.Anything, mock.Anything).
		Run(func(conn api.ShipConnectionInterface, handshakeCompleted bool) {
			closed <- closedReport{conn, handshakeCompleted}
		}).Return().Once()

	f.enterDataExchange()

	// the remote never replies: let the armed timer fire
	f.shortenAccessMethodsTimer(50 * time.Millisecond)

	var report closedReport
	select {
	case report = <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("the connection was not closed after the access methods timeout")
	}
	require.Eventually(t, func() bool {
		return f.reported(model.SmeStateError, api.ErrConnectionTimeout)
	}, 2*time.Second, 5*time.Millisecond, "the timeout must be reported as the connection's error")

	// the state machine ended in error, with a timeout, and nothing is left running
	state, err := f.sut.ShipHandshakeState()
	assert.Equal(t, model.SmeStateError, state)
	assert.ErrorIs(t, err, api.ErrConnectionTimeout)
	assert.False(t, f.sut.getHandshakeTimerRunning())

	// SHIP 13.4.7: the termination is announced first, then the websocket is closed
	assert.Equal(t, []string{
		fmt.Sprintf("write:%d", model.MsgTypeControl), // our accessMethodsRequest
		fmt.Sprintf("write:%d", model.MsgTypeEnd),     // connectionClose announce
		"close:4001",
	}, f.eventsSnapshot())

	var closeMsg model.ConnectionClose
	assert.Nil(t, f.sut.processShipJsonMessage(f.lastFrame(), &closeMsg))
	assert.Equal(t, model.ConnectionClosePhaseTypeAnnounce, closeMsg.ConnectionClose.Phase)

	// the hub learns that this connection closed without ever completing
	assert.True(t, report.conn == api.ShipConnectionInterface(f.sut), "HandleConnectionClosed must report this connection")
	assert.False(t, report.handshakeCompleted)

	// A reply arriving after the timeout must not revive the connection, and SPINE data must no longer
	// be delivered. The reader and ReportServiceShipID have no expectations, so either call fails the
	// test, as does a second close via the Once() expectations above.
	f.sut.HandleIncomingWebsocketMessage(accessMethodsMessage("RemoteShipID"))
	f.sut.HandleIncomingWebsocketMessage(spineDataMessage(t, `{"datagram":{"cmd":"detailed-discovery"}}`))

	state, _ = f.sut.ShipHandshakeState()
	assert.Equal(t, model.SmeStateError, state)
	assert.Len(t, f.eventsSnapshot(), 3, "nothing may be sent after the termination")
}

// Control for the test above: a reply that arrives in time stops the timer, so the connection is
// neither terminated nor reported as closed when the timeout would have expired. CloseDataConnection
// and HandleConnectionClosed have no expectations here, so any call to them fails the test.
func TestAccessMethodsTimeout_ResponseInTime_KeepsConnection(t *testing.T) {
	f := newAccessMethodsTimeoutFixture(t)
	f.enterDataExchange()

	timeout := 250 * time.Millisecond
	f.shortenAccessMethodsTimer(timeout)

	f.sut.HandleIncomingWebsocketMessage(accessMethodsMessage("RemoteShipID"))
	require.Equal(t, model.SmeStateComplete, f.sut.getState())
	assert.False(t, f.sut.getHandshakeTimerRunning(), "the reply must stop the access methods timer")

	// Wait past the moment the timer would have fired. A plain sleep rather than assert.Never: Never
	// polls from a goroutine it does not wait for, which can still be touching the connection while the
	// mocks' cleanup formats their recorded arguments - and those include the connection itself.
	time.Sleep(2 * timeout)

	assert.Equal(t, model.SmeStateComplete, f.sut.getState(), "the connection must stay complete past the timeout")
	assert.Len(t, f.eventsSnapshot(), 1, "only our accessMethodsRequest may have been sent")
}
