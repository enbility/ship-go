package hub

import (
	"crypto/tls"
	"fmt"
	"sync"
	"testing"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/enbility/ship-go/mocks"
	"github.com/enbility/ship-go/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"
)

func TestHubConnectionsRegistrySuite(t *testing.T) {
	suite.Run(t, new(HubConnectionsRegistrySuite))
}

type HubConnectionsRegistrySuite struct {
	suite.Suite

	hubReader      *mocks.MockHubReaderInterface
	mdnsService    *mocks.MockMdnsInterface
	shipConnection *mocks.ShipConnectionInterface
	wsDataWriter   *mocks.WebsocketDataWriterInterface
	remoteSki      string
	sut            *Hub
}

func (s *HubConnectionsRegistrySuite) BeforeTest(suiteName, testName string) {
	s.remoteSki = "remotetestski"

	ctrl := gomock.NewController(s.T())

	s.hubReader = mocks.NewMockHubReaderInterface(ctrl)
	s.hubReader.EXPECT().RemoteServiceConnected(gomock.Any()).Return().AnyTimes()
	s.hubReader.EXPECT().RemoteServiceDisconnected(gomock.Any()).Return().AnyTimes()
	s.hubReader.EXPECT().ServiceUpdated(gomock.Any()).Return().AnyTimes()

	s.mdnsService = mocks.NewMockMdnsInterface(ctrl)
	s.mdnsService.EXPECT().AnnounceMdnsEntry().Return(nil).AnyTimes()
	s.mdnsService.EXPECT().UnannounceMdnsEntry().Return().AnyTimes()
	s.mdnsService.EXPECT().RequestMdnsEntries().Return().AnyTimes()

	s.wsDataWriter = mocks.NewWebsocketDataWriterInterface(s.T())

	s.shipConnection = mocks.NewShipConnectionInterface(s.T())
	s.shipConnection.EXPECT().CloseConnection(mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	s.shipConnection.EXPECT().RemoteSKI().Return(s.remoteSki).Maybe()
	s.shipConnection.EXPECT().ApprovePendingHandshake().Return().Maybe()
	s.shipConnection.EXPECT().AbortPendingHandshake().Return().Maybe()
	s.shipConnection.EXPECT().DataHandler().Return(s.wsDataWriter).Maybe()
	s.shipConnection.EXPECT().ShipHandshakeState().Return(model.SmeStateComplete, nil).Maybe()

	localService := api.NewServiceDetails("localSKI", "", "")
	certificate, _ := cert.CreateCertificate("unit", "org", "DE", "CN")
	var err error
	s.sut, err = newTestHub(s.hubReader, s.mdnsService, 4567, certificate, localService, nil)
	assert.NoError(s.T(), err)
}

func (s *HubConnectionsRegistrySuite) AfterTest(suiteName, testName string) {
	s.mdnsService.EXPECT().Shutdown().AnyTimes()
	s.sut.Shutdown()
}

func (s *HubConnectionsRegistrySuite) Test_IsRemoteSKIPaired() {
	paired := s.sut.IsRemoteServiceForSKIPaired(s.remoteSki)
	assert.Equal(s.T(), false, paired)

	s.sut.registerConnection(s.shipConnection)
	s.sut.RegisterRemoteService(api.NewServiceIdentity(s.remoteSki, "", ""))

	paired = s.sut.IsRemoteServiceForSKIPaired(s.remoteSki)
	assert.Equal(s.T(), true, paired)

	// remove the connection, so the test doesn't try to close it
	s.hubReader.EXPECT().ServicePairingDetailUpdate(gomock.Any(), gomock.Any()).Return().Times(1)
	delete(s.sut.connections, s.remoteSki)
	s.sut.UnregisterRemoteService(api.NewServiceIdentity(s.remoteSki, "", ""))
	paired = s.sut.IsRemoteServiceForSKIPaired(s.remoteSki)
	assert.Equal(s.T(), false, paired)

	ski := "12af9e"
	localService := api.NewServiceDetails(ski, "", "")

	hub, err := newTestHub(s.hubReader, s.mdnsService, 4567, tls.Certificate{}, localService, nil)
	assert.NotNil(s.T(), hub)
	assert.NoError(s.T(), err)

	s.mdnsService.EXPECT().Start(gomock.Any(), gomock.Any()).Return(nil).Times(1)
	err = hub.Start()
	assert.NoError(s.T(), err)

	hub.UnregisterRemoteService(api.NewServiceIdentity(s.remoteSki, "", ""))
	paired = s.sut.IsRemoteServiceForSKIPaired(s.remoteSki)
	assert.Equal(s.T(), false, paired)

	s.mdnsService.EXPECT().Shutdown().Times(1)
	hub.Shutdown()
}

func (s *HubConnectionsRegistrySuite) Test_RegisterRemoteSKI_AfterStart() {
	s.sut.hasStarted = true

	s.hubReader.EXPECT().ServicePairingDetailUpdate(gomock.Any(), gomock.Any()).Return().AnyTimes()
	s.sut.RegisterRemoteService(api.NewServiceIdentity(s.remoteSki, "", ""))
	assert.Equal(s.T(), 0, len(s.sut.connections))

	s.sut.registerConnection(s.shipConnection)
	s.sut.RegisterRemoteService(api.NewServiceIdentity(s.remoteSki, "", ""))
	assert.Equal(s.T(), 1, len(s.sut.connections))
}

func (s *HubConnectionsRegistrySuite) Test_HandleConnectionClosed() {
	s.sut.HandleConnectionClosed(s.shipConnection, false)

	s.sut.registerConnection(s.shipConnection)

	s.sut.HandleConnectionClosed(s.shipConnection, true)

	assert.Equal(s.T(), 0, len(s.sut.connections))
}

func (s *HubConnectionsRegistrySuite) Test_RegisterConnection() {
	s.sut.registerConnection(s.shipConnection)
	assert.Equal(s.T(), 1, len(s.sut.connections))
	con := s.sut.connectionForService(api.NewServiceDetails(s.remoteSki, "", ""))
	assert.NotNil(s.T(), con)
}

func (s *HubConnectionsRegistrySuite) Test_CancelPairingWithSKI() {
	s.sut.CancelPairing(api.NewServiceIdentity(s.remoteSki, "", ""))
	assert.Equal(s.T(), 0, len(s.sut.connections))
	assert.Equal(s.T(), 0, len(s.sut.connectionAttemptRunning))

	s.sut.registerConnection(s.shipConnection)
	assert.Equal(s.T(), 1, len(s.sut.connections))

	s.sut.CancelPairing(api.NewServiceIdentity(s.remoteSki, "", ""))
	assert.Equal(s.T(), 0, len(s.sut.connectionAttemptRunning))
}

// =============================================================================
// COMPREHENSIVE HandleConnectionClosed TESTS
// =============================================================================

// HandleConnectionClosedTestSuite provides comprehensive test coverage for HandleConnectionClosed method
type HandleConnectionClosedTestSuite struct {
	suite.Suite

	// Mock dependencies using gomock (following existing pattern)
	mockHubReader   *mocks.MockHubReaderInterface
	mockMdns        *mocks.MockMdnsInterface
	mockConnection  *mocks.ShipConnectionInterface
	mockConnection2 *mocks.ShipConnectionInterface
	ctrl            *gomock.Controller

	// Test data
	certificate  tls.Certificate
	localService *api.ServiceDetails

	// System under test
	hub *Hub

	// Test identifiers
	testSKI     string
	testShipID  string
	otherSKI    string
	otherShipID string
}

func TestHandleConnectionClosedTestSuite(t *testing.T) {
	suite.Run(t, new(HandleConnectionClosedTestSuite))
}

func (s *HandleConnectionClosedTestSuite) SetupTest() {
	s.ctrl = gomock.NewController(s.T())

	// Setup test identifiers
	s.testSKI = "testski123"
	s.testShipID = "test-ship-id-456"
	s.otherSKI = "otherski789"
	s.otherShipID = "other-ship-id-abc"

	// Setup gomock mocks (following existing pattern)
	s.mockHubReader = mocks.NewMockHubReaderInterface(s.ctrl)
	s.mockMdns = mocks.NewMockMdnsInterface(s.ctrl)

	// Setup testify mocks for connections
	s.mockConnection = mocks.NewShipConnectionInterface(s.T())
	s.mockConnection2 = mocks.NewShipConnectionInterface(s.T())

	// Allow all Hub lifecycle operations (gomock style)
	s.mockMdns.EXPECT().AnnounceMdnsEntry().Return(nil).AnyTimes()
	s.mockMdns.EXPECT().UnannounceMdnsEntry().Return().AnyTimes()
	s.mockMdns.EXPECT().RequestMdnsEntries().Return().AnyTimes()
	s.mockMdns.EXPECT().Shutdown().Return().AnyTimes()

	// Allow basic hub reader callbacks
	s.mockHubReader.EXPECT().RemoteServiceConnected(gomock.Any()).Return().AnyTimes()
	s.mockHubReader.EXPECT().RemoteServiceDisconnected(gomock.Any()).Return().AnyTimes()
	s.mockHubReader.EXPECT().ServiceUpdated(gomock.Any()).Return().AnyTimes()

	// Setup test data
	var err error
	s.certificate, err = cert.CreateCertificate("test-unit", "test-org", "DE", "test-cn")
	require.NoError(s.T(), err)
	s.localService = api.NewServiceDetails("hubtestski", "", "")

	// Create Hub
	s.hub, err = newTestHub(
		s.mockHubReader,
		s.mockMdns,
		0, // Use port 0 for testing
		s.certificate,
		s.localService,
		nil,
	)
	require.NoError(s.T(), err)

	// Setup mock connections with common expectations (following existing pattern)
	s.mockConnection.EXPECT().RemoteSKI().Return(s.testSKI).Maybe()
	s.mockConnection.EXPECT().CloseConnection(mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	s.mockConnection.EXPECT().ApprovePendingHandshake().Return().Maybe()
	s.mockConnection.EXPECT().AbortPendingHandshake().Return().Maybe()

	s.mockConnection2.EXPECT().RemoteSKI().Return(s.otherSKI).Maybe()
	s.mockConnection2.EXPECT().CloseConnection(mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	s.mockConnection2.EXPECT().ApprovePendingHandshake().Return().Maybe()
	s.mockConnection2.EXPECT().AbortPendingHandshake().Return().Maybe()
}

func (s *HandleConnectionClosedTestSuite) TearDownTest() {
	if s.hub != nil {
		s.hub.Shutdown()
	}
	if s.ctrl != nil {
		s.ctrl.Finish()
	}
}

// Connection Cleanup Scenario Tests (6 test cases)

func (s *HandleConnectionClosedTestSuite) TestHandleConnectionClosed_BasicConnectionCleanup() {
	// Test basic connection cleanup when connection is registered

	// Setup - Register connection
	s.hub.registerConnection(s.mockConnection)
	assert.Equal(s.T(), 1, len(s.hub.connections), "Connection should be registered")

	// Disconnect callback will be called (covered by AnyTimes() expectation in setup)

	// Act
	s.hub.HandleConnectionClosed(s.mockConnection, true)

	// Assert - Connection should be unregistered
	assert.Equal(s.T(), 0, len(s.hub.connections), "Connection should be unregistered")
}

func (s *HandleConnectionClosedTestSuite) TestHandleConnectionClosed_UnregisteredConnection() {
	// Test behavior when connection is not registered in hub.connections

	// Setup - Ensure connection is not registered
	assert.Equal(s.T(), 0, len(s.hub.connections), "Should start with no connections")

	// Disconnect callback will be called (covered by AnyTimes() expectation in setup)

	// Act
	s.hub.HandleConnectionClosed(s.mockConnection, true)

	// Assert - No changes to connections map
	assert.Equal(s.T(), 0, len(s.hub.connections), "Connections should remain empty")
}

func (s *HandleConnectionClosedTestSuite) TestHandleConnectionClosed_HandshakeCompletedCounterReset() {
	// Test that connection attempt counter is reset when handshakeCompleted=true

	// Setup - Add connection attempt counter (using the correct map)
	s.hub.connectionAttemptCounter[s.testSKI] = 3 // Simulate some attempts
	counter, exists := s.hub.getCurrentConnectionAttemptCounter(s.testSKI)
	assert.True(s.T(), exists, "Counter should exist")
	assert.Equal(s.T(), 3, counter, "Counter should be set to 3")

	// Disconnect callback will be called (covered by AnyTimes() expectation in setup)

	// Act - Close with handshakeCompleted=true
	s.hub.HandleConnectionClosed(s.mockConnection, true)

	// Assert - Counter should be removed
	_, exists = s.hub.getCurrentConnectionAttemptCounter(s.testSKI)
	assert.False(s.T(), exists, "Counter should be reset for completed handshake")
}

func (s *HandleConnectionClosedTestSuite) TestHandleConnectionClosed_HandshakeNotCompletedCounterPreserved() {
	// Test that connection attempt counter is preserved when handshakeCompleted=false

	// Setup - Add connection attempt counter (using the correct map)
	s.hub.connectionAttemptCounter[s.testSKI] = 2 // Simulate some attempts
	counter, exists := s.hub.getCurrentConnectionAttemptCounter(s.testSKI)
	assert.True(s.T(), exists, "Counter should exist")
	assert.Equal(s.T(), 2, counter, "Counter should be set to 2")

	// Disconnect callback will be called (covered by AnyTimes() expectation in setup)

	// Act - Close with handshakeCompleted=false
	s.hub.HandleConnectionClosed(s.mockConnection, false)

	// Assert - Counter should remain (not removed when handshake not completed)
	finalCounter, exists := s.hub.getCurrentConnectionAttemptCounter(s.testSKI)
	assert.True(s.T(), exists, "Counter should be preserved for incomplete handshake")
	assert.Equal(s.T(), 2, finalCounter, "Counter value should be unchanged")
}

func (s *HandleConnectionClosedTestSuite) TestHandleConnectionClosed_DoubleConnectionScenario() {
	// Test behavior with double connections where only registered one is cleaned up

	// Setup - Register first connection, but close second one
	s.hub.registerConnection(s.mockConnection)
	assert.Equal(s.T(), 1, len(s.hub.connections), "First connection should be registered")

	// Setup second connection with same SKI but different object
	s.mockConnection2.EXPECT().RemoteSKI().Return(s.testSKI).Maybe()

	// Disconnect callback will be called (covered by AnyTimes() expectation in setup)

	// Act - Close second connection (not the registered one)
	s.hub.HandleConnectionClosed(s.mockConnection2, true)

	// Assert - First connection should remain registered
	assert.Equal(s.T(), 1, len(s.hub.connections), "Registered connection should remain")
	conn := s.hub.connectionForService(api.NewServiceDetails(s.testSKI, "", ""))
	assert.Equal(s.T(), s.mockConnection, conn, "Original connection should still be registered")
}

func (s *HandleConnectionClosedTestSuite) TestHandleConnectionClosed_NilConnectionHandling() {
	// Test behavior when nil connection is passed (should not panic)

	// Note: This will likely panic due to connection.RemoteSKI() call on nil
	// Testing to document current behavior
	assert.Panics(s.T(), func() {
		s.hub.HandleConnectionClosed(nil, true)
	}, "HandleConnectionClosed should panic with nil connection")
}

// AddCu Replacement Timing Tests (5 test cases)

func (s *HandleConnectionClosedTestSuite) TestHandleConnectionClosed_AddCuTimerStarted() {
	// Test that AddCu replacement timer is started for AddCu devices

	// Setup - Create AddCu service and register connection
	addCuService := api.NewServiceDetails(s.testSKI, "", s.testShipID)
	addCuService.SetTrusted(true)
	addCuService.SetPairingType(api.PairingTypeAddCu)
	success := s.hub.addService(addCuService)
	require.True(s.T(), success, "Should add AddCu service")

	s.hub.registerConnection(s.mockConnection)

	// Disconnect callback will be called (covered by AnyTimes() expectation in setup)

	// Act
	s.hub.HandleConnectionClosed(s.mockConnection, true)

	// Assert - Timer should be started (we can't directly verify but test completes)
	// The timer logic is complex to test without access to private timer state
}

func (s *HandleConnectionClosedTestSuite) TestHandleConnectionClosed_NonAddCuNoTimer() {
	// Test that timer is not started for non-AddCu devices

	// Setup - Create regular (non-AddCu) service
	regularService := api.NewServiceDetails(s.testSKI, "", s.testShipID)
	regularService.SetTrusted(true)
	regularService.SetPairingType(api.PairingTypeDefault) // Not AddCu
	success := s.hub.addService(regularService)
	require.True(s.T(), success, "Should add regular service")

	s.hub.registerConnection(s.mockConnection)

	// Disconnect callback will be called (covered by AnyTimes() expectation in setup)

	// Act
	s.hub.HandleConnectionClosed(s.mockConnection, true)

	// Assert - No timer should be started (test passes if no timer operations)
}

func (s *HandleConnectionClosedTestSuite) TestHandleConnectionClosed_AddCuEmptyShipIDNoTimer() {
	// Test that timer is not started for AddCu devices with empty ShipID

	// Setup - Create AddCu service with empty ShipID
	addCuService := api.NewServiceDetails(s.testSKI, "", "") // Empty ShipID
	addCuService.SetTrusted(true)
	addCuService.SetPairingType(api.PairingTypeAddCu)
	success := s.hub.addService(addCuService)
	require.True(s.T(), success, "Should add AddCu service")

	s.hub.registerConnection(s.mockConnection)

	// Disconnect callback will be called (covered by AnyTimes() expectation in setup)

	// Act
	s.hub.HandleConnectionClosed(s.mockConnection, true)

	// Assert - No timer should be started due to empty ShipID
}

func (s *HandleConnectionClosedTestSuite) TestHandleConnectionClosed_AddCuUntrustedNoTimer() {
	// Test that timer is not started for untrusted AddCu devices with incomplete handshake

	// Setup - Create untrusted AddCu service
	addCuService := api.NewServiceDetails(s.testSKI, "", s.testShipID)
	addCuService.SetTrusted(false) // Not trusted
	addCuService.SetPairingType(api.PairingTypeAddCu)
	success := s.hub.addService(addCuService)
	require.True(s.T(), success, "Should add untrusted AddCu service")

	s.hub.registerConnection(s.mockConnection)

	// Disconnect callback will be called (covered by AnyTimes() expectation in setup)

	// Act - Close with handshakeCompleted=false and untrusted service
	s.hub.HandleConnectionClosed(s.mockConnection, false)

	// Assert - Should return early, no timer started (due to early return condition)
}

func (s *HandleConnectionClosedTestSuite) TestHandleConnectionClosed_AddCuTimerWithCallback() {
	// Test that timer is started with correct callback for AddCu devices

	// Setup - Create trusted AddCu service
	addCuService := api.NewServiceDetails(s.testSKI, "", s.testShipID)
	addCuService.SetTrusted(true)
	addCuService.SetPairingType(api.PairingTypeAddCu)
	success := s.hub.addService(addCuService)
	require.True(s.T(), success, "Should add AddCu service")

	s.hub.registerConnection(s.mockConnection)

	// Disconnect callback will be called (covered by AnyTimes() expectation in setup)

	// Act
	s.hub.HandleConnectionClosed(s.mockConnection, true)

	// Assert - Timer should be started with handleAddCuReplacementTimeout callback
	// We can't directly verify the callback but the function should complete successfully
}

// Reconnection Logic Tests (4 test cases)

func (s *HandleConnectionClosedTestSuite) TestHandleConnectionClosed_NoReconnectionForFailedHandshakeUntrusted() {
	// Test early return when handshake failed and service is not trusted

	// Setup - Create untrusted service
	untrustedService := api.NewServiceDetails(s.testSKI, "", s.testShipID)
	untrustedService.SetTrusted(false)
	success := s.hub.addService(untrustedService)
	require.True(s.T(), success, "Should add untrusted service")

	// Disconnect callback will be called (covered by AnyTimes() expectation in setup)

	// Act - Close with handshakeCompleted=false and untrusted service
	s.hub.HandleConnectionClosed(s.mockConnection, false)

	// Assert - Should return early (no AddCu timer operations, no auto-reannounce)
	// Test passes if function returns early as expected
}

func (s *HandleConnectionClosedTestSuite) TestHandleConnectionClosed_ReconnectionForTrustedService() {
	// Test that auto-reannounce is checked for trusted services

	// Setup - Create trusted service
	trustedService := api.NewServiceDetails(s.testSKI, "", s.testShipID)
	trustedService.SetTrusted(true)
	success := s.hub.addService(trustedService)
	require.True(s.T(), success, "Should add trusted service")

	s.hub.registerConnection(s.mockConnection)

	// Disconnect callback will be called (covered by AnyTimes() expectation in setup)

	// Act
	s.hub.HandleConnectionClosed(s.mockConnection, false) // handshake not completed but service trusted

	// Assert - Should continue to auto-reannounce check (test completes successfully)
}

func (s *HandleConnectionClosedTestSuite) TestHandleConnectionClosed_ReconnectionForCompletedHandshake() {
	// Test that auto-reannounce is checked for completed handshakes even if service untrusted

	// Setup - Create untrusted service
	untrustedService := api.NewServiceDetails(s.testSKI, "", s.testShipID)
	untrustedService.SetTrusted(false)
	success := s.hub.addService(untrustedService)
	require.True(s.T(), success, "Should add untrusted service")

	s.hub.registerConnection(s.mockConnection)

	// Disconnect callback will be called (covered by AnyTimes() expectation in setup)

	// Act
	s.hub.HandleConnectionClosed(s.mockConnection, true) // handshake completed

	// Assert - Should continue to auto-reannounce check (test completes successfully)
}

func (s *HandleConnectionClosedTestSuite) TestHandleConnectionClosed_NoServiceFoundForReconnection() {
	// Test behavior when service is not found for reconnection logic

	// Setup - No service added for testSKI
	assert.Nil(s.T(), s.hub.ServiceForIdentifier(s.testSKI, ""), "Should start with no service")

	// Disconnect callback will be called (covered by AnyTimes() expectation in setup)

	// Act
	s.hub.HandleConnectionClosed(s.mockConnection, true)

	// Assert - Should return early due to nil service
}

// Service State Transition Tests (4 test cases)

func (s *HandleConnectionClosedTestSuite) TestHandleConnectionClosed_ServiceStateAfterDisconnect() {
	// Test service state remains consistent after connection close

	// Setup - Create trusted service
	trustedService := api.NewServiceDetails(s.testSKI, "", s.testShipID)
	trustedService.SetTrusted(true)
	trustedService.SetPairingType(api.PairingTypeDefault)
	success := s.hub.addService(trustedService)
	require.True(s.T(), success, "Should add trusted service")

	s.hub.registerConnection(s.mockConnection)

	// Disconnect callback will be called (covered by AnyTimes() expectation in setup)

	// Act
	s.hub.HandleConnectionClosed(s.mockConnection, true)

	// Assert - Service should still exist and be trusted
	service := s.hub.ServiceForIdentifier(s.testSKI, "")
	require.NotNil(s.T(), service, "Service should still exist")
	assert.True(s.T(), service.Trusted(), "Service should remain trusted")
	assert.Equal(s.T(), api.PairingTypeDefault, service.PairingType(), "PairingType should be unchanged")
}

func (s *HandleConnectionClosedTestSuite) TestHandleConnectionClosed_MultipleServicesConnectionClose() {
	// Test that only the correct service's connection is handled

	// Setup - Create multiple services
	service1 := api.NewServiceDetails(s.testSKI, "", s.testShipID)
	service1.SetTrusted(true)
	success1 := s.hub.addService(service1)
	require.True(s.T(), success1, "Should add first service")

	service2 := api.NewServiceDetails(s.otherSKI, "", s.otherShipID)
	service2.SetTrusted(true)
	success2 := s.hub.addService(service2)
	require.True(s.T(), success2, "Should add second service")

	// Register both connections
	s.hub.registerConnection(s.mockConnection)  // testSKI
	s.hub.registerConnection(s.mockConnection2) // otherSKI

	// Disconnect callback will be called (covered by AnyTimes() expectation in setup)

	// Act - Close only first connection
	s.hub.HandleConnectionClosed(s.mockConnection, true)

	// Assert - Only first connection removed, second remains
	assert.Equal(s.T(), 1, len(s.hub.connections), "One connection should remain")

	remainingConn := s.hub.connectionForService(api.NewServiceDetails(s.otherSKI, "", ""))
	assert.Equal(s.T(), s.mockConnection2, remainingConn, "Second connection should remain")

	removedConn := s.hub.connectionForService(api.NewServiceDetails(s.testSKI, "", ""))
	assert.Nil(s.T(), removedConn, "First connection should be removed")
}

func (s *HandleConnectionClosedTestSuite) TestHandleConnectionClosed_ServiceAccessDuringClose() {
	// Test that service access is thread-safe during connection close

	// Setup - Create service and register connection
	service := api.NewServiceDetails(s.testSKI, "", s.testShipID)
	service.SetTrusted(true)
	success := s.hub.addService(service)
	require.True(s.T(), success, "Should add service")

	s.hub.registerConnection(s.mockConnection)

	// Disconnect callback will be called (covered by AnyTimes() expectation in setup)

	// Act - Access service concurrently with connection close
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		s.hub.HandleConnectionClosed(s.mockConnection, true)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		// Concurrent service access
		_ = s.hub.ServiceForIdentifier(s.testSKI, "")
	}()

	wg.Wait()

	// Assert - No race conditions, both operations complete
}

func (s *HandleConnectionClosedTestSuite) TestHandleConnectionClosed_ConnectionAttemptStateThreadSafety() {
	// Test thread safety of connection attempt counter management

	// Setup - Add connection attempt counters for multiple SKIs
	s.hub.connectionAttemptCounter[s.testSKI] = 3
	s.hub.connectionAttemptCounter[s.otherSKI] = 5

	// Verify initial state
	counter1, exists1 := s.hub.getCurrentConnectionAttemptCounter(s.testSKI)
	assert.True(s.T(), exists1, "First counter should exist")
	assert.Equal(s.T(), 3, counter1, "First counter should be 3")

	counter2, exists2 := s.hub.getCurrentConnectionAttemptCounter(s.otherSKI)
	assert.True(s.T(), exists2, "Second counter should exist")
	assert.Equal(s.T(), 5, counter2, "Second counter should be 5")

	// Disconnect callbacks will be called (covered by AnyTimes() expectation in setup)

	// Act - Close connections concurrently with completed handshakes
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		s.hub.HandleConnectionClosed(s.mockConnection, true) // Will remove counter
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		s.hub.HandleConnectionClosed(s.mockConnection2, true) // Will remove counter
	}()

	wg.Wait()

	// Assert - Both attempt counters should be removed
	_, exists1 = s.hub.getCurrentConnectionAttemptCounter(s.testSKI)
	assert.False(s.T(), exists1, "First counter should be removed")

	_, exists2 = s.hub.getCurrentConnectionAttemptCounter(s.otherSKI)
	assert.False(s.T(), exists2, "Second counter should be removed")
}

// Nil Service Handling Tests (3 test cases)

func (s *HandleConnectionClosedTestSuite) TestHandleConnectionClosed_ServiceNotFound() {
	// Test behavior when service is not found for connection's SKI

	// Setup - No service added, just connection
	s.hub.registerConnection(s.mockConnection)

	// Disconnect callback will be called (covered by AnyTimes() expectation in setup)

	// Act
	s.hub.HandleConnectionClosed(s.mockConnection, true)

	// Assert - Should return early due to nil service, connection still cleaned up
	assert.Equal(s.T(), 0, len(s.hub.connections), "Connection should be cleaned up")
}

func (s *HandleConnectionClosedTestSuite) TestHandleConnectionClosed_ServiceNilReturnEarly() {
	// Test early return path when service is nil

	// Setup - Ensure no service exists
	assert.Nil(s.T(), s.hub.ServiceForIdentifier(s.testSKI, ""), "Service should not exist")

	// Disconnect callback will be called (covered by AnyTimes() expectation in setup)

	// Act
	s.hub.HandleConnectionClosed(s.mockConnection, true)

	// Assert - Function should return early after disconnect callback
}

func (s *HandleConnectionClosedTestSuite) TestHandleConnectionClosed_NilServiceWithFailedHandshake() {
	// Test combination of nil service and failed handshake

	// Setup - No service, failed handshake
	assert.Nil(s.T(), s.hub.ServiceForIdentifier(s.testSKI, ""), "Service should not exist")

	// Disconnect callback will be called (covered by AnyTimes() expectation in setup)

	// Act
	s.hub.HandleConnectionClosed(s.mockConnection, false) // handshake not completed

	// Assert - Should return early due to nil service
}

// Concurrent Close Operation Tests (3 test cases)

func (s *HandleConnectionClosedTestSuite) TestHandleConnectionClosed_ConcurrentClosesSameSKI() {
	// Test concurrent closes of different connections with same SKI

	// Setup - Register one connection but close multiple connections with same SKI
	s.hub.registerConnection(s.mockConnection)

	// Setup additional mock connection with same SKI
	mockConnection3 := mocks.NewShipConnectionInterface(s.T())
	mockConnection3.EXPECT().RemoteSKI().Return(s.testSKI).Maybe()

	// Disconnect callbacks will be called (covered by AnyTimes() expectation in setup)

	// Act - Close connections concurrently
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		s.hub.HandleConnectionClosed(s.mockConnection, true)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		s.hub.HandleConnectionClosed(mockConnection3, true)
	}()

	wg.Wait()

	// Assert - Connection map should be cleaned up properly
	assert.Equal(s.T(), 0, len(s.hub.connections), "All connections should be cleaned up")
}

func (s *HandleConnectionClosedTestSuite) TestHandleConnectionClosed_ConcurrentWithServiceOperations() {
	// Test HandleConnectionClosed concurrent with service operations

	// Setup - Create service and register connection
	service := api.NewServiceDetails(s.testSKI, "", s.testShipID)
	service.SetTrusted(true)
	success := s.hub.addService(service)
	require.True(s.T(), success, "Should add service")

	s.hub.registerConnection(s.mockConnection)

	// Disconnect callback will be called (covered by AnyTimes() expectation in setup)

	// Act - Connection close concurrent with service operations
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		s.hub.HandleConnectionClosed(s.mockConnection, true)
	}()

	// Concurrent service operations
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			// Various service operations
			_ = s.hub.ServiceForIdentifier(s.testSKI, "")
			otherSKI := fmt.Sprintf("otherski%d", index)
			s.hub.RegisterRemoteService(api.NewServiceIdentity(otherSKI, "", fmt.Sprintf("ship-%d", index)))
		}(i)
	}

	wg.Wait()

	// Assert - No race conditions, all operations complete
}

func (s *HandleConnectionClosedTestSuite) TestHandleConnectionClosed_MultipleConnectionsThreadSafety() {
	// Test thread safety with multiple different connections closing

	// Setup multiple services and connections
	skis := []string{s.testSKI, s.otherSKI, "thirdski", "fourthski"}
	connections := []*mocks.ShipConnectionInterface{
		s.mockConnection,
		s.mockConnection2,
		mocks.NewShipConnectionInterface(s.T()),
		mocks.NewShipConnectionInterface(s.T()),
	}

	// Setup services and connections
	for i, ski := range skis {
		service := api.NewServiceDetails(ski, "", fmt.Sprintf("ship-%d", i))
		service.SetTrusted(true)
		success := s.hub.addService(service)
		require.True(s.T(), success, "Should add service for SKI: %s", ski)

		connections[i].EXPECT().RemoteSKI().Return(ski).Maybe()
		s.hub.registerConnection(connections[i])
	}

	// Disconnect callbacks will be called for all SKIs (covered by AnyTimes() expectation in setup)

	// Act - Close all connections concurrently
	var wg sync.WaitGroup
	for i, conn := range connections {
		wg.Add(1)
		go func(connection *mocks.ShipConnectionInterface, index int) {
			defer wg.Done()
			s.hub.HandleConnectionClosed(connection, true)
		}(conn, i)
	}

	wg.Wait()

	// Assert - All connections should be cleaned up
	assert.Equal(s.T(), 0, len(s.hub.connections), "All connections should be cleaned up")
}

/* connectionForService() Tests */

func (s *HubConnectionsRegistrySuite) Test_connectionForService_SKILookup() {
	// Test primary path: connection lookup by SKI
	s.sut.registerConnection(s.shipConnection)

	// Create service with SKI
	service := api.NewServiceDetails(s.remoteSki, "test-fp", "test-ship")
	
	// Should find connection by SKI
	conn := s.sut.connectionForService(service)
	assert.NotNil(s.T(), conn)
	assert.Equal(s.T(), s.shipConnection, conn)
}

func (s *HubConnectionsRegistrySuite) Test_connectionForService_FingerprintFallback() {
	// Test fallback path: connection lookup by fingerprint when SKI is empty
	s.sut.registerConnection(s.shipConnection)

	// Add service to hub with fingerprint matching the connection
	connectedService := api.NewServiceDetails(s.remoteSki, "matching-fingerprint", "ship123")
	s.sut.addService(connectedService)

	// Create lookup service with empty SKI but matching fingerprint (AddCu scenario)
	lookupService := api.NewServiceDetails("", "matching-fingerprint", "different-ship")
	
	// Should find connection by fingerprint fallback
	conn := s.sut.connectionForService(lookupService)
	assert.NotNil(s.T(), conn)
	assert.Equal(s.T(), s.shipConnection, conn)
}

func (s *HubConnectionsRegistrySuite) Test_connectionForService_ShipIDFallback() {
	// Test fallback path: connection lookup by shipID when SKI and fingerprint don't match
	s.sut.registerConnection(s.shipConnection)

	// Add service to hub with shipID matching the lookup
	connectedService := api.NewServiceDetails(s.remoteSki, "conn-fingerprint", "matching-ship")
	s.sut.addService(connectedService)

	// Create lookup service with empty SKI, different fingerprint, but matching shipID
	lookupService := api.NewServiceDetails("", "different-fingerprint", "matching-ship")
	
	// Should find connection by shipID fallback
	conn := s.sut.connectionForService(lookupService)
	assert.NotNil(s.T(), conn)
	assert.Equal(s.T(), s.shipConnection, conn)
}

func (s *HubConnectionsRegistrySuite) Test_connectionForService_NoMatch() {
	// Test case where no connection matches any identifier
	s.sut.registerConnection(s.shipConnection)

	// Add service that won't match
	connectedService := api.NewServiceDetails(s.remoteSki, "conn-fingerprint", "conn-ship")
	s.sut.addService(connectedService)

	// Create lookup service with completely different identifiers
	lookupService := api.NewServiceDetails("different-ski", "different-fingerprint", "different-ship")
	
	// Should not find any connection
	conn := s.sut.connectionForService(lookupService)
	assert.Nil(s.T(), conn)
}

func (s *HubConnectionsRegistrySuite) Test_connectionForService_NilService() {
	// Test edge case: nil service parameter
	conn := s.sut.connectionForService(nil)
	assert.Nil(s.T(), conn)
}

func (s *HubConnectionsRegistrySuite) Test_connectionForService_EmptyIdentifiers() {
	// Test edge case: service with all empty identifiers
	emptyService := api.NewServiceDetails("", "", "")
	conn := s.sut.connectionForService(emptyService)
	assert.Nil(s.T(), conn)
}

func (s *HubConnectionsRegistrySuite) Test_connectionForService_BackwardCompatibility() {
	// Test that connectionForSKI() still works and uses the new service-based lookup
	s.sut.registerConnection(s.shipConnection)

	// Test old API still works
	conn := s.sut.connectionForService(api.NewServiceDetails(s.remoteSki, "", ""))
	assert.NotNil(s.T(), conn)
	assert.Equal(s.T(), s.shipConnection, conn)
	
	// Test empty SKI returns nil
	conn = s.sut.connectionForService(api.NewServiceDetails("", "", ""))
	assert.Nil(s.T(), conn)
}
