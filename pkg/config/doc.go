// Package config provides configuration types and loaders for the simulation core.
//
// # Dual Config Models
//
// This package defines two distinct configuration models with different roles:
//
// ## Scenario (Primary – Active)
//
// Scenario is the primary configuration format for simulation runs. It is used by:
//   - The simd daemon (gRPC/HTTP API)
//   - The executor, handlers, resource manager, interaction manager
//   - Workload generation and optimization
//
// Scenario defines:
//   - Hosts: physical hosts with CPU cores and optional memory_gb
//   - Services: microservices with replicas, endpoints, downstream calls
//   - Workload: array of workload patterns (from, to, arrival process)
//   - Policies: autoscaling, retries
//
// File: config/scenario.yaml
// API: RunInput.ScenarioYaml is parsed as Scenario.
//
// ## Config (Legacy – Alternative Format)
//
// Config is an alternative configuration format that uses a cluster- and graph-based
// model. It is not used by the simd daemon or internal simulation packages.
//
// Config defines:
//   - Clusters: compute clusters with network RTT and capacity
//   - ServiceGraph: nodes (services) and edges (dependencies)
//   - Workload: single arrival/rate/duration
//   - Policies, Optimization
//
// File: config/config.yaml
// Use: LoadConfig for file-based setups; smoke/integration tests.
//
// # Migration Path: Config → Scenario
//
// To migrate from Config to Scenario:
//
//   - Clusters → Hosts: map each cluster to one or more hosts (cluster capacity → host cores)
//   - ServiceGraph.Nodes → Services: each node becomes a service; cpu_cost_ms informs endpoint mean_cpu_ms
//   - ServiceGraph.Edges → Endpoint.Downstream: each edge becomes a DownstreamCall with to "service:path"
//   - Workload (single) → Workload (patterns): create one WorkloadPattern per entry point
//   - Policies → Policies: structure is compatible; map fields as needed
//
// The Scenario format is richer for per-endpoint modeling (replicas, net latency, downstream
// call count/latency) and supports multiple workload entry points.
package config
