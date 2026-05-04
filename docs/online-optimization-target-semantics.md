# Online optimization target semantics (design note)

**Status:** Phase 1 — semantics and contracts only.  
**Scope:** `optimization_target_primary` for **online** runs (`OptimizationConfig.online = true`). Batch optimization uses a separate path (`optimization.objective`, `improvement` package).

---

## Current behavior

### What `optimization_target_primary` controls today

In `runOnlineController` (`internal/simd/executor.go`), the primary string (default `p95_latency` when empty) affects:

1. **Progress / “best score” tracking**  
   `SetOptimizationProgress` is called with `bestScore` derived from `currentScore`, which is:
   - max non-client service **CPU** utilization when primary is `cpu_utilization`;
   - max non-client service **memory** utilization when primary is `memory_utilization`;
   - aggregate **P95 latency** otherwise.  
   SSE `optimization_progress` already gets `objective` / `unit` from `ObjectiveAndUnitForProgress` (`internal/simd/optimization_helpers.go`), so live progress can show `cpu_utilization` with unit `ratio` while steps still look P95-centric (see below).

2. **Service-level replica and vertical CPU scale-up / replica scale-down**  
   For `cpu_utilization` or `memory_utilization`, the controller uses per-service `svcCPUUtil` / `svcMemUtil` against `target_util_low` / `target_util_high` (defaults **0.4** / **0.7** if unset). Scale-up also reacts to **broker pressure** (queue/topic backlog, in-flight, oldest message age) similarly to P95 mode.

3. **Vertical scale-down (CPU / memory)**  
   CPU vertical downscale when primary is `cpu_utilization`; memory vertical downscale when primary is `memory_utilization`, each gated by utilization below the low band, `p95OkForDown`, optional `scale_down_*_util_max`, stabilization ticks, and `onlineScaleDownGuard`.

### What stays P95-driven even when primary is `cpu_utilization` (or `memory_utilization`)

- **Host scale-out** and **vertical host CPU increase while at `max_hosts`**: gated by `p95Guard && currentP95 > targetP95*1.05` **and** `maxHostCPU >= 0.8` (fixed `hostCPUHighThreshold`). So with a positive `target_p95_latency_ms`, host capacity expansion is still **triggered by P95 above target**, not by service CPU alone.
- **Host scale-in**: `hostScaleInCond` requires `p95Guard && currentP95 < targetP95*0.7` plus `scale_down_host_cpu_util_max > 0`, host count above `min_hosts`, and low max host CPU — i.e. **P95 must be “comfortably below” target** for host removal.
- **Replica / vertical scale-down safety**: `p95OkForDown := !p95Guard || currentP95 <= targetP95*1.05` — if `target_p95_latency_ms > 0`, scale-down paths that consult `p95OkForDown` are constrained by P95 vs target.
- **`onlineScaleDownGuard`**: if `target_p95_latency_ms > 0`, blocks scale-down when `LatencyP95 >= target * 0.95`, plus queue depth, concurrency, broker pressure, error-rate rise, etc.

### P95 guardrail when `target_p95_latency_ms` is zero

`p95Guard` is `targetP95 > 0`. If the primary is utilization-based **and** `target_p95_latency_ms` is **0**:

- Host scale-out / host scale-in branches that depend on `p95Guard` **never run**.
- `p95OkForDown` is always true (no P95 cap on scale-down from that flag).
- The P95 branch inside `onlineScaleDownGuard` is skipped (`targetP95 > 0` is false).

So “optional P95 guardrail” today literally means **no** P95-based host orchestration or latency-based guard unless a positive target is set; validation does **not** require `target_p95_latency_ms` for non–`p95_latency` primaries.

### History / replay vs controller signal

- `recordOptimizationStep` always fills `OptimizationStep.target_p95_ms` and `score_p95_ms` with **configured target P95** and **observed P95**, regardless of primary (`internal/simd/executor.go`).
- HTTP JSON (`convertOptimizationStepToJSON` in `internal/simd/http_server.go`) exposes the same as `target_p95_ms` / `score_p95_ms`.
- Some **reason** strings and log lines still say **“p95 above target”** for actions taken under utilization triggers (e.g. vertical CPU update path after utilization/pressure scale-up), which is misleading for CPU-primary runs.

### Validation

`validateOnlineOptimizationConfig` only enforces `target_p95_latency_ms > 0` when primary is **`p95_latency`** (or empty, treated as `p95_latency`). Utilization primaries do not require a P95 target.

### Contrast with batch optimization

Batch scoring uses `improvement.NewObjectiveFunction` (`internal/improvement/objective.go`): for `cpu_utilization` / `memory_utilization` with a valid band, the score is **distance to the band** (`scoreForUtilBand`), minimizing deviation from `[low, high]`. Online control uses **threshold actions** (scale above high / below low) rather than that scalar score, but the **same proto fields** `target_util_low` / `target_util_high` document the band for both modes in `simulation.proto`.

---

## Problem

- Proto comments describe utilization primaries with P95 as a **guardrail**, but **host scaling** is still implemented as **P95-triggered** when `target_p95_latency_ms > 0`.
- Replay and `OptimizationStep` surface **only** P95-named fields for the “score,” while progress can show CPU — **UI implies P95 is the objective** even when the operator chose CPU-primary.
- Mixed reason strings / logs reinforce the same confusion.

---

## Proposed semantics (normative for Phase 2)

### General

- **`optimization_target_primary`** names the **main closed-loop signal** for **service-level** capacity (replicas, per-instance CPU/memory where allowed): keep the process variable inside **`[target_util_low, target_util_high]`** when possible, with hysteresis/stabilization unchanged unless refined in Phase 2.
- **`target_p95_latency_ms`** (when > 0) is a **guardrail** for **destructive or latency-risky moves** (scale-in, replica down, vertical down, and optionally host shrink), **not** the routine scale-up trigger for utilization primaries.
- **Broker / queue / topic pressure** remains a valid **scale-up** trigger (service pressure) independent of P95 for utilization primaries, aligned with “pressure or failed placement” story in Phase 2.
- **`p95_latency` primary** keeps today’s intent: P95 vs `target_p95_latency_ms` drives scale-up/down with utilization gates where applicable — **backwards compatible**.

### CPU-primary policy rules

- **Primary control band:** `target_util_low` / `target_util_high` (defaults 0.4 / 0.7) on **per-service CPU utilization** (non-client services for the max aggregate used in progress is max across services — align Phase 2 wording with whether scale decisions are per-service max or global max; today service loop uses **per-service** util).
- **Scale-up (replicas / vertical CPU):** when CPU is above `target_util_high`, or under sustained broker pressure (existing signals), without requiring P95 > target.
- **Scale-down:** only when CPU is below `target_util_low` (and stabilization), **`hostCount > min_hosts`** for host removal, **`currentReplicas > min_replicas_per_service`**, and **guardrails safe** (P95 if configured, `onlineScaleDownGuard`, topology guard, cooldowns).
- **P95 guardrail:** when `target_p95_latency_ms > 0`, do **not** scale down (replicas, vertical, host shrink) in ways that would violate the guardrail; use existing `onlineScaleDownGuard` / `p95OkForDown` semantics, clarified as **safety only** in docs and UI.
- **Host scale-out:** should **not** require `currentP95 > target_p95` in CPU-primary mode. Triggers should be tied to **host-level pressure** (e.g. high **max host CPU**), **placement / capacity failure** during service scale-out, or **service-level** saturation signals — exact thresholds Phase 2 implements, but the **policy** is: host add is for **capacity / pressure**, not “latency exceeded” as the default gate.
- **Host scale-in:** requires **sustained low host utilization** (existing `scale_down_host_cpu_util_max` / mem analogue), **guardrails safe**, **`hostCount > min_hosts`**, and stabilization — **P95 “comfortably below target” must not be the sole enabler** if a P95 target is set; low host CPU with safe SLO guardrail should suffice (Phase 2 reconciles with current `currentP95 < 0.7 * target` condition).

### Memory-primary policy rules

- Same band semantics on **per-service memory utilization**.
- **Limitation:** memory utilization may stay **0** or very low until allocations / demand appear; the controller may see **no scale-up** from memory alone early in a run. Document that **warm-up / duration**, **workload that exercises memory**, or **pressure-based scale-up** may be required. Optional Phase 2: treat “unknown / zero” memory with a separate policy or minimum observation window (open question).
- **Vertical CPU scale-up branch** under memory-primary still consults **CPU hot threshold (0.8)** in the current code for vertical CPU vs horizontal choice — Phase 2 should either document this as intentional (CPU still caps throughput) or align memory-primary vertical paths with memory signals only.

### P95-primary compatibility

- Require **`target_p95_latency_ms > 0`** (already validated).
- Preserve existing P95 thresholds (`> 1.05 * target` scale-up, `< 0.7 * target` scale-down with utilization gates), host block tied to P95 + hot host CPU, broker pressure behavior, and history field meaning **unchanged** for this mode.

---

## Optimization history / replay contract

**Preserve** existing fields for backwards compatibility:

- `OptimizationStep.target_p95_ms`, `score_p95_ms` remain populated when P95 is meaningful (always populate **observed P95** in `score_p95_ms` for audit if available; `target_p95_ms` = configured guardrail or 0).

**Extend** (Phase 2 — proto / JSON):

- Primary objective id: e.g. `optimization_target_primary` snapshot or `primary_objective`.
- **Objective snapshot** at step: e.g. `primary_score` (ratio or ms), `primary_target_low`, `primary_target_high`, `p95_guardrail_ms` (configured), `p95_observed_ms` (duplicate of today’s score for clarity optional).
- Optional: `decision_signals` map (host max CPU, max service CPU, pressure flags) for replay without re-simulating.

**HTTP / SSE:**

- `optimization_step` JSON should mirror new fields; avoid implying `score_p95_ms` is the optimized quantity when primary ≠ `p95_latency` (deprecate in docs in favor of `p95_observed_ms` + `primary_score`, or add parallel fields and keep old names as aliases).

**Reason strings:** must be **objective-consistent** (no “p95 above target” when the trigger was utilization or host CPU only).

---

## Required config for host scaling

| Goal | Config / condition |
|------|---------------------|
| Host **scale-out** possible | `max_hosts` **>** initial host count from scenario (else `max_hosts` defaults to initial count and caps at initial). |
| Host **scale-in** possible | `min_hosts` **<** current host count when scale-in is desired; `min_hosts` defaults to initial count if unset (so explicit `min_hosts` below initial is required to shrink). |
| Host scale-in (current engine) | `scale_down_host_cpu_util_max > 0` (and today, P95 conditions when `target_p95_latency_ms > 0` — Phase 2 relaxes per policy above). |
| Host memory down | `scale_down_host_mem_util_max > 0`, stabilization, cores/memory above initial template where applicable. |
| P95 guardrail active | `target_p95_latency_ms > 0` (recommended for production CPU-primary even if not used as scale-up trigger). |

**Important:** If `max_hosts` equals initial host count, **horizontal host scale-out is impossible**; only in-place host vertical growth (when P95 block fires today) applies.

---

## Open questions / risks

- **Host scale-out triggers without P95:** need deterministic rules for “placement failure / insufficient host CPU” vs noisy metrics; may require resource manager signals not yet exposed to the controller loop.
- **Memory-primary + vertical CPU path** (CPU ≥ 0.8 for vertical CPU when over memory high) may surprise operators; clarify or change in Phase 2.
- **`ObjectiveAndUnitForProgress`** returns unit `ratio` for utilization primaries but online `OptimizationStep` has no parallel — UI must migrate to new fields.
- **Convergence (`max_noop_intervals`):** still based on config mutations; no change required for Phase 1, but CPU-primary might “flap” near band edges — optional deadband later.
- **`maxServiceUtilization` vs per-service loop:** progress uses **max across services**; replica decisions are **per-service** — document and optionally align in Phase 2 for consistent “global hottest service” story.

---

## Implementation acceptance criteria (Phase 2)

1. **CPU-primary:** Service scale-up triggers from **utilization above `target_util_high`** and/or **broker pressure** without requiring P95 > target; **host scale-out** does not require P95 > target (uses host/pressure/capacity rules per design).
2. **CPU-primary:** **P95 with `target_p95_latency_ms > 0`** blocks unsafe scale-down paths only; does not block justified scale-up solely because P95 is below target.
3. **Host scale-in:** Requires sustained low **host** utilization, guardrails, `hostCount > min_hosts`, stabilization; P95-only gating removed or narrowed per “guardrail only” semantics.
4. **History / replay / SSE:** Expose **primary objective**, **band**, **primary score**, and **P95 guardrail + observed P95**; fix **reason** strings and misleading logs.
5. **P95-primary:** Behavior and tests remain **backwards compatible** (same thresholds and host coupling unless explicitly version-gated).
6. **Docs / proto:** Comments in `simulation.proto` match implemented semantics; optional migration note for API consumers relying on `score_p95_ms` as “the” score.
7. **Tests:** Executor / HTTP tests cover CPU-primary host scale-out without P95 breach, replay JSON shape, and guardrail-only P95.

---

## Likely Phase 2 code touchpoints

| Area | Files (indicative) |
|------|-------------------|
| Online controller decisions & logging | `internal/simd/executor.go` (`runOnlineController`, `onlineScaleDownGuard`, `recordOptimizationStep`, `validateOnlineOptimizationConfig`) |
| Progress / objective naming | `internal/simd/optimization_helpers.go`, `internal/simd/http_server.go` (SSE `optimization_progress`, `optimization_step`) |
| Proto API | `proto/simulation/v1/simulation.proto` (`OptimizationStep`, possibly `OptimizationProgress`), regenerate `gen/go/...` |
| Run store / persistence | `internal/simd/runstore.go` (append step), any gRPC handlers mirroring HTTP |
| Tests | `internal/simd/executor_test.go`, `internal/simd/http_server_test.go`, integration tests under `test/integration/` |
| Docs | `docs/SSE_STREAM_FORMAT.md`, `docs/PROTOBUF.md` if event shapes change |

---

## References (code)

- Online validation: `validateOnlineOptimizationConfig` — `internal/simd/executor.go`
- Controller loop: `runOnlineController` — `internal/simd/executor.go`
- Step persistence: `recordOptimizationStep` — `internal/simd/executor.go`
- SSE objective/unit: `ObjectiveAndUnitForProgress` — `internal/simd/optimization_helpers.go`
- Step JSON: `convertOptimizationStepToJSON` — `internal/simd/http_server.go`
- Batch objectives: `internal/improvement/objective.go`
- Messages: `proto/simulation/v1/simulation.proto` (`OptimizationConfig`, `OptimizationStep`, `OptimizationProgress`)
