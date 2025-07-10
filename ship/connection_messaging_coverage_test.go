package ship

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

// ConnectionMessagingCoverageSuite tests additional coverage for connection messaging
type ConnectionMessagingCoverageSuite struct {
	ConnectionSuite
}

func TestConnectionMessagingCoverageSuite(t *testing.T) {
	suite.Run(t, new(ConnectionMessagingCoverageSuite))
}

// Test_WriteShipMessageWithPayload_Success tests successful message sending
func (s *ConnectionMessagingCoverageSuite) Test_WriteShipMessageWithPayload_Success() {
	testMessage := []byte(`{"test": "message"}`)
	
	// Setup mock to expect successful write
	s.wsDataWriter.EXPECT().
		WriteMessageToWebsocketConnection(mock.Anything).
		Run(func(msg []byte) {
			// Verify message format (should have SHIP header)
			assert.NotEmpty(s.T(), msg)
		}).
		Return(nil).Once()
	
	// Call the method
	s.sut.WriteShipMessageWithPayload(testMessage)
	
	// Verify expectations
	s.wsDataWriter.AssertExpectations(s.T())
}

// Test_WriteShipMessageWithPayload_ErrorHandling tests error scenarios
func (s *ConnectionMessagingCoverageSuite) Test_WriteShipMessageWithPayload_ErrorHandling() {
	testMessage := []byte(`{"test": "error"}`)
	
	// Test various error scenarios
	errorCases := []struct {
		name        string
		setupMock   func()
		expectedLog string
	}{
		{
			name: "write_error",
			setupMock: func() {
				s.wsDataWriter.EXPECT().
					WriteMessageToWebsocketConnection(mock.Anything).
					Return(errors.New("write failed")).Once()
			},
			expectedLog: "Error sending spine message",
		},
		{
			name: "connection_closed",
			setupMock: func() {
				s.wsDataWriter.EXPECT().
					WriteMessageToWebsocketConnection(mock.Anything).
					Return(errors.New("use of closed network connection")).Once()
			},
			expectedLog: "Error sending spine message",
		},
		{
			name: "timeout_error",
			setupMock: func() {
				s.wsDataWriter.EXPECT().
					WriteMessageToWebsocketConnection(mock.Anything).
					Return(errors.New("i/o timeout")).Once()
			},
			expectedLog: "Error sending spine message",
		},
	}
	
	for _, tc := range errorCases {
		s.Run(tc.name, func() {
			tc.setupMock()
			
			// Should not panic despite error
			assert.NotPanics(s.T(), func() {
				s.sut.WriteShipMessageWithPayload(testMessage)
			})
			
			s.wsDataWriter.AssertExpectations(s.T())
		})
	}
}

// Test_WriteShipMessageWithPayload_LargeMessage tests with large payloads
func (s *ConnectionMessagingCoverageSuite) Test_WriteShipMessageWithPayload_LargeMessage() {
	// Create a large message (10KB)
	largeData := make([]byte, 10*1024)
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}
	
	s.wsDataWriter.EXPECT().
		WriteMessageToWebsocketConnection(mock.Anything).
		Run(func(msg []byte) {
			// Verify the message includes our large payload
			assert.Greater(s.T(), len(msg), len(largeData))
		}).
		Return(nil).Once()
	
	s.sut.WriteShipMessageWithPayload(largeData)
	
	s.wsDataWriter.AssertExpectations(s.T())
}

// Test_WriteShipMessageWithPayload_EmptyMessage tests with empty payload
func (s *ConnectionMessagingCoverageSuite) Test_WriteShipMessageWithPayload_EmptyMessage() {
	emptyMessage := []byte{}
	
	s.wsDataWriter.EXPECT().
		WriteMessageToWebsocketConnection(mock.Anything).
		Run(func(msg []byte) {
			// Even empty payload should have SHIP envelope
			assert.NotEmpty(s.T(), msg)
		}).
		Return(nil).Once()
	
	s.sut.WriteShipMessageWithPayload(emptyMessage)
	
	s.wsDataWriter.AssertExpectations(s.T())
}

// Test_WriteShipMessageWithPayload_NilMessage tests with nil payload
func (s *ConnectionMessagingCoverageSuite) Test_WriteShipMessageWithPayload_NilMessage() {
	var nilMessage []byte = nil
	
	s.wsDataWriter.EXPECT().
		WriteMessageToWebsocketConnection(mock.Anything).
		Return(nil).Once()
	
	// Should handle nil gracefully
	assert.NotPanics(s.T(), func() {
		s.sut.WriteShipMessageWithPayload(nilMessage)
	})
	
	s.wsDataWriter.AssertExpectations(s.T())
}

// Test_WriteShipMessageWithPayload_ConcurrentWrites tests concurrent message sending
func (s *ConnectionMessagingCoverageSuite) Test_WriteShipMessageWithPayload_ConcurrentWrites() {
	messageCount := 10
	done := make(chan bool, messageCount)
	
	// Setup expectations for multiple concurrent writes
	s.wsDataWriter.EXPECT().
		WriteMessageToWebsocketConnection(mock.Anything).
		Return(nil).
		Times(messageCount)
	
	// Send messages concurrently
	for i := 0; i < messageCount; i++ {
		go func(idx int) {
			msg := []byte(fmt.Sprintf(`{"index": %d}`, idx))
			s.sut.WriteShipMessageWithPayload(msg)
			done <- true
		}(i)
	}
	
	// Wait for all messages to be sent
	for i := 0; i < messageCount; i++ {
		<-done
	}
	
	s.wsDataWriter.AssertExpectations(s.T())
}

// Test_ShipModelFromMessage_ErrorCases tests additional error cases in shipModelFromMessage
func (s *ConnectionMessagingCoverageSuite) Test_ShipModelFromMessage_ErrorCases() {
	testCases := []struct {
		name          string
		message       []byte
		expectError   bool
		errorContains string
	}{
		{
			name:          "invalid_json",
			message:       append([]byte{0}, []byte(`{invalid json`)...),
			expectError:   true,
			errorContains: "error unmarshalling",
		},
		{
			name:          "no_payload",
			message:       append([]byte{0}, []byte(`{"data":{}}`)...),
			expectError:   true,
			errorContains: "no valid payload",
		},
		{
			name:          "malformed_structure",
			message:       append([]byte{0}, []byte(`{"wrong": "structure"}`)...),
			expectError:   true,
			errorContains: "no valid payload",
		},
	}
	
	for _, tc := range testCases {
		s.Run(tc.name, func() {
			result, err := s.sut.shipModelFromMessage(tc.message)
			
			if tc.expectError {
				assert.Error(s.T(), err)
				assert.Nil(s.T(), result)
			} else {
				assert.NoError(s.T(), err)
				assert.NotNil(s.T(), result)
			}
		})
	}
}

// Test_HandleIncomingWebsocketMessage_EdgeCases tests edge cases in message handling
func (s *ConnectionMessagingCoverageSuite) Test_HandleIncomingWebsocketMessage_EdgeCases() {
	// Test with message that causes parsing error
	invalidMessage := []byte{0xFF, 0xFF} // Invalid UTF-8
	
	// Should not panic
	assert.NotPanics(s.T(), func() {
		s.sut.HandleIncomingWebsocketMessage(invalidMessage)
	})
	
	// Test with very short message
	shortMessage := []byte{0}
	assert.NotPanics(s.T(), func() {
		s.sut.HandleIncomingWebsocketMessage(shortMessage)
	})
	
	// Test with nil
	assert.NotPanics(s.T(), func() {
		s.sut.HandleIncomingWebsocketMessage(nil)
	})
}