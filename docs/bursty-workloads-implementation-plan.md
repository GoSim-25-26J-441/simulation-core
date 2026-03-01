# Bursty Workloads – Implementation Plan

**Branch:** `feature/bursty-workloads`  
**Goal:** Implement true bursty arrival (alternating burst and quiet periods) instead of the current Poisson alias.

---

## 1. Current State

- **Config:** `pkg/config/scenario_types.go` – `ArrivalSpec` already has:
  - `BurstRateRPS` – rate during bursts (req/s)
  - `BurstDurationSeconds` – length of each burst period
  - `QuietDurationSeconds` – length of each quiet period between bursts
- **Behavior:** `internal/simd/workload_state.go` – `case "bursty"` uses the same logic as Poisson (exponential inter-arrival at `RateRPS`). The burst/quiet fields are ignored.
- **Docs:** `docs/DYNAMIC_CONFIGURATION.md` and `docs/BACKEND_INTEGRATION.md` note that bursty is a Poisson alias (TODO).

---

## 2. Target Behavior

- **Burst period:** For `BurstDurationSeconds`, generate arrivals at **`BurstRateRPS`** (or at `RateRPS` if `BurstRateRPS` is 0). Use a suitable process (e.g. Poisson at that rate).
- **Quiet period:** For `QuietDurationSeconds`, generate **no** arrivals (or optionally a low “background” rate if we add a field later).
- **Alternation:** After each burst duration, switch to quiet for `QuietDurationSeconds`; then switch back to burst, and repeat. Start simulation in either burst or quiet (configurable or fixed; e.g. start in burst).
- **Defaults:** If `BurstDurationSeconds` or `QuietDurationSeconds` is missing/zero, treat as “no real bursty” and fall back to current Poisson behavior (so existing YAML without burst params is unchanged).

---

## 3. Implementation Steps

### 3.1 Extend workload state (per-pattern bursty state)

- **File:** `internal/simd/workload_state.go`
- **Changes:**
  - Add optional bursty state to **`WorkloadPatternState`** (or a small struct referenced by it):
    - `InBurst bool` – whether we are currently in a burst period
    - `PeriodEndTime time.Time` – sim time at which the current burst or quiet period ends
  - Keep this state only for patterns with `arrival.type == "bursty"` and valid burst/quiet durations; others stay stateless as today.

### 3.2 Implement bursty logic in `calculateNextArrivalTime`

- **File:** `internal/simd/workload_state.go`
- **Current:** `calculateNextArrivalTime(arrival, currentTime)` returns the next arrival time using only `arrival` and `currentTime` (no per-pattern state).
- **Options:**
  - **A (stateless):** Pass `*WorkloadPatternState` (or bursty state) into `calculateNextArrivalTime` so the bursty case can read/update “am I in burst?” and “when does this period end?”. Caller must persist state after each call.
  - **B (state in pattern):** Keep bursty state inside `WorkloadPatternState` and pass it into `calculateNextArrivalTime`; update it inside the bursty branch (period end, flip InBurst).
- **Logic for bursty (conceptual):**
  1. If `BurstDurationSeconds <= 0 || QuietDurationSeconds <= 0`: keep current behavior (Poisson at `RateRPS`), no new state.
  2. Else:
     - If `currentTime` is past `PeriodEndTime`: advance to next period (flip InBurst, set `PeriodEndTime = currentTime + burstDuration` or `+ quietDuration`).
     - If currently in **quiet**: next “arrival” is at `PeriodEndTime` (no real request); then at that time we’ll flip to burst and generate real arrivals. So we can either:
       - Schedule a no-op and then use burst logic, or
       - Have `calculateNextArrivalTime` return the time when the next burst starts (no arrival during quiet), and have the event loop treat “next event at T” as “switch to burst at T” and then schedule first arrival of the burst.
  3. **Simpler approach:** In burst: next arrival = current time + Poisson(BurstRateRPS). When that time exceeds `PeriodEndTime`, clamp to `PeriodEndTime` and treat the next call as “start of quiet” (return `PeriodEndTime + QuietDurationSeconds` as the next “arrival” time, but that’s actually the start of the next burst; then generate first arrival of next burst). So we need two kinds of “next time”: (1) next request arrival, (2) next period boundary. Cleanest is: **during burst**, compute next arrival; if it’s after period end, next event is period end (switch to quiet); **during quiet**, next event is period end (switch to burst), then generate first arrival of burst.
- **Concrete:** For bursty with valid params:
  - **In burst:** next arrival = min(periodEnd, currentTime + Poisson(BurstRateRPS)). If next arrival == periodEnd, on that tick we don’t emit a request; we set InBurst=false, PeriodEndTime += QuietDurationSeconds, and next call will be “in quiet” so next event time = PeriodEndTime (end of quiet = start of next burst).
  - **In quiet:** next event time = PeriodEndTime (no arrivals); when we “arrive” at that time, set InBurst=true, PeriodEndTime += BurstDurationSeconds, then compute first real arrival with Poisson(BurstRateRPS).
- So the event loop must support “at this time, either emit a request or just advance bursty state”. That implies the generator loop uses the same “next event time” for both: sometimes it’s a request, sometimes it’s a state transition. Easiest: **next event time** is always the next time we need to “wake up”; at that time we either emit a request (and then compute next arrival) or transition period (and then compute next arrival in new period). So `calculateNextArrivalTime` for bursty returns the next wake-up time and a flag or second return “emit request at this time?” or we have a separate method “advance bursty state and return next event time and whether to emit”. Alternatively, we can have “during quiet” return next event time = period end and “emit request = false”, and the loop only schedules a request when emit = true. So we need an extra return (or state) indicating “this event is a period boundary, not a request”.

**Design matching current loop:**  
The loop in `generateNextEvents()` does: (1) schedule a request at `patternState.NextEventTime`, (2) call `calculateNextArrivalTime(arrival, patternState.NextEventTime)` to get next time, (3) set `NextEventTime = nextTime`. So we must support “sometimes the next event is a period boundary, not a request”.

- Extend **return value:** `calculateNextArrivalTime` → `(nextTime time.Time, emitRequest bool)`. For all non-bursty types, return `emitRequest = true`. For bursty with valid params: during burst, if next Poisson(BurstRateRPS) arrival is before period end → return (arrivalTime, true); else → return (periodEnd, false). During quiet → return (periodEnd, false).
- **State:** Add to `WorkloadPatternState`: `InBurst bool`, `PeriodEndTime time.Time`. Only meaningful when `arrival.Type == "bursty"` and burst/quiet durations > 0. Pass `patternState` into `calculateNextArrivalTime` so bursty branch can read/update this state.
- **Loop change:** In `generateNextEvents()`, only call `ScheduleAt(EventTypeRequestArrival, ...)` when `emitRequest == true`; always set `LastEventTime = NextEventTime` and `NextEventTime = nextTime`.

Implement accordingly in `workload_state.go`.

### 3.3 Config validation and defaults

- **File:** `pkg/config/` (loader or parse)
- Validate that for `type: bursty`, if any of `burst_rate_rps`, `burst_duration_seconds`, `quiet_duration_seconds` are set, they are non-negative; optionally require all three for “true” bursty.
- Defaults: e.g. `BurstRateRPS == 0` → use `RateRPS` for burst rate; `QuietDurationSeconds == 0` and `BurstDurationSeconds > 0` → treat as “no quiet” (constant burst) or fall back to Poisson; document in schema.

### 3.4 Tests

- **Unit:** `internal/simd/workload_state_test.go` (or equivalent):
  - Bursty with short burst/quiet: run for a few seconds, count requests; expect requests only during burst windows, none during quiet (or document if we allow a small background rate).
  - Period boundaries: assert that first event after a quiet period is at the start of the next burst and that no events occur during quiet.
- **Integration:** Optional: add a short scenario in `config/scenario.yaml` or a test fixture with bursty workload and assert approximate request counts per window.

### 3.5 Documentation

- **docs/DYNAMIC_CONFIGURATION.md:** Remove the “Bursty is TODO” limitation; describe burst and quiet durations and rates.
- **docs/BACKEND_INTEGRATION.md** (if it describes workload/arrival): Add bursty parameters to the arrival spec.
- **pkg/config doc comments:** Ensure `ArrivalSpec` fields for bursty are clearly documented.

---

## 4. Out of Scope (for this branch)

- Event cancellation when rate/pattern changes (pre-scheduled events unchanged) – already a separate limitation.
- “Background” rate during quiet (can be a follow-up).
- Changing burst/quiet params at runtime via PATCH (future enhancement).

---

## 5. Acceptance Criteria

- For a scenario with `arrival: { type: bursty, rate_rps: X, burst_rate_rps: Y, burst_duration_seconds: B, quiet_duration_seconds: Q }` with B, Q > 0:
  - Requests are generated at ~Y RPS only during burst windows.
  - No requests (or documented low rate) during quiet windows.
  - Burst and quiet durations match config within simulation timing.
- Existing scenarios without bursty, or with bursty but no burst/quiet params, behave unchanged (Poisson at `rate_rps`).
- Tests added and docs updated as above.

---

## 6. Order of Work (suggested)

1. **Plan and branch** (done).
2. **Extend `WorkloadPatternState`** with bursty state; add `calculateNextArrivalTime` return (or struct) for “next time + emit request”.
3. **Implement bursty branch** in `calculateNextArrivalTime` and wire period transitions in the event loop.
4. **Config validation/defaults** in `pkg/config`.
5. **Unit tests** for bursty.
6. **Docs** update and remove TODO references.
