package ws

import "time"

const (
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second // SHIP 4.2: ping interval + pong timeout
	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = 50 * time.Second // SHIP 4.2: ping interval

	// MaxMessageSize limits incoming message size to prevent DoS attacks
	// and expensive EEBUS JSON conversions. Typical SPINE messages are <50KB.
	MaxMessageSize = 100 * 1024 // 100KB

	// DefaultWriteBufferSize is the default size of the write channel buffer.
	// Based on analysis of real-world EEBUS logs, 256 messages provides
	// a good balance between memory usage and handling burst scenarios.
	// Maximum observed burst was 106 messages in 100ms.
	DefaultWriteBufferSize = 256
)
