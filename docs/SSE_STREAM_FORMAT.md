# SSE metrics stream format (frontend reference)

The simd HTTP API exposes a **Server-Sent Events (SSE)** stream for live run metrics:

- **Endpoint:** `GET /v1/runs/{run_id}/metrics/stream`
- **Query:** `interval_ms` (optional, default 1000) – how often metrics snapshots are sent (milliseconds).
- **Headers:** Client should set `Accept: text/event-stream`.

## Capturing a sample to a file

With simd running (e.g. `go run ./cmd/simd`):

```powershell
.\scripts\capture_sse.ps1 -BaseUrl "http://localhost:8080" -OutputFile "sse_output.txt" -DurationSeconds 8 -IntervalMs 500
```

This creates a run, starts it, streams SSE for 8 seconds into `sse_output.txt`, then stops the run. Use that file to see real event sequences.

## Event format

Each SSE message consists of:

1. A line `event: <event_type>`
2. A line `data: <json_object>`
3. A blank line (message boundary)

Example:

```
event: status_change
data: {"status":"RUN_STATUS_RUNNING"}

event: metric_update
data: {"metric":"request_rate","labels":{"run_id":"run-123","service":"svc1"},"value":10.5,"timestamp_ms":1700000000000}

event: complete
data: {"status":"RUN_STATUS_COMPLETED"}
```

## Event types the frontend should handle

| Event type             | When / meaning |
|------------------------|----------------|
| `status_change`        | Run status changed; `data.status` is the new status string (e.g. `RUN_STATUS_RUNNING`, `RUN_STATUS_COMPLETED`). |
| `metric_update`        | One metric data point; `data` has `metric`, `labels`, `value`, and often `timestamp`. Labels may include `host` (node-level), `service`, `instance`, or `endpoint`. |
| `metrics_snapshot`      | Aggregated snapshot: `metrics` (run/service aggregates), optional `host_metrics` (per-host `host_id`, `cpu_utilization`, `memory_utilization`), and optional `resources` (current pod/host allocations). |
| `complete`             | Run reached a terminal state (completed/failed/cancelled). |
| `optimization_progress` | For optimization runs; iteration and best score. |
| `optimization_step`    | For online optimization runs; emitted when the controller applies a config change (replicas, CPU, hosts). Backend can append to `run.metadata.optimization_history`. |
| `error`                | Stream or run error; `data.error` has the message. |

### `metric_update` value semantics

- **`request_count`** and **`request_error_count`**: `data.value` is the **cumulative total** so far for that (metric, labels). You can plot `(timestamp, value)` directly for “total requests over time” without client-side accumulation.
- **All other metrics** (e.g. `request_latency_ms`, `cpu_utilization`, `memory_utilization`, `queue_length`, `concurrent_requests`): `data.value` is the **latest reading** (one observation or current gauge value). **`concurrent_requests`** is the current in-flight request count per instance (gauge).

### `metrics_snapshot` payload shape

`metrics_snapshot` is sent **every poll interval** while the run is active (aggregates computed from the collector) and when the run has stored metrics (e.g. after completion). So the frontend receives periodic run-wide and per-service totals during the run without summing `metric_update` events.

The `data` payload for `metrics_snapshot` has this high-level structure:

- **`metrics`**: JSON form of the protobuf `RunMetrics` (totals + per-service aggregates).
- **`host_metrics`** (optional): Array of host-level utilization snapshots:
  - `host_id` (string)
  - `cpu_utilization` (0.0–1.0)
  - `memory_utilization` (0.0–1.0)
- **`resources`** (optional): Current resource allocations for pods and hosts:
  - `resources.services`: array of:
    - `service_id` (string)
    - `replicas` (int)
    - `cpu_cores` (float, per-instance)
    - `memory_mb` (float, per-instance)
  - `resources.hosts`: array of:
    - `host_id` (string)
    - `cpu_cores` (int, host capacity)
    - `memory_gb` (int, host capacity)

The `metrics.service_metrics[].active_replicas` field reflects the current run configuration (same source as `resources.services[].replicas`) for both in-run and post-run snapshots.

These fields are populated from the simulator’s live configuration via `GetRunConfiguration`, so they reflect any dynamic updates performed by the online optimizer (horizontal/vertical pod scaling and host scaling).

### `optimization_step` payload shape (online optimization)

When the online controller applies a configuration change, the stream emits:

- **`iteration_index`**: Step index (1-based).
- **`target_p95_ms`**: Target p95 latency.
- **`score_p95_ms`**: Current p95 at time of change.
- **`reason`**: Human-readable reason (e.g. `"p95 above target, scaled replicas up"`).
- **`previous_config`**: `{ services, workload, hosts }` before the change.
- **`current_config`**: `{ services, workload, hosts }` after the change.

**Backend integration:** Append each step to `run.metadata.optimization_history` for audit, replay, and UI visibility. Expose via `GET /simulation/runs/{id}` or `/optimization-history`.

## Frontend usage (EventSource)

```javascript
const runId = '...'; // from create/start run response
const url = `http://localhost:8080/v1/runs/${runId}/metrics/stream?interval_ms=500`;
const es = new EventSource(url);

es.addEventListener('status_change', (e) => {
  const data = JSON.parse(e.data);
  console.log('Status:', data.status);
});

es.addEventListener('metric_update', (e) => {
  const data = JSON.parse(e.data);
  console.log('Metric:', data.metric, data.labels, data.value);
});

es.addEventListener('complete', () => {
  es.close();
});

es.onerror = (err) => {
  console.error('SSE error', err);
  es.close();
};
```

Use the captured `sse_output.txt` to see the exact field names and shapes for your environment.
