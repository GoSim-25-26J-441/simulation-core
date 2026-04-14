# Simulation semantics (DES core)

## Synchronous vs asynchronous downstream

- **Sync** (default `mode`, or explicit `sync`): When a request finishes **local** work, it records **service_request_latency_ms** (hop-local time from `request_start` to local completion). If there are **synchronous** downstream edges, the request stays **processing** until **every** sync child request has **fully completed** (including their own sync subtrees). The parent is then **finalized** at the simulation time of the **last** completing sync child. **request_latency_ms** and **root_request_latency_ms** (ingress-only) are emitted at **finalize**; the ingress root trace uses **root_request_latency_ms** with full sync subtree duration.

- **Async / event** (`mode: async|event`): Downstream work is scheduled **without** blocking the parent. The parent **finalizes** immediately after local work (no pending sync count). Async children still consume **resources** and increment **request_count** with `origin=downstream`.

## Timeouts and failures

- **Deadline**: For `timeout_ms > 0`, a `downstream_timeout` DES event is scheduled at `child.ArrivalTime + timeout_ms` (after the downstream call event fires). Priority 1 vs 0 for normal events so that a completion at the exact deadline is ordered before the timeout.

- **Sync wait**: If the timeout fires before the child subtree has finished (from the parent’s perspective), the simulator records the **attempt** failure (`sync_wait_timed_out`, `request_error_count` with `reason=timeout`, circuit-breaker failure if enabled). **If `policies.retries` allows another attempt**, the failed child is isolated with `caller_sync_resolved` **without** decrementing the parent’s `pendingSync`, and a `downstream_retry` event is scheduled at `simTime + GetBackoffDuration(attempt)` (simulation time only). Only when retries are exhausted (or retries are disabled) does the parent’s `pendingSync` decrement via `notifyParentSyncChildResolved` with failure. **root_request_latency_ms** for ingress includes backoff and retry work while the sync subtree is still open. Late completion of an isolated failed attempt does not notify the parent again and does not double-count circuit-breaker failure on finalize.

- **Async / event** (`timeout_ms`): Same deadline from downstream arrival; the parent does **not** wait. On timeout, the attempt records errors / CB failure. If retries apply, `async_attempt_abandoned` is set and a retry is scheduled without blocking the parent; otherwise `async_operation_timed_out` is set and local completion later skips success latency for that hop.

- **Start failures** (CPU/memory): Sync children that fail before `request_complete` either schedule a retry (same isolation as sync timeout: `caller_sync_resolved`, no `pendingSync` decrement yet) when `policies.retries` allows it, or call `propagateSyncChildFailureFromStartFailure` to decrement `pendingSync` and fail ancestors. **Ingress** admission failures (no instance, rate limit, circuit open on the ingress hop) are **not** retried in this pass.

- **request_error_count** labels: `reason` (`timeout`, `downstream_failure`, `cpu_capacity`, `memory_capacity`, `rate_limited`, `circuit_open`, `no_instance`, …), plus `origin`, `service`, `endpoint`, and optional `traffic_class` / `source_kind` when present. Failed **retry attempts** emit separate error samples; optional low-cardinality labels `is_retry=true` and `attempt` (0-based attempt index) are added when applicable.

## Retries (`policies.retries`)

- **Policy**: `internal/policy` `RetryPolicy.ShouldRetry(attempt, err)` and `GetBackoffDuration(attempt)` with `attempt >= 1` for backoff delay after failure `attempt` 0,1,…; `max_retries` from config matches existing semantics (`max_retries == 0` means no retries). Retries are modeled only as **DES events** (`EventTypeDownstreamRetry`), never wall-clock sleeps.
- **Per-attempt requests**: Each physical attempt is a distinct `models.Request` with shared `trace_id`, stable `logical_call_id` (first attempt’s id), `retry_attempt`, and `is_retry` on later attempts. **request_count** increments once per attempt (real work).
- **Metrics**: **service_request_latency_ms** remains per-attempt local hop time. **root_request_latency_ms** on ingress includes time while sync children are outstanding, including backoff gaps before retry. **Circuit breaker** `RecordFailure` on each failed attempt; `RecordSuccess` when an attempt completes successfully and emits success latency.

## Metrics

- **service_request_latency_ms**: Local service time per hop (CPU + net + queue delay for that instance).
- **request_latency_ms**: Total duration for a request node when it **finalizes** (includes waiting for sync children when applicable).
- **root_request_latency_ms**: Emitted only for **ingress** requests (`ParentID` empty) at finalize; represents external trace latency through the synchronous subtree.
- **Run-level** latency percentiles in `ConvertToRunMetrics` prefer **root_request_latency_ms** samples when present; otherwise **request_latency_ms**.
- **request_count** continues to use `origin` (`ingress`/`downstream`) plus optional `traffic_class` and `source_kind` labels when present on the request.

## Service model, kind, and role

- **`model`**: `cpu` — CPU + network + memory follow endpoint stats (default queue mean ≈ `mean_cpu_ms` + `net_latency_ms.mean`). `mixed` — same sampling path with higher **memory** influence on concurrency cost (working-set pressure). `db_latency` — **IO/latency dominated**: sampled CPU work is **capped** below network/query latency unless the endpoint explicitly configures high CPU; queue backlog uses an IO-weighted mean so queues reflect datastore behavior more than raw CPU.
- **`kind` / `role`**: `api_gateway` / `ingress` classify ingress-facing work (queue class `ingress`). `database` / `datastore` use datastore IO queue class. `cache` slightly reduces CPU vs generic services; `external` nudges network latency up. **`kind: queue` is rejected at scenario validation** (not implemented). `cache` and `external` are accepted and use the generic execution path with light nudges (partially differentiated).
- **`scaling`**: `pkg/config` scaling helpers (`ServiceAllowsBatchScalingAction`, `ServiceAllowsHorizontalScaling`, …) gate **batch** and **online** actions. **Database** services with **no** `scaling` block **horizontal** scaling by default (vertical changes still require an explicit policy when `scaling` is set; when `scaling` is nil on a database, **all** optimizer dimensions are blocked).

## Optimizer / scaling guards

- **Online**: Primary target `p95_latency` (default) **requires** `target_p95_latency_ms > 0` when starting an online optimization run. **Utilization-primary** (`cpu_utilization`, `memory_utilization`) does **not** require a P95 target; when `target_p95_latency_ms > 0`, it acts as an optional guardrail for scale-down decisions.
- **Batch**: Neighbor generation uses `ServiceAllowsBatchScalingAction` so database defaults and explicit `scaling` flags are respected.
- **Placement**: Initial scenario load and runtime **scale-out** require a host with enough **CPU and memory reservation**; otherwise initialization or `ScaleService` fails with a clear error (host scale-out can add capacity before retrying).

## Workload: uniform arrivals

- **Non-realtime (bounded horizon)**: For `arrival.type: uniform`, the simulator uses **N = round(rate_rps × horizon_seconds)** independent uniform offsets in **[start, end)** (sorted), matching the legacy `internal/workload.Generator.scheduleUniformArrivals` full-horizon behavior.
- **Realtime / online (lazy)**: Uniform arrivals are generated in **lazy** windows of `EventGenerationLookaheadWindow` (10s) without materializing the full online horizon. For each chunk **[chunkStart, chunkEnd)**, the **count** is **floor(rate × sec1) − floor(rate × sec0)** where **sec0/sec1** are seconds since the pattern **Epoch** at chunk start/end (Epoch is re-anchored on `Start`, `UpdateRate`, and `UpdatePattern` for uniform in realtime mode). **N** arrivals are placed as **i.i.d. uniform** within the chunk interval (same placement helper as non-realtime). This preserves long-run rate (e.g. 0.01 RPS over 1000s → 10 arrivals) without per-chunk independent rounding bias.

## Randomness

- **RunInput.seed** (non-zero): A single **effectiveRunSeed** is chosen per run and passed to **scenario state** (main RNG + `seed+2` for interaction branching) and **workload** (`seed+1` for workload generator). **Seed 0**: one bootstrap `int64` is generated per run for both subsystems so they stay aligned for that execution.

## Event ordering

- Events with the same `Time` and `Priority` are ordered by monotonic **Sequence** assigned at schedule time (tie-breaker for deterministic replay).

## Scenario identity / optimizer hashing

- **Single source of truth**: `internal/batchspec.ConfigHash` fingerprints the full v2 scenario for batch candidate deduplication, `CandidateStore` lookup (`hash → runID`), and deterministic per-candidate seeds (`seed = int64(ConfigHash(scenario)) ^ …` in batch evaluation). `internal/improvement.configsMatch` delegates to `batchspec.ScenarioSemanticsEqual` (hash equality) so the optimizer and orchestrator never disagree on “same scenario.”
- **Fields included**: `metadata.schema_version`; `simulation_limits` (`max_trace_depth`, `max_async_hops`); every host (`id`, `cores`, `memory_gb`); every service (`id`, `kind`, `role`, `replicas`, `model`, `cpu_cores`, `memory_mb`, scaling flags); every endpoint (`path`, CPU stats, `default_memory_mb`, `net_latency_ms`); every downstream call (full edge: `to`, `mode`, `kind`, probabilities, latencies, `timeout_ms`, `downstream_fraction_cpu`); every workload row (`from`, `source_kind`, `traffic_class`, `to`, full `arrival` including bursty parameters); full `policies` (`autoscaling` and `retries` including `backoff` and `base_ms`).
- **Ordering**: Hosts, services, endpoints, downstream edges, and workload rows are hashed in **canonical** sorted order (hosts by `id`, services by `id`, endpoints by `path` with stable tie-break on slice index for duplicate paths, downstream by full tuple + index, workload by full semantic tuple + index). **Service slice order in YAML is not part of identity**—only the multiset of services by `id` matters. If two workload rows are fully identical, relative order is preserved via stable sort so multiplicity stays consistent.
- **Why it matters**: If two behaviorally different scenarios collapsed to the same hash, batch optimization could dedupe them incorrectly, reuse metrics, or reuse seeds, producing wrong recommendations even when the DES is accurate.
