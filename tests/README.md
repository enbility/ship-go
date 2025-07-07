# Tests Directory

This directory contains additional testing documentation and utilities for the ship-go project.

## Contents

### `deadlock_test_guide.md`
Comprehensive guide for understanding and running deadlock detection tests in the ship-go codebase.

## Deadlock Testing

The deadlock detection tests are integrated directly into the respective packages:
- `ship/connection_deadlock_test.go` - Ship connection deadlock tests
- `hub/deadlock_detection_test.go` - Hub deadlock detection tests

### Running Tests

Use the Makefile targets for convenient testing:

```bash
# Quick deadlock tests
make test-deadlock-specific

# All deadlock tests
make test-deadlock

# Full concurrency test suite
make test-concurrency
```

### Manual Testing

```bash
# Ship deadlock tests
go test -race -tags=deadlock -timeout=30s ./ship

# Hub deadlock tests  
go test -race -tags=deadlock -timeout=30s ./hub

# Stress tests
go test -race -tags=stress -timeout=60s ./...
```

## Test Organization

Deadlock and concurrency tests are co-located with the code they test:

- **`ship/`** package tests are in `ship/*_test.go`
- **`hub/`** package tests are in `hub/*_test.go`
- **Shared utilities** are in `internal/testing/testhelper/`

This organization ensures:
- Tests have access to internal methods and types
- Tests are maintained alongside the code they verify
- No complex import dependencies between test packages

## CI/CD Integration

The deadlock tests are automatically run in GitHub Actions:

- **Every push/PR**: Basic deadlock detection tests
- **Concurrency changes**: Enhanced testing with multiple core counts
- **Nightly**: Full stress testing and performance monitoring

See the concurrency guides for more details:
- [Hub Concurrency Guide](../hub/CONCURRENCY_GUIDE.md)
- [Ship Concurrency Guide](../ship/CONCURRENCY_GUIDE.md)