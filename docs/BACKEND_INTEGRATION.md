# Backend Integration Guide

This document provides comprehensive guidance for integrating `simulation-core` with your backend system. It covers API usage, Docker deployment, real-time metrics streaming, and architecture patterns for handling multiple concurrent simulation runs.

---

## Table of Contents

- [Overview](#overview)
- [Architecture](#architecture)
- [Quick Start](#quick-start)
- [HTTP API Reference](#http-api-reference)
- [Real-Time Metrics Streaming](#real-time-metrics-streaming)
- [Docker Deployment](#docker-deployment)
- [Integration Patterns](#integration-patterns)
- [Error Handling](#error-handling)
- [Best Practices](#best-practices)
- [Examples](#examples)

---

## Overview

The `simulation-core` provides a containerized microservices simulation engine with:
- **HTTP API** (port 8080): Primary interface for backend integration
- **gRPC API** (port 50051): Alternative interface for programmatic access
- **Real-time metrics**: SSE streaming for dashboard integration
- **Containerized**: Ready for deployment via Docker or Kubernetes

### Key Features for Backend Integration

- ✅ Create and manage simulation runs via HTTP API
- ✅ Real-time metrics streaming via Server-Sent Events (SSE)
- ✅ Time-series metrics retrieval with filtering
- ✅ Run export for complete data retrieval
- ✅ Docker containerization for isolated execution
- ✅ Concurrent run support (in-memory storage)
- ✅ Dynamic workload rate updates during simulation execution

---

## Architecture

### Service Architecture

```
┌─────────────────┐
│   Your Backend  │
│                 │
│  ┌───────────┐  │
│  │  Run      │  │
│  │  Manager  │  │
│  └─────┬─────┘  │
│        │        │
└────────┼────────┘
         │ HTTP API (8080)
         │ SSE Streaming
         ▼
┌─────────────────┐
│ simulation-core │
│   Container     │
│                 │
│  ┌───────────┐  │
│  │ RunStore  │  │
│  │ (Memory)  │  │
│  └───────────┘  │
│                 │
│  ┌───────────┐  │
│  │ Executor  │  │
│  │ (Simulation)│
│  └───────────┘  │
└─────────────────┘
```

### Deployment Options

1. **Single Container**: One simulation-core instance handling all runs
2. **Multiple Containers**: Deploy one container per simulation run (recommended for isolation)
3. **Container Orchestration**: Use Kubernetes/Docker Swarm for dynamic scaling

---

## Quick Start

### 1. Build Docker Image

```bash
# From simulation-core directory
docker build -t simulation-core:latest .
```

### 2. Run Container

```bash
# Run with default ports
docker run -d \
  --name simulation-core \
  -p 8080:8080 \
  -p 50051:50051 \
  simulation-core:latest

# Or with custom configuration
docker run -d \
  --name simulation-core \
  -p 8080:8080 \
  -p 50051:50051 \
  simulation-core:latest \
  -grpc-addr :50051 \
  -http-addr :8080 \
  -log-level info
```

### 3. Verify Health

```bash
curl http://localhost:8080/healthz
# Response: {"status":"ok","timestamp":"2024-01-15T10:30:00Z"}
```

### 4. Create a Simulation Run

```bash
curl -X POST http://localhost:8080/v1/runs \
  -H "Content-Type: application/json" \
  -d '{
    "input": {
      "scenario_yaml": "hosts:\n  - id: host-1\n    cores: 2\nservices:\n  - id: svc1\n    replicas: 1\n    model: cpu\n    endpoints:\n      - path: /test\n        mean_cpu_ms: 10\n        downstream: []\nworkload:\n  - from: client\n    to: svc1:/test\n    arrival:\n      type: poisson\n      rate_rps: 10",
      "duration_ms": 5000
    }
  }'
```

---

## HTTP API Reference

Base URL: `http://localhost:8080`

All endpoints return JSON unless otherwise specified. Error responses follow this format:

```json
{
  "error": "error message"
}
```

### Health Check

**GET** `/healthz`

Check if the service is running.

**Response:**
```json
{
  "status": "ok",
  "timestamp": "2024-01-15T10:30:00Z"
}
```

**Status Codes:**
- `200 OK`: Service is healthy

---

### Create Simulation Run

**POST** `/v1/runs`

Create a new simulation run.

**Request Body:**
```json
{
  "run_id": "optional-run-id",  // Optional: auto-generated if omitted
  "input": {
    "scenario_yaml": "yaml content here",
    "duration_ms": 5000
  }
}
```

**Response:**
```json
{
  "run": {
    "id": "run-20240115-103000-abc123",
    "status": "RUN_STATUS_PENDING",
    "created_at_unix_ms": 1705312200000
  }
}
```

**Status Codes:**
- `201 Created`: Run created successfully
- `400 Bad Request`: Invalid request body or run ID format
- `409 Conflict`: Run ID already exists

**Notes:**
- `run_id` must not contain `:` or `/` characters
- If `run_id` is omitted, a unique ID is auto-generated
- Run is created in `PENDING` status and must be started explicitly

---

### Start Simulation Run

**POST** `/v1/runs/{run_id}`

Start executing a simulation run. The run status will change to `RUNNING` and simulation will begin asynchronously.

**Response:**
```json
{
  "run": {
    "id": "run-20240115-103000-abc123",
    "status": "RUN_STATUS_RUNNING",
    "created_at_unix_ms": 1705312200000,
    "started_at_unix_ms": 1705312201000
  }
}
```

**Status Codes:**
- `200 OK`: Run started successfully
- `404 Not Found`: Run not found
- `409 Conflict`: Run is already running or in terminal state

**Note:** The simulation executes asynchronously. Use the metrics endpoints or SSE streaming to monitor progress.

---

### Stop Simulation Run

**POST** `/v1/runs/{run_id}:stop`

Cancel a running simulation run.

**Response:**
```json
{
  "run": {
    "id": "run-20240115-103000-abc123",
    "status": "RUN_STATUS_CANCELLED",
    "created_at_unix_ms": 1705312200000,
    "started_at_unix_ms": 1705312201000,
    "completed_at_unix_ms": 1705312205000
  }
}
```

**Status Codes:**
- `200 OK`: Run stopped successfully
- `404 Not Found`: Run not found
- `409 Conflict`: Run is already in terminal state

---

### Update Workload Rate

**PATCH** `/v1/runs/{run_id}/workload`

Update the request rate or pattern for a specific workload pattern in a running simulation.

**Request Body (Rate Update):**
```json
{
  "pattern_key": "client:svc1:/test",
  "rate_rps": 50.0
}
```

**Request Body (Pattern Update):**
```json
{
  "pattern_key": "client:svc1:/test",
  "pattern": {
    "from": "client",
    "to": "svc1:/test",
    "arrival": {
      "type": "poisson",
      "rate_rps": 100.0
    }
  }
}
```

**Response:**
```json
{
  "message": "workload updated successfully",
  "run_id": "run-20240115-103000-abc123",
  "pattern_key": "client:svc1:/test"
}
```

**Status Codes:**
- `200 OK`: Workload updated successfully
- `400 Bad Request`: Invalid request (missing pattern_key, negative rate, run not running, or invalid pattern)
- `404 Not Found`: Run not found or workload pattern not found
- `500 Internal Server Error`: Server error

**Notes:**
- The run must be in `RUNNING` status to update workload
- `pattern_key` format: `"{from}:{to}"` (e.g., `"client:svc1:/test"`)
- At least one of `rate_rps` or `pattern` must be provided; if both are provided, `rate_rps` is applied and `pattern` is ignored
- Rate changes take effect immediately and affect future event generation
- This endpoint enables dynamic configuration during simulation execution

---

### Get Simulation Run

**GET** `/v1/runs/{run_id}`

Retrieve information about a simulation run.

**Response:**
```json
{
  "run": {
    "id": "run-20240115-103000-abc123",
    "status": "RUN_STATUS_COMPLETED",
    "created_at_unix_ms": 1705312200000,
    "started_at_unix_ms": 1705312201000,
    "completed_at_unix_ms": 1705312206000
  }
}
```

**Status Codes:**
- `200 OK`: Run found
- `404 Not Found`: Run not found

---

### List Simulation Runs

**GET** `/v1/runs?limit=50&offset=0&status=COMPLETED`

List simulation runs with pagination and optional status filtering.

**Query Parameters:**
- `limit` (optional, default: 50, max: 1000): Number of runs to return
- `offset` (optional, default: 0): Number of runs to skip
- `status` (optional): Filter by status (`PENDING`, `RUNNING`, `COMPLETED`, `FAILED`, `CANCELLED`)

**Response:**
```json
{
  "runs": [
    {
      "id": "run-20240115-103000-abc123",
      "status": "RUN_STATUS_COMPLETED",
      "created_at_unix_ms": 1705312200000
    }
  ],
  "total": 1
}
```

**Status Codes:**
- `200 OK`: List retrieved successfully

**Notes:**
- Runs are sorted by creation time (newest first)
- Status filter is case-insensitive

---

### Get Run Metrics

**GET** `/v1/runs/{run_id}/metrics`

Retrieve aggregated metrics for a completed simulation run.

**Response:**
```json
{
  "run_id": "run-20240115-103000-abc123",
  "metrics": {
    "total_requests": 1000,
    "successful_requests": 950,
    "failed_requests": 50,
    "latency_p50_ms": 10.5,
    "latency_p95_ms": 25.3,
    "latency_p99_ms": 45.7,
    "latency_mean_ms": 12.8,
    "throughput_rps": 200.5
  }
}
```

**Status Codes:**
- `200 OK`: Metrics retrieved successfully
- `404 Not Found`: Run not found
- `412 Precondition Failed`: Metrics not yet available (run still in progress)

**Note:** Metrics are only available after the run completes or is stopped.

---

### Get Time-Series Metrics

**GET** `/v1/runs/{run_id}/metrics/timeseries?metric=cpu_utilization&service=svc1&start_time=1705312200000&end_time=1705312206000`

Retrieve time-series metrics with optional filtering.

**Query Parameters:**
- `metric` (optional): Filter by metric name (e.g., `cpu_utilization`, `request_latency_ms`)
- `service` (optional): Filter by service ID
- `start_time` (optional): Unix timestamp in milliseconds (start of time range)
- `end_time` (optional): Unix timestamp in milliseconds (end of time range)

**Response:**
```json
{
  "run_id": "run-20240115-103000-abc123",
  "points": [
    {
      "metric": "cpu_utilization",
      "value": 0.65,
      "timestamp_unix_ms": 1705312201000,
      "labels": {
        "service": "svc1",
        "instance": "svc1-0"
      }
    },
    {
      "metric": "cpu_utilization",
      "value": 0.72,
      "timestamp_unix_ms": 1705312202000,
      "labels": {
        "service": "svc1",
        "instance": "svc1-0"
      }
    }
  ]
}
```

**Status Codes:**
- `200 OK`: Time-series data retrieved successfully
- `404 Not Found`: Run not found
- `412 Precondition Failed`: Metrics collector not available

**Notes:**
- Multiple filters can be combined (e.g., `?metric=cpu_utilization&service=svc1`)
- If no filters are provided, all time-series data is returned
- Timestamps are in Unix milliseconds

---

### Export Run Data

**GET** `/v1/runs/{run_id}/export`

Export complete run data including run information, input, aggregated metrics, and time-series data.

**Response:**
```json
{
  "run": {
    "id": "run-20240115-103000-abc123",
    "status": "RUN_STATUS_COMPLETED",
    "created_at_unix_ms": 1705312200000,
    "started_at_unix_ms": 1705312201000,
    "completed_at_unix_ms": 1705312206000
  },
  "input": {
    "scenario_yaml": "...",
    "duration_ms": 5000
  },
  "metrics": {
    "total_requests": 1000,
    "successful_requests": 950,
    "failed_requests": 50,
    "latency_p50_ms": 10.5,
    "latency_p95_ms": 25.3,
    "latency_p99_ms": 45.7,
    "latency_mean_ms": 12.8,
    "throughput_rps": 200.5
  },
  "time_series": [
    {
      "metric": "cpu_utilization",
      "value": 0.65,
      "timestamp_unix_ms": 1705312201000,
      "labels": {
        "service": "svc1",
        "instance": "svc1-0"
      }
    }
  ]
}
```

**Status Codes:**
- `200 OK`: Export retrieved successfully
- `404 Not Found`: Run not found

**Note:** `time_series` array may be empty if metrics collector was not stored.

---

### Real-Time Metrics Streaming (SSE)

**GET** `/v1/runs/{run_id}/metrics/stream?interval=1000`

Stream real-time metrics updates using Server-Sent Events (SSE).

**Query Parameters:**
- `interval` (optional, default: 1000): Update interval in milliseconds

**Response Format:** Server-Sent Events (SSE)

```
Content-Type: text/event-stream
Cache-Control: no-cache
Connection: keep-alive

event: status_change
data: {"status":"RUN_STATUS_RUNNING"}

event: metrics_snapshot
data: {"metrics":{"total_requests":100,"latency_p95_ms":15.2}}

event: metric_update
data: {"metric":"cpu_utilization","value":0.65,"timestamp":"2024-01-15T10:30:01Z","labels":{"service":"svc1"}}

event: optimization_progress
data: {"iteration":2,"best_score":12.5,"best_run_id":"opt-1234567890-abc123"}

```

**Event Types:**
- `status_change`: Run status changes (e.g. `RUN_STATUS_RUNNING`, `RUN_STATUS_COMPLETED`)
- `metrics_snapshot`: Aggregated metrics updates
- `metric_update`: Single time-series metric point
- `optimization_progress`: (Optimization runs only) Iteration progress with `iteration`, `best_score`, `best_run_id`
- `complete`: Stream ending; run reached terminal status

**Status Codes:**
- `200 OK`: Stream started successfully
- `404 Not Found`: Run not found
- `412 Precondition Failed`: Metrics collector not available

**Notes:**
- Connection remains open until the run completes or is stopped
- Use EventSource API in JavaScript or SSE client libraries
- Recommended for dashboard integration

### Optimization Run SSE Events

For runs created with `optimization` config, the stream emits additional events:

**Event: `optimization_progress`**
```json
{
  "iteration": 2,
  "best_score": 12.5,
  "best_run_id": "opt-1234567890-abc123"
}
```
- `iteration`: Current optimization iteration (0 = initial config)
- `best_score`: Best objective score found so far (lower is better for minimization)
- `best_run_id`: Sub-run ID with best score (populated when known; may be empty during run)

**Integration:** Backend and frontend can subscribe to the same `/v1/runs/{id}/metrics/stream` endpoint. For optimization runs, listen for `optimization_progress` to show iteration progress and best score in real time.

---

## Real-Time Metrics Streaming

### JavaScript Example (Frontend)

```javascript
const runId = 'run-20240115-103000-abc123';
const eventSource = new EventSource(
  `http://localhost:8080/v1/runs/${runId}/metrics/stream?interval=1000`
);

// Handle status updates
eventSource.addEventListener('status_change', (event) => {
  const data = JSON.parse(event.data);
  console.log('Run status:', data.status);
  updateStatusIndicator(data.status);
});

// Handle metrics updates
eventSource.addEventListener('metrics_snapshot', (event) => {
  const data = JSON.parse(event.data);
  console.log('Metrics:', data.metrics);
  updateMetricsDashboard(data.metrics);
});

// Handle time-series updates
eventSource.addEventListener('metric_update', (event) => {
  const data = JSON.parse(event.data);
  updateTimeSeriesChart([data]);
});

// Handle optimization progress (for optimization runs)
eventSource.addEventListener('optimization_progress', (event) => {
  const data = JSON.parse(event.data);
  console.log('Optimization:', data.iteration, data.best_score, data.best_run_id);
  updateOptimizationProgress(data.iteration, data.best_score, data.best_run_id);
});

// Handle stream completion
eventSource.addEventListener('complete', (event) => {
  const data = JSON.parse(event.data);
  console.log('Stream complete:', data.status);
  eventSource.close();
});

// Handle errors
eventSource.onerror = (error) => {
  console.error('SSE error:', error);
  eventSource.close();
};

// Close connection when done
// eventSource.close();
```

### Go Example (Backend)

```go
package main

import (
    "bufio"
    "encoding/json"
    "fmt"
    "net/http"
    "strings"
)

func streamMetrics(runID string) error {
    url := fmt.Sprintf("http://localhost:8080/v1/runs/%s/metrics/stream?interval=1000", runID)
    resp, err := http.Get(url)
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    scanner := bufio.NewScanner(resp.Body)
    for scanner.Scan() {
        line := scanner.Text()
        
        if strings.HasPrefix(line, "event: ") {
            eventType := strings.TrimPrefix(line, "event: ")
            scanner.Scan()
            dataLine := strings.TrimPrefix(scanner.Text(), "data: ")
            
            var data map[string]interface{}
            if err := json.Unmarshal([]byte(dataLine), &data); err != nil {
                continue
            }
            
            switch eventType {
            case "status_change":
                fmt.Printf("Status: %v\n", data["status"])
            case "metrics_snapshot":
                fmt.Printf("Metrics: %v\n", data["metrics"])
            case "metric_update":
                fmt.Printf("Metric: %v\n", data)
            case "optimization_progress":
                fmt.Printf("Optimization: iter=%v best_score=%v\n", data["iteration"], data["best_score"])
            case "complete":
                fmt.Printf("Complete: %v\n", data["status"])
            }
        }
    }
    
    return scanner.Err()
}
```

---

## Docker Deployment

### Build Image

```bash
# From simulation-core directory
docker build -t simulation-core:latest .

# Tag for registry
docker tag simulation-core:latest your-registry/simulation-core:v1.0.0
```

### Push to Registry

```bash
# Docker Hub
docker push your-username/simulation-core:v1.0.0

# AWS ECR
aws ecr get-login-password --region us-east-1 | docker login --username AWS --password-stdin 123456789012.dkr.ecr.us-east-1.amazonaws.com
docker tag simulation-core:latest 123456789012.dkr.ecr.us-east-1.amazonaws.com/simulation-core:v1.0.0
docker push 123456789012.dkr.ecr.us-east-1.amazonaws.com/simulation-core:v1.0.0

# Google Container Registry
docker tag simulation-core:latest gcr.io/your-project/simulation-core:v1.0.0
docker push gcr.io/your-project/simulation-core:v1.0.0
```

### Run Container

```bash
# Basic run
docker run -d \
  --name simulation-core \
  -p 8080:8080 \
  -p 50051:50051 \
  simulation-core:latest

# With custom configuration
docker run -d \
  --name simulation-core \
  -p 8080:8080 \
  -p 50051:50051 \
  simulation-core:latest \
  -grpc-addr :50051 \
  -http-addr :8080 \
  -log-level debug

# With environment variables (if supported)
docker run -d \
  --name simulation-core \
  -p 8080:8080 \
  -p 50051:50051 \
  -e LOG_LEVEL=info \
  simulation-core:latest
```

### Docker Compose

See `deployments/docker/docker-compose.yml` for examples:

```bash
# Start single instance
cd deployments/docker
docker-compose up -d

# Start multiple instances (for concurrent runs)
docker-compose up --scale simd-instance=3 -d
```

---

## Integration Patterns

### Pattern 1: One Container Per Run (Recommended)

Deploy a new container for each simulation run. This provides:
- **Isolation**: Each run has its own resources
- **Scalability**: Easy to scale horizontally
- **Resource Management**: Can limit resources per container

**Backend Flow:**
1. User requests simulation
2. Backend creates new container with unique run ID
3. Backend calls `POST /v1/runs` to create run
4. Backend calls `POST /v1/runs/{id}` to start simulation
5. Backend streams metrics via SSE
6. On completion, export run data
7. Backend stops/removes container

**Example:**
```bash
# Create container for run
docker run -d \
  --name sim-run-abc123 \
  -p 8081:8080 \
  simulation-core:latest

# Create and start run
curl -X POST http://localhost:8081/v1/runs \
  -H "Content-Type: application/json" \
  -d '{"input": {...}}'

curl -X POST http://localhost:8081/v1/runs/abc123

# Stream metrics
curl http://localhost:8081/v1/runs/abc123/metrics/stream

# Export and cleanup
curl http://localhost:8081/v1/runs/abc123/export > run-data.json
docker stop sim-run-abc123
docker rm sim-run-abc123
```

### Pattern 2: Shared Container (Multiple Runs)

Use a single container to handle multiple runs. This is simpler but provides less isolation.

**Backend Flow:**
1. User requests simulation
2. Backend calls `POST /v1/runs` on shared container
3. Backend calls `POST /v1/runs/{id}` to start simulation
4. Backend polls or streams metrics
5. On completion, export run data

**Limitations:**
- All runs share the same container resources
- In-memory storage: data is lost if container restarts
- No resource isolation between runs

### Pattern 3: Container Orchestration (Kubernetes)

Deploy simulation-core as a Kubernetes Job or Deployment for better orchestration.

**Kubernetes Job Example:**
```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: simulation-run-abc123
spec:
  template:
    spec:
      containers:
      - name: simulation-core
        image: your-registry/simulation-core:v1.0.0
        ports:
        - containerPort: 8080
        - containerPort: 50051
        command: ["/usr/local/bin/simd"]
        args: ["-http-addr", ":8080", "-grpc-addr", ":50051"]
      restartPolicy: Never
```

---

## Error Handling

### Common HTTP Status Codes

- `200 OK`: Request succeeded
- `201 Created`: Resource created successfully
- `400 Bad Request`: Invalid request (check request body format)
- `404 Not Found`: Resource not found (check run ID)
- `409 Conflict`: Resource conflict (e.g., run ID already exists, run in terminal state)
- `412 Precondition Failed`: Precondition not met (e.g., metrics not available)
- `500 Internal Server Error`: Server error (check logs)

### Error Response Format

```json
{
  "error": "descriptive error message"
}
```

### Retry Logic

For transient errors (network issues, temporary unavailability):
- Implement exponential backoff
- Retry with jitter to avoid thundering herd
- Maximum retry count: 3-5 attempts

### Timeout Considerations

- **Create/Start Run**: Should complete quickly (< 1 second)
- **Get Metrics**: Fast (< 100ms)
- **Time-Series Query**: Depends on data size (100ms - 5s)
- **SSE Stream**: Long-lived connection (until run completes)
- **Export**: Depends on data size (100ms - 10s)

---

## Best Practices

### 1. Run ID Generation

- Generate unique, URL-safe run IDs on your backend
- Avoid using `:` or `/` characters
- Consider using UUIDs or timestamp-based IDs

**Example:**
```go
import "github.com/google/uuid"

runID := uuid.New().String()
// Or: runID := fmt.Sprintf("run-%d-%s", time.Now().Unix(), uuid.New().String()[:8])
```

### 2. Async Operation Handling

- Runs execute asynchronously after `POST /v1/runs/{id}`
- Poll run status or use SSE streaming to monitor progress
- Don't block on run completion in your API handlers

**Example:**
```go
// Start run and return immediately
run, err := client.StartRun(runID)
if err != nil {
    return err
}

// Handle completion asynchronously
go func() {
    for {
        status, err := client.GetRun(runID)
        if err != nil {
            log.Error(err)
            break
        }
        if isTerminalStatus(status.Status) {
            // Handle completion
            break
        }
        time.Sleep(1 * time.Second)
    }
}()
```

### 3. Metrics Collection

- Use SSE streaming for real-time dashboards
- Use time-series endpoint for historical analysis
- Cache aggregated metrics to reduce API calls
- Consider exporting run data for long-term storage

### 4. Resource Management

- Set resource limits on containers (CPU, memory)
- Monitor container resource usage
- Clean up completed containers promptly
- Consider using container orchestration for automatic cleanup

**Example (Docker):**
```bash
docker run -d \
  --name sim-run-abc123 \
  --memory="512m" \
  --cpus="1.0" \
  simulation-core:latest
```

### 5. Health Checks

- Implement health checks for containers
- Use `/healthz` endpoint for container health
- Monitor container status and restart if unhealthy

**Example:**
```yaml
healthcheck:
  test: ["CMD", "wget", "--quiet", "--tries=1", "--spider", "http://localhost:8080/healthz || exit 1"]
  interval: 10s
  timeout: 5s
  retries: 3
  start_period: 5s
```

### 6. Logging and Monitoring

- Enable structured logging (`-log-level info` or `debug`)
- Monitor API response times
- Track run completion rates and errors
- Set up alerts for failed runs

### 7. Data Persistence

⚠️ **Important**: Current implementation uses in-memory storage.

- Run data is lost if container restarts
- Export run data for long-term storage
- Consider implementing persistent storage backend (future enhancement)
- Store exported data in your backend database or object storage

---

## Examples

### Complete Integration Example (Go)

```go
package main

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "time"
)

type SimulationClient struct {
    BaseURL string
    Client  *http.Client
}

func NewSimulationClient(baseURL string) *SimulationClient {
    return &SimulationClient{
        BaseURL: baseURL,
        Client:  &http.Client{Timeout: 30 * time.Second},
    }
}

type CreateRunRequest struct {
    RunID string `json:"run_id,omitempty"`
    Input struct {
        ScenarioYAML string `json:"scenario_yaml"`
        DurationMs   int64  `json:"duration_ms"`
    } `json:"input"`
}

type RunResponse struct {
    Run struct {
        ID              string `json:"id"`
        Status          string `json:"status"`
        CreatedAtUnixMs int64  `json:"created_at_unix_ms"`
    } `json:"run"`
}

func (c *SimulationClient) CreateRun(scenarioYAML string, durationMs int64) (*RunResponse, error) {
    reqBody := CreateRunRequest{
        Input: struct {
            ScenarioYAML string `json:"scenario_yaml"`
            DurationMs   int64  `json:"duration_ms"`
        }{
            ScenarioYAML: scenarioYAML,
            DurationMs:   durationMs,
        },
    }

    body, err := json.Marshal(reqBody)
    if err != nil {
        return nil, err
    }

    resp, err := c.Client.Post(
        c.BaseURL+"/v1/runs",
        "application/json",
        bytes.NewReader(body),
    )
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusCreated {
        body, _ := io.ReadAll(resp.Body)
        return nil, fmt.Errorf("create run failed: %s", string(body))
    }

    var runResp RunResponse
    if err := json.NewDecoder(resp.Body).Decode(&runResp); err != nil {
        return nil, err
    }

    return &runResp, nil
}

func (c *SimulationClient) StartRun(runID string) error {
    resp, err := c.Client.Post(
        c.BaseURL+"/v1/runs/"+runID,
        "application/json",
        nil,
    )
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        body, _ := io.ReadAll(resp.Body)
        return fmt.Errorf("start run failed: %s", string(body))
    }

    return nil
}

func (c *SimulationClient) GetRun(runID string) (*RunResponse, error) {
    resp, err := c.Client.Get(c.BaseURL + "/v1/runs/" + runID)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("get run failed: status %d", resp.StatusCode)
    }

    var runResp RunResponse
    if err := json.NewDecoder(resp.Body).Decode(&runResp); err != nil {
        return nil, err
    }

    return &runResp, nil
}

func (c *SimulationClient) WaitForCompletion(runID string, timeout time.Duration) error {
    deadline := time.Now().Add(timeout)
    ticker := time.NewTicker(1 * time.Second)
    defer ticker.Stop()

    for time.Now().Before(deadline) {
        run, err := c.GetRun(runID)
        if err != nil {
            return err
        }

        status := run.Run.Status
        if status == "RUN_STATUS_COMPLETED" || status == "RUN_STATUS_FAILED" || status == "RUN_STATUS_CANCELLED" {
            return nil
        }

        <-ticker.C
    }

    return fmt.Errorf("timeout waiting for run completion")
}

// Usage
func main() {
    client := NewSimulationClient("http://localhost:8080")

    scenarioYAML := `
hosts:
  - id: host-1
    cores: 2
services:
  - id: svc1
    replicas: 1
    model: cpu
    endpoints:
      - path: /test
        mean_cpu_ms: 10
        downstream: []
workload:
  - from: client
    to: svc1:/test
    arrival:
      type: poisson
      rate_rps: 10
`

    // Create run
    run, err := client.CreateRun(scenarioYAML, 5000)
    if err != nil {
        panic(err)
    }
    fmt.Printf("Created run: %s\n", run.Run.ID)

    // Start run
    if err := client.StartRun(run.Run.ID); err != nil {
        panic(err)
    }
    fmt.Println("Run started")

    // Wait for completion
    if err := client.WaitForCompletion(run.Run.ID, 1*time.Minute); err != nil {
        panic(err)
    }
    fmt.Println("Run completed")
}
```

### Python Example

```python
import requests
import time
import json

class SimulationClient:
    def __init__(self, base_url):
        self.base_url = base_url
        self.session = requests.Session()

    def create_run(self, scenario_yaml, duration_ms, run_id=None):
        url = f"{self.base_url}/v1/runs"
        data = {
            "input": {
                "scenario_yaml": scenario_yaml,
                "duration_ms": duration_ms
            }
        }
        if run_id:
            data["run_id"] = run_id

        response = self.session.post(url, json=data)
        response.raise_for_status()
        return response.json()["run"]

    def start_run(self, run_id):
        url = f"{self.base_url}/v1/runs/{run_id}"
        response = self.session.post(url)
        response.raise_for_status()
        return response.json()["run"]

    def get_run(self, run_id):
        url = f"{self.base_url}/v1/runs/{run_id}"
        response = self.session.get(url)
        response.raise_for_status()
        return response.json()["run"]

    def wait_for_completion(self, run_id, timeout=60):
        deadline = time.time() + timeout
        while time.time() < deadline:
            run = self.get_run(run_id)
            status = run["status"]
            if status in ["RUN_STATUS_COMPLETED", "RUN_STATUS_FAILED", "RUN_STATUS_CANCELLED"]:
                return run
            time.sleep(1)
        raise TimeoutError("Run did not complete within timeout")

# Usage
client = SimulationClient("http://localhost:8080")

scenario_yaml = """
hosts:
  - id: host-1
    cores: 2
services:
  - id: svc1
    replicas: 1
    model: cpu
    endpoints:
      - path: /test
        mean_cpu_ms: 10
        downstream: []
workload:
  - from: client
    to: svc1:/test
    arrival:
      type: poisson
      rate_rps: 10
"""

# Create and start run
run = client.create_run(scenario_yaml, 5000)
print(f"Created run: {run['id']}")

run = client.start_run(run["id"])
print("Run started")

# Wait for completion
run = client.wait_for_completion(run["id"])
print(f"Run completed with status: {run['status']}")
```

---

## Troubleshooting

### Container Won't Start

- Check Docker logs: `docker logs simulation-core`
- Verify ports are not in use: `netstat -tulpn | grep 8080`
- Check Docker resources (memory, CPU)

### Run Stays in PENDING Status

- Verify you called `POST /v1/runs/{id}` to start the run
- Check container logs for errors
- Verify scenario YAML is valid

### Metrics Not Available

- Wait for run to complete (`RUN_STATUS_COMPLETED`)
- Check if metrics collector was stored (may be empty for very short runs)
- Verify run completed successfully (not failed/cancelled)

### SSE Connection Drops

- Check network connectivity
- Verify run is still active
- Implement reconnection logic in client
- Check container logs for errors

### High Memory Usage

- Limit container memory: `docker run --memory="512m" ...`
- Monitor run duration (longer runs use more memory)
- Consider using one container per run for better isolation

---

## gRPC API Reference

The simulator also exposes a gRPC API (port 50051) with similar functionality to the HTTP API. Key methods include:

- `CreateRun`: Create a new simulation run
- `StartRun`: Start a simulation run
- `StopRun`: Stop a running simulation
- `GetRun`: Get run information
- `ListRuns`: List simulation runs
- `GetRunMetrics`: Get aggregated metrics
- `StreamRunEvents`: Stream lifecycle events
- **`UpdateWorkloadRate`**: Update workload rate for a running simulation ⭐

**Example (UpdateWorkloadRate):**
```go
client := simulationv1.NewSimulationServiceClient(conn)
req := &simulationv1.UpdateWorkloadRateRequest{
    RunId:      "run-20240115-103000-abc123",
    PatternKey: "client:svc1:/test",
    RateRps:    50.0,
}
resp, err := client.UpdateWorkloadRate(ctx, req)
```

For complete gRPC API definitions, see `proto/simulation/v1/simulation.proto`.

---

## Additional Resources

- **README.md**: General project documentation
- **deployments/docker/README.md**: Docker deployment details
- **proto/simulation/v1/simulation.proto**: gRPC API definition
- **test/integration/**: Integration test examples
- **docs/DYNAMIC_CONFIGURATION.md**: Dynamic configuration guide

---

## Support

For issues or questions:
1. Check container logs: `docker logs <container-name>`
2. Review integration tests in `test/integration/`
3. Check API documentation above
4. Review error responses for detailed error messages

---

## Dynamic Configuration

### Workload Rate Updates

✅ **The simulator now supports:**
- Dynamic request rate adjustments during simulation execution
- Real-time workload pattern modifications via HTTP API
- Updating workload patterns for running simulations

**Endpoints:**
- **HTTP**: `PATCH /v1/runs/{run_id}/workload` - Update workload rate or pattern
- **gRPC**: `UpdateWorkloadRate` - Update workload rate (see proto definition)

**Example Use Case:**
Use a frontend slider to adjust request rates in real-time and observe how the system reacts to changing load patterns.

**For details**: See [Dynamic Configuration Guide](./DYNAMIC_CONFIGURATION.md)

