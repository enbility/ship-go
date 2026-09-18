package ship

import (
	"time"
)

type shipRole string

// ShipRole is the exported name of the connection role type, so callers can hold a
// role in a variable (e.g. to defer starting a connection) instead of only passing
// the constants through directly.
type ShipRole = shipRole

const (
	ShipRoleServer shipRole = "server"
	ShipRoleClient shipRole = "client"
)

const (
	cmiTimeout              = 10 * time.Second // SHIP 4.2
	cmiCloseTimeout         = 100 * time.Millisecond
	tHelloInit              = 60 * time.Second // SHIP 13.4.4.1.3
	tHelloInc               = tHelloInit       // SHIP 13.4.4.1.3: the same value as T_hello_init
	tHelloProlongWaitingGap = 15 * time.Second
)

// SHIP 13.4.4.1.3: an SME User SHALL accept at least two prolongation requests
const helloProlongationRequestsAlwaysAccepted = 2

// Variables that can be overridden in tests
var (
	tHelloProlongMin    = 1 * time.Second  // Minimum prolongation time
	tHelloProlongThrInc = 30 * time.Second // Prolongation threshold increment
	tAbortDelay         = 1 * time.Second  // Delay before closing connection after abort state
)

type timeoutTimerType uint

const (
	// SHIP 13.4.4.1.3: The communication partner must send its "READY" state (or request for prolongation") before the timer expires.
	timeoutTimerTypeWaitForReady timeoutTimerType = iota
	// SHIP 13.4.4.1.3: Local timer to request for prolongation at the communication partner in time (i.e. before the communication partner's Wait-For-Ready-Timer expires).
	timeoutTimerTypeSendProlongationRequest
	// SHIP 13.4.4.1.3: Detection of response timeout on prolongation request.
	timeoutTimerTypeProlongRequestReply
)
