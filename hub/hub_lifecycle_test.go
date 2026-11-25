package hub

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/enbility/ship-go/mocks"
	"github.com/enbility/ship-go/model"
	"github.com/enbility/ship-go/ship"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// Phase 1 (Red): These tests demonstrate two lifecycle bugs in Hub:
//
// Bug 1: Start() has no duplicate guard -- calling Start() twice launches
//         a second HTTP server goroutine instead of returning an error.
//
// Bug 2: Start() after Shutdown() panics -- the serverStarted channel
//         (created once in NewHub) is closed by the server goroutine on exit.
//         A second Start() call launches a new goroutine that calls close()
//         on the already-closed channel, causing a panic.
//         Additionally, Shutdown() never resets hasStarted or serverStarted.

// TestHub_Lifecycle_DoubleStart_ReturnsError verifies that calling Start()
// on an already-running Hub returns ErrHubAlreadyStarted.
func TestHub_Lifecycle_DoubleStart_ReturnsError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	hubReader := mocks.NewMockHubReaderInterface(ctrl)
	mdnsService := mocks.NewMockMdnsInterface(ctrl)
	mdnsService.EXPECT().Start(gomock.Any(), gomock.Any()).Return(nil).Times(1)

	certificate, _ := cert.CreateCertificate("unit", "org", "DE", "CN")
	localService, _ := api.NewServiceDetails("localSKI", "", "")

	hub, err := NewHub(hubReader, mdnsService, 0, certificate, localService, nil, nil)

	assert.NoError(t, err)

	// First start should succeed
	err = hub.Start()
	assert.NoError(t, err, "First Start() should succeed")
	assert.True(t, hub.hasStarted)

	// Second start on the same running Hub must return an error
	err = hub.Start()
	assert.Error(t, err, "Second Start() should return an error")
	assert.ErrorIs(t, err, api.ErrHubAlreadyStarted,
		"Second Start() should return ErrHubAlreadyStarted")

	// Cleanup
	mdnsService.EXPECT().Shutdown()
	hub.Shutdown()
}

// TestHub_Lifecycle_RestartAfterShutdown verifies that a Hub can be
// stopped and started again without panicking.
//
// Current behavior: panics with "close of closed channel" because
// serverStarted is created once in NewHub() and never reset.
func TestHub_Lifecycle_RestartAfterShutdown(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	hubReader := mocks.NewMockHubReaderInterface(ctrl)
	mdnsService := mocks.NewMockMdnsInterface(ctrl)
	mdnsService.EXPECT().Start(gomock.Any(), gomock.Any()).Return(nil).Times(2)
	mdnsService.EXPECT().Shutdown().Times(2)

	certificate, _ := cert.CreateCertificate("unit", "org", "DE", "CN")
	localService, _ := api.NewServiceDetails("localSKI", "", "")

	hub, err := NewHub(hubReader, mdnsService, 0, certificate, localService, nil, nil)

	assert.NoError(t, err)

	// First lifecycle: start then shutdown
	err = hub.Start()
	assert.NoError(t, err, "First Start() should succeed")
	assert.True(t, hub.hasStarted)

	hub.Shutdown()

	// Second lifecycle: start again -- must not panic
	assert.NotPanics(t, func() {
		err = hub.Start()
	}, "Start() after Shutdown() must not panic")
	assert.NoError(t, err, "Restart should succeed")
	assert.True(t, hub.hasStarted)

	// Clean up second lifecycle
	hub.Shutdown()
}

// TestHub_Lifecycle_ShutdownResetsState verifies that Shutdown() resets
// the hasStarted flag so the Hub can be restarted.
//
// Current behavior: hasStarted stays true forever after first Start().
func TestHub_Lifecycle_ShutdownResetsState(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	hubReader := mocks.NewMockHubReaderInterface(ctrl)
	mdnsService := mocks.NewMockMdnsInterface(ctrl)
	mdnsService.EXPECT().Start(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	mdnsService.EXPECT().Shutdown().Times(1)

	certificate, _ := cert.CreateCertificate("unit", "org", "DE", "CN")
	localService, _ := api.NewServiceDetails("localSKI", "", "")

	hub, err := NewHub(hubReader, mdnsService, 0, certificate, localService, nil, nil)

	assert.NoError(t, err)

	err = hub.Start()
	assert.NoError(t, err)
	assert.True(t, hub.hasStarted, "hasStarted should be true after Start()")

	hub.Shutdown()
	assert.False(t, hub.hasStarted, "hasStarted should be false after Shutdown()")
}

// TestHub_Lifecycle_RetryAfterMdnsFailure verifies that Start() can be
// retried on the same Hub instance after a partial failure (server started
// but mDNS failed).
//
// Current behavior: the serverStarted channel is closed by the first
// attempt's server goroutine. On retry, the select reads from the
// closed channel immediately (wrong), and the new server goroutine
// panics when it tries to close it again.
func TestHub_Lifecycle_RetryAfterMdnsFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	hubReader := mocks.NewMockHubReaderInterface(ctrl)
	mdnsService := mocks.NewMockMdnsInterface(ctrl)

	// First call fails, second succeeds
	gomock.InOrder(
		mdnsService.EXPECT().Start(gomock.Any(), gomock.Any()).Return(errors.New("mDNS failed")),
		mdnsService.EXPECT().Start(gomock.Any(), gomock.Any()).Return(nil),
	)

	certificate, _ := cert.CreateCertificate("unit", "org", "DE", "CN")
	localService, _ := api.NewServiceDetails("localSKI", "", "")

	hub, err := NewHub(hubReader, mdnsService, 0, certificate, localService, nil, nil)

	assert.NoError(t, err)

	// First start fails on mDNS (server starts then gets shut down)
	err = hub.Start()
	assert.Error(t, err, "First Start() should fail due to mDNS")
	assert.False(t, hub.hasStarted, "Hub should not be marked as started")

	// Wait for server goroutine to exit and close the channel
	time.Sleep(200 * time.Millisecond)

	// Retry should succeed without panic
	assert.NotPanics(t, func() {
		err = hub.Start()
	}, "Retry after mDNS failure must not panic")
	assert.NoError(t, err, "Retry should succeed")
	assert.True(t, hub.hasStarted)

	// Cleanup
	mdnsService.EXPECT().Shutdown()
	hub.Shutdown()
}

// Shutdown closes the connections registered at that moment. An outgoing dial that was
// already in flight must not register a connection that outlives it.
func TestHub_Lifecycle_ShutdownDuringInflightDial(t *testing.T) {
	log := newDcEventLog()
	dutCert, toolCert := certsBySkiOrder(t)

	tool := newTestTool(t, log, toolCert, policyPassive)
	dut := newDUT(t, dutCert)
	peer := dut.trust(t, tool.ski)

	release := tool.holdInbound()
	defer release()

	host, port := tool.address()
	dialDone := dut.dial(peer, host, port)
	log.waitFor(t, evATLSDone, 5*time.Second)

	dut.Shutdown()

	release()
	select {
	case <-dialDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("dial did not return\n%s", log.dump())
	}

	assert.Equal(t, 0, dut.connectionCount(), "no connection must be registered after Shutdown")
	log.waitFor(t, evAClosed, 5*time.Second)
}

// A delay timer that had already fired when Shutdown stopped the timers must not dial.
func TestHub_Lifecycle_TimerCallbackAfterShutdown(t *testing.T) {
	log := newDcEventLog()
	dutCert, toolCert := certsBySkiOrder(t)

	tool := newTestTool(t, log, toolCert, policyPassive)
	dut := newDUT(t, dutCert)
	dut.trust(t, tool.ski)

	counter := dut.increaseConnectionAttemptCounter(tool.ski)
	dut.setConnectionAttemptRunning(tool.ski, true)

	dut.Shutdown()

	host, _ := tool.address()
	dut.prepareConnectionInitation(tool.ski, counter, &api.MdnsEntry{Host: host, Port: tool.port, Path: "/ship/"})

	assert.False(t, log.has(evATLSDone), "no connection attempt after Shutdown\n%s", log.dump())
	assert.False(t, dut.isConnectionAttemptRunning(tool.ski), "attempt flag must be cleared")
}

// A delay timer callback that fired while Shutdown is still stopping the timers must not dial.
func TestHub_Lifecycle_TimerCallbackDuringShutdown(t *testing.T) {
	log := newDcEventLog()
	dutCert, toolCert := certsBySkiOrder(t)

	tool := newTestTool(t, log, toolCert, policyPassive)
	dut := newDUT(t, dutCert)
	dut.trust(t, tool.ski)

	counter := dut.increaseConnectionAttemptCounter(tool.ski)

	// hold Shutdown at the point where it stops the timers
	dut.muxTimers.Lock()
	done := make(chan struct{})
	go func() {
		dut.Shutdown()
		close(done)
	}()

	shuttingDown := func() bool {
		dut.muxCon.RLock()
		defer dut.muxCon.RUnlock()
		return dut.shutDown
	}
	if !assert.Eventually(t, shuttingDown, 5*time.Second, 10*time.Millisecond, "Shutdown must refuse connection attempts before it stops the timers") {
		dut.muxTimers.Unlock()
		<-done
		return
	}

	host, _ := tool.address()
	dut.prepareConnectionInitation(tool.ski, counter, &api.MdnsEntry{Host: host, Port: tool.port, Path: "/ship/"})

	dut.muxTimers.Unlock()
	<-done

	assert.False(t, log.has(evATLSDone), "no connection attempt during Shutdown\n%s", log.dump())
}

// After Shutdown and Start, outgoing connection attempts must work again.
func TestHub_Lifecycle_OutgoingConnectionAfterRestart(t *testing.T) {
	log := newDcEventLog()
	dutCert, toolCert := certsBySkiOrder(t)

	tool := newTestTool(t, log, toolCert, policyPassive)
	dut := newDUT(t, dutCert)
	dut.trust(t, tool.ski)

	host, _ := tool.address()
	entry := &api.MdnsEntry{Host: host, Port: tool.port, Path: "/ship/"}

	// last delay range, so the timer is still pending at Shutdown
	dut.increaseConnectionAttemptCounter(tool.ski)
	dut.increaseConnectionAttemptCounter(tool.ski)
	dut.coordinateConnectionInitations(tool.ski, entry)
	require.True(t, dut.isConnectionAttemptRunning(tool.ski))

	dut.Shutdown()
	require.NoError(t, dut.Start())

	assert.False(t, dut.isConnectionAttemptRunning(tool.ski), "attempt flag must not be stuck after restart")

	dut.coordinateConnectionInitations(tool.ski, entry)
	dut.muxTimers.Lock()
	_, armed := dut.connectionDelayTimers[tool.ski]
	dut.muxTimers.Unlock()
	assert.True(t, armed, "a new connection attempt must be scheduled after restart")

	counter, _ := dut.getCurrentConnectionAttemptCounter(tool.ski)
	dut.prepareConnectionInitation(tool.ski, counter, entry)

	_, connected := dut.registeredConnection(tool.ski)
	assert.True(t, connected, "outgoing connection must register after restart\n%s", log.dump())
}

// The map entry of a delay timer whose callback has run must be removed.
func TestHub_FiredConnectionDelayTimerIsRemoved(t *testing.T) {
	dutCert, toolCert := certsBySkiOrder(t)
	dut := newDUT(t, dutCert)
	ski := skiOfCertificate(t, toolCert)
	dut.trust(t, ski)

	// no address: the attempt fails right away
	dut.coordinateConnectionInitations(ski, &api.MdnsEntry{})

	assert.Eventually(t, func() bool {
		dut.muxTimers.Lock()
		defer dut.muxTimers.Unlock()
		_, exists := dut.connectionDelayTimers[ski]
		return !exists
	}, 5*time.Second, 5*time.Millisecond, "fired timer must be removed")
}

// closeSignalConn reports when the websocket closes its socket, which happens after it marked itself closed.
type closeSignalConn struct {
	net.Conn
	once   sync.Once
	closed chan struct{}
}

func (c *closeSignalConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

// A connection that closes before it is registered must not stay registered, and must
// not displace a live connection for the same SKI.
func TestHub_StartShipConnection_ClosedBeforeRegistration(t *testing.T) {
	for _, withLive := range []bool{false, true} {
		t.Run(fmt.Sprintf("live=%v", withLive), func(t *testing.T) {
			dutCert, toolCert := certsBySkiOrder(t)
			dut := newDUT(t, dutCert)
			ski := skiOfCertificate(t, toolCert)
			peer := dut.trust(t, ski)

			var live *mocks.ShipConnectionInterface
			if withLive {
				live = mocks.NewShipConnectionInterface(t)
				live.EXPECT().ShipHandshakeState().Return(model.SmeStateComplete, nil).Maybe()
				live.EXPECT().CloseConnection(mock.Anything, mock.Anything, mock.Anything).Maybe()
				dut.muxCon.Lock()
				dut.connections[ski] = live
				dut.muxCon.Unlock()
			}

			// the peer drops the connection right after the upgrade
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil); err == nil {
					_ = c.Close()
				}
			}))
			defer server.Close()

			socket := &closeSignalConn{closed: make(chan struct{})}
			dialer := websocket.Dialer{NetDial: func(network, addr string) (net.Conn, error) {
				c, err := net.Dial(network, addr)
				if err != nil {
					return nil, err
				}
				socket.Conn = c
				return socket, nil
			}}
			conn, _, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
			require.NoError(t, err)

			// Holding muxReg stops Run before its first write, until the socket is closed.
			// Run then fails synchronously and the close is handled before registration.
			dut.muxReg.Lock()
			done := make(chan struct{})
			go func() {
				dut.startShipConnection(conn, peer, ship.ShipRoleClient)
				close(done)
			}()
			closed := false
			select {
			case <-socket.closed:
				closed = true
			case <-time.After(5 * time.Second):
			}
			dut.muxReg.Unlock()
			require.True(t, closed, "socket was not closed")
			<-done

			registered, ok := dut.registeredConnection(ski)
			if withLive {
				assert.True(t, ok && registered == live, "live connection must stay registered")
				assert.Equal(t, 0, dut.disconnect.count(ski), "peer is still connected")
			} else {
				assert.False(t, ok, "closed connection must not be registered")
				assert.Equal(t, 1, dut.disconnect.count(ski), "disconnect reported once")
			}
		})
	}
}
