# Test Build Tags Documentation

## Overview

The ship-go library supports a `test` build tag that modifies timer values to make tests run faster.

## Timer Values

### Production Values (default)
- Hello Init Timeout: **60 seconds**
- Abort Delay: **1 second**
- CMI Timeout: **10 seconds**
- Hello Prolongation Min: **1 second**
- Hello Prolongation Threshold: **30 seconds**

### Test Values (with `-tags=test`)
- Hello Init Timeout: **500 milliseconds**
- Abort Delay: **100 milliseconds**
- CMI Timeout: **500 milliseconds**
- Hello Prolongation Min: **100 milliseconds**
- Hello Prolongation Threshold: **200 milliseconds**

## When to Use `-tags=test`

### Use it when:
1. **Running tests locally during development** - Makes test suite run ~120x faster
2. **Debugging test failures** - Shorter timeouts mean faster iteration
3. **Running tests in a loop** - When you need to run tests multiple times
4. **CI/CD with time constraints** - If your CI has strict time limits

### Don't use it when:
1. **Testing actual timeout behavior** - You need realistic timing
2. **Integration testing** - Real-world timing matters
3. **Benchmarking** - Performance measurements need production values

## How to Use

```bash
# Run all tests with fast timers
go test -tags=test -race ./ship

# Run specific test with fast timers
go test -tags=test -v -run TestHandshake ./ship

# Combine with other tags
go test -tags="test,deadlock" -race ./ship
```

## Current Status

- ✅ Timer infrastructure supports test mode
- ✅ `TestTimerValues` validates test mode works correctly
- ❌ Makefile doesn't have a target for test mode
- ❌ GitHub Actions doesn't use test mode

## Why It's Not Used in CI

The project currently doesn't use `-tags=test` in CI because:

1. **Test coverage**: Some tests were written to verify real timeout behavior
2. **Production fidelity**: CI tests should mirror production as closely as possible
3. **Hidden bugs**: Fast timers might hide timing-related issues

However, the infrastructure exists if you need faster test execution locally.

## Adding to Makefile

If you want to add this to your workflow, you could add to the Makefile:

```makefile
test-fast: ## Run tests with short timer values for faster execution
	$(GOTEST) -tags=test -race -v ./...

test-fast-coverage: ## Run fast tests with coverage
	$(GOTEST) -tags=test -race -coverprofile=$(COVERAGE_FILE) -covermode=atomic ./...
```

## Implementation Details

The test build tag is implemented in:
- `ship/timer_values_test.go` - Defines test timer values
- `ship/timer_values.go` - Defines production timer values
- `ship/timer_test.go` - Validates timer values are correct

The build constraint `//go:build test` ensures the test values are only used when explicitly requested.