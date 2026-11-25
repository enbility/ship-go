package hub

import (
	"errors"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/enbility/ship-go/mocks"
	"github.com/stretchr/testify/assert"
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
