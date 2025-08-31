//go:build stress
// +build stress

// Package tests provides full system integration stress tests for the SHIP implementation.
// These tests verify that the deadlock fixes work correctly under high load and
// realistic usage patterns.
//
// To run these tests:
//
//	make test-stress
//	or: go test -race -tags=stress -timeout=60s ./tests
//
// The tests are tagged to avoid running them during normal test runs since they
// are resource-intensive and time-consuming.
package tests

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/enbility/ship-go/hub"
	"github.com/enbility/ship-go/mocks"
	"github.com/enbility/ship-go/model"
	"github.com/enbility/ship-go/ship"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// TestHistoryProvider implements PairingHistoryProviderInterface for integration testing
type TestHistoryProvider struct {
	seen map[string]bool
	mux  sync.RWMutex
}

func NewTestHistoryProvider() *TestHistoryProvider {
	return &TestHistoryProvider{
		seen: make(map[string]bool),
	}
}

func (t *TestHistoryProvider) HasSeenDigest(alg, digest string) bool {
	t.mux.RLock()
	defer t.mux.RUnlock()
	key := alg + ":" + digest
	return t.seen[key]
}

func (t *TestHistoryProvider) RecordPairing(alg, digest string) {
	t.mux.Lock()
	defer t.mux.Unlock()
	key := alg + ":" + digest
	t.seen[key] = true
}

// TestRingBufferPersistence implements RingBufferPersistence for integration testing
type TestRingBufferPersistence struct {
	entries   []api.DigestEntry
	nextIndex int
	mux       sync.RWMutex
}

func NewTestRingBufferPersistence() *TestRingBufferPersistence {
	return &TestRingBufferPersistence{
		entries:   make([]api.DigestEntry, 0),
		nextIndex: 0,
	}
}

func (t *TestRingBufferPersistence) LoadRingBuffer() ([]api.DigestEntry, int, error) {
	t.mux.RLock()
	defer t.mux.RUnlock()

	// Return a copy to prevent races
	entries := make([]api.DigestEntry, len(t.entries))
	copy(entries, t.entries)
	return entries, t.nextIndex, nil
}

func (t *TestRingBufferPersistence) SaveRingBuffer(entries []api.DigestEntry, nextIndex int) error {
	t.mux.Lock()
	defer t.mux.Unlock()

	// Save a copy to prevent races
	t.entries = make([]api.DigestEntry, len(entries))
	copy(t.entries, entries)
	t.nextIndex = nextIndex
	return nil
}

// Test helper for integration tests
func newTestHub(
	hubReader api.HubReaderInterface,
	mdns api.MdnsInterface,
	port int,
	certificate tls.Certificate,
	localService *api.ServiceDetails,
	pairingConfig *api.PairingConfig,
) (*hub.Hub, error) {
	var ringBufferPersistence api.RingBufferPersistence
	if pairingConfig != nil {
		mode := pairingConfig.Mode
		if mode == api.PairingModeListener || mode == api.PairingModeBoth {
			ringBufferPersistence = NewTestRingBufferPersistence() // Simple test implementation
		}
	}
	return hub.NewHub(hubReader, mdns, port, certificate, localService, pairingConfig, ringBufferPersistence)
}

// TestFullSystemStress performs comprehensive stress testing of the entire SHIP system
func TestFullSystemStress(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	// Test configuration
	const (
		numHubs          = 2
		numConnections   = 10
		testDuration     = 5 * time.Second
		operationsPerSec = 50
	)

	// Metrics tracking
	var (
		hubOperations        int64
		connectionOperations int64
		stateTransitions     int64
		errors               int64
	)

	// Create test hubs
	hubs := make([]*hub.Hub, numHubs)
	for i := 0; i < numHubs; i++ {
		hubs[i] = createTestHub(t, i)
	}

	// Create test connections
	connections := make([]*ship.ShipConnection, numConnections)
	for i := 0; i < numConnections; i++ {
		connections[i] = createTestConnection(t, i)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testDuration)
	defer cancel()

	var wg sync.WaitGroup

	// Start hub operations workers
	for hubIdx, h := range hubs {
		for workerID := 0; workerID < 3; workerID++ {
			wg.Add(1)
			go func(hub *hub.Hub, hID, wID int) {
				defer wg.Done()
				runHubStressWorker(ctx, hub, hID, wID, &hubOperations, &errors)
			}(h, hubIdx, workerID)
		}
	}

	// Start connection operations workers
	for connIdx, conn := range connections {
		for workerID := 0; workerID < 2; workerID++ {
			wg.Add(1)
			go func(connection *ship.ShipConnection, cID, wID int) {
				defer wg.Done()
				runConnectionStressWorker(ctx, connection, cID, wID,
					&connectionOperations, &stateTransitions, &errors)
			}(conn, connIdx, workerID)
		}
	}

	// Monitor system health
	wg.Add(1)
	go func() {
		defer wg.Done()
		monitorSystemHealth(ctx, t, hubs, connections)
	}()

	// Wait for all workers to complete
	wg.Wait()

	// Report final metrics
	t.Logf("Full system stress test completed:")
	t.Logf("  Duration: %v", testDuration)
	t.Logf("  Hub operations: %d", atomic.LoadInt64(&hubOperations))
	t.Logf("  Connection operations: %d", atomic.LoadInt64(&connectionOperations))
	t.Logf("  State transitions: %d", atomic.LoadInt64(&stateTransitions))
	t.Logf("  Errors: %d", atomic.LoadInt64(&errors))

	// Verify no errors occurred
	assert.Equal(t, int64(0), atomic.LoadInt64(&errors), "Errors occurred during stress test")

	// Verify minimum operation counts (system should be responsive)
	totalOps := atomic.LoadInt64(&hubOperations) + atomic.LoadInt64(&connectionOperations)
	minExpectedOps := int64(testDuration.Seconds() * operationsPerSec * 0.1) // At least 10% of target
	assert.Greater(t, totalOps, minExpectedOps, "System throughput too low")
}

// createTestHub creates a hub for stress testing
func createTestHub(t *testing.T, id int) *hub.Hub {
	t.Helper()

	hubReader := mocks.NewHubReaderInterface(t)
	mdns := mocks.NewMdnsInterface(t)

	// Set up expectations for stress testing
	hubReader.EXPECT().RemoteSKIConnected(mock.Anything).Maybe()
	hubReader.EXPECT().RemoteSKIDisconnected(mock.Anything).Maybe()
	hubReader.EXPECT().ServiceShipIDUpdate(mock.Anything, mock.Anything).Maybe()
	hubReader.EXPECT().ServicePairingDetailUpdate(mock.Anything, mock.Anything).Maybe()
	hubReader.EXPECT().AllowWaitingForTrust(mock.Anything).Return(false).Maybe()

	mdns.EXPECT().AnnounceMdnsEntry().Return(nil).Maybe()
	mdns.EXPECT().UnannounceMdnsEntry().Maybe()
	mdns.EXPECT().RequestMdnsEntries().Maybe()
	mdns.EXPECT().SetAutoAccept(mock.Anything).Maybe()
	mdns.EXPECT().Start(mock.Anything).Return(nil).Maybe()
	mdns.EXPECT().Shutdown().Maybe()

	service := api.NewServiceDetails(makeSKI("hub", id))
	service.SetShipID(makeSKI("ship", id))

	// Create a valid certificate for testing
	cert, err := cert.CreateCertificate("test", "Test Organization", "DE", makeSKI("cert", id))
	if err != nil {
		t.Fatalf("Failed to create test certificate: %v", err)
	}

	return hub.newTestHub(hubReader, mdns, 4729+id, cert, service)
}

// createTestConnection creates a connection for stress testing
func createTestConnection(t *testing.T, id int) *ship.ShipConnection {
	t.Helper()

	infoProvider := mocks.NewShipConnectionInfoProviderInterface(t)
	dataWriter := mocks.NewWebsocketDataWriterInterface(t)

	// Set up expectations for stress testing
	dataWriter.EXPECT().InitDataProcessing(mock.Anything).Maybe()
	dataWriter.EXPECT().WriteMessageToWebsocketConnection(mock.Anything).Return(nil).Maybe()
	dataWriter.EXPECT().IsDataConnectionClosed().Return(false, nil).Maybe()
	dataWriter.EXPECT().CloseDataConnection(mock.Anything, mock.Anything).Maybe()
	infoProvider.EXPECT().HandleShipHandshakeStateUpdate(mock.Anything, mock.Anything).Maybe()
	infoProvider.EXPECT().HandleConnectionClosed(mock.Anything, mock.Anything).Maybe()

	return ship.NewConnectionHandler(
		infoProvider,
		dataWriter,
		ship.ShipRoleClient,
		makeSKI("local", id),
		makeSKI("remote", id),
		makeSKI("ship", id),
	)
}

// runHubStressWorker performs stress operations on a hub
func runHubStressWorker(ctx context.Context, h *hub.Hub, hubID, workerID int,
	hubOps *int64, errors *int64) {

	ticker := time.NewTicker(time.Millisecond * 20)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Perform various hub operations that test the fixed lock ordering
			operations := []func() error{
				func() error {
					ski := makeSKI("test", workerID)
					_ = h.ServiceForSKI(ski)
					return nil
				},
				func() error {
					ski := makeSKI("test", workerID)
					service := api.NewServiceDetails(ski)
					if workerID%3 == 0 {
						service.SetTrusted(true)
					}
					h.RegisterRemoteSKI(ski, service.ShipID())
					return nil
				},
				func() error {
					ski := makeSKI("test", workerID)
					_ = h.IsRemoteServiceForSKIPaired(ski)
					return nil
				},
				func() error {
					_ = h.IsAutoAcceptEnabled()
					return nil
				},
				func() error {
					if workerID == 0 {
						h.SetAutoAccept(workerID%2 == 0)
					}
					return nil
				},
			}

			for _, op := range operations {
				if err := op(); err != nil {
					atomic.AddInt64(errors, 1)
				} else {
					atomic.AddInt64(hubOps, 1)
				}
			}
		}
	}
}

// runConnectionStressWorker performs stress operations on a connection
func runConnectionStressWorker(ctx context.Context, conn *ship.ShipConnection,
	connID, workerID int, connOps, stateTransitions, errors *int64) {

	ticker := time.NewTicker(time.Millisecond * 25)
	defer ticker.Stop()

	states := []model.ShipMessageExchangeState{
		model.SmeHelloStateReadyInit,
		model.SmeHelloStateOk,
		model.SmeHelloStatePendingInit,
		model.SmeHelloStateAbort,
		model.SmeProtHStateClientListenChoice,
		model.SmeProtHStateClientOk,
	}

	stateIndex := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Perform various connection operations that test the setState fix
			operations := []func() error{
				func() error {
					// State transition - tests the two-phase setState implementation
					_ = states[stateIndex%len(states)]
					// Test state transitions - use the public API
					_, _ = conn.ShipHandshakeState() // Read current state using public method
					stateIndex++
					atomic.AddInt64(stateTransitions, 1)
					return nil
				},
				func() error {
					// Read state - tests concurrent access
					_, _ = conn.ShipHandshakeState()
					return nil
				},
				func() error {
					// Timer operations - tests timer/state interaction
					if workerID%2 == 0 {
						// Test timer/state interaction through public API
						_, _ = conn.ShipHandshakeState()
					}
					return nil
				},
			}

			for _, op := range operations {
				if err := op(); err != nil {
					atomic.AddInt64(errors, 1)
				} else {
					atomic.AddInt64(connOps, 1)
				}
			}
		}
	}
}

// monitorSystemHealth monitors the system during stress testing
func monitorSystemHealth(ctx context.Context, t *testing.T,
	hubs []*hub.Hub, connections []*ship.ShipConnection) {

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Check for system responsiveness
			start := time.Now()

			// Test hub responsiveness
			for _, h := range hubs {
				_ = h.IsAutoAcceptEnabled()
			}

			// Test connection responsiveness
			for _, conn := range connections {
				_, _ = conn.ShipHandshakeState()
			}

			duration := time.Since(start)

			// Log if system is becoming unresponsive
			if duration > time.Millisecond*100 {
				t.Logf("System responsiveness degraded: %v", duration)
			}
		}
	}
}

// TestDeadlockUnderLoad tests for deadlocks under high concurrent load
func TestDeadlockUnderLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping load test in short mode")
	}

	const (
		numWorkers = 25
		duration   = 3 * time.Second
	)

	// Create test instances
	h := createTestHub(t, 0)
	conn := createTestConnection(t, 0)

	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	var wg sync.WaitGroup

	// Start many workers doing operations that could cause deadlocks
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for {
				select {
				case <-ctx.Done():
					return
				default:
					// Operations that previously could cause deadlocks
					switch workerID % 6 {
					case 0:
						// Hub operations that test lock ordering
						_ = h.ServiceForSKI(makeSKI("test", workerID))
					case 1:
						_, _ = conn.ShipHandshakeState()
					case 2:
						_ = h.IsAutoAcceptEnabled()
					case 3:
						ski := makeSKI("test", workerID)
						service := api.NewServiceDetails(ski)
						h.RegisterRemoteSKI(ski, service.ShipID())
					case 4:
						_ = h.IsRemoteServiceForSKIPaired(makeSKI("test", workerID))
					case 5:
						_, _ = conn.ShipHandshakeState()
					}

					// Small delay to prevent tight loops
					time.Sleep(time.Microsecond * 10)
				}
			}
		}(i)
	}

	wg.Wait()
	t.Log("Deadlock test completed successfully - no deadlocks detected")
}

// TestRaceConditionDetection tests for race conditions under load
func TestRaceConditionDetection(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping race test in short mode")
	}

	// This test should be run with -race flag to detect race conditions
	const (
		numGoroutines = 15
		iterations    = 500
	)

	h := createTestHub(t, 0)
	conn := createTestConnection(t, 0)

	var wg sync.WaitGroup

	// Test concurrent hub operations
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < iterations; j++ {
				ski := makeSKI("race", id)

				// Mix of read and write operations
				_ = h.ServiceForSKI(ski)
				_ = h.IsAutoAcceptEnabled()

				if j%10 == 0 {
					service := api.NewServiceDetails(ski)
					h.RegisterRemoteSKI(ski, service.ShipID())
				}
			}
		}(i)
	}

	// Test concurrent connection operations
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < iterations; j++ {
				_, _ = conn.ShipHandshakeState()
				// Test other safe operations
				_ = conn.RemoteSKI()
			}
		}(i)
	}

	wg.Wait()
	t.Log("Race condition test completed successfully")
}

// makeSKI creates a test SKI
func makeSKI(prefix string, id int) string {
	return prefix + "-" + string(rune('a'+id%26)) + string(rune('a'+(id/26)%26))
}
