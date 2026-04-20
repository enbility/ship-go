package hub

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBug11_SendWSCloseMessageReturnsQuickly verifies that sendWSCloseMessage
// does not contain a structural delay before closing the connection. The original
// implementation slept 100ms unconditionally between writing the close frame and
// closing the underlying connection, accumulating fd debt under connection churn.
//
// gorilla/websocket's WriteMessage is synchronous to the kernel send buffer; the
// OS handles graceful FIN propagation. The 100ms sleep was unnecessary.
func TestBug11_SendWSCloseMessageReturnsQuickly(t *testing.T) {
	// Spin up a minimal websocket server.
	upgrader := websocket.Upgrader{}
	serverReady := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		close(serverReady)
		// Drain reads until peer closes.
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				_ = c.Close()
				return
			}
		}
	}))
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1)
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer func() { _ = clientConn.Close() }()

	<-serverReady

	hub := setupTestHubForTimer(t)

	start := time.Now()
	hub.sendWSCloseMessage(clientConn)
	elapsed := time.Since(start)

	// Threshold of 10ms is comfortable: the function does only WriteMessage
	// (kernel-buffered, microseconds) + Close (microseconds). 100ms is the buggy value.
	assert.Less(t, elapsed, 10*time.Millisecond,
		"sendWSCloseMessage must not contain a structural delay (took %s)", elapsed)
}
