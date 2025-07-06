# Hub Concurrency Guide

This document provides comprehensive guidelines for thread-safe programming in the SHIP hub implementation.

## Overview

The Hub is the central coordinator for SHIP connections, managing WebSocket servers, mDNS discovery, device pairing, and connection lifecycle. It maintains registries of known remote services and coordinates all communication between devices.

## Lock Ordering Rules

To prevent deadlocks, the hub uses multiple mutexes with a strict ordering hierarchy. Locks **MUST** be acquired in the following order when multiple locks are needed:

1. `muxReg` - Remote services registry lock
2. `muxCon` - Active connections registry lock  
3. `muxConAttempt` - Connection attempt counters lock
4. `muxMdns` - mDNS entries lock
5. `muxStarted` - Hub started state lock

## Safe Programming Patterns

### ✅ CORRECT Patterns

```go
// Acquire locks in correct order
h.muxReg.RLock()
defer h.muxReg.RUnlock()
h.muxCon.RLock()
defer h.muxCon.RUnlock()
// ... do work

// Single lock usage
h.muxReg.RLock()
service := h.remoteServices[ski]
h.muxReg.RUnlock()

// Atomic operations without nested locks
func (h *Hub) checkAutoReannounce() {
    countPairedServices := h.numberPairedServices() // locks/unlocks muxReg
    h.muxCon.RLock()
    countConnections := len(h.connections)
    h.muxCon.RUnlock()
    // Process without holding any locks
}
```

### ❌ DANGEROUS Anti-Patterns

```go
// WRONG: Reverse order (can cause deadlock)
h.muxCon.Lock()
h.muxReg.Lock()    // DEADLOCK RISK!

// WRONG: Calling methods while holding locks
h.muxCon.Lock()
h.ServiceForSKI(ski) // This acquires muxReg!
h.muxCon.Unlock()

// WRONG: Long-running operations under lock
h.muxReg.Lock()
result := expensiveNetworkCall() // Don't do this!
h.muxReg.Unlock()
```

## Mutex Responsibilities

### muxReg (Remote Services Registry) - `sync.RWMutex`
- **Protects**: `remoteServices` map, `autoAccept` flag
- **Usage**: Service discovery, pairing operations, trust management
- **Pattern**: Read-heavy operations use `RLock()`, writes use `Lock()`

### muxCon (Active Connections) - `sync.RWMutex`
- **Protects**: `connections` map
- **Usage**: Connection registration, lookup, lifecycle management
- **Pattern**: Frequent lookups use `RLock()`, registration uses `Lock()`

### muxConAttempt (Connection Attempts) - `sync.RWMutex`
- **Protects**: `connectionAttempts` map, `connectionAttemptRunning` map
- **Usage**: Rate limiting, reconnection logic, connection state tracking
- **Pattern**: Status checks use `RLock()`, updates use `Lock()`

### muxMdns (mDNS Entries) - `sync.Mutex`
- **Protects**: `announcedMdnsEntries` map
- **Usage**: Service announcement, discovery state management
- **Pattern**: Infrequent operations, standard mutex sufficient

### muxStarted (Hub State) - `sync.RWMutex`
- **Protects**: `hasStarted` boolean
- **Usage**: Hub lifecycle management, preventing duplicate operations
- **Pattern**: Status checks use `RLock()`, lifecycle changes use `Lock()`

## Deadlock Prevention Techniques

### 1. Lock-Free Data Access
Capture data atomically, then process without locks:

```go
// Get snapshot of data
h.muxReg.RLock()
servicesCopy := make(map[string]api.RemoteServiceInterface)
for k, v := range h.remoteServices {
    servicesCopy[k] = v
}
h.muxReg.RUnlock()

// Process without holding locks
for ski, service := range servicesCopy {
    processService(ski, service)
}
```

### 2. Atomic Operations
Use methods like `UnregisterConnectionIfMatch` for atomic compare-and-swap:

```go
func (h *Hub) UnregisterConnectionIfMatch(ski string, conn api.ShipConnectionInterface) bool {
    h.muxCon.Lock()
    defer h.muxCon.Unlock()
    
    if h.connections[ski] == conn {
        delete(h.connections, ski)
        return true
    }
    return false
}
```

### 3. Early Lock Release
Release locks before calling external interfaces:

```go
h.muxReg.RLock()
service := h.remoteServices[ski]
h.muxReg.RUnlock()

// Call external interface without holding locks
if service != nil {
    service.SomeMethod()
}
```

### 4. Read/Write Lock Usage
Use `RLock()` for read-only operations to allow concurrency:

```go
func (h *Hub) ServiceForSKI(ski string) api.RemoteServiceInterface {
    // Try read-only access first
    h.muxReg.RLock()
    service, exists := h.remoteServices[ski]
    h.muxReg.RUnlock()
    
    if exists {
        return service
    }
    
    // Need write access to create new entry
    h.muxReg.Lock()
    defer h.muxReg.Unlock()
    
    // Double-check after acquiring write lock
    if service, exists := h.remoteServices[ski]; exists {
        return service
    }
    
    // Create new entry
    service = api.NewServiceDetails(ski)
    h.remoteServices[ski] = service
    return service
}
```

## Testing and Validation

### Running Deadlock Detection Tests

#### Local Testing
```bash
# Using Makefile (recommended)
make test-deadlock      # Enhanced deadlock detection
make test-race          # Standard race detection  
make test-stress        # Stress testing
make test-concurrency   # All concurrency tests
make test-all           # Comprehensive test suite

# Quick development workflow
make dev-test           # Format, lint, and race test
make quick-test         # Alias for test-race

# Manual test commands
go test -race ./hub                           # Standard race detection
go test -race -tags=deadlock ./hub            # Enhanced deadlock detection
go test -race -tags=stress -timeout=60s ./hub # Stress testing
```

#### CI/CD Integration
The deadlock tests are automatically run in GitHub Actions:

- **Standard Workflow** (`default.yml`): Runs on every push/PR
  - Basic race detection tests
  - Deadlock detection tests with `-tags=deadlock`
  - Stress tests with `-tags=stress`

- **Dedicated Concurrency Workflow** (`concurrency-tests.yml`): 
  - Triggered on concurrency-critical file changes
  - Runs nightly for continuous monitoring
  - Multiple test matrices with different `GOMAXPROCS`
  - Performance regression detection

### Available Test Suites
- `TestHubMutexOrderingDeadlock`: Tests lock ordering under concurrent load
- `TestConnectionRegistrationRace`: Tests connection lifecycle race conditions  
- `TestAtomicUnregisterIfMatch`: Validates atomic operations
- `TestHubStressWithAllOperations`: High-load stress testing

## Performance Considerations

### Lock Granularity
- Keep critical sections small
- Prefer multiple fine-grained locks over single coarse lock
- Release locks before I/O operations

### Read-Heavy Optimizations
The following methods have been optimized with `RWMutex` for concurrent reads:
- `connectionForSKI()` - Connection lookups
- `isSkiConnected()` - Connection existence checks
- `numberPairedServices()` - Service counting
- `IsAutoAcceptEnabled()` - Configuration checks
- `getCurrentConnectionAttemptCounter()` - Attempt tracking
- `isConnectionAttemptRunning()` - Status checks
- `checkHasStarted()` - Lifecycle checks

### Monitoring Lock Contention
```go
// Add metrics for lock acquisition time in production
start := time.Now()
h.muxReg.RLock()
// ... critical section
h.muxReg.RUnlock()
duration := time.Since(start)

if duration > threshold {
    metrics.RecordSlowLock("muxReg", duration)
}
```

## Common Usage Patterns

### Service Lookup Pattern
```go
service := h.ServiceForSKI(ski)
if service != nil && service.Trusted() {
    // Use service
}
```

### Connection Management Pattern
```go
if h.UnregisterConnectionIfMatch(ski, conn) {
    // Connection was unregistered
    h.infoProvider.RemoteSKIDisconnected(ski)
}
```

### Registry Update Pattern
```go
h.muxReg.Lock()
h.remoteServices[ski] = service
h.muxReg.Unlock()
h.infoProvider.ServicePairingDetailUpdate(ski, service.PairingDetail())
```

## Future Documentation

This guide follows the naming pattern `[COMPONENT]_[TOPIC]_GUIDE.md`. Future documentation should follow this pattern:

- `HUB_PERFORMANCE_GUIDE.md` - Performance optimization guidelines
- `HUB_TESTING_GUIDE.md` - Testing strategies and patterns
- `SHIP_CONCURRENCY_GUIDE.md` - Ship connection concurrency guidelines
- `MDNS_INTEGRATION_GUIDE.md` - mDNS service integration patterns

## References

- [Go Memory Model](https://golang.org/ref/mem)
- [Effective Go - Concurrency](https://golang.org/doc/effective_go#concurrency)
- [Mutex vs RWMutex Performance](https://golang.org/pkg/sync/#RWMutex)

Following these guidelines ensures deadlock-free operation and optimal performance of the SHIP hub under concurrent load.