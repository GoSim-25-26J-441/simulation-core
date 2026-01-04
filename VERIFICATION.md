# Simulator Verification Guide

This guide provides steps to verify that the simulator is functional after the interaction package integration.

## Quick Verification

### 1. Run All Tests

```bash
# Run all unit tests
go test ./... -short

# Run integration tests (requires integration tag)
go test ./test/integration/... -tags=integration -v

# Run all tests including long-running ones
go test ./...
```

**Expected Result**: All tests should pass (PASS)

### 2. Build the Simulator

```bash
# Build the main daemon
go build ./cmd/simd/...

# Verify it compiles without errors
go build ./...
```

**Expected Result**: No compilation errors

### 3. Run End-to-End Integration Tests

```bash
# Run E2E tests for interaction package
go test ./test/integration/... -tags=integration -v -run TestE2E
```

**Expected Result**: 
- ✓ Interaction package integration test passed
- ✓ Graph validation test passed  
- ✓ Engine integration test passed

## Comprehensive Verification

### Test Categories

1. **Unit Tests** (`go test ./... -short`)
   - All packages should have passing unit tests
   - Key packages: `interaction`, `simd`, `engine`, `resource`, `metrics`, `policy`

2. **Integration Tests** (`go test ./test/integration/... -tags=integration`)
   - End-to-end scenario execution
   - Interaction package integration
   - Graph validation and cycle detection

3. **Component Tests**
   - Interaction package: Service graph, downstream calls, branching
   - Handlers: Request lifecycle, downstream call handling
   - Executor: Full simulation runs

## Verification Checklist

- [ ] All unit tests pass (`go test ./... -short`)
- [ ] All integration tests pass (`go test ./test/integration/... -tags=integration`)
- [ ] Code compiles without errors (`go build ./...`)
- [ ] No linting errors (`golangci-lint run` or `gofmt -l .` returns empty)
- [ ] Interaction package tests pass (`go test ./internal/interaction/...`)
- [ ] Handler tests pass (`go test ./internal/simd/...`)
- [ ] E2E tests pass (`go test ./test/integration/... -tags=integration -run TestE2E`)

## Key Functionality to Verify

### 1. Interaction Package
- ✅ Service graph creation from scenario
- ✅ Downstream call resolution
- ✅ Cycle detection in service graphs
- ✅ Branching probability strategies
- ✅ Call semantics (sync/async)

### 2. Handler Integration
- ✅ Request arrival handling
- ✅ Request processing with resource allocation
- ✅ Downstream call scheduling
- ✅ Request completion and metrics recording

### 3. Full Simulation Flow
- ✅ Scenario parsing and validation
- ✅ Resource manager initialization
- ✅ Workload scheduling
- ✅ Event processing
- ✅ Metrics collection
- ✅ Run completion

## Running a Manual Test

You can manually test the simulator using the example scenario:

```bash
# If you have a CLI tool
./simd --scenario config/scenario.yaml --duration 10s

# Or via gRPC/HTTP API (if server is running)
curl -X POST http://localhost:8080/api/v1/runs \
  -H "Content-Type: application/json" \
  -d '{"scenario_yaml": "...", "duration_ms": 10000}'
```

## Troubleshooting

### If tests fail:

1. **Check for compilation errors**:
   ```bash
   go build ./...
   ```

2. **Check for formatting issues**:
   ```bash
   gofmt -l .
   gofmt -w .  # Fix formatting
   ```

3. **Check for linting errors**:
   ```bash
   golangci-lint run
   ```

4. **Run tests with verbose output**:
   ```bash
   go test ./... -v
   ```

5. **Check specific package**:
   ```bash
   go test ./internal/interaction/... -v
   go test ./internal/simd/... -v
   ```

### Common Issues

- **Import errors**: Ensure all dependencies are installed (`go mod tidy`)
- **Test failures**: Check test output for specific error messages
- **Compilation errors**: Verify Go version compatibility (requires Go 1.21+)

## Performance Verification

For performance testing:

```bash
# Run benchmarks
go test ./... -bench=. -benchmem

# Run with race detector
go test ./... -race
```

## Continuous Integration

The simulator should pass all checks in CI:
- Unit tests
- Integration tests  
- Linting
- Build verification
- Code coverage (if configured)

---

**Last Verified**: After interaction package integration
**Status**: ✅ All tests passing

