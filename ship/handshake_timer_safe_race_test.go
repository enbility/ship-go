package ship

import (
	"sync"
	"testing"
	"time"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/mocks"
	"github.com/enbility/ship-go/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// safeInfoProvider wraps the mock to avoid race conditions when formatting
type safeInfoProvider struct {
	mock *mocks.ShipConnectionInfoProviderInterface
}

func (s *safeInfoProvider) IsRemoteServiceForSKIPaired(ski string) bool {
	return s.mock.IsRemoteServiceForSKIPaired(ski)
}

func (s *safeInfoProvider) IsAutoAcceptEnabled() bool {
	return s.mock.IsAutoAcceptEnabled()
}

func (s *safeInfoProvider) HandleConnectionClosed(_ api.ShipConnectionInterface, handshakeCompleted bool) {
	// Don't pass the connection to avoid formatting races
	s.mock.HandleConnectionClosed(nil, handshakeCompleted)
}

func (s *safeInfoProvider) ReportServiceShipID(ski string, shipID string) {
	s.mock.ReportServiceShipID(ski, shipID)
}

func (s *safeInfoProvider) AllowWaitingForTrust(ski string) bool {
	return s.mock.AllowWaitingForTrust(ski)
}

func (s *safeInfoProvider) HandleShipHandshakeStateUpdate(ski string, state model.ShipState) {
	s.mock.HandleShipHandshakeStateUpdate(ski, state)
}

func (s *safeInfoProvider) SetupRemoteService(ski string, writeI api.ShipConnectionDataWriterInterface) api.ShipConnectionDataReaderInterface {
	return s.mock.SetupRemoteService(ski, writeI)
}

// TestHandshakeTimerSafeRace tests timer operations without mock formatting races
func TestHandshakeTimerSafeRace(t *testing.T) {
	t.Run("concurrent_timer_operations_safe", func(t *testing.T) {
		// Create mocks
		mockInfo := mocks.NewShipConnectionInfoProviderInterface(t)
		mockData := mocks.NewWebsocketDataWriterInterface(t)

		// Wrap to avoid races
		safeInfo := &safeInfoProvider{mock: mockInfo}

		// Setup expectations
		mockInfo.EXPECT().HandleShipHandshakeStateUpdate(mock.Anything, mock.Anything).Maybe()
		mockInfo.EXPECT().HandleConnectionClosed(mock.Anything, mock.Anything).Maybe()
		mockInfo.EXPECT().IsRemoteServiceForSKIPaired(mock.Anything).Return(false).Maybe()
		mockInfo.EXPECT().IsAutoAcceptEnabled().Return(false).Maybe()

		mockData.EXPECT().InitDataProcessing(mock.Anything).Maybe()
		mockData.EXPECT().WriteMessageToWebsocketConnection(mock.Anything).Return(nil).Maybe()
		mockData.EXPECT().CloseDataConnection(mock.Anything, mock.Anything).Maybe()
		mockData.EXPECT().IsDataConnectionClosed().Return(false, nil).Maybe()

		// Create connection with safe wrapper
		conn := NewConnectionHandler(
			safeInfo,
			mockData,
			ShipRoleClient,
			"local-id",
			"remote-ski",
			"remote-id",
		)

		// Run concurrent operations
		var wg sync.WaitGroup

		// Set timers concurrently
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				duration := time.Duration(10+idx) * time.Millisecond
				conn.setHandshakeTimer(timeoutTimerTypeWaitForReady, duration)
			}(i)
		}

		// Stop timers concurrently
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				done := conn.stopHandshakeTimer()
				select {
				case <-done:
				case <-time.After(50 * time.Millisecond):
				}
			}()
		}

		// Change states concurrently
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				conn.setState(model.SmeHelloStateReadyInit, nil)
			}()
		}

		wg.Wait()

		// Cleanup
		conn.stopHandshakeTimer()
	})

	t.Run("timer_internal_race_check", func(t *testing.T) {
		// Direct test of timer operations without mocks
		conn := &ShipConnection{
			handshakeTimerMux: sync.Mutex{},
			mux:               sync.Mutex{},
			remoteSKI:         "test-ski",
		}

		var wg sync.WaitGroup

		// Concurrent timer sets
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				conn.setHandshakeTimer(timeoutTimerTypeWaitForReady, time.Duration(idx)*time.Millisecond)
			}(i)
		}

		// Concurrent timer stops
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				done := conn.stopHandshakeTimer()
				select {
				case <-done:
				case <-time.After(10 * time.Millisecond):
				}
			}()
		}

		wg.Wait()

		// Final cleanup
		conn.stopHandshakeTimer()

		// Verify clean state
		assert.False(t, conn.getHandshakeTimerRunning())
	})
}
