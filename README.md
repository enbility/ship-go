# ship-go

[![Build Status](https://github.com/enbility/ship-go/actions/workflows/default.yml/badge.svg?branch=dev)](https://github.com/enbility/ship-go/actions/workflows/default.yml/badge.svg?branch=dev)
[![GoDoc](https://img.shields.io/badge/godoc-reference-5272B4)](https://godoc.org/github.com/enbility/ship-go)
[![Coverage Status](https://coveralls.io/repos/github/enbility/ship-go/badge.svg?branch=dev)](https://coveralls.io/github/enbility/ship-go?branch=dev)
[![Go report](https://goreportcard.com/badge/github.com/enbility/ship-go)](https://goreportcard.com/report/github.com/enbility/ship-go)
[![CodeFactor](https://www.codefactor.io/repository/github/enbility/ship-go/badge)](https://www.codefactor.io/repository/github/enbility/ship-go)
[![Ask DeepWiki](https://deepwiki.com/badge.svg)](https://deepwiki.com/enbility/ship-go)

This library provides an implementation of SHIP 1.0.1 in [go](https://golang.org), which is part of the [EEBUS](https://eebus.org) specification.

Basic understanding of the EEBUS concepts SHIP and SPINE to use this library is required. Please check the corresponding specifications on the [EEBUS downloads website](https://www.eebus.org/media-downloads/).

This repository was started as part of the [eebus-go](https://github.com/enbility/eebus-go) before it was moved into its own repository and this separate go package.

## Important API Changes

**Breaking Changes in v0.8.0:**

1. **Hub Constructor:** The `hub.NewHub()` function now requires 7 parameters instead of 6. The new 7th parameter is `ringBufferPersistence` for SHIP Pairing Service replay protection:
   - For pairing modes `Listener` and `Both`: **Must** provide a `RingBufferPersistence` implementation for storage operations
   - For pairing modes `Announcer` and `Off`: Pass `nil`
   - The library handles ALL ring buffer algorithm logic - applications only handle storage

2. **Interface Changes:** All callback interfaces now use `ServiceIdentity` instead of `*ServiceDetails`:
   - `HubReaderInterface` methods receive `ServiceIdentity` parameters
   - `PairingServiceReaderInterface` methods receive `ServiceIdentity` parameters
   - Hub management methods like `RegisterRemoteService()` take `ServiceIdentity` parameters
   - `ServiceIdentity` is a simple value type, thread-safe without requiring `Copy()` calls

- See [Integration Examples](#ship-pairing-service-integration) for implementation details

## Overview

Includes:

- Certificate handling
- mDNS, incl. avahi support (recommended)
- Websocket server and client
- Connection handling, including reconnection and double connections
- Advanced SHIP Pairing Service with:
      * Automatic device pairing
      * Device Replacement Timing Logic for AddCu devices
      * HMAC-based authentication
      * QR code support
- SHIP handshake
- Logging which is also used by [spine-go](https://github.com/enbility/spine-go) and [eebus-go](https://github.com/enbility/eebus-go)

## Documentation

### Getting Started
- **[Getting Started Guide](docs/GETTING_STARTED.md)** - Complete guide to integrating ship-go (🚀 10 minute quickstart)
- **[Security Model](SECURITY.md)** - Why `InsecureSkipVerify: true` is correct and secure
- **[Working Examples](examples/)** - 5 complete examples from quickstart to production
- **[Error Handling](docs/ERROR_HANDLING.md)** - Common errors and solutions

### Production Deployment
- **[Production Guide](docs/PRODUCTION.md)** - Complete deployment guide with monitoring and security
- **[Specification Compliance](docs/SPEC_COMPLIANCE.md)** - 95% SHIP TS 1.0.1 compliance analysis
- **[Troubleshooting](docs/TROUBLESHOOTING.md)** - Systematic debugging approach

### Technical Deep Dive
- **[Handshake Guide](docs/HANDSHAKE_GUIDE.md)** - 5-phase SHIP handshake with state diagrams
- **[Connection Lifecycle](docs/CONNECTION_LIFECYCLE.md)** - Connection states and management
- **[analysis-docs/](analysis-docs/)** - Comprehensive technical analysis
  - [README_START_HERE.md](analysis-docs/README_START_HERE.md) - Navigation guide to all analysis documents
  - [EXECUTIVE_SUMMARY.md](analysis-docs/EXECUTIVE_SUMMARY.md) - Business-level overview (implementation score: 9.8/10)
  - Detailed security model, specification deviations, and improvement roadmap

### Development Resources
- **Concurrency Guides** - Thread safety and deadlock prevention
  - [Hub Concurrency Guide](hub/CONCURRENCY_GUIDE.md) - Lock ordering and thread safety patterns
  - [Ship Concurrency Guide](ship/CONCURRENCY_GUIDE.md) - Connection concurrency patterns
- **[Testing Documentation](tests/)** - Testing guides and utilities

## Development

### Quick Start

New to ship-go? Start with the [Getting Started Guide](docs/GETTING_STARTED.md) for a complete walkthrough.

```bash
# Build the project
make build

# Run tests with race detection
make test-race

# Run deadlock detection tests
make test-deadlock

# Complete development test cycle
make dev-test
```

**Examples:**
- [Quickstart](examples/quickstart/) - Minimal working hub in 5 minutes
- [Production](examples/production/) - Production-ready hub with monitoring
- [Client](examples/client/) - Interactive client for connecting to devices
- [Pairing](examples/pairing/) - Advanced pairing strategies

### Testing

```bash
# Standard testing
make test                    # Basic tests
make test-race              # Race detection
make test-short             # Quick tests only

# Concurrency testing
make test-deadlock          # Deadlock detection
make test-deadlock-specific # Core deadlock tests only
make test-stress            # High-load stress tests
make test-concurrency       # All concurrency tests

# Comprehensive testing
make test-all               # All tests
make test-ci                # Simulate CI environment
```

#### Fast Test Execution with Test Build Tags

The library supports a `test` build tag that reduces timer values for faster test execution:

```bash
# Run tests with short timers (120x faster)
go test -tags=test -race ./ship

# Production timers: 60s hello timeout, 10s CMI timeout
# Test timers: 500ms hello timeout, 500ms CMI timeout
```

See [ship/TEST_BUILD_TAGS.md](ship/TEST_BUILD_TAGS.md) for detailed documentation on when and how to use test build tags.

### Development Workflow

```bash
# Quick development cycle
make dev-test               # Format, lint, and test

# Pre-commit validation
make pre-commit             # Complete validation

# Performance monitoring
make benchmark              # Run benchmarks
make profile-cpu            # Generate CPU profile
```

### Debugging Concurrency Issues

```bash
# Debug deadlocks
make debug-deadlock         # Verbose deadlock testing
make test-multicore         # Test with different core counts

# Debug race conditions  
make debug-race             # Multiple test runs
make test-race-verbose      # Detailed race output

# Performance debugging
make benchmark-contention   # Lock contention analysis
```

### CI/CD Integration

The project includes comprehensive CI/CD testing:

- **Standard workflow**: Runs on every push/PR with race detection and deadlock tests
- **Concurrency workflow**: Enhanced testing for concurrency-critical changes
- **Nightly monitoring**: Continuous validation of thread safety

Local simulation of CI tests:
```bash
make test-ci               # Run exactly what CI runs
```

## Configuration

### Connection Limits

The hub supports configurable connection limits to prevent resource exhaustion:

```go
// Create hub with default limit of 10 connections
// Note: 7th parameter (ringBufferPersistence) is nil when not using pairing listener modes
hub := hub.NewHub(hubReader, mdns, port, certificate, localService, nil, nil)

// Configure custom connection limit
hub.SetMaxConnections(20)  // Allow up to 20 simultaneous connections

// Setting 0 or negative values will use the default of 10
hub.SetMaxConnections(0)   // Uses default of 10
```

The connection limit helps protect resource-constrained devices (e.g., Raspberry Pi) from:
- Buggy devices creating excessive connections
- Development mistakes (script loops)
- General resource exhaustion

When the limit is reached:
- Incoming connections receive HTTP 503 (Service Unavailable)
- Outgoing connection attempts return an error

## SHIP Pairing Service Integration

The SHIP Pairing Service enables automatic device pairing using QR codes, HMAC authentication, and Device Replacement Timing Logic. For complete architectural details, see [ARCHITECTURE_SHIPPAIRING.md](ARCHITECTURE_SHIPPAIRING.md).

### Required Interfaces

#### 1. PairingServiceReaderInterface (All pairing modes)

Applications must implement `PairingServiceReaderInterface` to handle pairing events:

```go
type PairingServiceReaderInterface interface {
    // Called when device is automatically trusted via pairing service
    ServiceAutoTrusted(identity ServiceIdentity)
    
    // Called when pairing service fails for a service  
    ServiceAutoTrustFailed(identity ServiceIdentity, reason error)
    
    // Called when device trust is automatically removed via replacement logic
    ServiceAutoTrustRemoved(identity ServiceIdentity, reason string)
}
```

#### 2. RingBufferPersistence (Listener and Both modes)

**IMPORTANT:** For `PairingModeListener` and `PairingModeBoth`, applications **MUST** implement `RingBufferPersistence` to provide storage operations for replay attack protection as required by the SHIP Pairing Service specification section 11:

```go
type RingBufferPersistence interface {
    // LoadRingBuffer restores the ring buffer state from persistent storage
    // Called once during Hub initialization
    // Returns: entries array, nextIndex position, error
    // For new installations: return empty slice and nextIndex=0
    LoadRingBuffer() ([]DigestEntry, int, error)
    
    // SaveRingBuffer persists the current ring buffer state
    // Called after each successful pairing
    // Parameters: complete ring buffer array, current nextIndex
    SaveRingBuffer(entries []DigestEntry, nextIndex int) error
}
```

**Key Design Principle:**
- **Library manages ring buffer logic**: The library handles ALL ring buffer algorithm complexity per SHIP specification
- **Applications handle storage only**: You only implement load/save operations - no ring buffer logic needed
- **Clean separation of concerns**: This design ensures SHIP specification compliance while keeping application code simple

**Storage Requirements:**
- **Persistence is mandatory**: The SHIP specification requires "devA SHALL store 'digest-ring-buffer' and 'next' persistently"
- **Thread-safe implementation**: Must handle concurrent access safely
- **Atomic operations**: SaveRingBuffer should be atomic (either fully saves or fails)
- **For Announcer/Off modes**: Pass `nil` as these modes don't require persistence

**Reference Implementation:** See [examples/pairing-listener/main.go](examples/pairing-listener/main.go) for `ExampleRingBufferPersistence` - shows the storage pattern without actual persistence (add database/file storage for production).

### Configuration

Create a pairing configuration with the desired mode and secret, and provide ring buffer persistence for listener modes:

```go
// From QR code secret (16 bytes)
secret := api.PairingSecret(secretBytesFromQRCode)
defer secret.Clear() // Securely clear from memory

// Configure pairing mode
config := api.NewPairingConfig(api.PairingModeListener, secret)

// Create ring buffer persistence (required for Listener and Both modes)
// This MUST persist data per SHIP specification!
ringBufferPersistence := NewMyPersistence() // Your storage implementation

// Create hub with pairing support
// 7th parameter is the ring buffer persistence (nil for Announcer/Off modes)
hub, err := hub.NewHub(hubReader, mdns, port, cert, serviceDetails, config, ringBufferPersistence)
```

**Pairing Modes:**
- `PairingModeOff` - No automatic pairing (traditional SHIP only)
- `PairingModeListener` - Listen for incoming pairing requests (target device)
- `PairingModeAnnouncer` - Announce pairing to discovered devices (requesting device)
- `PairingModeBoth` - Support both modes (flexible device)

### Implementation Example

```go
type MyHubReader struct {
    devices map[string]api.ServiceIdentity
}

// Handle automatic device trust from pairing service
func (m *MyHubReader) ServiceAutoTrusted(identity api.ServiceIdentity) {
    log.Printf("Device auto-trusted: SKI=%s, ShipID=%s", identity.SKI, identity.ShipID)
    
    // Mark AddCu devices for replacement timing logic (devices paired via SHIP Pairing Service)
    identity.PairingType = api.PairingTypeAddCu
    
    // Store trusted device
    m.devices[identity.SKI] = identity
    
    // Update application state, UI, etc.
}

// Handle pairing failures (security events)
func (m *MyHubReader) ServiceAutoTrustFailed(identity api.ServiceIdentity, reason error) {
    log.Printf("Pairing failed: ShipID=%s, Reason=%v", identity.ShipID, reason)
    
    // Log security event for audit
    m.logSecurityEvent("pairing_failed", identity, reason)
}

// Handle device replacement (15-minute timeout logic for AddCu devices)
func (m *MyHubReader) ServiceAutoTrustRemoved(identity api.ServiceIdentity, reason string) {
    log.Printf("Device trust removed: ShipID=%s, Reason=%s", identity.ShipID, reason)
    
    // Clean up resources
    delete(m.devices, identity.SKI)
    
    // Notify user based on reason
    if strings.Contains(reason, "timeout") {
        m.notifyUser(fmt.Sprintf("Device %s timed out after 15 minutes", identity.ShipID))
    } else if strings.Contains(reason, "Replaced") {
        m.notifyUser(fmt.Sprintf("Device %s was replaced", identity.ShipID))
    }
}

// Implement other required HubReaderInterface methods...
func (m *MyHubReader) RemoteServiceConnected(identity api.ServiceIdentity) { /* ... */ }
func (m *MyHubReader) AllowWaitingForTrust(identity api.ServiceIdentity) bool { return true }
```

### Key Integration Points

1. **Ring Buffer Persistence** - Applications implement ONLY storage operations:
   ```go
   // Simple storage-only implementation (no ring buffer logic!)
   type MyPersistence struct {
       db *sql.DB
   }
   
   func (p *MyPersistence) LoadRingBuffer() ([]api.DigestEntry, int, error) {
       // Load from database/file
       // Return empty for new installations: return []api.DigestEntry{}, 0, nil
   }
   
   func (p *MyPersistence) SaveRingBuffer(entries []api.DigestEntry, nextIndex int) error {
       // Save to database/file - library manages all ring buffer logic
   }
   ```

2. **Device Replacement Logic** - Handle AddCu devices with automatic trust removal:
   ```go
   // Set pairing type for devices paired via pairing service
   identity.PairingType = api.PairingTypeAddCu
   ```

3. **Memory Safety** - ServiceIdentity is a simple value type, safe for concurrent operations:
   ```go
   // ServiceIdentity is safe for concurrent access (no internal mutexes)
   go processDevice(identity)
   ```

4. **Secret Security** - Use `PairingSecret` type with secure cleanup:
   ```go
   secret := api.PairingSecret(secretBytes)
   defer secret.Clear() // Wipe from memory
   ```

### Production Considerations

**Persistence Requirements:** The SHIP Pairing Service specification **requires** persistent storage of the digest history to prevent replay attacks across application restarts. Applications must:

1. **Implement RingBufferPersistence** with persistent storage (database, file, etc.)
2. **Library handles ring buffer algorithm** - You don't implement this! The library manages all ring buffer logic per SHIP spec section 11
3. **Store complete state** - Save exactly what the library provides (entries array and nextIndex)
4. **Handle concurrent access** safely with proper synchronization

**Security Implications of Non-Persistence:**
- Without persistence, replay attacks are possible after application restart
- An attacker could capture and replay pairing requests
- This violates SHIP specification security requirements

**Implementation Patterns:**
```go
// Example: Database-backed persistence (storage only - no ring buffer logic!)
type DatabasePersistence struct {
    db  *sql.DB
    mux sync.RWMutex
}

func (d *DatabasePersistence) LoadRingBuffer() ([]api.DigestEntry, int, error) {
    d.mux.RLock()
    defer d.mux.RUnlock()
    
    // Load from database
    row := d.db.QueryRow("SELECT entries, next_index FROM ring_buffer WHERE device_id = ?")
    // Parse and return - library handles all ring buffer logic
}

func (d *DatabasePersistence) SaveRingBuffer(entries []api.DigestEntry, nextIndex int) error {
    d.mux.Lock()
    defer d.mux.Unlock()
    
    // Save exactly what library provides - no ring buffer logic needed!
    _, err := d.db.Exec("REPLACE INTO ring_buffer (device_id, entries, next_index) VALUES (?, ?, ?)",
        deviceID, entries, nextIndex)
    return err
}
```

For complete examples, see:
- [examples/pairing-listener/](examples/pairing-listener/) - Target device with **ExampleRingBufferPersistence** showing the storage pattern
- [examples/pairing-announcer/](examples/pairing-announcer/) - Requesting device (no persistence needed)
- **Important:** The example shows the pattern but doesn't persist - add real storage for production!

## Implementation notes

For complete details, see [Specification Compliance](docs/SPEC_COMPLIANCE.md) (95% compliance).

**Key deviations from SHIP TS 1.0.1:**
- **Double connection handling** - Uses "connection initiator" logic instead of "most recent" (SHIP 12.2.2)
- **PIN Verification** - Only supports "none" PIN state (SHIP 13.4.4.3.5.1)
- **Access Methods** - Basic implementation only (SHIP 13.4.6)
- **TLS Fragment Control** - Uses standard TLS record sizes (Go crypto/tls limitation)
- **Pairing Service** - Full implementation of SHIP Pairing Service specification
- **Device Replacement** - Automatic 15-minute trust management for AddCu devices

**Supported registration mechanisms (SHIP 5):**
- Auto accept (for testing/demos only)
- User verification (recommended for production)

**SHIP Pairing Service Capabilities:**
- Supports full SHIP Pairing Service specification
- Implements 15-minute device replacement timer for AddCu devices
- Supports HMAC-based authentication with automatic replay attack protection
- Flexible pairing modes: auto-accept and user verification

**Security Model:**
- Uses self-signed certificates with SKI-based device identification
- `InsecureSkipVerify: true` is **correct and secure** per SHIP specification
- See [Security Model](SECURITY.md) for detailed explanation
