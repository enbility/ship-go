package ship

import (
	"testing"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/model"
)

// noOpInfoProvider is a minimal implementation for tests that need to avoid mock race conditions
type noOpInfoProvider struct{}

func (n *noOpInfoProvider) IsRemoteServiceForSKIPaired(ski string) bool {
	return false
}

func (n *noOpInfoProvider) IsAutoAcceptEnabled() bool {
	return false
}

func (n *noOpInfoProvider) HandleShipHandshakeStateUpdate(ski string, state model.ShipState) {
	// No-op
}

func (n *noOpInfoProvider) SetupRemoteDevice(ski string, writeI api.ShipConnectionDataWriterInterface) api.ShipConnectionDataReaderInterface {
	return nil
}

func (n *noOpInfoProvider) HandleConnectionClosed(connection api.ShipConnectionInterface, handshakeCompleted bool) {
	// No-op - avoids race conditions with mocks
}

func (n *noOpInfoProvider) ReportServiceShipID(ski string, shipID string) {
	// No-op
}

func (n *noOpInfoProvider) AllowWaitingForTrust(ski string) bool {
	return true
}

// noOpDataWriter is a minimal implementation for tests
type noOpDataWriter struct {
	closed bool
}

func (n *noOpDataWriter) InitDataProcessing(dataProcessing api.WebsocketDataReaderInterface) {
	// No-op
}

func (n *noOpDataWriter) CloseDataConnection(code int, reason string) {
	n.closed = true
}

func (n *noOpDataWriter) IsDataConnectionClosed() (bool, error) {
	return n.closed, nil
}

func (n *noOpDataWriter) WriteMessageToWebsocketConnection(msg []byte) error {
	return nil
}

// createTestConnectionNoMocks creates a basic connection without mocks to avoid race conditions
func createTestConnectionNoMocks(_ *testing.T) *ShipConnection {
	infoProvider := &noOpInfoProvider{}
	dataWriter := &noOpDataWriter{}

	conn := NewConnectionHandler(
		infoProvider,
		dataWriter,
		ShipRoleClient,
		"local-id",
		"remote-ski",
		"remote-id",
	)

	return conn
}
