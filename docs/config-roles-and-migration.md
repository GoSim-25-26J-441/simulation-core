# Config vs Scenario: Roles and Migration

## Overview

The simulation-core has two configuration models with distinct roles:

| Aspect | Scenario (Primary) | Config (Legacy) |
|--------|--------------------|-----------------|
| **Used by** | simd daemon, executor, engine, all internal packages | File-based loaders, smoke tests |
| **File** | `config/scenario.yaml` | `config/config.yaml` |
| **API** | `RunInput.ScenarioYaml` | N/A (file only) |
| **Model** | Hosts + Services + Workload patterns | Clusters + ServiceGraph + Workload |

## Scenario (Primary)

- **Hosts**: Physical hosts with CPU cores; services are placed on hosts via resource manager
- **Services**: Microservices with replicas, endpoints, per-endpoint CPU/latency, downstream calls
- **Workload**: Array of entry points `{from, to, arrival}`; supports multiple traffic patterns
- **Policies**: Autoscaling, retries (compatible structure)

## Config (Legacy)

- **Clusters**: Abstract compute clusters with network RTT and capacity
- **ServiceGraph**: Nodes (services with cpu_cost_ms) and edges (from/to with mode, probability)
- **Workload**: Single arrival type, rate, duration, warmup
- **Optimization**: Top-level optimization settings

## Migration: Config → Scenario

1. **Clusters → Hosts**
   - Map each cluster to one or more hosts
   - `cluster.Capacity.CPUCores` → host `cores` (split as needed)

2. **ServiceGraph.Nodes → Services**
   - Each node → one Service with `id = node.Name`
   - `node.CPUCostMs` → endpoint `mean_cpu_ms` (use one endpoint per node, e.g. `/`)

3. **ServiceGraph.Edges → Endpoint.Downstream**
   - Each edge `from A to B` → add `DownstreamCall{To: "B:/"}` to service A's endpoint

4. **Workload → Workload patterns**
   - Create `WorkloadPattern{From: "client", To: "gateway:/", Arrival: {...}}` for entry nodes
   - Map `Config.Workload.Arrival`, `RateRPS` to `ArrivalSpec`

5. **Policies** – Structure is similar; copy autoscaling/retries fields

## Recommendation

For new work, use **Scenario** exclusively. The Config format remains for backward compatibility and tests but is not wired into the simulation engine.
