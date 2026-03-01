# Dynamic Configuration and Request Rate Changes

## Current Status: ✅ SUPPORTED

**Important**: The simulator now supports dynamic request rate adjustments during simulation execution through continuous event generation.

---

## Supported Features

### 1. Dynamic Request Rates ✅
- ✅ **Can change request rates** during simulation execution
- ✅ **Continuous event generation** - events are generated on-demand based on current rates
- ✅ **Real-time rate updates** via HTTP and gRPC APIs
- ✅ **Thread-safe updates** - rate changes are applied safely during simulation

### 2. API Endpoints ✅

The following endpoints are available:

- **HTTP**: `PATCH /v1/runs/{run_id}/workload` - Update workload rate or pattern
- **gRPC**: `UpdateWorkloadRate` - Update workload rate (see proto definition)

**Example Request:**
```json
PATCH /v1/runs/abc123/workload
{
  "pattern_key": "client:svc1:/test",
  "rate_rps": 50.0  // Changed from 10 RPS to 50 RPS
}
```

Or update the entire pattern:
```json
PATCH /v1/runs/abc123/workload
{
  "pattern_key": "client:svc1:/test",
  "pattern": {
    "from": "client",
    "to": "svc1:/test",
    "arrival": {
      "type": "poisson",
      "rate_rps": 50.0
    }
  }
}
```

### 3. How It Works

**Current Architecture:**
```
Create Run → Parse Scenario → Start Continuous Event Generation → Execute Events
                                        ↓
                              (Ongoing event generation with dynamic rates)
                                        ↓
                              (Rate updates via API)
```

**Implementation Details:**

1. **Continuous Event Generation**: A background goroutine (`WorkloadState`) continuously generates arrival events based on current rates
2. **Rate Management**: Current rates are tracked per workload pattern with thread-safe updates
3. **Event Scheduling**: Events are scheduled up to 1 second ahead (lookahead window) to ensure smooth execution
4. **Dynamic Updates**: Rate changes take effect immediately and affect future event generation

---

## Implementation Details

### Continuous Event Generation

The implementation uses a background goroutine that:

1. **Tracks Workload Patterns**: Maintains state for each workload pattern (`WorkloadPatternState`)
2. **Generates Events Continuously**: Uses a ticker (100ms interval) to check and generate new events
3. **Maintains Lookahead Window**: Schedules events up to 1 second ahead of current simulation time
4. **Supports Rate Updates**: Updates rates dynamically via thread-safe mutex-protected operations

**Architecture:**
```
┌─────────────────────┐
│  WorkloadState     │
│  (Event Generation) │
│                     │
│  - Tracks rates     │
│  - Generates events │
│  - Schedules to     │
│    event queue      │
└──────────┬──────────┘
           │
           ▼
┌─────────────────────┐
│   Event Queue       │
│   (Priority Queue)  │
└──────────┬──────────┘
           │
           ▼
┌─────────────────────┐
│  Simulation Engine  │
│   (Event Loop)      │
└─────────────────────┘
```

### Thread Safety

- `WorkloadState` uses `sync.RWMutex` for concurrent access
- Each `WorkloadPatternState` has its own mutex for pattern-specific updates
- Rate updates are atomic and don't interfere with event generation

### Limitations

1. **Configuration Changes**: Full scenario configuration changes (e.g., service replicas, policies) are not yet supported - only workload rates/patterns
2. **Bursty Workloads**: Bursty workload pattern is currently an alias for Poisson distribution (full bursty logic with burst/idle periods is TODO)
3. **Event Cancellation**: Pre-scheduled events are not cancelled when rates change - only future events use the new rate

---

## Frontend Integration Example

With dynamic rates implemented, the frontend could:

```javascript
// Frontend slider component
const RateSlider = ({ runId, initialRate }) => {
    const [rate, setRate] = useState(initialRate);
    
    const handleRateChange = async (newRate) => {
        setRate(newRate);
        
        // Update rate on backend
        await fetch(`http://localhost:8080/v1/runs/${runId}/workload`, {
            method: 'PATCH',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                workload_patterns: [{
                    from: 'client',
                    to: 'svc1:/test',
                    arrival: {
                        type: 'poisson',
                        rate_rps: newRate
                    }
                }]
            })
        });
    };
    
    return (
        <input
            type="range"
            min="0"
            max="1000"
            value={rate}
            onChange={(e) => handleRateChange(parseFloat(e.target.value))}
        />
    );
};
```

---

## Configuration Changes

For broader configuration changes (e.g., service replicas, policies), similar approach:

1. **Store Configuration State** in `RunExecutor`
2. **Add Update Endpoint**: `PATCH /v1/runs/{run_id}/configuration`
3. **Apply Changes**: Update resource manager, policy manager, etc.
4. **Thread Safety**: Ensure changes are applied safely during simulation

**Example:**
```go
// PATCH /v1/runs/{run_id}/configuration
{
  "services": [
    {
      "id": "svc1",
      "replicas": 5  // Changed from 2 to 5
    }
  ],
  "policies": {
    "autoscaling": {
      "target_cpu_util": 0.8  // Changed from 0.7
    }
  }
}
```

---

## Complexity Considerations

### Challenges

1. **Thread Safety**: Multiple goroutines accessing shared state
2. **Event Ordering**: Ensuring events remain in correct chronological order
3. **State Consistency**: Configuration changes must be applied atomically
4. **Performance**: Continuous generation vs. pre-scheduled events
5. **Determinism**: Dynamic changes may reduce simulation reproducibility

### Benefits

1. **Real-Time Exploration**: See system behavior as rates change
2. **Interactive Dashboards**: Frontend sliders for live exploration
3. **Better Understanding**: Understand system response to changes
4. **What-If Scenarios**: Test different configurations interactively

---

## Alternative: Multiple Runs with Different Rates

**Workaround**: Until dynamic configuration is implemented, you can:

1. **Create multiple runs** with different rates
2. **Run them in parallel** or sequentially
3. **Compare results** to understand system behavior at different rates
4. **Frontend**: Show comparison across multiple runs

**Example:**
```javascript
// Create multiple runs with different rates
const rates = [10, 20, 50, 100, 200]; // RPS
const runs = await Promise.all(
    rates.map(rate => createRunWithRate(rate))
);

// Start all runs
await Promise.all(runs.map(run => startRun(run.id)));

// Stream metrics from all runs
runs.forEach(run => {
    streamMetrics(run.id);
});
```

---

## Usage Examples

### HTTP API Example

```bash
# Update workload rate for a running simulation
curl -X PATCH http://localhost:8080/v1/runs/run-123/workload \
  -H "Content-Type: application/json" \
  -d '{
    "pattern_key": "client:svc1:/test",
    "rate_rps": 50.0
  }'
```

### gRPC API Example

```go
client := simulationv1.NewSimulationServiceClient(conn)
req := &simulationv1.UpdateWorkloadRateRequest{
    RunId:      "run-123",
    PatternKey: "client:svc1:/test",
    RateRps:    50.0,
}
resp, err := client.UpdateWorkloadRate(ctx, req)
```

### Frontend Integration Example

```javascript
// Frontend slider component
const RateSlider = ({ runId, patternKey, initialRate }) => {
    const [rate, setRate] = useState(initialRate);
    
    const handleRateChange = async (newRate) => {
        setRate(newRate);
        
        // Update rate on backend
        await fetch(`http://localhost:8080/v1/runs/${runId}/workload`, {
            method: 'PATCH',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                pattern_key: patternKey,
                rate_rps: newRate
            })
        });
    };
    
    return (
        <input
            type="range"
            min="0"
            max="1000"
            value={rate}
            onChange={(e) => handleRateChange(parseFloat(e.target.value))}
        />
    );
};
```

---

## Future Enhancements

### Configuration Changes

For broader configuration changes (e.g., service replicas, policies), similar approach could be implemented:

1. **Store Configuration State** in `RunExecutor`
2. **Add Update Endpoint**: `PATCH /v1/runs/{run_id}/configuration`
3. **Apply Changes**: Update resource manager, policy manager, etc.
4. **Thread Safety**: Ensure changes are applied safely during simulation

**Example:**
```json
PATCH /v1/runs/{run_id}/configuration
{
  "services": [
    {
      "id": "svc1",
      "replicas": 5  // Changed from 2 to 5
    }
  ],
  "policies": {
    "autoscaling": {
      "target_cpu_util": 0.8  // Changed from 0.7
    }
  }
}
```

---

## Related Documentation

- [Backend Integration Guide](./BACKEND_INTEGRATION.md) - Complete API reference and integration examples
- [API Reference](./BACKEND_INTEGRATION.md#http-api-reference) - HTTP endpoint documentation
- [gRPC API Reference](./BACKEND_INTEGRATION.md#grpc-api-reference) - gRPC method documentation

---

## Summary

| Feature | Status | Notes |
|---------|--------|-------|
| Dynamic Request Rates | ✅ Supported | Via HTTP and gRPC APIs |
| Real-Time Rate Updates | ✅ Supported | Immediate effect on future events |
| Workload Pattern Updates | ✅ Supported | Can update entire pattern or just rate |
| Configuration Changes | ❌ Not Supported | Future enhancement |
| Multiple Runs Comparison | ✅ Supported | Can compare runs with different rates |

**Current Status**: Dynamic request rate changes are fully supported and ready for use in interactive dashboards and real-time exploration scenarios.

