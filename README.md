# Microservices Simulation Engine (Go)

A modular, high-performance backend for simulating microservice architectures.  
It predicts latency, throughput, and resource utilization under configurable workloads, service graphs, and scaling policies.

---

## Contents
- [Features](#features)
- [Architecture](#architecture)
- [Directory Structure](#directory-structure)
- [Quick Start](#quick-start)
- [Configuration](#configuration)
- [API (Management Plane)](#api-management-plane)
- [Development](#development)
- [Design Notes](#design-notes)
- [Roadmap](#roadmap)
- [Contributing](#contributing)
- [License](#license)

---

## Features

- **Discrete-event simulation**: High-performance event-driven simulation of request lifecycles and inter-service fan-out.
- **Workload modeling**: 
  - Multiple arrival distributions: Poisson/Exponential, Uniform, Normal/Gaussian, Constant rate
  - Bursty workloads with configurable on/off periods
  - User flow modeling for multi-step request sequences
  - Configurable arrival rates and patterns
- **Interaction modeling**: Service DAGs, branching probabilities, sync/async calls, downstream service calls.
- **Resource modeling**: 
  - CPU and memory tracking per service instance
  - Host capacity constraints and resource limits
  - Queueing effects and capacity-based request queuing
  - Instance selection and load distribution
- **Metrics collection**: 
  - Time-series metrics collection during simulation
  - Label-based metric aggregation (service, endpoint, instance, host)
  - Percentile calculations (P50, P95, P99) and statistical aggregations
  - Request latency, count, error tracking
  - CPU/memory utilization and queue length metrics
  - Automatic conversion to run metrics format
- **Policy sandbox**: 
  - **Rate limiting**: Token bucket algorithm for per-service/per-endpoint request rate limiting
  - **Circuit breaker**: Failure threshold-based circuit breaking with half-open state recovery
  - **Retry policy**: Configurable retry logic with exponential, linear, or constant backoff
  - **Autoscaling**: CPU-based scaling with scale up/down logic and hysteresis (implementation complete, integration pending)
- **Optimization loop**: Heuristic hill-climbing for parameter tuning across runs (planned).
- **Multi-cluster support**: Separate latency/capacity models and cross-cluster links (planned).
- **API surface**: gRPC and HTTP APIs for external clients (CLI, UI, CI).

---

## Architecture

The engine is organized around a simulation orchestrator that drives:
1. **Workload** generators (arrival processes, user sessions).
2. **Interaction** models (service graph traversal; sync/async edges).
3. **Resource** models (service CPU, mem, net; host capacity; queues).
4. **Improvement** loop (multi-run optimization; hill-climbing).
5. **Metrics** collection and export.

Management is exposed via two planes:

- **gRPC (CLI plane)**: interactive control from `simulator-cli` (default `:50051`)
- **HTTP (orchestrator plane)**: backend-to-simd coordination across multiple simd containers (default `:8080`)

---

## Directory Structure

```
simulation-engine/
├── cmd/
│   └── simd/                 # Daemon entrypoint
│
├── internal/
│   ├── engine/               # Orchestrator: event loop, run lifecycle
│   ├── workload/             # Arrivals, distributions, time series
│   ├── interaction/          # Service DAG, branching, call semantics
│   ├── resource/             # Host & service resource models, queues
│   ├── improvement/          # Optimizers (hill-climbing), experiments
│   ├── metrics/              # Collectors, aggregations, exporters
│   └── storage/              # Optional persistence (e.g., Postgres, TSDB)
│
├── pkg/
│   ├── config/               # YAML/ENV config loader & validation
│   ├── logger/               # Structured logging
│   ├── models/               # Shared types (runs, services, traces)
│   └── utils/                # Helpers, rand, time, backoff
│
├── api/                      # Management plane
│   ├── handlers.go           # HTTP/gRPC handlers
│   ├── schemas.go            # DTOs / OpenAPI/Proto structs
│   └── middleware.go
│
├── deployments/              # Dockerfiles, k8s manifests, helm
├── test/                     # Unit/integration tests
├── docs/                     # ADRs, design notes, diagrams
│
├── go.mod
├── go.sum
├── README.md
└── config.yaml               # Example configuration
```

---

## Quick Start

### Prerequisites
- Go 1.22+
- Make (optional)
- Docker (optional, for containerized runs)

### Build and run

```bash
git clone https://github.com/go-sim/simulation-engine.git
cd simulation-engine

go mod tidy
go build -o bin/simd ./cmd/simd
./bin/simd
```

You should see startup logs indicating the configuration and that the simulation loop is ready.


## Configuration

The engine is configuration-driven via YAML and environment variables.  
Example `config.yaml`:

```yaml
log_level: info

clusters:
  - name: cluster-a
    network_rtt_ms: 1.2
    capacity:
      cpu_cores: 32
      mem_gb: 128

  - name: cluster-b
    network_rtt_ms: 8.0
    capacity:
      cpu_cores: 64
      mem_gb: 256

service_graph:
  nodes:
    - name: gateway
      cluster: cluster-a
      cpu_cost_ms: 0.2
      concurrency: 64
    - name: users
      cluster: cluster-a
      cpu_cost_ms: 0.6
    - name: orders
      cluster: cluster-b
      cpu_cost_ms: 1.0
    - name: payments
      cluster: cluster-b
      cpu_cost_ms: 1.5
  edges:
    - from: gateway
      to: users
      mode: sync
      p: 1.0
    - from: gateway
      to: orders
      mode: sync
      p: 0.6
    - from: orders
      to: payments
      mode: async
      p: 0.4

workload:
  arrival: poisson
  rate_rps: 500
  duration: 60s
  warmup: 5s

policies:
  autoscaling:
    enabled: true
    target_cpu_util: 0.7
    scale_step: 1
  retries:
    enabled: true
    max_retries: 2
    backoff: exponential
    base_ms: 10

optimization:
  enabled: true
  objective: "p95_latency_ms"
  max_iterations: 20
```

### Scenario Configuration

Simulation runs are defined using scenario YAML files. Example `scenario.yaml`:

```yaml
hosts:
  - id: host-1
    cores: 4
  - id: host-2
    cores: 8

services:
  - id: auth
    replicas: 2
    model: cpu
    cpu_cores: 1.0
    memory_mb: 512
    endpoints:
      - path: /auth/login
        mean_cpu_ms: 10
        cpu_sigma_ms: 2
        net_latency_ms:
          mean: 1
          sigma: 0.5
        default_memory_mb: 20

workload:
  - from: client
    to: auth:/auth/login
    arrival:
      type: poisson
      rate_rps: 100

policies:
  autoscaling:
    enabled: true
    target_cpu_util: 0.7
    scale_step: 1
  retries:
    enabled: true
    max_retries: 3
    backoff: exponential
    base_ms: 10
```

### Policy Configuration

The simulation engine supports several policies for controlling request behavior and resource management:

#### Rate Limiting

Rate limiting uses a token bucket algorithm to control request rates per service/endpoint. Currently integrated into the simulation handlers to reject requests that exceed the configured rate limit.

**Status**: ✅ Implemented and integrated

**How it works**:
- Token bucket algorithm with configurable rate limit per second
- Per-service/per-endpoint rate limiting
- Requests exceeding the rate limit are rejected immediately

**Note**: Rate limiting configuration is currently programmatic. YAML configuration support is planned.

#### Circuit Breaker

Circuit breaker pattern prevents cascading failures by opening the circuit when failure thresholds are exceeded.

**Status**: ✅ Implemented and integrated

**How it works**:
- **Closed state**: Normal operation, requests are allowed
- **Open state**: Circuit is open, requests are rejected immediately
- **Half-open state**: Testing if service has recovered, allows limited requests
- Automatically transitions based on failure/success thresholds and timeout
- Per-service/per-endpoint circuit state tracking

**Configuration** (programmatic, YAML support planned):
- `failureThreshold`: Number of failures before opening circuit
- `successThreshold`: Number of successes needed in half-open to close
- `timeout`: Duration circuit stays open before transitioning to half-open

**Integration**: Circuit breaker checks occur in `RequestArrival` handler. Success/failure is recorded in `RequestComplete` handler.

#### Retry Policy

Retry policy handles automatic retries for failed requests with configurable backoff strategies.

**Status**: ✅ Implemented (integration pending)

**Configuration**:
```yaml
policies:
  retries:
    enabled: true
    max_retries: 3        # Maximum number of retry attempts
    backoff: exponential  # Backoff type: exponential, linear, or constant
    base_ms: 10           # Base delay in milliseconds
```

**Backoff types**:
- **exponential**: Delay = `base_ms * 2^attempt`
- **linear**: Delay = `base_ms * attempt`
- **constant**: Delay = `base_ms` (same for all attempts)

**Note**: Retry policy is implemented but not yet integrated into request handlers. Integration will require tracking retry attempts and scheduling retry events with backoff delays.

#### Autoscaling Policy

Autoscaling policy enables CPU-based automatic scaling of service instances.

**Status**: ✅ Implemented (integration pending)

**Configuration**:
```yaml
policies:
  autoscaling:
    enabled: true
    target_cpu_util: 0.7  # Target CPU utilization (0.0 to 1.0)
    scale_step: 1         # Number of replicas to add/remove per scaling action
```

**How it works**:
- Monitors average CPU utilization across service instances
- Scales up when CPU utilization exceeds target
- Scales down when CPU utilization is below target (with hysteresis)
- Uses scale step to incrementally adjust replica count

**Note**: Autoscaling policy is implemented but requires integration with periodic evaluation and dynamic instance scaling in the resource manager.

## Development

```bash
go mod tidy
go build -o bin/simd ./cmd/simd
./bin/simd
go test ./...
```

## API Contracts (Milestone 0)

This repo defines the gRPC contract under `proto/` (see `proto/simulation/v1/simulation.proto`).

### gRPC (CLI plane)

- Default address: `localhost:50051`
- Service: `simulation.v1.SimulationService`

### HTTP (orchestrator plane)

Intended minimal endpoints (to be implemented in `simd`):

- `GET /healthz`
- `POST /v1/runs`
- `GET /v1/runs/{id}`
- `POST /v1/runs/{id}:stop`
- `GET /v1/runs/{id}/metrics`

### Proto generation

This repo uses [buf](https://buf.build/) for code generation.

```bash
./scripts/gen-proto.sh
```

On Windows (PowerShell):

```powershell
.\scripts\gen-proto.ps1
```

## Testing

### Unit tests (default)

Runs fast unit tests that live alongside the code:

```bash
go test ./...
```

### Integration tests (build-tagged)

Integration tests live under `test/integration/` and are excluded from default runs.

Run only integration tests:

```bash
go test -tags=integration ./test/...
```

Run unit + integration together:

```bash
go test -tags=integration ./...
```

---

## Design Notes

- **Discrete-event simulation**: Enables scalability and controlled fidelity by processing events in chronological order.
- **Resource modeling**: Tracks CPU and memory usage per service instance, enforces host capacity limits, and models queueing delays when instances are at capacity.
- **Metrics collection**: Time-series metrics are collected during simulation with label-based aggregation, enabling detailed analysis of service performance, resource utilization, and request patterns.
- **Workload patterns**: Multiple arrival distributions allow modeling of realistic traffic patterns, including bursty workloads and user flows.
- **Policy sandbox**: Integrated policies (rate limiting, circuit breaker) provide runtime control over request behavior. Additional policies (retry, autoscaling) are implemented and ready for integration.
- **Heuristic optimization**: Hill-climbing tunes scaling/configs across iterations (planned).
- **Bottleneck detection**: Comes from analyzing metrics, not the heuristic.
- **Deterministic seeds**: Ensure reproducibility of simulation runs.
- **Each run exports**: Metrics, logs, and summaries for validation and analysis.

---

## Roadmap

- gRPC management plane and metric streaming
- Fault injection and degradation modeling
- Trace-driven workload replay (OpenTelemetry)
- Multi-cluster network topology simulation
- Automatic calibration from real-world metrics

---

## Contributing

1. Fork and create a feature branch.  
2. Write tests and run `go test ./...`.  
3. Submit a PR with a clear description.  

---

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE).