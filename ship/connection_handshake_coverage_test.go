package ship

import (
	"errors"
	"testing"
	"time"

	"github.com/enbility/ship-go/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

// ConnectionHandshakeCoverageSuite tests additional coverage for connection handshake
type ConnectionHandshakeCoverageSuite struct {
	ConnectionSuite
}

func TestConnectionHandshakeCoverageSuite(t *testing.T) {
	suite.Run(t, new(ConnectionHandshakeCoverageSuite))
}

// Test_ValidateConnectionBeforeApproval_AllScenarios tests all validation scenarios
func (s *ConnectionHandshakeCoverageSuite) Test_ValidateConnectionBeforeApproval_AllScenarios() {
	tests := []struct {
		name           string
		setupMocks     func()
		expectedResult bool
	}{
		{
			name: "websocket_closed_with_error",
			setupMocks: func() {
				s.wsDataWriter.EXPECT().
					IsDataConnectionClosed().
					Return(true, errors.New("connection reset")).Once()
			},
			expectedResult: false,
		},
		{
			name: "websocket_closed_without_error",
			setupMocks: func() {
				s.wsDataWriter.EXPECT().
					IsDataConnectionClosed().
					Return(true, nil).Once()
			},
			expectedResult: false,
		},
		{
			name: "websocket_open_but_not_paired",
			setupMocks: func() {
				s.wsDataWriter.EXPECT().
					IsDataConnectionClosed().
					Return(false, nil).Once()
				s.infoProvider.EXPECT().
					IsRemoteServiceForSKIPaired(s.sut.remoteSKI).
					Return(false).Once()
			},
			expectedResult: false,
		},
		{
			name: "websocket_open_and_paired",
			setupMocks: func() {
				s.wsDataWriter.EXPECT().
					IsDataConnectionClosed().
					Return(false, nil).Once()
				s.infoProvider.EXPECT().
					IsRemoteServiceForSKIPaired(s.sut.remoteSKI).
					Return(true).Once()
			},
			expectedResult: true,
		},
		{
			name: "websocket_check_returns_error",
			setupMocks: func() {
				s.wsDataWriter.EXPECT().
					IsDataConnectionClosed().
					Return(false, errors.New("check failed")).Once()
				s.infoProvider.EXPECT().
					IsRemoteServiceForSKIPaired(s.sut.remoteSKI).
					Return(true).Once()
			},
			expectedResult: true,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			tt.setupMocks()
			result := s.sut.validateConnectionBeforeApproval()
			assert.Equal(s.T(), tt.expectedResult, result)
		})
	}
}

// Test_ApprovePendingHandshake_StateTransitions tests state transitions during approval
func (s *ConnectionHandshakeCoverageSuite) Test_ApprovePendingHandshake_StateTransitions() {
	tests := []struct {
		name              string
		initialState      model.ShipMessageExchangeState
		setupMocks        func()
		expectedFinalState model.ShipMessageExchangeState
		waitForAsync      bool
	}{
		{
			name:              "not_in_pending_state",
			initialState:      model.SmeHelloStateOk,
			setupMocks:        func() {},
			expectedFinalState: model.SmeHelloStateOk,
			waitForAsync:      false,
		},
		{
			name:              "pending_state_validation_fails",
			initialState:      model.SmeHelloStatePendingListen,
			setupMocks: func() {
				// validateConnectionBeforeApproval will be called and return false
				s.wsDataWriter.EXPECT().
					IsDataConnectionClosed().
					Return(true, nil).Once()
			},
			expectedFinalState: model.SmeHelloStateAbortDone,
			waitForAsync:      true,
		},
		{
			name:              "pending_state_validation_succeeds",
			initialState:      model.SmeHelloStatePendingListen,
			setupMocks: func() {
				// validateConnectionBeforeApproval will return true
				s.wsDataWriter.EXPECT().
					IsDataConnectionClosed().
					Return(false, nil).Once()
				s.infoProvider.EXPECT().
					IsRemoteServiceForSKIPaired(s.sut.remoteSKI).
					Return(true).Once()
				
				// handshakeHello_PendingListen will be called
				s.wsDataWriter.EXPECT().
					WriteMessageToWebsocketConnection(mock.Anything).
					Return(nil).Once()
			},
			expectedFinalState: model.SmeHelloStateOk,
			waitForAsync:      true,
		},
		{
			name:              "pending_init_validation_succeeds",
			initialState:      model.SmeHelloStatePendingInit,
			setupMocks: func() {
				// validateConnectionBeforeApproval will return true
				s.wsDataWriter.EXPECT().
					IsDataConnectionClosed().
					Return(false, nil).Once()
				s.infoProvider.EXPECT().
					IsRemoteServiceForSKIPaired(s.sut.remoteSKI).
					Return(true).Once()
				
				// handshakeHello_PendingInit will be called
				s.wsDataWriter.EXPECT().
					WriteMessageToWebsocketConnection(mock.Anything).
					Return(nil).Once()
			},
			expectedFinalState: model.SmeHelloStateOk,
			waitForAsync:      true,
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.sut.smeState = tt.initialState
			tt.setupMocks()
			
			s.sut.ApprovePendingHandshake()
			
			if tt.waitForAsync {
				// Wait for async processing
				time.Sleep(100 * time.Millisecond)
			}
			
			assert.Equal(s.T(), tt.expectedFinalState, s.sut.smeState)
		})
	}
}

// Test_ApprovePendingHandshake_ConcurrentCalls tests concurrent approval attempts
func (s *ConnectionHandshakeCoverageSuite) Test_ApprovePendingHandshake_ConcurrentCalls() {
	// Set initial state to pending
	s.sut.smeState = model.SmeHelloStatePendingListen
	
	// Setup mocks for multiple validation attempts
	// Only the first should proceed
	s.wsDataWriter.EXPECT().
		IsDataConnectionClosed().
		Return(false, nil).Maybe()
	s.infoProvider.EXPECT().
		IsRemoteServiceForSKIPaired(s.sut.remoteSKI).
		Return(true).Maybe()
	s.wsDataWriter.EXPECT().
		WriteMessageToWebsocketConnection(mock.Anything).
		Return(nil).Maybe()
	
	// Call ApprovePendingHandshake concurrently
	done := make(chan bool, 5)
	for i := 0; i < 5; i++ {
		go func() {
			s.sut.ApprovePendingHandshake()
			done <- true
		}()
	}
	
	// Wait for all goroutines
	for i := 0; i < 5; i++ {
		<-done
	}
	
	// Wait for state transition
	time.Sleep(100 * time.Millisecond)
	
	// Should have transitioned to OK state
	assert.Equal(s.T(), model.SmeHelloStateOk, s.sut.smeState)
}

// Test_ValidateConnectionBeforeApproval_EdgeCases tests edge cases
func (s *ConnectionHandshakeCoverageSuite) Test_ValidateConnectionBeforeApproval_EdgeCases() {
	// Test with empty remote SKI
	s.sut.remoteSKI = ""
	
	s.wsDataWriter.EXPECT().
		IsDataConnectionClosed().
		Return(false, nil).Once()
	s.infoProvider.EXPECT().
		IsRemoteServiceForSKIPaired("").
		Return(false).Once()
	
	result := s.sut.validateConnectionBeforeApproval()
	assert.False(s.T(), result)
	
	// Test with very long remote SKI
	longSKI := "very-long-ski-" + string(make([]byte, 1000))
	s.sut.remoteSKI = longSKI
	
	s.wsDataWriter.EXPECT().
		IsDataConnectionClosed().
		Return(false, nil).Once()
	s.infoProvider.EXPECT().
		IsRemoteServiceForSKIPaired(longSKI).
		Return(true).Once()
	
	result = s.sut.validateConnectionBeforeApproval()
	assert.True(s.T(), result)
}

// Test_ApprovePendingHandshake_WithClosedConnection tests approval with closed connection
func (s *ConnectionHandshakeCoverageSuite) Test_ApprovePendingHandshake_WithClosedConnection() {
	// Set state to pending
	s.sut.smeState = model.SmeHelloStatePendingListen
	
	// Setup validation to fail due to closed connection
	s.wsDataWriter.EXPECT().
		IsDataConnectionClosed().
		Return(true, errors.New("connection closed")).Once()
	
	s.sut.ApprovePendingHandshake()
	
	// Wait for async processing
	time.Sleep(100 * time.Millisecond)
	
	// Should transition to abort state
	assert.Equal(s.T(), model.SmeHelloStateAbortDone, s.sut.smeState)
}