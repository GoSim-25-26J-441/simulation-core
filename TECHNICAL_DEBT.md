# Technical Debt and TODO Items

This document tracks technical debt, incomplete features, and TODO items in the simulation-core codebase.

## High Priority

### 1. Bursty Workload Implementation
**Location**: `internal/simd/workload_state.go:339`
**Status**: Partially implemented (currently uses Poisson distribution)
**Description**: The bursty workload pattern is currently an alias for Poisson distribution. Full implementation should include:
- Burst periods with high arrival rates
- Idle periods with low/no arrivals
- Configurable burst duration and idle duration
- Smooth transitions between states

**Impact**: Medium - Bursty workloads are a common real-world pattern

---

### 2. Deprecated Metrics API Cleanup
**Location**: `internal/simd/handlers.go:398`
**Status**: Deprecated code still in use
**Description**: `rm.RecordLatency` should be removed once all metrics consumers have migrated to the new collector-based metrics pipeline.

**Impact**: Low - Code works but has technical debt

---

### 3. Host-Level Resource Aggregation
**Location**: `internal/resource/host.go:99,106`
**Status**: Placeholder methods
**Description**: 
- `GetCPUUtilization()` - Should aggregate CPU utilization from all service instances on the host
- `GetMemoryUtilization()` - Should aggregate memory utilization from all service instances on the host

Currently, utilization is tracked at the Manager level, but host-level aggregation would be useful for:
- Host capacity planning
- Resource allocation optimization
- Multi-host scenarios

**Impact**: Medium - Useful for multi-host scenarios

---

## Medium Priority

### 4. gRPC Server Security
**Location**: `cmd/simd/main.go:36`
**Status**: Security not configured
**Description**: Production deployment requires:
- TLS/SSL encryption
- Authentication (e.g., mTLS, API keys, OAuth)
- Rate limiting per client
- Request validation

**Impact**: High for production, Low for development/testing

---

### 5. API Package Placeholders
**Location**: `api/handlers.go`, `api/schemas.go`
**Status**: Empty placeholder files
**Description**: These files contain TODO comments but no implementation. Options:
- Remove if not needed (API is implemented in `internal/simd/`)
- Implement if intended for future API versioning or separation

**Impact**: Low - Currently unused

---

## Documentation Updates

### 6. Dynamic Configuration Documentation
**Location**: `docs/DYNAMIC_CONFIGURATION.md`
**Status**: Outdated
**Description**: Document still says "NOT SUPPORTED" but dynamic configuration has been implemented. Should be updated to:
- Reflect current implementation status
- Document the continuous event generation approach
- Update examples and limitations

**Impact**: Medium - Misleading documentation

---

### 7. README.md Status Updates
**Location**: `README.md:48,343,349`
**Status**: Mentions "integration pending" for autoscaling and retry policies
**Description**: Need to verify and update status:
- Autoscaling policy integration status
- Retry policy integration status
- Any other outdated status mentions

**Impact**: Low - Documentation accuracy

---

## Completed Items

- ✅ Dynamic configuration and request rate changes (implemented in `feat/dynamic-configuration` branch)
- ✅ Continuous event generation for workload patterns
- ✅ HTTP and gRPC endpoints for dynamic workload updates

---

## Recommendations

1. **Immediate**: Update documentation (items 6-7) to reflect current state
2. **Short-term**: Implement bursty workload logic (item 1) - commonly requested feature
3. **Short-term**: Complete host-level resource aggregation (item 3) - useful for multi-host scenarios
4. **Medium-term**: Add gRPC security (item 4) - required for production
5. **Long-term**: Clean up deprecated code (item 2) - low priority but improves code quality

---

## Notes

- All items are tracked in this document for visibility
- Priority is based on impact and usage frequency
- Some items may be deferred if not critical for current use cases

