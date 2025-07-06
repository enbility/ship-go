# ship-go Makefile
# Development and testing commands for the SHIP protocol implementation

.PHONY: help build test test-race test-deadlock test-stress test-all lint fmt clean deps benchmark profile

# Default target
.DEFAULT_GOAL := help

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOLINT=golangci-lint

# Test parameters
TIMEOUT_SHORT=30s
TIMEOUT_LONG=60s
BENCHTIME=1s

# Coverage
COVERAGE_DIR=coverage
COVERAGE_FILE=$(COVERAGE_DIR)/coverage.out
COVERAGE_HTML=$(COVERAGE_DIR)/coverage.html

help: ## Show this help message
	@echo 'Usage: make <target>'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

## Build targets
build: ## Build the project
	$(GOBUILD) -v ./...

clean: ## Clean build artifacts
	$(GOCLEAN)
	rm -rf $(COVERAGE_DIR)
	rm -f results.sarif

## Dependency management
deps: ## Download and tidy dependencies
	$(GOGET) -v ./...
	$(GOMOD) tidy

deps-deadlock: ## Install deadlock detection dependency
	$(GOGET) github.com/sasha-s/go-deadlock

## Formatting and linting
fmt: ## Format code
	$(GOCMD) fmt ./...

lint: ## Run linter
	$(GOLINT) run --timeout=3m ./...

## Standard testing
test: ## Run standard tests
	$(GOTEST) -v ./...

test-race: ## Run tests with race detection
	$(GOTEST) -race -v ./...

test-short: ## Run short tests only
	$(GOTEST) -short -v ./...

## Concurrency and deadlock testing
test-deadlock: deps-deadlock ## Run deadlock detection tests
	@echo "Running deadlock detection tests..."
	$(GOTEST) -race -tags=deadlock -timeout=$(TIMEOUT_SHORT) ./ship ./hub

test-stress: ## Run stress tests (may take longer)
	@echo "Running stress tests..."
	$(GOTEST) -race -tags=stress -timeout=$(TIMEOUT_LONG) ./ship ./hub

test-concurrency: test-race test-deadlock ## Run all concurrency tests
	@echo "All concurrency tests completed"

test-deadlock-specific: deps-deadlock ## Run specific deadlock-prone tests
	@echo "Running specific deadlock detection tests..."
	$(GOTEST) -race -tags=deadlock -timeout=$(TIMEOUT_SHORT) -run="TestSetStateTimerDeadlock|TestStateTimerConsistency|TestTimerLifecycleDeadlock" ./ship
	$(GOTEST) -race -tags=deadlock -timeout=$(TIMEOUT_SHORT) -run="TestHubMutexOrderingDeadlock|TestConnectionRegistrationRace|TestAtomicUnregisterIfMatch" ./hub

test-race-verbose: ## Run race detection with detailed output
	$(GOTEST) -race -v -timeout=$(TIMEOUT_SHORT) -run=".*Race.*|.*Concurrent.*|.*Timer.*" ./...

## Comprehensive testing
test-all: test-race test-deadlock test-stress ## Run all tests (comprehensive)
	@echo "All tests completed successfully!"

test-ci: ## Run CI-equivalent tests locally
	$(GOTEST) -race -v -coverprofile=coverage_temp.out -covermode=atomic ./...
	$(GOTEST) -race -tags=deadlock -timeout=$(TIMEOUT_SHORT) -v ./ship ./hub
	$(GOTEST) -race -tags=stress -timeout=$(TIMEOUT_LONG) ./ship ./hub

## Coverage testing
test-coverage: ## Run tests with coverage
	@mkdir -p $(COVERAGE_DIR)
	$(GOTEST) -race -coverprofile=$(COVERAGE_FILE) -covermode=atomic ./...
	@grep -v "/ship-go/mocks/" $(COVERAGE_FILE) > $(COVERAGE_FILE).tmp && mv $(COVERAGE_FILE).tmp $(COVERAGE_FILE)

coverage-html: test-coverage ## Generate HTML coverage report
	$(GOCMD) tool cover -html=$(COVERAGE_FILE) -o $(COVERAGE_HTML)
	@echo "Coverage report generated: $(COVERAGE_HTML)"

coverage-func: test-coverage ## Show function coverage
	$(GOCMD) tool cover -func=$(COVERAGE_FILE)

## Performance testing
benchmark: ## Run performance benchmarks
	@echo "Running performance benchmarks..."
	$(GOTEST) -bench=BenchmarkSetState -benchtime=$(BENCHTIME) ./ship
	$(GOTEST) -bench=BenchmarkConnectionForSKI -benchtime=$(BENCHTIME) ./hub
	$(GOTEST) -bench=BenchmarkNumberPairedServices -benchtime=$(BENCHTIME) ./hub

benchmark-all: ## Run all benchmarks
	$(GOTEST) -bench=. -benchtime=$(BENCHTIME) ./...

benchmark-memory: ## Run benchmarks with memory allocation stats
	$(GOTEST) -bench=. -benchmem ./ship ./hub

benchmark-contention: ## Run lock contention benchmarks
	$(GOTEST) -bench=BenchmarkHighContention -benchtime=500ms ./ship ./hub
	$(GOTEST) -bench=BenchmarkMixedReadWrite -benchtime=500ms ./hub

## Profiling
profile-cpu: ## Generate CPU profile
	$(GOTEST) -bench=BenchmarkSetState -cpuprofile=cpu.prof ./ship
	@echo "CPU profile generated: cpu.prof"
	@echo "View with: go tool pprof cpu.prof"

profile-mem: ## Generate memory profile
	$(GOTEST) -bench=BenchmarkSetState -memprofile=mem.prof ./ship
	@echo "Memory profile generated: mem.prof"  
	@echo "View with: go tool pprof mem.prof"

## Security
security: ## Run security scanner
	gosec -no-fail -fmt sarif -out results.sarif ./...

## Development workflows
dev-test: fmt lint test-race ## Quick development test cycle
	@echo "Development test cycle completed"

pre-commit: fmt lint test-all coverage-func ## Pre-commit validation
	@echo "Pre-commit checks passed"

pre-pr: pre-commit benchmark ## Pre-pull request validation
	@echo "Ready for pull request"

## Debugging and troubleshooting
debug-deadlock: deps-deadlock ## Debug deadlock issues with verbose output
	$(GOTEST) -race -tags=deadlock -timeout=$(TIMEOUT_SHORT) -v -run=".*Deadlock.*" ./...

debug-race: ## Debug race conditions with multiple runs
	@echo "Running race detection multiple times..."
	for i in 1 2 3 4 5; do \
		echo "Run $$i:"; \
		$(GOTEST) -race -timeout=$(TIMEOUT_SHORT) -count=1 ./ship ./hub || exit 1; \
	done

debug-contention: ## Debug lock contention issues
	GOMAXPROCS=8 $(GOTEST) -race -timeout=$(TIMEOUT_SHORT) -run=".*Contention.*|.*Stress.*" ./...

## Quick aliases for common tasks
quick-test: test-race ## Quick test with race detection (alias)

deadlock: test-deadlock ## Run deadlock tests (short alias)

stress: test-stress ## Run stress tests (short alias)

bench: benchmark ## Run benchmarks (short alias)

## Environment setup
setup: deps deps-deadlock ## Initial project setup
	@echo "Installing additional tools..."
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "Installing golangci-lint..."; \
		curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $$(go env GOPATH)/bin; \
	}
	@command -v gosec >/dev/null 2>&1 || { \
		echo "Installing gosec..."; \
		go install github.com/securecodewarrior/gosec/v2/cmd/gosec@latest; \
	}
	@echo "Setup completed successfully!"

## Docker targets (if needed)
docker-test: ## Run tests in Docker environment
	docker run --rm -v $$(pwd):/workspace -w /workspace golang:1.22 make test-all

## Multi-core testing
test-multicore: ## Test with different GOMAXPROCS values
	@echo "Testing with different core counts..."
	@for cores in 1 2 4 8; do \
		echo "Testing with GOMAXPROCS=$$cores"; \
		GOMAXPROCS=$$cores $(GOTEST) -race -timeout=$(TIMEOUT_SHORT) ./ship ./hub || exit 1; \
	done

## Documentation
docs-serve: ## Serve documentation locally (if godoc is available)
	@command -v godoc >/dev/null 2>&1 && godoc -http=:6060 || echo "Install godoc: go install golang.org/x/tools/cmd/godoc@latest"

## Statistics
stats: ## Show project statistics
	@echo "=== Project Statistics ==="
	@echo "Go files: $$(find . -name '*.go' -not -path './mocks/*' | wc -l)"
	@echo "Test files: $$(find . -name '*_test.go' -not -path './mocks/*' | wc -l)"
	@echo "Lines of code: $$(find . -name '*.go' -not -path './mocks/*' -exec cat {} \; | wc -l)"
	@echo "Dependencies: $$(go list -m all | wc -l)"

## Help for common issues
troubleshoot: ## Show troubleshooting tips
	@echo "=== Troubleshooting Tips ==="
	@echo ""
	@echo "Deadlock issues:"
	@echo "  make debug-deadlock     # Run deadlock tests with verbose output"
	@echo "  make test-multicore     # Test with different core counts"
	@echo ""
	@echo "Race conditions:"
	@echo "  make debug-race         # Run race detection multiple times"
	@echo "  make test-race-verbose  # Verbose race testing"
	@echo ""
	@echo "Performance issues:"
	@echo "  make benchmark-contention  # Check lock contention"
	@echo "  make profile-cpu          # Generate CPU profile"
	@echo ""
	@echo "CI/CD simulation:"
	@echo "  make test-ci            # Run exactly what CI runs"
	@echo ""
	@echo "For more help: make help"