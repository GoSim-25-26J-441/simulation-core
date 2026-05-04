# Manual validation: CPU-primary online scale-up (Phase 3)

Use an **online** optimization run with:

- `optimization.online: true`
- `optimization.optimization_target_primary: "cpu_utilization"`
- `optimization.target_util_high` / `target_util_low` in the usual band (defaults 0.7 / 0.4 if unset)
- `optimization.max_hosts` **greater than** the initial host count from scenario YAML (e.g. initial 1–2 hosts, `max_hosts: 6` or `8`)
- `optimization.min_hosts` at or below the initial count if you expect host scale-in later (Phase 4)
- Service with a **CPU-heavy** endpoint and **horizontal scaling** allowed (default when scaling is unset)
- Start with **low** `replicas` (e.g. 1)

## Workload ramp

1. Start the run, then **PATCH** workload / `rate_rps` (or equivalent dynamic API) to raise ingress significantly so service CPU utilization crosses `target_util_high`.

## Expected progression

1. Per-interval metrics show **service CPU** above the high band.
2. **Replicas** increase (`service CPU above target, scaled replicas up` in optimization history when horizontal scale-up succeeds).
3. If replica adds hit **host capacity / placement** limits, **hosts** increase without requiring P95 above `target_p95_latency_ms` (`service CPU above target, scaled out hosts for placement`).
4. If **cluster host CPU** stays above `target_util_high` while a service is hot or broker pressure exists, hosts may still scale out (`host CPU above target, scaled out hosts`).
5. After a successful host add from a placement failure, the controller **retries** the blocked replica scale once so instances can land on the new host.

## API / tooling

- Observe **SSE** `optimization_step` events or persisted `optimization_history` on the run record.
- `target_p95_ms` / `score_p95_ms` on each step remain populated for compatibility; P95 is **not** required to breach target for CPU-primary host horizontal scale-out.
