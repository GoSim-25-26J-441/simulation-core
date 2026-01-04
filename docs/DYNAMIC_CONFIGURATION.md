# Dynamic Configuration and Request Rate Changes

## Current Status: NOT SUPPORTED ⚠️

**Important**: The current implementation does **NOT** support dynamic configuration changes or real-time request rate adjustments during simulation execution.

---

## Current Limitations

### 1. Configuration Changes
- ❌ **Cannot modify scenario configuration** after a run is created
- ❌ **No API endpoint** to update configuration during execution
- ❌ Configuration is **locked** when the run starts

### 2. Dynamic Request Rates
- ❌ **Cannot change request rates** during simulation execution
- ❌ **All arrival events are pre-scheduled** at the beginning of simulation
- ❌ Workload pattern rates are **fixed** from the scenario YAML

### 3. How It Currently Works
1. Run is created with scenario YAML containing fixed workload rates
2. Simulation starts: `ScheduleWorkload()` pre-schedules ALL arrival events
3. Simulation executes: Events are processed in chronological order
4. **No mechanism** to inject new events or modify existing ones during execution

---

## What Would Be Needed

To support dynamic configuration and request rate changes, the following features would need to be implemented:

### 1. Dynamic Workload Generation

**Current Architecture:**
```
Create Run → Parse Scenario → Schedule ALL Events → Execute Events Sequentially
```

**Required Architecture:**
```
Create Run → Parse Scenario → Start Event Generation Loop → Execute Events
                                        ↓
                              (Ongoing event generation with dynamic rates)
```

### 2. API Endpoints

New endpoints would be needed:

```
PATCH /v1/runs/{run_id}/workload
PUT /v1/runs/{run_id}/configuration
POST /v1/runs/{run_id}/workload:update-rate
```

**Example Request:**
```json
PATCH /v1/runs/abc123/workload
{
  "workload_patterns": [
    {
      "from": "client",
      "to": "svc1:/test",
      "arrival": {
        "type": "poisson",
        "rate_rps": 50.0  // Changed from 10 RPS to 50 RPS
      }
    }
  ]
}
```

### 3. Engine Modifications

The engine would need:

1. **Ongoing Event Generation**: Instead of pre-scheduling all events, continuously generate new arrival events based on current rates
2. **Rate Management**: Track current rates per workload pattern with ability to update
3. **Thread-Safe Rate Updates**: Allow rate changes while simulation is running
4. **Event Queue Modification**: Ability to cancel/reschedule pending events if needed

### 4. Workload Generator Changes

The `workload.Generator` would need:

1. **Continuous Generation Mode**: Generate events on-demand rather than pre-scheduling
2. **Rate Update Mechanism**: Update generation rates dynamically
3. **State Tracking**: Track last generation time per workload pattern
4. **Thread Safety**: Support concurrent rate updates

---

## Implementation Approach

### Option 1: Continuous Event Generation (Recommended)

**Concept**: Use a background goroutine that continuously generates arrival events based on current rates.

**Architecture:**
```
┌─────────────────────┐
│  Event Generation   │
│     Goroutine       │
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

**Implementation Steps:**

1. **Modify `RunExecutor`** to store workload state:
   ```go
   type WorkloadState struct {
       mu            sync.RWMutex
       patterns      map[string]*WorkloadPatternState  // key: "from:to"
       generator     *workload.Generator
       engine        *engine.Engine
       ctx           context.Context
       cancel        context.CancelFunc
   }
   
   type WorkloadPatternState struct {
       Pattern       config.WorkloadPattern
       LastEventTime time.Time
       Active        bool
   }
   ```

2. **Add Rate Update Method** to `RunExecutor`:
   ```go
   func (e *RunExecutor) UpdateWorkloadRate(runID string, patternKey string, newRateRPS float64) error {
       // Update rate for specific workload pattern
       // Triggers regeneration of events from current time forward
   }
   ```

3. **Modify Event Generation** to be continuous:
   ```go
   func (ws *WorkloadState) generateEventsLoop() {
       ticker := time.NewTicker(100 * time.Millisecond) // Check every 100ms
       defer ticker.Stop()
       
       for {
           select {
           case <-ws.ctx.Done():
               return
           case <-ticker.C:
               ws.mu.RLock()
               currentSimTime := ws.engine.GetSimTime()
               for patternKey, state := range ws.patterns {
                   if !state.Active {
                       continue
                   }
                   // Generate next events based on current rate
                   nextEventTime := calculateNextArrival(
                       state.Pattern.Arrival.RateRPS,
                       state.LastEventTime,
                   )
                   if nextEventTime.Before(currentSimTime.Add(1 * time.Second)) {
                       // Schedule next event
                       ws.scheduleNextArrival(patternKey, nextEventTime)
                       state.LastEventTime = nextEventTime
                   }
               }
               ws.mu.RUnlock()
           }
       }
   }
   ```

4. **Add HTTP Endpoint**:
   ```go
   // PATCH /v1/runs/{run_id}/workload
   func (s *HTTPServer) handleUpdateWorkload(w http.ResponseWriter, r *http.Request, runID string) {
       var req struct {
           WorkloadPatterns []config.WorkloadPattern `json:"workload_patterns"`
       }
       // Validate request
       // Update workload rates in executor
       // Return success
   }
   ```

### Option 2: Event Rescheduling (Alternative)

**Concept**: Pre-schedule events with a "rate change" event that triggers rescheduling.

**Approach:**
- Schedule rate change events at specific times
- When rate change event is processed, cancel remaining arrivals and reschedule with new rate
- More complex but preserves discrete-event simulation purity

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

## Recommendation

For **MVP/Production Use**:
- Use the **multiple runs approach** as a workaround
- Implement dynamic configuration as a **future enhancement**
- Focus on getting the core simulation working first

For **Interactive Exploration**:
- Dynamic configuration is highly valuable
- Consider implementing it as a **Phase 2** feature
- Start with request rate changes (simpler) before full configuration changes

---

## Related Documentation

- [Backend Integration Guide](./BACKEND_INTEGRATION.md)
- [API Reference](./BACKEND_INTEGRATION.md#http-api-reference)
- [Architecture](./BACKEND_INTEGRATION.md#architecture)

---

## Summary

| Feature | Status | Complexity | Priority |
|---------|--------|------------|----------|
| Dynamic Request Rates | ❌ Not Supported | High | Medium |
| Configuration Changes | ❌ Not Supported | Very High | Low |
| Real-Time Rate Updates | ❌ Not Supported | High | Medium |
| Multiple Runs Comparison | ✅ Supported | Low | High (Workaround) |

**Current Workaround**: Create multiple runs with different configurations and compare results.

