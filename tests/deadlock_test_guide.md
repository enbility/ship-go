# Deadlock Detection Test Guide for ship-go

## Overview

This guide provides comprehensive strategies and implementations for detecting deadlocks in the ship-go codebase. The tests target specific concurrency issues identified in the codebase analysis.

## Test Strategy Evaluation

### 1. **Go's Race Detector (-race)**
- **Use Case**: Always enable for all concurrency tests
- **Command**: `go test -race -v ./...`
- **Effectiveness**: Essential baseline, catches data races but not deadlocks

### 2. **Custom Deadlock Detection with Timeouts**
- **Use Case**: Integration tests and specific deadlock scenarios
- **Implementation**: See `TestDeadlockScenarioWithTimeout` in deadlock_detection_test.go
- **Effectiveness**: Good for known problematic patterns

### 3. **go-deadlock Library**
- **Use Case**: Development and debugging phase
- **Implementation**: Replace sync.Mutex with deadlock.Mutex
- **Effectiveness**: Best for finding lock ordering issues

### 4. **Stress Testing**
- **Use Case**: Finding rare timing-dependent issues
- **Implementation**: See `TestStressWithMetrics` tests
- **Effectiveness**: Good for production-like scenarios

### 5. **Property-Based Testing**
- **Use Case**: Systematic exploration of state space
- **Implementation**: See property_based_deadlock_test.go
- **Effectiveness**: Excellent for finding edge cases

## Specific Issues Addressed

### 1. setState() Timer Lock Ordering
**Issue**: setState() holds mux while calling timer methods that need handshakeTimerMux
**Test**: `TestSetStateTimerDeadlock`
**Solution**: Release mux before timer operations or use lock-free timer state

### 2. Hub Connection Registration Race
**Issue**: Gap between checking and modifying connection map
**Test**: `TestConnectionRegistrationRace`
**Solution**: Atomic `UnregisterConnectionIfMatch` method

### 3. Timer Lifecycle Races
**Issue**: Timer state changes during concurrent operations
**Test**: `TestTimerLifecycleRaceConditions`
**Solution**: Atomic timer operations with proper state tracking

## Running the Tests

### Basic Test Execution
```bash
# Run all deadlock detection tests with race detector
go test -race -v -run "Deadlock|Race" ./...

# Run specific test suites
go test -race -v -run TestSetStateTimerDeadlock ./ship
go test -race -v -run TestHubMutexOrderingDeadlock ./hub
go test -race -v -run TestPropertyBased ./ship

# Run with timeout detection
go test -race -v -timeout 30s -run "Deadlock" ./...
```

### Stress Testing
```bash
# Run stress tests with extended duration
go test -race -v -run TestStress -timeout 5m ./...

# Run with CPU profiling to identify contention
go test -race -v -run TestStress -cpuprofile=cpu.prof ./ship
go tool pprof cpu.prof
```

### Continuous Integration
```bash
# CI-friendly command with coverage
go test -race -v -coverprofile=coverage.out -covermode=atomic \
  -run "Deadlock|Race|Stress" ./...
```

## Interpreting Results

### Deadlock Indicators
1. **Test Timeout**: Tests hanging indicate potential deadlock
2. **High Contention**: Metric showing >1% contention suggests lock conflicts
3. **Race Detector Output**: Data race warnings may indicate improper synchronization
4. **Inconsistent State**: Final state not matching expected indicates race conditions

### Metrics to Monitor
- **Operation Latency**: Operations taking >10ms indicate contention
- **Success Rates**: Operations failing due to state conflicts
- **Contention Events**: Frequency of lock contention
- **State Consistency**: Invariant violations

## Integration Recommendations

### 1. **Immediate Actions**
- Add deadlock tests to CI pipeline
- Enable race detector for all tests
- Monitor test execution times

### 2. **Development Workflow**
```bash
# Pre-commit hook
#!/bin/bash
go test -race -short -run "Deadlock|Race" ./...
```

### 3. **Production Monitoring**
- Add mutex contention metrics
- Monitor goroutine counts
- Track operation latencies

## Cost-Benefit Analysis

### Minimum Effective Test Set
1. **TestSetStateTimerDeadlock** - Critical path deadlock
2. **TestConnectionRegistrationRace** - Data loss scenario  
3. **TestStressWithMetrics** - Production-like load
4. **TestPropertyBasedDeadlockDetection** - Edge case discovery

### Resource Requirements
- **CI Time**: +2-3 minutes per test run
- **Memory**: 2-3x normal test memory with race detector
- **Development Time**: 1-2 days initial implementation

### Expected Benefits
- **Bug Prevention**: Catch deadlocks before production
- **Performance**: Identify contention bottlenecks
- **Reliability**: Ensure concurrent operations are safe
- **Confidence**: Refactor with safety net

## Advanced Techniques

### 1. **Deadlock Injection**
```go
// Force specific timing to reproduce deadlocks
runtime.Gosched() // Yield to other goroutines
time.Sleep(time.Microsecond) // Add controlled delays
```

### 2. **Lock Ordering Verification**
```go
// Use go-deadlock with custom configuration
deadlock.Opts.DeadlockTimeout = 100 * time.Millisecond
deadlock.Opts.MaxMapSize = 10000
```

### 3. **Goroutine Leak Detection**
```go
// Check goroutine count before/after tests
before := runtime.NumGoroutine()
// Run test
after := runtime.NumGoroutine()
assert.Equal(t, before, after, "Goroutine leak detected")
```

## Troubleshooting

### Common Issues
1. **Flaky Tests**: Increase timeouts or use deterministic scheduling
2. **False Positives**: Verify actual deadlock vs slow operations
3. **Race Detector Overhead**: Use -short flag for quick checks
4. **CI Timeouts**: Split tests into parallel jobs

### Debug Commands
```bash
# Generate blocking profile
go test -race -blockprofile=block.prof -v ./...
go tool pprof block.prof

# Trace execution
go test -race -trace=trace.out -v -run TestSpecific ./...
go tool trace trace.out
```

## Conclusion

These deadlock detection tests provide comprehensive coverage of known concurrency issues in ship-go. Regular execution and monitoring of these tests will significantly improve the reliability and performance of concurrent operations.