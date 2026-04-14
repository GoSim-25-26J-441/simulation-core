# Simulation semantics (DES core)

## Synchronous vs asynchronous downstream

- **Sync** (default `mode`, or explicit `sync`): When a request finishes **local** work, it records **service_request_latency_ms** (hop-local time from this request’s **ArrivalTime** at the service to local completion, including **FIFO queue wait** + CPU + net). If there are **synchronous** downstream edges, the request stays **processing** until **every** sync child request has **fully completed** (including their own sync subtrees). The parent is then **finalized** at the simulation time of the **last** completing sync child. **request_latency_ms** and **root_request_latency_ms** (ingress-only) are emitted at **finalize**; the ingress root trace uses **root_request_latency_ms** with full sync subtree duration.

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
- **Metrics**: **service_request_latency_ms** remains per-attempt hop time (queue wait + processing for that attempt). **service_processing_latency_ms** records CPU + net only (StartTime → completion). **root_request_latency_ms** on ingress includes time while sync children are outstanding, including backoff gaps before retry. **Circuit breaker** `RecordFailure` on each failed attempt; `RecordSuccess` when an attempt completes successfully and emits success latency.

## Metrics

### Aggregates (RunMetrics / ServiceMetrics)

- **Run-level latency** (`latency_p50_ms`, …, `latency_mean_ms`): Prefer **`root_request_latency_ms`** samples when present (ingress SLO); else **`request_latency_ms`** (per-node finalize).
- **`failed_requests` / `attempt_failed_requests`**: Sum of **`request_error_count`** samples (attempt-level): includes downstream internal failures and **failed retry attempts** that are later superseded by a successful retry. Use for diagnostics, not as the sole user-visible SLO error rate.
- **`ingress_failed_requests` / `ingress_error_rate`**: **User-visible** ingress/root logical failures. **`ingress_logical_failure_count`** is incremented once per failed **external** trace (rate limit, admission failure, subtree failure on ingress, drain eviction on ingress, etc.). **`ingress_error_rate` = ingress_failed_requests / ingress_requests** when **`ingress_requests > 0`**. **Workload arrivals** are counted as **`request_count`** with **`origin=ingress`** at admission (including arrivals rejected by policy or placement) so the denominator matches all ingress attempts.
- **`attempt_error_rate`**: **`attempt_failed_requests / total_requests`** where **`total_requests`** is the sum of all **`request_count`** samples (ingress + downstream hops).
- **`retry_attempts`**: Sum of **`request_count`** samples with **`is_retry=true`**.
- **`timeout_errors`**: Sum of **`request_error_count`** with **`reason=timeout`**.
- **Per-service latency breakdown** (in **`ServiceMetrics`**): Existing **`latency_*`** fields remain **hop total** (**`service_request_latency_ms`**: queue wait + CPU + net). **`queue_wait_*`** aggregates **`queue_wait_ms`**; **`processing_latency_*`** aggregates **`service_processing_latency_ms`** (CPU + net for the hop, excluding queue wait).
- **Batch optimization** **`max_error_rate`** guardrail uses **`ingress_error_rate`** when **`ingress_requests > 0`**; otherwise it falls back to **`failed_requests / total_requests`** (legacy attempt-level ratio).

### Time series

- **queue_wait_ms**: Emitted at **request_start** (simulation time). Value is **StartTime − ArrivalTime** for that hop—the actual DES wait before service processing begins. Labels include `service`, `endpoint`, `instance`, `origin`, optional `traffic_class` / `source_kind`, and retry labels (`is_retry`, `attempt`) when applicable. **queue_length** remains a **gauge** (current backlog depth per instance); it is **not** multiplied by mean service time to infer latency.
- **service_request_latency_ms**: Per-hop **total** local time from **ArrivalTime** to **CompletionTime** at that service (queue wait + CPU + network for this hop). **Not** “start to complete” processing-only.
- **service_processing_latency_ms**: Per-hop **processing** time only (**StartTime** → **CompletionTime**): CPU + sampled network latency, **excluding** queue wait.
- **request_latency_ms**: Total duration for a request node when it **finalizes** (includes waiting for sync children when applicable).
- **root_request_latency_ms**: Emitted only for **ingress** requests (`ParentID` empty) at finalize; represents external trace latency through the synchronous subtree (includes all queue waits and sync subtree work).
- **Run-level** latency percentiles in `ConvertToRunMetrics` prefer **root_request_latency_ms** samples when present; otherwise **request_latency_ms**. Per-service rollups in `ServiceMetrics` use **service_request_latency_ms** aggregates (hop totals including queue wait).
- **request_count** continues to use `origin` (`ingress`/`downstream`) plus optional `traffic_class` and `source_kind` labels when present on the request.

## CPU scheduling (per-instance FIFO)

- **`mean_cpu_ms` / sampled CPU demand** is **total work** for the hop in **core·ms** (or equivalently “milliseconds of CPU time if this instance had exactly one core”). It feeds **`AllocateCPU`** accounting (sliding utilization window) and the **admission scheduler**.
- **`cpu_cores`** on the service instance is modeled as a **single logical FCFS server** whose **service rate** scales with cores: wall-clock CPU interval is **`cpuDemandMs / max(cpu_cores, ε)`** (duration rounded to integer milliseconds). This is **not** “N independent single-core workers” running unrelated jobs in parallel; it is a deterministic **throughput multiplier** for the same demand distribution.
- **Atomic admission**: **`ReserveCPUWork`** runs in **`handleRequestStart`**. It sets **`cpuStart = max(request.ArrivalTime, cpuNextFree)`**, **`cpuEnd = cpuStart + duration`**, and commits **`cpuNextFree = cpuEnd`**. Same-simulation-time **`request_arrival`** events all schedule **`RequestStart`** at the same time; **FIFO reservation order** (event sequence) prevents duplicate “free CPU” observations before work is committed.
- If **`cpuStart > simTime`**, the handler **defers** processing by scheduling **`RequestStart`** at **`cpuStart`** (request stays **pending** until then). **`StartTime`** is set to **`cpuStart`** (not the first event time when deferred).
- **Metrics**: **`queue_wait_ms` = `StartTime − ArrivalTime`** (DES wait before CPU service begins). **`service_processing_latency_ms` = `CompletionTime − StartTime`** (CPU wall interval for the hop + sampled network latency on that hop). **`service_request_latency_ms`** is hop **total** time **`ArrivalTime → CompletionTime`** (includes queue wait). **`cpu_utilization`** (instance and host) still uses the **sliding window** fed by **`AllocateCPU`/`ReleaseCPU`**; **`concurrent_requests` / memory** track **in-flight** work and release on completion.

## Queueing (FIFO DES + optional instance queue)

- Waiting for CPU is modeled as **elapsed simulation time** until **`cpuStart`** (see above). The legacy **instance `requestQueue`** is still used for explicit enqueue paths (e.g. gauges); it is **not** the primary CPU admission mechanism.
- The simulator does **not** implement **M/M/c** closed-form queueing; **capacity** is enforced by the **CPU FIFO scheduler** plus **memory/host** limits. Dropped or drain-evicted **pending** requests never reach **`RequestStart`**, so they do not emit **`queue_wait_ms`** or success-path service latency.

## Broker / messaging queue (`kind: queue` + `downstream.kind: queue`)

- **Broker edges**: Use **`downstream.kind: queue`** targeting a **`kind: queue`** service. The **topic** is the downstream **endpoint path** on the broker (e.g. `to: broker:/orders`). Producer work charges **`downstream_fraction_cpu`** on the caller, then samples **publish/ack** delay from **`behavior.queue.delivery_latency_ms`** before **`queue_enqueue`**.
- **Async dispatch timing (default)**: Unless **`behavior.queue.async_fire_and_forget: true`**, the producer hop **defers** **`service_request_latency_ms`** / **`service_processing_latency_ms`** until **publish ack time** (caller serialization CPU + delivery latency). The **consumer** runs as a separate request on **`consumer_target`** and does **not** extend the producer hop duration. **`async_fire_and_forget: true`** finalizes the producer hop at local completion (publish ack not included in that hop’s latency).
- **Mixed sync + async queue**: If the hop has both **sync** children and **async queue** edges with deferred ack, parent finalize waits for **both** the last sync child and the publish-ack deadline (`max(ack_time, sync_completion)`).
- **Backpressure**: **`drop_policy`** applies when **`capacity`** is reached (`block` rejects with `reason=block_full`; `reject` / `drop_newest` drop the publish; `drop_oldest` evicts the oldest message). **`queue_drop_count`** records drops with **`reason`**.
- **Consumers**: **`queue_dequeue`** starts a consumer request when **concurrency** allows; **`queue_ack_timeout`** fails slow consumers, increments **redelivery** up to **`max_redeliveries`**, then **`queue_dlq`** (metrics + optional DLQ endpoint in config).
- **Metrics**: **`queue_depth`** (gauge), **`queue_enqueue_count`**, **`queue_dequeue_count`**, **`queue_drop_count`**, **`queue_redelivery_count`**, **`queue_dlq_count`**, **`message_age_ms`**, **`queue_publish_latency_ms`**, with **`broker_service`**, **`topic`/`queue`**, producer/consumer **`service`/`endpoint`**, **`origin`**, **`traffic_class`**, **`source_kind`**, **`reason`** where applicable. **RunMetrics** rollups include **`queue_*_count_total`** and **`queue_depth_sum`** (sum of latest **`queue_depth`** gauges per label set). **Ingress** **`request_count`** remains workload arrivals; broker consumer work is **`origin=downstream`**.

## Service model, kind, and role

- **`model`**: `cpu` — CPU + network + memory follow endpoint stats. `mixed` — same sampling path with higher **memory** influence on concurrency cost (working-set pressure). `db_latency` — **IO/latency dominated**: sampled CPU work is **capped** below network/query latency unless the endpoint explicitly configures high CPU; **QueueMeanWorkMs** in the execution profile reflects IO-weighted means for hints, not synthetic queue delay in DES.
- **`kind` / `role`**: `api_gateway` / `ingress` classify ingress-facing work (queue class `ingress`). `database` / `datastore` use datastore IO queue class. `cache` slightly reduces CPU vs generic services; `external` nudges network latency up. **`kind: queue`** is supported only when **`behavior.queue`** is present (or merged defaults apply): it models a **broker** with per-topic FIFO backlog, **consumer_concurrency**, publish **delivery_latency_ms** (producer ack), **ack_timeout_ms**, **max_redeliveries**, **drop_policy** (`block`, `reject`, `drop_oldest`, `drop_newest`), and optional **dlq** target. `cache` and `external` are accepted and use the generic execution path with light nudges (partially differentiated).
- **`scaling`**: `pkg/config` scaling helpers (`ServiceAllowsBatchScalingAction`, `ServiceAllowsHorizontalScaling`, …) gate **batch** and **online** actions. **Database** services with **no** `scaling` block **horizontal** scaling by default (vertical changes still require an explicit policy when `scaling` is set; when `scaling` is nil on a database, **all** optimizer dimensions are blocked).

### Service `behavior` (optional, backward compatible)

- **`failure_rate` [0,1]**: Bernoulli local failure sampled in `handle_request_start` before CPU admission (combined additively with `endpoint.failure_rate`, capped at 1). Emits `request_error_count` with `reason=local_failure` and user-visible ingress failure when the logical request fails after retries (if any).
- **`saturation_latency_factor`**: Multiplies sampled CPU and network work by `1 + factor * CPUUtilizationAt(instance)` after instance selection (light load-dependent slowdown).
- **`max_connections`**: Default limit for a **datastore-style IO pool** on each replica (parallel FIFO slots, capped at 64 slots internally). **Endpoint** `connection_pool` overrides when `> 0`. Used only when the workload is modeled as datastore IO (see below).
- **`cache`**: When set, the simulator samples **hit** vs **miss** with `hit_rate`; hit path reshapes CPU/net toward `hit_latency_ms`, miss path adds work from `miss_latency_ms`. **Cache hits** skip scheduling downstream edges for that hop (`cache_hit` metadata) and emit `cache_hit_count` / `cache_miss_count`.

### Endpoint fields (optional)

- **`failure_rate`**, **`timeout_ms`**: Local Bernoulli failure (combined with service behavior) and **local processing deadline** from `StartTime`: if CPU + datastore IO + net would finish after `StartTime + timeout_ms`, completion is moved to the deadline and recorded as `local_failure`.
- **`io_ms`**, **`connection_pool`**: For datastore IO modeling, sampled IO duration after CPU (`io_ms` defaults to `net_latency_ms` sampling when unset). `connection_pool` overrides service `behavior.max_connections` when `> 0`.

### Downstream calls (optional)

- **`failure_rate` [0,1]**: After `call_latency_ms` delay, Bernoulli **transport/dependency failure** before the child request starts. Target **`kind: external`** service **`behavior.failure_rate`** is merged independently: \(1-(1-p_{edge})(1-p_{svc})\). Reasons: `dependency_failure` (generic) or `external_failure` (external target). **`retryable`**: when `false`, sync/async retries via `policies.retries` are not scheduled for that edge.
- **`downstream_fraction_cpu` [0,1]** (default **0**): Fraction of a deterministic **reference CPU work** (ms) charged on the **caller’s instance** as extra CPU before the downstream hop is scheduled. The reference is **`call_latency_ms.mean`** when `> 0`; otherwise **`mean_cpu_ms`** of the **target endpoint**; otherwise **0** (no overhead). The work uses **`ReserveCPUWork` → `AllocateCPU` / `ReleaseCPU`** on the parent instance, participates in FIFO CPU scheduling and utilization, and is included in **`service_request_latency_ms`** / **`service_processing_latency_ms`** for that hop (added to the locally measured hop times). **Sync**: overhead completes before the delayed `downstream_call` (network `call_latency_ms`) and child spawn; **root latency** extends because the child subtree starts later. **Async**: overhead is sequenced before the async child is emitted; the parent does not wait on the child. **Retries**: each retry attempt that re-enters the downstream path pays overhead again. **Dependency failure** (`failure_rate`): still sampled at spawn **after** network delay; caller overhead has already run. **Metric**: **`downstream_caller_cpu_ms`** (per-edge attempt, low-cardinality labels including caller/downstream service+endpoint, mode, origin, traffic/source, retry labels when applicable).

### Datastore IO and connection pool (distinct from CPU FIFO)

- For **database** / **datastore** / **`model: db_latency`**, and for **`kind: external`** only when **`io_ms`**, **`connection_pool`**, or **`behavior.max_connections`** is set explicitly (avoids changing legacy external one-phase hops).
- After CPU wall time ends, a **second phase** reserves a **connection slot** for sampled **`io_ms`** (or `net_latency_ms` fallback). **`db_wait_ms`** records queueing for a slot after CPU (`io_start - cpu_end` when positive). **`active_connections`** gauge tracks in-flight pooled IO per instance. **`queue_wait_ms`** remains **CPU** wait only (Arrival → `StartTime` at CPU).

### External dependency failure and timeouts

- Dependency failures are evaluated at **downstream spawn** (after `call_latency_ms`). Existing **`timeout_ms`** on the edge still schedules `downstream_timeout` from child arrival. Retries follow **`policies.retries`** and **`retryable`**.

### Metrics (new series / reasons)

- Reasons: `external_failure`, `dependency_failure`, `local_failure`, `db_connection_timeout`, `db_connection_rejected` (reserved; pool uses FIFO wait rather than reject in the current model).
- Series: `db_wait_ms`, `active_connections` (datastore pool gauge), `cache_hit_count`, `cache_miss_count`, `downstream_caller_cpu_ms` (caller-side downstream serialization / client CPU per edge attempt).

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
- **Fields included**: `metadata.schema_version`; `simulation_limits` (`max_trace_depth`, `max_async_hops`); every host (`id`, `cores`, `memory_gb`); every service (`id`, `kind`, `role`, `replicas`, `model`, `cpu_cores`, `memory_mb`, scaling flags, full optional `behavior` including `cache`); every endpoint (`path`, CPU stats, `default_memory_mb`, `failure_rate`, `timeout_ms`, `io_ms`, `connection_pool`, `net_latency_ms`); every downstream call (full edge: `to`, `mode`, `kind`, probabilities, latencies, `timeout_ms`, `failure_rate`, `retryable`, `downstream_fraction_cpu`); every workload row (`from`, `source_kind`, `traffic_class`, `to`, full `arrival` including bursty parameters); full `policies` (`autoscaling` and `retries` including `backoff` and `base_ms`).
- **Ordering**: Hosts, services, endpoints, downstream edges, and workload rows are hashed in **canonical** sorted order (hosts by `id`, services by `id`, endpoints by `path` with stable tie-break on slice index for duplicate paths, downstream by full tuple + index, workload by full semantic tuple + index). **Service slice order in YAML is not part of identity**—only the multiset of services by `id` matters. If two workload rows are fully identical, relative order is preserved via stable sort so multiplicity stays consistent.
- **Why it matters**: If two behaviorally different scenarios collapsed to the same hash, batch optimization could dedupe them incorrectly, reuse metrics, or reuse seeds, producing wrong recommendations even when the DES is accurate.
