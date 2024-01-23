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

var _ api.WebsocketDataWriterInterface = (*dataHandlerTest)(nil)

func (s *dataHandlerTest) InitDataProcessing(dataProcessing api.WebsocketDataReaderInterface) {}

func (s *dataHandlerTest) WriteMessageToWebsocketConnection(message []byte) error {
	s.mux.Lock()
	defer s.mux.Unlock()

	s.sentMessage = message

	return nil
}

func (s *dataHandlerTest) CloseDataConnection(int, string)       {}
func (w *dataHandlerTest) IsDataConnectionClosed() (bool, error) { return false, nil }
func (w *dataHandlerTest) SetupRemoteDevice(ski string, writeI api.ShipConnectionDataWriterInterface) api.ShipConnectionDataReaderInterface {
	return nil
}

var _ api.ShipConnectionInfoProviderInterface = (*dataHandlerTest)(nil)

func (s *dataHandlerTest) IsRemoteServiceForSKIPaired(string) bool { return true }
func (s *dataHandlerTest) HandleConnectionClosed(api.ShipConnectionInterface, bool) {
	s.handleConnectionClosedInvoked = true
}
func (s *dataHandlerTest) ReportServiceShipID(string, string) {}
func (s *dataHandlerTest) AllowWaitingForTrust(string) bool {
	return s.allowWaitingForTrust
}
func (s *dataHandlerTest) HandleShipHandshakeStateUpdate(string, model.ShipState) {}

func initTest(role shipRole) (*ShipConnection, *dataHandlerTest) {
	dataHandler := &dataHandlerTest{}
	conhandler := NewConnectionHandler(dataHandler, dataHandler, role, "LocalShipID", "RemoveDevice", "RemoteShipID")

	return conhandler, dataHandler
}

func shutdownTest(conhandler *ShipConnection) {
	conhandler.stopHandshakeTimer()
}
