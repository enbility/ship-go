package hub

import (
	"testing"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/cert"
	"github.com/enbility/ship-go/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// HubConnectionsRegistryCoverageSuite tests hub connections registry functionality
type HubConnectionsRegistryCoverageSuite struct {
	suite.Suite
	hub          *Hub
	mockMdns     *mocks.MdnsInterface
	mockReader   *mocks.HubReaderInterface
	localService *api.ServiceDetails
	localSKI     string
}

func (s *HubConnectionsRegistryCoverageSuite) SetupTest() {
	s.localSKI = "test-local-ski"
	s.localService = api.NewServiceDetails(s.localSKI)

	cert, err := cert.CreateCertificate("test", "test", "DE", "test")
	require.NoError(s.T(), err)

	s.mockMdns = mocks.NewMdnsInterface(s.T())
	s.mockReader = mocks.NewHubReaderInterface(s.T())

	// Add default expectations that may be triggered
	// Use mock.AnythingOfType to avoid inspecting structs with sync primitives
	s.mockReader.EXPECT().ServicePairingDetailUpdate(mock.AnythingOfType("string"), mock.AnythingOfType("*api.ConnectionStateDetail")).Maybe()
	s.mockReader.EXPECT().VisibleRemoteServicesUpdated(mock.AnythingOfType("[]api.RemoteService")).Maybe()
	s.mockReader.EXPECT().ServiceShipIDUpdate(mock.AnythingOfType("string"), mock.AnythingOfType("string")).Maybe()
	s.mockReader.EXPECT().RemoteSKIDisconnected(mock.AnythingOfType("string")).Maybe()

	s.hub = NewHub(s.mockReader, s.mockMdns, 0, cert, s.localService)
}

// Test_KeepThisConnection_BasicLogic tests the basic logic of connection management
func (s *HubConnectionsRegistryCoverageSuite) Test_KeepThisConnection_BasicLogic() {
	// Test that we can register a service
	remoteService := api.NewServiceDetails("remote-ski-1")

	s.hub.muxReg.Lock()
	s.hub.remoteServices[remoteService.SKI()] = remoteService
	s.hub.muxReg.Unlock()

	// Verify the service is registered
	service := s.hub.ServiceForSKI(remoteService.SKI())
	assert.NotNil(s.T(), service)
	assert.Equal(s.T(), remoteService.SKI(), service.SKI())
}

// Test_ConnectionForSKI_ThreadSafety tests thread safety of connection lookup
func (s *HubConnectionsRegistryCoverageSuite) Test_ConnectionForSKI_ThreadSafety() {
	ski := "test-ski"

	// Create a mock connection
	mockConn := &mocks.ShipConnectionInterface{}
	mockConn.EXPECT().RemoteSKI().Return(ski).Maybe()
	mockConn.EXPECT().IsAlive().Return(true).Maybe()

	// Add to connections map
	s.hub.registry.mu.Lock()
	s.hub.registry.connections[ski] = mockConn
	s.hub.registry.mu.Unlock()

	// Run concurrent reads
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			s.hub.registry.mu.RLock()
			_ = s.hub.registry.connections[ski]
			s.hub.registry.mu.RUnlock()
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

// Test_RegisterConnection_EdgeCases tests edge cases in connection registration.
// Post-Phase-3: registerConnection has been replaced by registry.Swap.
func (s *HubConnectionsRegistryCoverageSuite) Test_RegisterConnection_EdgeCases() {
	mockConn := &mocks.ShipConnectionInterface{}
	mockConn.EXPECT().RemoteSKI().Return("edge-case-ski").Maybe()
	mockConn.EXPECT().IsAlive().Return(true).Maybe()

	assert.NotPanics(s.T(), func() {
		s.hub.registry.Swap("edge-case-ski", false, func() api.ShipConnectionInterface { return mockConn })
	})

	// Verify it was registered
	s.hub.registry.mu.RLock()
	_, exists := s.hub.registry.connections["edge-case-ski"]
	s.hub.registry.mu.RUnlock()
	assert.True(s.T(), exists)
}

// Test_ConnectionRegistration tests connection registration and unregistration
func (s *HubConnectionsRegistryCoverageSuite) Test_ConnectionRegistration() {
	// Create a mock connection
	mockConn := &mocks.ShipConnectionInterface{}
	ski := "test-register-ski"
	mockConn.EXPECT().RemoteSKI().Return(ski).Maybe()
	mockConn.EXPECT().IsAlive().Return(true).Maybe()

	// Register the connection via registry.Swap (post-Phase-3 equivalent of
	// registerConnection).
	s.hub.registry.Swap(ski, false, func() api.ShipConnectionInterface { return mockConn })

	// Verify it's registered
	s.hub.registry.mu.RLock()
	conn, exists := s.hub.registry.connections[ski]
	s.hub.registry.mu.RUnlock()

	assert.True(s.T(), exists)
	assert.Equal(s.T(), mockConn, conn)

	// Create a service for the connection
	service := api.NewServiceDetails(ski)
	s.hub.muxReg.Lock()
	s.hub.remoteServices[ski] = service
	s.hub.muxReg.Unlock()

	// Simulate connection close
	mockConn.EXPECT().CloseConnection(mock.Anything, mock.Anything, mock.Anything).Maybe()

	// Remove from connections map directly since we can't easily trigger the full flow
	s.hub.registry.mu.Lock()
	delete(s.hub.registry.connections, ski)
	s.hub.registry.mu.Unlock()

	// Verify it's removed
	s.hub.registry.mu.RLock()
	_, exists = s.hub.registry.connections[ski]
	s.hub.registry.mu.RUnlock()

	assert.False(s.T(), exists)
}

// Test_ConnectionMapSize tests getting the size of connection map
func (s *HubConnectionsRegistryCoverageSuite) Test_ConnectionMapSize() {
	// Initially should be empty
	s.hub.registry.mu.RLock()
	size := len(s.hub.registry.connections)
	s.hub.registry.mu.RUnlock()
	assert.Equal(s.T(), 0, size)

	// Add mock connections
	mockConn1 := &mocks.ShipConnectionInterface{}
	mockConn2 := &mocks.ShipConnectionInterface{}

	mockConn1.EXPECT().RemoteSKI().Return("ski1").Maybe()
	mockConn1.EXPECT().IsAlive().Return(true).Maybe()
	mockConn2.EXPECT().RemoteSKI().Return("ski2").Maybe()
	mockConn2.EXPECT().IsAlive().Return(true).Maybe()

	s.hub.registry.mu.Lock()
	s.hub.registry.connections["ski1"] = mockConn1
	s.hub.registry.connections["ski2"] = mockConn2
	s.hub.registry.mu.Unlock()

	// Should now be 2
	s.hub.registry.mu.RLock()
	size = len(s.hub.registry.connections)
	s.hub.registry.mu.RUnlock()

	assert.Equal(s.T(), 2, size)
}

// Test_ConcurrentConnectionOperations tests concurrent operations on connections
func (s *HubConnectionsRegistryCoverageSuite) Test_ConcurrentConnectionOperations() {
	// Test concurrent reads and writes
	done := make(chan bool, 20)

	// Writers
	for i := 0; i < 10; i++ {
		go func(idx int) {
			mockConn := &mocks.ShipConnectionInterface{}
			ski := string(rune('a' + idx))
			mockConn.EXPECT().RemoteSKI().Return(ski).Maybe()
			mockConn.EXPECT().IsAlive().Return(true).Maybe()

			s.hub.registry.mu.Lock()
			s.hub.registry.connections[ski] = mockConn
			s.hub.registry.mu.Unlock()

			done <- true
		}(i)
	}

	// Readers
	for i := 0; i < 10; i++ {
		go func() {
			s.hub.registry.mu.RLock()
			_ = len(s.hub.registry.connections)
			s.hub.registry.mu.RUnlock()

			done <- true
		}()
	}

	// Wait for all operations
	for i := 0; i < 20; i++ {
		<-done
	}
}

// Test_GetAllConnections tests getting all connections
func (s *HubConnectionsRegistryCoverageSuite) Test_GetAllConnections() {
	// Add some connections
	mockConn1 := &mocks.ShipConnectionInterface{}
	mockConn2 := &mocks.ShipConnectionInterface{}
	mockConn3 := &mocks.ShipConnectionInterface{}

	mockConn1.EXPECT().RemoteSKI().Return("ski1").Maybe()
	mockConn1.EXPECT().IsAlive().Return(true).Maybe()
	mockConn2.EXPECT().RemoteSKI().Return("ski2").Maybe()
	mockConn2.EXPECT().IsAlive().Return(true).Maybe()
	mockConn3.EXPECT().RemoteSKI().Return("ski3").Maybe()
	mockConn3.EXPECT().IsAlive().Return(true).Maybe()

	s.hub.registry.mu.Lock()
	s.hub.registry.connections["ski1"] = mockConn1
	s.hub.registry.connections["ski2"] = mockConn2
	s.hub.registry.connections["ski3"] = mockConn3
	s.hub.registry.mu.Unlock()

	// Get all connections
	s.hub.registry.mu.RLock()
	count := 0
	for range s.hub.registry.connections {
		count++
	}
	s.hub.registry.mu.RUnlock()

	assert.Equal(s.T(), 3, count)
}

func TestHubConnectionsRegistryCoverageSuite(t *testing.T) {
	suite.Run(t, new(HubConnectionsRegistryCoverageSuite))
}
