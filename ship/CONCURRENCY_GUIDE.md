# Ship Connection Concurrency Guide

This document provides guidelines for thread-safe programming in the SHIP connection implementation.

## Overview

The ShipConnection handles the data connection and coordinates SHIP and SPINE message I/O. It manages handshake state machines, timer operations, and message buffering in a concurrent environment.

## Lock Structure

The ShipConnection uses multiple mutexes to protect different aspects of the connection:

- `mux` - Main connection state mutex  
- `bufferMux` - SPINE message buffer protection
- `handshakeTimerMux` - Handshake timer state protection
- `shutdownOnce` - Ensures single shutdown execution

## Key Concurrency Fix: Two-Phase setState()

### Problem (FIXED)
The original `setState()` method held the main lock while calling timer methods, creating potential deadlocks:

```go
// OLD PROBLEMATIC PATTERN (Fixed)
func setState(newState, err) {
    c.mux.Lock()           // Main lock acquired
    // ... state update
    c.setHandshakeTimer()  // Timer method called while holding lock
    c.mux.Unlock()
}
```

### Solution: Two-Phase State Updates
The new implementation separates state changes from timer operations:

```go
// NEW SAFE PATTERN
func (c *ShipConnection) setState(newState model.ShipMessageExchangeState, err error) {
    // Phase 1: Update state atomically while holding lock
    var timerOp func()
    var shouldNotify bool
    var notifyState model.ShipState
    
    c.mux.Lock()
    oldState := c.smeState
    c.smeState = newState
    c.smeError = err
    
    // Determine timer operation while holding lock, but don't execute it yet
    switch newState {
    case model.SmeHelloStateReadyInit:
        timerOp = func() { c.setHandshakeTimer(timeoutTimerTypeWaitForReady, tHelloInit) }
    case model.SmeHelloStateOk:
        timerOp = func() { c.stopHandshakeTimer() }
    }
    
    if oldState != newState {
        shouldNotify = true
        notifyState = model.ShipState{State: newState, Error: err}
    }
    c.mux.Unlock()

    // Phase 2: Execute timer operations and notifications without holding lock
    if timerOp != nil {
        timerOp()
    }
    
    if shouldNotify {
        c.infoProvider.HandleShipHandshakeStateUpdate(c.remoteSKI, notifyState)
    }
}
```

## Benefits of the Two-Phase Approach

1. **Deadlock Prevention**: Timer operations no longer execute while holding the main lock
2. **Atomicity**: State changes remain atomic within the critical section
3. **Performance**: Reduced lock holding time improves concurrency
4. **Safety**: External callbacks happen without locks, preventing circular dependencies

## Timer Management Patterns

### Safe Timer Operations
```go
// Timer operations are now safe to call from setState()
func (c *ShipConnection) setHandshakeTimer(timerType timeoutTimerType, duration time.Duration) {
    c.stopHandshakeTimer() // Safe - no main lock held
    
    c.handshakeTimerMux.Lock()
    c.handshakeTimerRunning = true
    c.handshakeTimerType = timerType
    c.handshakeTimerMux.Unlock()
    
    // Timer goroutine can safely call handleState()
    go func() {
        select {
        case <-time.After(duration):
            c.handleState(true, nil) // No deadlock risk
        case <-c.handshakeTimerStopChan:
            return
        }
    }()
}
```

### Atomic Timer Queries
```go
func (c *ShipConnection) getHandshakeTimerRunning() bool {
    c.handshakeTimerMux.Lock()
    defer c.handshakeTimerMux.Unlock()
    return c.handshakeTimerRunning
}
```

## Buffer Management

SPINE message buffering uses a separate mutex to avoid contention:

```go
func (c *ShipConnection) HandleIncomingWebsocketMessage(message []byte) {
    c.bufferMux.Lock()
    c.spineBuffer = append(c.spineBuffer, message)
    c.bufferMux.Unlock()
    
    // Process buffer without holding lock
    c.processBufferedMessages()
}
```

## State Query Patterns

### Thread-Safe State Access
```go
func (c *ShipConnection) getState() model.ShipMessageExchangeState {
    c.mux.Lock()
    defer c.mux.Unlock()
    return c.smeState
}
```

### Atomic State Transitions
```go
func (c *ShipConnection) ApproveIfPending() bool {
    c.mux.Lock()
    
    if c.smeState != model.SmeHelloStatePendingListen {
        c.mux.Unlock()
        return false
    }
    
    // Update state while holding lock
    c.smeState = model.SmeHelloStateReadyInit
    c.mux.Unlock()

    // Handle state transitions without lock to avoid deadlock
    c.stopHandshakeTimer()
    c.handleState(false, nil)
    
    return true
}
```

## Testing Concurrency

### Deadlock Detection Tests

#### Local Testing
```bash
# Using Makefile (recommended)
make test-deadlock           # Enhanced deadlock detection
make test-deadlock-specific  # Run specific deadlock tests
make test-race               # Standard race detection
make test-concurrency        # All concurrency tests

# Development workflow
make dev-test               # Quick format, lint, and test
make pre-commit             # Full pre-commit validation

# Manual test commands
go test -race -tags=deadlock -timeout=30s ./ship
```

#### CI/CD Integration
Deadlock tests are automatically executed in the CI/CD pipeline:
- Every push and pull request runs deadlock detection tests
- Nightly runs with enhanced concurrency testing
- Performance regression monitoring

### Available Test Suites
- `TestSetStateTimerDeadlock`: Tests the specific setState/timer deadlock scenario
- `TestStateTimerConsistency`: Verifies timer state consistency under concurrent modifications
- `TestTimerLifecycleDeadlock`: Tests complete timer lifecycle for deadlock issues
- `TestConcurrentStateChangeWithTimerExpiry`: Tests race between timer expiry and state changes

### Performance Benchmarks
```bash
go test -bench=BenchmarkSetState -benchtime=1s ./ship
go test -bench=BenchmarkConcurrentStateChanges ./ship
```

## Common Patterns

### State Change Pattern
```go
// Always use setState() for state changes
conn.setState(model.SmeHelloStateOk, nil)

// Read state safely
currentState := conn.getState()
```

### Timer Management Pattern
```go
// Timer operations are safe to call from any context
conn.setHandshakeTimer(timeoutTimerTypeWaitForReady, duration)
conn.stopHandshakeTimer()

// Check timer state
if conn.getHandshakeTimerRunning() {
    // Timer is active
}
```

### Connection Lifecycle Pattern
```go
// Proper connection shutdown
conn.CloseConnection(false, 4001, "reason")

// The shutdownOnce ensures cleanup happens only once
```

## Performance Considerations

### Lock Holding Time
- Critical sections are kept minimal
- Timer operations happen outside of main lock
- External callbacks occur without holding locks

### Concurrency Benefits
- State queries don't block timer operations
- Timer operations don't block state changes  
- Multiple goroutines can query state simultaneously

### Memory Management
- Buffer operations use separate mutex
- No locks held during memory allocations
- Timer goroutines clean up automatically

## Best Practices

### Do's ✅
- Use `setState()` for all state changes
- Query state with `getState()` 
- Use atomic approval/abort methods
- Test with race detector enabled
- Keep critical sections small

### Don'ts ❌
- Don't call timer methods while holding `mux`
- Don't hold locks during external callbacks
- Don't manually manipulate `c.smeState` directly
- Don't create new timer patterns without review

## Error Handling

State changes with errors:
```go
conn.setState(model.SmeStateError, fmt.Errorf("connection failed: %v", err))
```

Timer expiry handling:
```go
// Timer goroutines automatically handle expiry
// No manual intervention required
```

## Future Documentation

Following the pattern `[COMPONENT]_[TOPIC]_GUIDE.md`:

- `SHIP_PERFORMANCE_GUIDE.md` - Performance optimization guidelines
- `SHIP_TESTING_GUIDE.md` - Testing strategies and patterns  
- `SHIP_HANDSHAKE_GUIDE.md` - Handshake state machine documentation
- `SHIP_MESSAGE_GUIDE.md` - Message handling patterns

## Migration Notes

For code that previously relied on the old `setState()` behavior:

1. **Timer operations**: Now happen asynchronously after state change
2. **Callbacks**: Now happen without locks held
3. **Atomicity**: State changes remain atomic, but side effects are deferred
4. **Performance**: Improved due to reduced lock contention

The two-phase approach maintains all functional correctness while eliminating deadlock risks and improving performance.