package ship

import (
	"sync"

	"github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/model"
)

type dataHandlerTest struct {
	sentMessage []byte

	mux sync.Mutex

	allowWaitingForTrust bool

	handleConnectionClosedInvoked bool
}

func (s *dataHandlerTest) lastMessage() []byte {
	s.mux.Lock()
	defer s.mux.Unlock()

	return s.sentMessage
}

var _ api.WebsocketDataConnection = (*dataHandlerTest)(nil)

func (s *dataHandlerTest) InitDataProcessing(dataProcessing api.WebsocketDataProcessing) {}

func (s *dataHandlerTest) WriteMessageToDataConnection(message []byte) error {
	s.mux.Lock()
	defer s.mux.Unlock()

	s.sentMessage = message

	return nil
}

func (s *dataHandlerTest) CloseDataConnection(int, string)       {}
func (w *dataHandlerTest) IsDataConnectionClosed() (bool, error) { return false, nil }
func (w *dataHandlerTest) SetupRemoteDevice(ski string, writeI api.SpineDataConnection) api.SpineDataProcessing {
	return nil
}

var _ api.ShipServiceDataProvider = (*dataHandlerTest)(nil)

func (s *dataHandlerTest) IsRemoteServiceForSKIPaired(string) bool { return true }
func (s *dataHandlerTest) HandleConnectionClosed(api.ShipConnection, bool) {
	s.handleConnectionClosedInvoked = true
}
func (s *dataHandlerTest) ReportServiceShipID(string, string) {}
func (s *dataHandlerTest) AllowWaitingForTrust(string) bool {
	return s.allowWaitingForTrust
}
func (s *dataHandlerTest) HandleShipHandshakeStateUpdate(string, model.ShipState) {}

func initTest(role shipRole) (*ShipConnectionImpl, *dataHandlerTest) {
	dataHandler := &dataHandlerTest{}
	conhandler := NewConnectionHandler(dataHandler, dataHandler, role, "LocalShipID", "RemoveDevice", "RemoteShipID")

	return conhandler, dataHandler
}

func shutdownTest(conhandler *ShipConnectionImpl) {
	conhandler.stopHandshakeTimer()
}
