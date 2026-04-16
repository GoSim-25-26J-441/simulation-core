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
- **Metrics**: **`queue_depth`** (gauge), **`queue_enqueue_count`**, **`queue_dequeue_count`**, **`queue_drop_count`**, **`queue_redelivery_count`**, **`queue_dlq_count`**, **`message_age_ms`**, **`queue_publish_latency_ms`**. State gauges (`queue_depth`, topic backlog/lag gauges) use shard-identity labels only (`broker_service`, `topic`/`queue`, and topic `consumer_group`/`subscriber`), while event counters/latency keep rich producer/consumer labels plus **`origin`**, **`traffic_class`**, **`source_kind`**, **`reason`** where applicable. **`queue_enqueue_count_total`** is accepted enqueues only; `queue_drop_rate` uses queue publish attempts as denominator (accepted + rejected attempts) so it stays in `[0,1]` even when all attempts are dropped. **RunMetrics** rollups include **`queue_*_count_total`** and **`queue_depth_sum`** (sum of latest **`queue_depth`** gauges per shard label set). **Ingress** **`request_count`** remains workload arrivals; broker consumer work is **`origin=downstream`**.

## Broker / pub-sub topic (`kind: topic` + `downstream.kind: topic`)

- **Semantics**: **`kind: topic`** models **fan-out** to **independent consumer groups** (Kafka-style offsets / RabbitMQ fan-out) and partition-scoped queues. Runtime shards are keyed by `(broker_service, topic, partition, consumer_group)`. Each **`behavior.topic.subscribers[]`** entry is one group: **`consumer_group`** key, **`consumer_target`**, **`consumer_concurrency`**, **`ack_timeout_ms`**, **`max_redeliveries`**, **`dlq`**, **`drop_policy`**. A **publish** creates **one queued message per subscriber group** in one assigned partition; backlog depth, in-flight, redelivery, and DLQ are **per partition per group**.
- **Offsets / commit / lag**: Each `(broker, topic, partition)` has a monotonic **log offset** (0,1,2,…) assigned at publish (one logical offset per publish, shared across subscriber fan-out). Each subscriber group tracks a **committed offset frontier** (contiguous offsets considered done). **`topic_consumer_lag`** (gauge) uses **`max(0, high_watermark_exclusive − committed_offset_exclusive)`**, so in-flight work before commit still counts as lag, matching **HW − committed** semantics (not raw queue depth alone). Successful consumer completion **commits** the message offset. Ack-timeout / redelivery **does not** commit until success or DLQ.
- **Per-group enqueue drops**: If a subscriber shard rejects a message (`reject` / `drop_newest` / `block_full` when full), the partition offset was still allocated for that publish; the simulator **commits/skips** that offset for **that group only** via `RecordTopicOffsetProcessed` so consumer lag does not stick permanently while depth/in_flight are zero. `drop_oldest` still commits the evicted head offset separately from accepting the new message.
- **Retention (`topic_retention_expire`)**: When **`retention_ms > 0`**, a DES **`topic_retention_expire`** event is scheduled for **oldest queued enqueue time + retention_ms** (per shard/partition/group), with **per-shard dedup**: a new event is scheduled only if there is no pending schedule or the new **fire time** is **strictly earlier** than the next already-scheduled retention fire (avoids O(publishes) duplicate events for the same earliest-expiry instant). The handler removes queued messages whose age is ≥ retention at the current simulation time, emits **`topic_drop_count`** with **`reason=retention_expired`**, advances the commit frontier for removed offsets (treat as skipped/expired), clears the dedup token, refreshes backlog/lag gauges, and reschedules the next expiry if backlog remains. Late/stale retention events are idempotent.
- **DLQ**: When **`topic_dlq`** fires after max redeliveries, the shard records DLQ and **commits** that message’s offset so lag cannot stick unbounded (“resolved” DLQ, not unresolved ghost lag).
- **Edges**: Use **`downstream.kind: topic`** targeting a **`kind: topic`** service; the **topic path** is the broker **endpoint** (e.g. `to: events-broker:/orders`). Optional edge fields: **`partition_key`** (static string) and **`partition_key_from`** (parent **`Request.Metadata`** field name); if both are set, **`partition_key`** wins for routing hash / round-robin partition choice.
- **Producer timing**: Same async pattern as queue: caller CPU + **`delivery_latency_ms`** unless **`async_fire_and_forget: true`**. **`max_async_hops`** / **`max_trace_depth`** still apply.
- **Metrics**: **`topic_*`** series (`topic_publish_count`, `topic_deliver_count`, `topic_drop_count`, `topic_redelivery_count`, `topic_dlq_count`, `topic_backlog_depth`, `topic_message_age_ms`, `topic_publish_latency_ms`, optional **`topic_consumer_lag`**) with labels **`topic_service`/`broker_service`**, **`topic`**, **`partition`**, **`subscriber`**, **`consumer_group`**, producer/consumer **`service`/`endpoint`**, **`origin`**, **`source_kind`**, **`traffic_class`**, **`reason`**. **`topic_drop_rate`** uses subscriber delivery attempts as denominator (`topic_deliver_count + topic_drop_count`), so fan-out drop rates stay bounded by 1. Retention uses simulated time (`retention_ms`) and emits drops with `reason=retention_expired`. **RunMetrics** rollups: **`topic_*_count_total`**, **`topic_backlog_depth_sum`**, **`topic_consumer_lag_sum`**.

## Broker health SLO snapshots (optimizer/controller inputs)

- **Ingress SLOs vs async SLOs**: Ingress latency/error describe user-visible synchronous paths. Broker backlog/lag SLOs describe asynchronous health and must be tracked independently.
- **Live snapshot fields**: Resource manager exposes queue/topic shard snapshots with **`depth`**, **`in_flight`**, **`max_concurrency`**, **`consumer_target`**, **`oldest_message_age_ms`**, and shard counters (**drop/redelivery/dlq** where tracked).
- **RunMetrics broker risk fields**: `queue_oldest_message_age_ms`, `topic_oldest_message_age_ms`, `max_queue_depth`, `max_topic_backlog_depth`, `max_topic_consumer_lag`, `queue_drop_rate`, `topic_drop_rate` (plus existing queue/topic totals and sums).
- **Aggregation semantics**: Multi-seed aggregation keeps broker risk fields conservative via **max** (`max_*`, oldest-age, drop-rate) while keeping count/sum totals averaged as run-level summaries.

### Batch optimization broker guardrails

- `BatchOptimizationConfig` supports broker guardrails: `max_queue_depth_sum`, `max_topic_backlog_depth_sum`, `max_topic_consumer_lag_sum`, `max_queue_oldest_message_age_ms`, `max_topic_oldest_message_age_ms`, `max_queue_drop_count`, `max_topic_drop_count`, `max_queue_dlq_count`, `max_topic_dlq_count`.
- `ComputeBatchScore` treats broker guardrail breaches as **violation terms** (feasibility-first), alongside p95/p99/error/throughput violations.
- Neighbor ordering (`GenerateBatchNeighbors`) treats broker pressure breaches as **stress**, prioritizing capacity-increasing neighbors first.

### Online controller broker awareness

- The online controller derives broker pressure per consumer target service from live queue/topic shard snapshots.
- Scale-down guard blocks when broker pressure exists for that service (`backlog`, `in_flight`, `oldest_message_age_ms`, drops, or DLQ).
- Scale-up preference is broker-aware: when a consumer target has broker pressure, controller prefers scaling that consumer service (vertical CPU first when allowed, otherwise horizontal replicas).

### Broker resources payload schema

- Broker shard snapshots are exposed under `resources.queues` and `resources.topics` in:
  - `GET /v1/runs/{id}/metrics` (when run manager state is available),
  - SSE `metrics_snapshot` payload (`/metrics/stream`),
  - `GET /v1/runs/{id}/export`,
  - callback notifications (`NotificationPayload.resources`).
- Callback `resources` also carries configuration snapshots when available from `FinalConfig`:
  - `resources.services`
  - `resources.hosts` (including host topology metadata such as `zone`/`labels` when present in scenario)
  - `resources.placements` (including `host_zone`/`host_labels` enrichment when scenario topology is available)
- `resources.queues[]` fields:
  - `broker_service`, `topic`,
  - `depth`, `in_flight`, `max_concurrency`,
  - `consumer_target`,
  - `oldest_message_age_ms`,
  - `drop_count`, `redelivery_count`, `dlq_count`.
- `resources.topics[]` fields:
  - `broker_service`, `topic`, `partition`, `subscriber`, `consumer_group`,
  - `high_watermark`, `committed_offset`, `consumer_lag` (partition log end offset exclusive, group commit frontier exclusive, and lag in offset units),
  - `depth`, `in_flight`, `max_concurrency`,
  - `consumer_target`,
  - `oldest_message_age_ms`,
  - `drop_count`, `redelivery_count`, `dlq_count`.

### Upstream app-map → scenario (transformer guidance)

- **`type: user` / `type: client`**: map to **`workload.from`** with **`source_kind: user|client`** and **`traffic_class: ingress|background`** (not simulator **`services`** entries).
- **`type: gateway`**: **`kind: api_gateway`**, **`role: ingress`**.
- **`type: service`**: **`kind: service`**, **`role: internal`**.
- **`type: database`**: **`kind: database`**, **`role: datastore`**, **`model: db_latency`**.
- **`type: external`**: **`kind: external`**.
- **`type: topic`**: **`kind: topic`** when **multiple subscribers / groups** are required; dependencies **into** a topic are **async/event publish** edges; dependencies **out** are **subscriber** processing, not synchronous RPC.

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
- **Online topology guardrails**: Online optimization accepts `min_locality_hit_rate`, `max_cross_zone_request_fraction`, and `max_topology_latency_penalty_mean_ms`. These guardrails block host scale-in, replica scale-down, and service vertical downscale when the proposed action would happen while topology health is outside configured bounds. If a scenario declares locality routing and the measured `locality_hit_rate` is `0`, the controller treats that as a real all-miss violation, not as missing data.
- **Online topology decision traces**: When a topology guard blocks an action, the controller appends a no-op `OptimizationStep` with identical `previous_config` / `current_config`. The `reason` is a structured `key=value` trace containing `action`, `service_id` when applicable, `decision_reason`, observed topology metrics, and configured guardrail values. Existing optimization history, SSE `optimization_step`, export, and callback paths can surface this without a schema break.
- **Batch**: Neighbor generation uses `ServiceAllowsBatchScalingAction` so database defaults and explicit `scaling` flags are respected.
- **Placement**: Initial scenario load and runtime **scale-out** require a host that satisfies topology and capacity constraints. Supported placement knobs are `required_host_labels`, `preferred_host_labels`, `required_zones`, `preferred_zones`, legacy `affinity_zones`/`anti_affinity_zones`, `anti_affinity_services`, `spread_across_zones`, and `max_replicas_per_host`. If no host satisfies constraints, initialization or `ScaleService` fails with a clear placement/capacity reason.
- **Topology-aware host scale-out**: Online and batch host scale-out prefer template hosts whose zone/labels satisfy pending service placement needs, so new capacity is added in feasible zones first.
- **Topology-aware host scale-in search**: Batch `HOST_SCALE_IN` now evaluates removal of every removable host (not only the last host). Candidates are ordered to try lower-risk removals first: empty hosts, then auto-created hosts, then lower-impact hosts; feasibility is still validated by `resource.Manager.InitializeFromScenario`.

## Batch topology-aware optimization

- Batch config now includes topology guardrails:
  - `min_locality_hit_rate`
  - `max_cross_zone_request_fraction`
  - `max_topology_latency_penalty_mean_ms`
- Batch penalty weights now include:
  - `penalty_weights.locality`
  - `penalty_weights.cross_zone`
  - `penalty_weights.topology_latency`
- Feasibility-first ordering is unchanged:
  - Topology guardrail breaches are added to `violation_score` (hard constraints).
  - Topology soft penalties are added to `efficiency_score` (tie-break among similarly feasible candidates).
- Neighbor generation adds topology-sensitive host heuristics:
  - Host scale-in scoring penalizes removing scarce required-zone capacity.
  - Host scale-out zone preference is biased toward zones likely to improve locality under cross-zone pressure.

## Workload: uniform arrivals

- **Non-realtime (bounded horizon)**: For `arrival.type: uniform`, the simulator uses **N = round(rate_rps × horizon_seconds)** independent uniform offsets in **[start, end)** (sorted), matching the legacy `internal/workload.Generator.scheduleUniformArrivals` full-horizon behavior.
- **Realtime / online (lazy)**: Uniform arrivals are generated in **lazy** windows of `EventGenerationLookaheadWindow` (10s) without materializing the full online horizon. For each chunk **[chunkStart, chunkEnd)**, the **count** is **floor(rate × sec1) − floor(rate × sec0)** where **sec0/sec1** are seconds since the pattern **Epoch** at chunk start/end (Epoch is re-anchored on `Start`, `UpdateRate`, and `UpdatePattern` for uniform in realtime mode). **N** arrivals are placed as **i.i.d. uniform** within the chunk interval (same placement helper as non-realtime). This preserves long-run rate (e.g. 0.01 RPS over 1000s → 10 arrivals) without per-chunk independent rounding bias.

## Randomness

- **RunInput.seed** (non-zero): A single **effectiveRunSeed** is chosen per run and passed to **scenario state** (main RNG + `seed+2` for interaction branching) and **workload** (`seed+1` for workload generator). **Seed 0**: one bootstrap `int64` is generated per run for both subsystems so they stay aligned for that execution.

## Event ordering

- Events with the same `Time` and `Priority` are ordered by monotonic **Sequence** assigned at schedule time (tie-breaker for deterministic replay).

## Routing and load balancing

- Services/endpoints can define optional `routing` policy with strategy:
  `round_robin` (default), `random`, `least_connections`, `least_queue`, `least_cpu`,
  `weighted_round_robin`, `sticky`.
- Request placement now uses **`resource.Manager.SelectInstanceForRequest(service, request, simTime)`**.
  It is request-aware, ignores draining replicas, and uses current DES state (active requests, queue length, CPU utilization at simulation time) for least-* strategies.
- Optional locality preference: `routing.locality_zone_from` reads a request metadata key (for example `client_zone`) and prefers instances on hosts whose `zone` matches that value. If no instances match, routing falls back to the full active instance set (no hard failure).
- Workload-driven locality metadata: `workload[].metadata` is copied into generated request metadata for each arrival (`source_kind` / `traffic_class` are still mirrored as dedicated fields). This allows normal scenario YAML to provide keys like `client_zone` consumed by `locality_zone_from`.
- Endpoint routing policy still overrides service routing policy for all routing fields, including `locality_zone_from`.
- Sticky routing uses `sticky_key_from` to hash a request metadata field to a stable active instance.
  If the key is missing, routing falls back to round-robin (explicit fallback).
- Weighted round robin uses per-instance weights (`weights[instance_id]`) with deterministic cursor progression.
- Weighted round robin supports fractional weights by mapping each weight onto a fixed deterministic wheel scale (1000 slots per unit weight). `0` means no weighted traffic for that instance; if all effective weights are zero, routing falls back to round-robin explicitly.
- Random/weighted strategies are deterministic under fixed run seed (`SetRoutingSeed(seed)` from scenario state bootstrap).
- Scale-out replicas become routable immediately after manager cache rebuild; draining replicas stop receiving new work immediately.
- Routing observability metrics:
  - `route_selection_count` with labels `{service, endpoint, instance, strategy}`
  - `route_rejection_count` with labels `{service, endpoint, strategy}` when no eligible instance exists
  - `locality_route_hit_count` / `locality_route_miss_count` with labels `{service, endpoint, instance, host, host_zone, requested_zone, origin, traffic_class, source_kind}` when `locality_zone_from` is configured and request metadata carries the requested zone.
  - `same_zone_request_count` / `cross_zone_request_count` with the same topology labels; for downstream calls these compare caller host zone vs selected callee host zone when known.
- RunMetrics topology rollups (backward-compatible additions):
  - `locality_hit_rate`
  - `same_zone_request_count_total`
  - `cross_zone_request_count_total`
  - `cross_zone_request_fraction`
  - `cross_zone_latency_penalty_ms_*` (inter-zone hops only)
  - `same_zone_latency_penalty_ms_*` (same-zone, different-host hops when configured)
  - `external_latency_ms_*` (hops to `kind: external` when `external_latency_ms` / per-service override is set)
  - `topology_latency_penalty_ms_*` (aggregate sampled penalty for every downstream hop that applied a topology-class overlay)

## Network topology overlays (`scenario.network`)

- **Directed cross-zone map**: `network.cross_zone_latency_ms` is keyed by **caller zone → callee zone**. If `symmetric_cross_zone_latency` is **false** (default), there is **no automatic reverse edge**—list both directions explicitly, or use `default_cross_zone_latency_ms`, or set symmetric to **true** to reuse the reverse entry when the forward key is absent.
- **Hop classes** (all optional; zero mean/sigma preserves legacy “no extra penalty” behavior):
  - **`same_host_latency_ms`**: Same caller instance host as callee instance (when `caller_instance_id` resolves).
  - **`same_zone_latency_ms`**: Same zone, **different** hosts (requires both host IDs).
  - **`cross_zone_latency_ms` / `default_cross_zone_latency_ms`**: Different zones.
  - **`external_latency_ms`**: Default overlay for downstream hops whose target service has **`kind: external`**. A per-service **`external_network_latency_ms`** overrides the scenario default for that service.
- **Precedence** (for non-`external` services): same host → same zone (different hosts) → cross zone. **`external`** hops use only the external overlay (and do not combine with zone classes), independent of host zones.
- **Metrics**: Cross-zone penalties still populate **`cross_zone_latency_penalty_ms`**. Same-zone-different-host and external overlays use **`same_zone_latency_penalty_ms`** and **`external_latency_penalty_ms`** respectively. **`topology_latency_penalty_ms`** duplicates the per-hop total applied penalty for aggregation (`topology_latency_penalty_ms_total` / `_mean` on **`RunMetrics`**).

## Scenario identity / optimizer hashing

- **Single source of truth**: `internal/batchspec.ConfigHash` fingerprints the full v2 scenario for batch candidate deduplication, `CandidateStore` lookup (`hash → runID`), and deterministic per-candidate seeds (`seed = int64(ConfigHash(scenario)) ^ …` in batch evaluation). `internal/improvement.configsMatch` delegates to `batchspec.ScenarioSemanticsEqual` (hash equality) so the optimizer and orchestrator never disagree on “same scenario.”
- **Fields included**: `metadata.schema_version`; `simulation_limits` (`max_trace_depth`, `max_async_hops`); optional `network` (`symmetric_cross_zone_latency`, `same_host_latency_ms`, `same_zone_latency_ms`, `default_cross_zone_latency_ms`, `cross_zone_latency_ms` pairs, `external_latency_ms`); every host (`id`, `cores`, `memory_gb`, `zone`, `labels`); every service (`id`, `kind`, `role`, `replicas`, `model`, `cpu_cores`, `memory_mb`, optional `external_network_latency_ms`, scaling flags, optional `placement` including required/preferred zones, required/preferred labels, affinity/anti-affinity, spread, and max-per-host, full optional `behavior` including `cache`, optional `routing` including `locality_zone_from`/`sticky_key_from`/`weights`); every endpoint (`path`, CPU stats, `default_memory_mb`, `failure_rate`, `timeout_ms`, `io_ms`, `connection_pool`, `net_latency_ms`, optional `routing` including locality/sticky/weights); every downstream call (full edge: `to`, `mode`, `kind`, probabilities, latencies, `timeout_ms`, `failure_rate`, `retryable`, `downstream_fraction_cpu`); every workload row (`from`, `source_kind`, `traffic_class`, `to`, full `arrival` including bursty parameters); full `policies` (`autoscaling` and `retries` including `backoff` and `base_ms`).
- **Ordering**: Hosts, services, endpoints, downstream edges, and workload rows are hashed in **canonical** sorted order (hosts by `id`, services by `id`, endpoints by `path` with stable tie-break on slice index for duplicate paths, downstream by full tuple + index, workload by full semantic tuple + index). **Service slice order in YAML is not part of identity**—only the multiset of services by `id` matters. If two workload rows are fully identical, relative order is preserved via stable sort so multiplicity stays consistent.
- **Why it matters**: If two behaviorally different scenarios collapsed to the same hash, batch optimization could dedupe them incorrectly, reuse metrics, or reuse seeds, producing wrong recommendations even when the DES is accurate.

## Calibration and validation (`internal/calibration`)

- **ObservedMetrics**: Vendor-neutral structs with **`ObservedValue[T]{Present, Value}`** on scalar observation fields so **explicit zero** is not confused with **missing**. Helpers **`F64` / `I64`** mark populated values. **`WorkloadTargetObservation`** optionally maps throughput to **`workload.to` / `traffic_class` / `source_kind`**. Used as **targets** for calibration and as **expected** values for validation.
- **FromRunMetrics**: Converts a completed simulator **`RunMetrics` plus window duration** into `ObservedMetrics` (golden runs and tests), marking fields **`Present`** for every quantity copied from the run. Fills **ingress throughput** from recorded ingress RPS or `ingress_requests` / window, or **`total_requests` / window** when ingress split is absent.
- **CalibrateScenario**: Clones the scenario via **`cloneScenarioViaYAML`** (marshal scenario to YAML and parse back) so **`internal/calibration` does not import `internal/improvement`**. That avoids **`calibration` → `improvement` → `simd` → `calibration`** package cycles while still producing an independent copy for edits. Ratio-based updates apply when **`CalibrateOptions.PredictedRun`** is set (or when HTTP/CLI **auto-predict** runs **`RunBaselinePredictedRun`**). **`shouldApply(overwrite, fieldEmpty, confidence, ConfidenceFloor)`** gates each heuristic. Global workload scaling: **mixed `traffic_class`** with external observations uses **sum of ingress-like rates** as the denominator (see **`CalibrationReport.AmbiguousMappings` / `Warnings`**); **`simulator_run_metrics`** with mixed classes scales **all** workloads to preserve **`FromRunMetrics`** golden behavior. Per-row **`WorkloadTargets`** override. Skipped low-confidence updates are listed in **`CalibrationReport.SkippedLowConfidence`**.
- **ValidateScenario**: Runs **`simd.RunScenarioForMetrics`** per seed, then **aggregates** conservatively (mean for throughput, root P50/mean; max across seeds for tails, error/drop rates, broker stress, DLQ counts, oldest message ages, retries, timeouts). **Only `Present` observation fields** are checked. Queue/topic **observed drop rates** use the same denominators as **`RunMetrics`** (queue: publish attempts, with enqueue+drop fallback + warning; topic: deliver+drop, never publish). **`RunMetrics.EndpointRequestStats`** (from collector labels) enables **per-endpoint** error-rate checks; **`ValidationReport.Warnings`** note approximate denominators, skipped broker checks, or **`*`** / missing-rollups using service-level predictions.
- **Outcome**: **`ValidationReport.Pass`**, **`MetricCheckResult`** rows, **`largest_errors`**, and **`Warnings`**. **`CalibrationReport`** records **`Changes`**, **`Warnings`**, **`Skipped`**, **`SkippedLowConfidence`**, and **`AmbiguousMappings`**.
