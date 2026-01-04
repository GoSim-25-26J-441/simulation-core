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
- **Optimization loop**: ✅ Heuristic hill-climbing for parameter tuning across runs with multiple objective functions, convergence detection, and parallel execution.
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

**Status**: ✅ Implemented (programmatic initialization only)

**How it works**:
- Token bucket algorithm with configurable rate limit per second
- Per-service/per-endpoint rate limiting
- Requests exceeding the rate limit are rejected immediately

**Configuration**: Currently programmatic initialization only. YAML configuration support is planned.

#### Circuit Breaker

Circuit breaker pattern prevents cascading failures by opening the circuit when failure thresholds are exceeded.

**Status**: ✅ Implemented (programmatic initialization only)

**How it works**:
- **Closed state**: Normal operation, requests are allowed
- **Open state**: Circuit is open, requests are rejected immediately
- **Half-open state**: Testing if service has recovered, allows limited requests
- Automatically transitions based on failure/success thresholds and timeout
- Per-service/per-endpoint circuit state tracking

**Configuration** (programmatic initialization only, YAML support planned):
- `failureThreshold`: Number of failures before opening circuit
- `successThreshold`: Number of successes needed in half-open to close
- `timeout`: Duration circuit stays open before transitioning to half-open

**Integration**: Circuit breaker checks occur in `RequestArrival` handler. Failure events are recorded in `RequestStart` handler when resource allocation fails. Success recording is not yet implemented.

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
- **exponential**: Delay = `base_ms * 2^(attempt-1)`
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

## Optimization Loop

The optimization loop enables automatic tuning of service configurations (replicas, resources, policies) to optimize for specific objectives like latency, throughput, or cost.

### Features

- **Hill-climbing optimizer**: Iterative algorithm that explores neighboring configurations to find optimal settings
- **Multiple objective functions**: Optimize for P95/P99/Mean latency, throughput, error rate, or cost
- **Convergence detection**: Automatic stopping when optimization converges (no improvement, plateau, low variance)
- **Parameter exploration**: Adjusts service replicas, CPU/memory resources, workload rates, and policy parameters
- **Parallel execution**: Evaluate multiple configurations concurrently for faster optimization
- **Best configuration selection**: Automatically identifies the best configuration from optimization history

### Objective Functions

The optimizer supports several built-in objective functions:

- **P95LatencyObjective**: Minimize 95th percentile latency
- **P99LatencyObjective**: Minimize 99th percentile latency
- **MeanLatencyObjective**: Minimize mean latency
- **ThroughputObjective**: Maximize requests per second
- **ErrorRateObjective**: Minimize error rate
- **CostObjective**: Minimize weighted cost (CPU + memory + replicas)

### Usage Example

```go
package main

import (
    "context"
    "fmt"
    "time"
    
    "github.com/GoSim-25-26J-441/simulation-core/internal/improvement"
    "github.com/GoSim-25-26J-441/simulation-core/internal/simd"
    "github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func main() {
    // Initialize components
    store := simd.NewRunStore()
    executor := simd.NewRunExecutor(store)
    
    // Create optimizer with P95 latency objective
    objective := &improvement.P95LatencyObjective{}
    optimizer := improvement.NewOptimizer(objective, 10, 1.0) // max iterations: 10, step size: 1.0
    
    // Configure convergence strategy (optional)
    convergenceConfig := &improvement.ConvergenceConfig{
        NoImprovementIterations: 3,
        MinIterations:           5,
        ImprovementThreshold:   0.01, // 1% improvement threshold
    }
    optimizer.WithConvergenceStrategy(
        improvement.NewNoImprovementStrategy(convergenceConfig),
    )
    
    // Create orchestrator
    orchestrator := improvement.NewOrchestrator(
        store, executor, optimizer, objective,
    ).WithMaxParallelRuns(3) // Enable parallel execution
    
    // Define initial scenario
    initialConfig := &config.Scenario{
        Hosts: []config.Host{
            {ID: "host1", Cores: 8},
        },
        Services: []config.Service{
            {
                ID:       "api",
                Replicas: 2,
                Model:    "cpu",
                CPUCores: 1.0,
                MemoryMB: 512.0,
                Endpoints: []config.Endpoint{
                    {
                        Path:            "/api/v1/users",
                        MeanCPUMs:       20,
                        CPUSigmaMs:      5,
                        DefaultMemoryMB: 50.0,
                        NetLatencyMs:    config.LatencySpec{Mean: 2, Sigma: 1},
                    },
                },
            },
        },
        Workload: []config.WorkloadPattern{
            {
                From: "client",
                To:   "api:/api/v1/users",
                Arrival: config.ArrivalSpec{
                    Type:    "poisson",
                    RateRPS: 100,
                },
            },
        },
    }
    
    // Run optimization experiment
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
    defer cancel()
    
    result, err := orchestrator.RunExperiment(ctx, initialConfig, 5000) // 5 second simulations
    if err != nil {
        panic(err)
    }
    
    // Print results
    fmt.Printf("Optimization completed!\n")
    fmt.Printf("  Converged: %v (%s)\n", result.Converged, result.ConvergenceReason)
    fmt.Printf("  Total runs: %d\n", result.TotalRuns)
    fmt.Printf("  Best score: %.2f\n", result.BestScore)
    fmt.Printf("  Best replicas: %d\n", result.BestConfig.Services[0].Replicas)
    fmt.Printf("  Duration: %v\n", result.Duration)
}
```

### Custom Exploration Strategy

You can customize how the optimizer explores the parameter space:

```go
// Use conservative explorer (smaller changes)
optimizer.WithExplorer(improvement.NewConservativeExplorer())

// Use default explorer (balanced exploration)
optimizer.WithExplorer(improvement.NewDefaultExplorer())
```

### Convergence Strategies

Multiple convergence detection strategies are available:

```go
// No improvement strategy: stops when no improvement for N iterations
convergenceConfig := &improvement.ConvergenceConfig{
    NoImprovementIterations: 5,
    MinIterations:           3,
}
optimizer.WithConvergenceStrategy(
    improvement.NewNoImprovementStrategy(convergenceConfig),
)

// Plateau strategy: stops when scores plateau (within tolerance)
plateauConfig := &improvement.ConvergenceConfig{
    PlateauIterations: 5,
    ScoreTolerance:    0.01, // 1% tolerance
    MinIterations:     3,
}
optimizer.WithConvergenceStrategy(
    improvement.NewPlateauStrategy(plateauConfig),
)

// Combined strategy: uses multiple strategies (default)
optimizer.WithConvergenceStrategy(
    improvement.NewCombinedStrategy(improvement.DefaultConvergenceConfig()),
)
```

### Parallel Execution

Enable parallel evaluation of configurations for faster optimization:

```go
orchestrator := improvement.NewOrchestrator(store, executor, optimizer, objective).
    WithMaxParallelRuns(5) // Evaluate up to 5 configurations in parallel
```

### Best Configuration Selection

After optimization, select the best configuration using different strategies:

```go
// Get candidates from optimization history
candidates := []*improvement.ConfigurationCandidate{
    // ... populated from optimization runs
}

// Select best using different strategies
strategy := &improvement.BestScoreStrategy{}
best, err := strategy.SelectBest(candidates, objective)

// Or use Pareto optimality for multi-objective optimization
paretoStrategy := &improvement.ParetoOptimalStrategy{}
best, err := paretoStrategy.SelectBest(candidates, objective)
```

### Metrics Comparison

Compare metrics across optimization runs:

```go
comparison, err := improvement.CompareMetrics(metrics1, metrics2, objective)
if err != nil {
    panic(err)
}

fmt.Printf("Improvement: %v\n", comparison.Improvement)
fmt.Printf("Objective diff: %.2f\n", comparison.ObjectiveDiff)
fmt.Printf("P95 latency diff: %.2f ms\n", comparison.LatencyDiff.P95Diff)
```

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
- **Heuristic optimization**: ✅ Hill-climbing optimizer tunes scaling/configs across iterations with configurable exploration strategies and convergence detection.
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