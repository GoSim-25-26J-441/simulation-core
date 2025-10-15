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

- Discrete-event simulation of request lifecycles and inter-service fan-out.
- Workload modeling: arrival processes, concurrency, burstiness, and user flows.
- Interaction modeling: service DAGs, branching probabilities, sync/async calls.
- Resource modeling: CPU, memory, network, I/O, queueing effects.
- Metrics: Prometheus-style exporters, run artifacts, summary reports.
- Policy sandbox: auto-scaling strategies, rate limiting, retries, circuit breaking.
- Optimization loop: heuristic hill-climbing for parameter tuning across runs.
- Multi-cluster support: separate latency/capacity models and cross-cluster links.
- API surface for external clients (CLI, UI, CI).

---

## Architecture

The engine is organized around a simulation orchestrator that drives:
1. **Workload** generators (arrival processes, user sessions).
2. **Interaction** models (service graph traversal; sync/async edges).
3. **Resource** models (service CPU, mem, net; host capacity; queues).
4. **Improvement** loop (multi-run optimization; hill-climbing).
5. **Metrics** collection and export.

Management is exposed via REST (and/or gRPC) to decouple clients (separate CLI, dashboards) from the core.

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



## Development

```bash
go mod tidy
go build -o bin/simd ./cmd/simd
./bin/simd
go test ./...
```

---

## Design Notes

- Discrete-event simulation enables scalability and controlled fidelity.
- Heuristic optimization (hill-climbing) tunes scaling/configs across iterations.
- Bottleneck detection comes from analyzing metrics, not the heuristic.
- Deterministic seeds ensure reproducibility.
- Each run exports metrics, logs, and summaries for validation.

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