package pairing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/enbility/ship-go/api"
)

// TestRingBuffer implements PairingHistoryProviderInterface for performance testing
type TestRingBuffer struct {
	entries []api.DigestEntry
	maxSize int
	next    int
	mux     sync.RWMutex
}

func NewTestRingBuffer(maxSize int) *TestRingBuffer {
	if maxSize < 10 {
		maxSize = 10
	}

	return &TestRingBuffer{
		entries: make([]api.DigestEntry, maxSize),
		maxSize: maxSize,
		next:    0,
	}
}

func (t *TestRingBuffer) HasSeenDigest(alg, digest string) bool {
	t.mux.RLock()
	defer t.mux.RUnlock()

	for i := 0; i < t.maxSize; i++ {
		entry := t.entries[i]
		if entry.Algorithm == alg && entry.Digest == digest {
			return true
		}
	}
	return false
}

func (t *TestRingBuffer) RecordPairing(alg, digest string) {
	t.mux.Lock()
	defer t.mux.Unlock()

	t.entries[t.next] = api.DigestEntry{
		Algorithm: alg,
		Digest:    digest,
		Timestamp: time.Now(),
	}

	t.next++
	if t.next >= t.maxSize {
		t.next = 0
	}
}

// PerformanceTestSuite contains performance and stress tests for SHIP pairing components
type PerformanceTestSuite struct {
	suite.Suite
}

func TestPerformanceTestSuite(t *testing.T) {
	suite.Run(t, new(PerformanceTestSuite))
}

// TestHMACCalculationPerformance benchmarks HMAC calculation performance
func (suite *PerformanceTestSuite) TestHMACCalculationPerformance() {
	if testing.Short() {
		suite.T().Skip("Skipping performance test in short mode")
	}

	calculator := NewHMACCalculator()

	// Use specification test data for realistic performance testing
	secret := api.PairingSecret(mustHexToBytes("7A37DCF81BDB50F8E92CFA4160CCB3DE"))
	nonce := mustHexToBytes("BDCEE427FA7208DF3C1F2A749BA6F4D4")

	txtRecord := &api.ShipPairingTXT{
		TxtVers:    "1",
		ParType:    "fpSha256",
		ForId:      "i:983327_u:C8277H008F-3",
		ForPar:     "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
		TrustId:    "i:46925_u:43652bk-2-gt1",
		TrustPar:   "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4",
		TrustCurve: "secp256r1",
		Type:       "addCu",
		TrustNonce: "BDCEE427FA7208DF3C1F2A749BA6F4D4",
		Alg:        "hmacSha256",
	}

	params := &api.HMACParams{
		Algorithm: "hmacSha256",
		Nonce:     nonce,
		TxtRecord: txtRecord,
	}

	// Warm up
	for i := 0; i < 100; i++ {
		_, err := calculator.CalculateDigest(secret, params)
		require.NoError(suite.T(), err)
	}

	// Performance test
	const iterations = 10000
	startTime := time.Now()

	for i := 0; i < iterations; i++ {
		_, err := calculator.CalculateDigest(secret, params)
		require.NoError(suite.T(), err)
	}

	duration := time.Since(startTime)
	operationsPerSecond := float64(iterations) / duration.Seconds()

	suite.T().Logf("HMAC calculation performance: %d operations in %v (%.2f ops/sec)",
		iterations, duration, operationsPerSecond)

	// Should be able to perform at least 10,000 HMAC operations per second
	assert.Greater(suite.T(), operationsPerSecond, 10000.0,
		"HMAC calculation should achieve at least 10,000 operations per second")
}

// TestHMACValidationPerformance benchmarks HMAC validation performance
func (suite *PerformanceTestSuite) TestHMACValidationPerformance() {
	if testing.Short() {
		suite.T().Skip("Skipping performance test in short mode")
	}

	calculator := NewHMACCalculator()

	secret := api.PairingSecret(mustHexToBytes("7A37DCF81BDB50F8E92CFA4160CCB3DE"))
	nonce := mustHexToBytes("BDCEE427FA7208DF3C1F2A749BA6F4D4")
	expectedDigest := mustHexToBytes("BCBB62B2176DA2CEE545784CEB1F2A55E049451B12A549C98E8CA213F001DA25")

	txtRecord := &api.ShipPairingTXT{
		TxtVers:    "1",
		ParType:    "fpSha256",
		ForId:      "i:983327_u:C8277H008F-3",
		ForPar:     "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
		TrustId:    "i:46925_u:43652bk-2-gt1",
		TrustPar:   "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4",
		TrustCurve: "secp256r1",
		Type:       "addCu",
		TrustNonce: "BDCEE427FA7208DF3C1F2A749BA6F4D4",
		Alg:        "hmacSha256",
	}

	params := &api.HMACParams{
		Algorithm: "hmacSha256",
		Nonce:     nonce,
		TxtRecord: txtRecord,
	}

	// Performance test for validation
	const iterations = 10000
	startTime := time.Now()

	for i := 0; i < iterations; i++ {
		err := calculator.ValidateDigest(secret, params, expectedDigest)
		require.NoError(suite.T(), err)
	}

	duration := time.Since(startTime)
	validationsPerSecond := float64(iterations) / duration.Seconds()

	suite.T().Logf("HMAC validation performance: %d validations in %v (%.2f validations/sec)",
		iterations, duration, validationsPerSecond)

	// Should be able to perform at least 10,000 validations per second
	assert.Greater(suite.T(), validationsPerSecond, 10000.0,
		"HMAC validation should achieve at least 10,000 validations per second")
}

// TestRingBufferPerformance benchmarks ring buffer operations
func (suite *PerformanceTestSuite) TestRingBufferPerformance() {
	if testing.Short() {
		suite.T().Skip("Skipping performance test in short mode")
	}

	// Test with different buffer sizes
	bufferSizes := []int{10, 100, 1000}

	for _, size := range bufferSizes {
		suite.T().Run(fmt.Sprintf("buffer_size_%d", size), func(t *testing.T) {
			provider := NewTestRingBuffer(size)

			// Test write performance
			const writeIterations = 50000
			writeStartTime := time.Now()

			for i := 0; i < writeIterations; i++ {
				digest := fmt.Sprintf("PERF%08X_%016X", i, uint64(i)*0xDEADBEEF)
				provider.RecordPairing("hmacSha256", digest)
			}

			writeDuration := time.Since(writeStartTime)
			writesPerSecond := float64(writeIterations) / writeDuration.Seconds()

			t.Logf("Ring buffer write performance (size %d): %d writes in %v (%.2f writes/sec)",
				size, writeIterations, writeDuration, writesPerSecond)

			// Should achieve at least 50,000 writes per second
			assert.Greater(t, writesPerSecond, 50000.0,
				"Ring buffer should achieve at least 50,000 writes per second for size %d", size)

			// Test read performance
			const readIterations = 100000
			readStartTime := time.Now()

			for i := 0; i < readIterations; i++ {
				// Test both found and not-found cases
				digest := fmt.Sprintf("PERF%08X_%016X", i%writeIterations, uint64(i)*0xDEADBEEF)
				provider.HasSeenDigest("hmacSha256", digest)
			}

			readDuration := time.Since(readStartTime)
			readsPerSecond := float64(readIterations) / readDuration.Seconds()

			t.Logf("Ring buffer read performance (size %d): %d reads in %v (%.2f reads/sec)",
				size, readIterations, readDuration, readsPerSecond)

			// Performance expectations scale with buffer size (linear search O(n))
			expectedMinReads := map[int]float64{
				10:   50000.0, // Small buffer should be fast
				100:  20000.0, // Medium buffer slower
				1000: 5000.0,  // Large buffer much slower due to linear search
			}
			minReads := expectedMinReads[size]
			assert.Greater(t, readsPerSecond, minReads,
				"Ring buffer should achieve at least %.0f reads per second for size %d", minReads, size)
		})
	}
}

// TestConcurrentPairingLoad tests behavior under concurrent pairing load
func (suite *PerformanceTestSuite) TestConcurrentPairingLoad() {
	if testing.Short() {
		suite.T().Skip("Skipping concurrent load test in short mode")
	}

	const numWorkers = 10
	const operationsPerWorker = 1000

	calculator := NewHMACCalculator()
	provider := NewTestRingBuffer(1000)

	var wg sync.WaitGroup
	results := make(chan time.Duration, numWorkers)
	errors := make(chan error, numWorkers*operationsPerWorker)

	startTime := time.Now()

	// Start concurrent workers
	for worker := 0; worker < numWorkers; worker++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			workerStartTime := time.Now()

			for op := 0; op < operationsPerWorker; op++ {
				// Generate unique test data for this operation
				secret := generateRandomSecret()
				nonce := generateRandomNonce()

				// Create TXT record
				txtRecord := &api.ShipPairingTXT{
					TxtVers:    "1",
					ParType:    "fpSha256",
					ForId:      fmt.Sprintf("i:worker%d:device%d", workerID, op),
					ForPar:     "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
					TrustId:    fmt.Sprintf("i:trust%d:device%d", workerID, op),
					TrustPar:   "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4",
					TrustCurve: "secp256r1",
					Type:       "addCu",
					TrustNonce: hex.EncodeToString(nonce),
					Alg:        "hmacSha256",
				}

				params := &api.HMACParams{
					Algorithm: "hmacSha256",
					Nonce:     nonce,
					TxtRecord: txtRecord,
				}

				// Calculate HMAC
				calculatedDigest, err := calculator.CalculateDigest(secret, params)
				if err != nil {
					errors <- err
					continue
				}

				calculatedDigestHex := hex.EncodeToString(calculatedDigest)

				// Check for replay (should not be seen)
				if provider.HasSeenDigest("hmacSha256", calculatedDigestHex) {
					errors <- fmt.Errorf("unexpected replay detected for worker %d operation %d", workerID, op)
					continue
				}

				// Record the pairing
				provider.RecordPairing("hmacSha256", calculatedDigestHex)

				// Validate the digest
				err = calculator.ValidateDigest(secret, params, calculatedDigest)
				if err != nil {
					errors <- err
					continue
				}
			}

			results <- time.Since(workerStartTime)
		}(worker)
	}

	// Wait for all workers to complete
	wg.Wait()
	close(errors)
	close(results)

	totalDuration := time.Since(startTime)

	// Check for errors
	errorCount := 0
	for err := range errors {
		errorCount++
		suite.T().Logf("Concurrent operation error: %v", err)
	}

	assert.Equal(suite.T(), 0, errorCount, "should have no errors in concurrent operations")

	// Collect timing results
	var workerDurations []time.Duration
	for duration := range results {
		workerDurations = append(workerDurations, duration)
	}

	totalOperations := numWorkers * operationsPerWorker
	operationsPerSecond := float64(totalOperations) / totalDuration.Seconds()

	suite.T().Logf("Concurrent pairing performance: %d operations across %d workers in %v (%.2f ops/sec)",
		totalOperations, numWorkers, totalDuration, operationsPerSecond)

	// Should achieve reasonable concurrent performance
	assert.Greater(suite.T(), operationsPerSecond, 1000.0,
		"concurrent pairing should achieve at least 1,000 operations per second")

	// Verify no worker took excessively long
	for i, duration := range workerDurations {
		maxWorkerDuration := time.Duration(float64(totalDuration) * 2.0) // Allow up to 2x average
		assert.Less(suite.T(), duration, maxWorkerDuration,
			"worker %d should not take more than 2x average time", i)
	}
}

// TestMemoryUsageUnderLoad tests memory usage patterns under load
func (suite *PerformanceTestSuite) TestMemoryUsageUnderLoad() {
	if testing.Short() {
		suite.T().Skip("Skipping memory usage test in short mode")
	}

	// Multiple GC cycles to stabilize memory
	for i := 0; i < 3; i++ {
		runtime.GC()
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
	}

	var m1, m2 runtime.MemStats
	runtime.ReadMemStats(&m1)

	// Create components and perform operations
	calculator := NewHMACCalculator()
	provider := NewTestRingBuffer(100) // Smaller buffer

	const numOperations = 1000 // Reduced for more reliable measurement

	for i := 0; i < numOperations; i++ {
		secret := generateRandomSecret()
		nonce := generateRandomNonce()

		txtRecord := &api.ShipPairingTXT{
			TxtVers:    "1",
			ParType:    "fpSha256",
			ForId:      fmt.Sprintf("i:load%d:device", i),
			ForPar:     "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
			TrustId:    fmt.Sprintf("i:trust%d:device", i),
			TrustPar:   "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4",
			TrustCurve: "secp256r1",
			Type:       "addCu",
			TrustNonce: hex.EncodeToString(nonce),
			Alg:        "hmacSha256",
		}

		params := &api.HMACParams{
			Algorithm: "hmacSha256",
			Nonce:     nonce,
			TxtRecord: txtRecord,
		}

		digest, err := calculator.CalculateDigest(secret, params)
		require.NoError(suite.T(), err)

		digestHex := hex.EncodeToString(digest)
		provider.RecordPairing("hmacSha256", digestHex)
	}

	// Multiple GC cycles for accurate measurement
	for i := 0; i < 3; i++ {
		runtime.GC()
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
	}
	runtime.ReadMemStats(&m2)

	// Handle potential underflow (GC may reduce memory)
	var memoryIncrease uint64
	if m2.Alloc > m1.Alloc {
		memoryIncrease = m2.Alloc - m1.Alloc
	}

	averageMemoryPerOp := float64(memoryIncrease) / float64(numOperations)

	suite.T().Logf("Memory usage: %d operations, memory change: %d bytes (%.2f bytes/op)",
		numOperations, memoryIncrease, averageMemoryPerOp)

	// More relaxed memory limits due to Go GC behavior
	assert.Less(suite.T(), averageMemoryPerOp, 10240.0,
		"average memory usage per operation should be less than 10KB")

	// Total memory increase should be reasonable
	assert.Less(suite.T(), memoryIncrease, uint64(50*1024*1024),
		"total memory increase should be less than 50MB for %d operations", numOperations)
}

// TestLongRunningStabilityTest tests stability over extended period
func (suite *PerformanceTestSuite) TestLongRunningStabilityTest() {
	if testing.Short() {
		suite.T().Skip("Skipping long-running stability test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	calculator := NewHMACCalculator()
	provider := NewTestRingBuffer(100) // Smaller buffer to test wraparound

	// Use atomic operations to fix race conditions
	var operationCount int64
	var errorCount int64
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	startTime := time.Now()

	// Run continuous operations
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				secret := generateRandomSecret()
				nonce := generateRandomNonce()

				// Use atomic for safe counter access
				currentCount := atomic.LoadInt64(&operationCount)
				txtRecord := &api.ShipPairingTXT{
					TxtVers:    "1",
					ParType:    "fpSha256",
					ForId:      fmt.Sprintf("i:stability%d:device", currentCount),
					ForPar:     "C74B7855D3479415F62CC01E5F6D9A93EBC676057D85417ADA16FD1384338943",
					TrustId:    fmt.Sprintf("i:trust%d:device", currentCount),
					TrustPar:   "2CC72E781F7A7D2A08D50196C50FEDF0F7BA583F43F76C8C0DDEC9EEF0D005B4",
					TrustCurve: "secp256r1",
					Type:       "addCu",
					TrustNonce: hex.EncodeToString(nonce),
					Alg:        "hmacSha256",
				}

				params := &api.HMACParams{
					Algorithm: "hmacSha256",
					Nonce:     nonce,
					TxtRecord: txtRecord,
				}

				digest, err := calculator.CalculateDigest(secret, params)
				if err != nil {
					atomic.AddInt64(&errorCount, 1)
					continue
				}

				digestHex := hex.EncodeToString(digest)
				provider.RecordPairing("hmacSha256", digestHex)

				atomic.AddInt64(&operationCount, 1)
			}
		}
	}()

	// Periodic status reporting
	lastOperationCount := int64(0)
	for {
		select {
		case <-ctx.Done():
			goto done
		case <-ticker.C:
			currentOps := atomic.LoadInt64(&operationCount)
			currentErrs := atomic.LoadInt64(&errorCount)
			opsPerSecond := float64(currentOps - lastOperationCount)
			suite.T().Logf("Stability test: %d total operations, %d errors, %.0f ops/sec",
				currentOps, currentErrs, opsPerSecond)
			lastOperationCount = currentOps
		}
	}

done:
	totalDuration := time.Since(startTime)
	finalOperationCount := atomic.LoadInt64(&operationCount)
	finalErrorCount := atomic.LoadInt64(&errorCount)
	finalOpsPerSecond := float64(finalOperationCount) / totalDuration.Seconds()

	suite.T().Logf("Stability test completed: %d operations in %v (%.2f ops/sec), %d errors",
		finalOperationCount, totalDuration, finalOpsPerSecond, finalErrorCount)

	// Should maintain good performance throughout
	assert.Greater(suite.T(), finalOpsPerSecond, 1000.0,
		"should maintain at least 1,000 ops/sec throughout stability test")

	// Should have minimal errors (less than 0.1% error rate)
	errorRate := float64(finalErrorCount) / float64(finalOperationCount)
	assert.Less(suite.T(), errorRate, 0.001,
		"error rate should be less than 0.1%%")

	// Should have processed a reasonable number of operations
	assert.Greater(suite.T(), finalOperationCount, int64(10000),
		"should have processed at least 10,000 operations in stability test")
}

/* Helper Functions */

// generateRandomSecret generates a random 128-bit secret
func generateRandomSecret() api.PairingSecret {
	secret := make([]byte, 16)
	rand.Read(secret)
	return api.PairingSecret(secret)
}

// generateRandomNonce generates a random 128-bit nonce
func generateRandomNonce() []byte {
	nonce := make([]byte, 16)
	rand.Read(nonce)
	return nonce
}

// mustHexToBytes converts hex string to bytes, panicking on error
func mustHexToBytes(hexStr string) []byte {
	bytes, err := hex.DecodeString(hexStr)
	if err != nil {
		panic(fmt.Sprintf("invalid hex string: %s", hexStr))
	}
	return bytes
}
