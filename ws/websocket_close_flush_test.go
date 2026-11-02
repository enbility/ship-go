package ws

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/enbility/ship-go/mocks"
	"github.com/enbility/ship-go/model"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestCloseDataConnection_DeliversQueuedMessages covers the ordering a SHIP abort depends on.
// abortProtocolHandshake queues its "protocol handshake error" and closes in the same breath
// (TC_SHIP_PROT_003-006), as does the CMI rejection (TC_SHIP_CMI_001/005). The peer must receive
// the queued message before the connection ends, whether or not a close frame is sent - before
// the flush, the message was lost on every close.
func TestCloseDataConnection_DeliversQueuedMessages(t *testing.T) {
	abort := append([]byte{model.MsgTypeControl}, []byte(`{"messageProtocolHandshakeError":[{"error":1}]}`)...)

	tests := []struct {
		name          string
		reason        string
		peerCloseCode int
	}{
		// abortProtocolHandshake closes without a reason: no close frame, the peer sees 1006
		{name: "without close frame", reason: "", peerCloseCode: websocket.CloseAbnormalClosure},
		// a reason makes CloseDataConnection write a close frame directly, bypassing the queue
		{name: "with close frame", reason: "close", peerCloseCode: 4001},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// the write pump and the close run on different goroutines, so repeat the sequence
			for run := 0; run < 20; run++ {
				received, closeCode := queueThenClose(t, abort, tt.reason)

				require.Equal(t, [][]byte{abort}, received,
					"run %d: the queued message must reach the peer before the connection ends", run)
				require.Equal(t, tt.peerCloseCode, closeCode, "run %d", run)
			}
		})
	}
}

// queueThenClose serves one websocket connection that queues message and immediately calls
// CloseDataConnection with reason. It returns the binary messages the dialing peer received before
// the connection ended, and the close code the peer saw.
func queueThenClose(t *testing.T, message []byte, reason string) ([][]byte, int) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		reader := mocks.NewWebsocketDataReaderInterface(t)
		reader.EXPECT().HandleIncomingWebsocketMessage(mock.Anything).Return().Maybe()
		reader.EXPECT().ReportConnectionError(mock.Anything).Return().Maybe()

		dut := NewWebsocketConnection(conn, "test-ski")
		dut.InitDataProcessing(reader)

		if err := dut.WriteMessageToWebsocketConnection(message); err != nil {
			return
		}
		dut.CloseDataConnection(4001, reason)
	}))
	defer server.Close()

	peer, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	defer peer.Close()

	require.NoError(t, peer.SetReadDeadline(time.Now().Add(5*time.Second)))

	var received [][]byte
	for {
		msgType, data, err := peer.ReadMessage()
		if err != nil {
			var closeErr *websocket.CloseError
			require.ErrorAs(t, err, &closeErr, "the connection must end with a close, not a read timeout")
			return received, closeErr.Code
		}
		if msgType == websocket.BinaryMessage {
			received = append(received, data)
		}
	}
}
