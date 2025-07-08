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

## Overview

Includes:

- Certificate handling
- mDNS, incl. avahi support (recommended)
- Websocket server and client
- Connection handling, including reconnection and double connections
- Handling of device pairing
- SHIP handshake
- Logging which is also used by [spine-go](https://github.com/enbility/spine-go) and [eebus-go](https://github.com/enbility/eebus-go)

## Documentation

- **[analysis-docs/](analysis-docs/)** - Comprehensive analysis of SHIP protocol and implementation
  - [README_START_HERE.md](analysis-docs/README_START_HERE.md) - Navigation guide to all analysis documents
  - [EXECUTIVE_SUMMARY.md](analysis-docs/EXECUTIVE_SUMMARY.md) - Business-level overview with key findings (implementation score: 7.5/10)
  - Detailed technical analysis including security model, specification deviations, and improvement roadmap

- **Concurrency Guides** - Thread safety and deadlock prevention
  - [Hub Concurrency Guide](hub/CONCURRENCY_GUIDE.md) - Lock ordering and thread safety patterns
  - [Ship Concurrency Guide](ship/CONCURRENCY_GUIDE.md) - Connection concurrency patterns

- **Testing Documentation** - Additional testing resources
  - [Tests Directory](tests/) - Testing guides and utilities

## Development

### Quick Start

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
hub := hub.NewHub(hubReader, mdns, port, certificate, localService)

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

## Implementation notes

- Double connection handling is not implemented according to SHIP 12.2.2. Instead the connection initiated by the higher SKI will be kept. Much simpler and always works
- PIN Verification is _NOT_ supported other than SHIP 13.4.4.3.5.1 _"none"_ PIN state is supported!
- Access Methods SHIP 13.4.6 only supports the most basic scenario and only works after PIN verification state is completed.
- Supported registration mechanisms (SHIP 5):
  - auto accept (without any interaction mechanism!)
  - user verification
