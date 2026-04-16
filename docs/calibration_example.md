# Calibration and validation example

This document sketches how to use `internal/calibration` with simulator metrics and HTTP/CLI workflows. Supported observation formats are:

- `simulator_export` (`{"window_seconds":..., "run_metrics": {...}}`)
- `observed_metrics` (vendor-neutral partial JSON with explicit presence semantics)
- `prometheus_json` (vendor-neutral `sim_*` JSON samples; no Prometheus client dependency)

## 1. Observed metrics (input)

Conceptually, you capture latency, throughput, utilization, and broker state over a window (e.g. 10 minutes). **Numeric fields use `ObservedValue` with `Present: true` and an explicit `Value`** so a measured **zero** is not treated as “missing.” Use helpers **`calibration.F64`** / **`calibration.I64`** when building observations by hand.

```go
obs := &calibration.ObservedMetrics{
    Window: calibration.ObservationWindow{
        Duration: 10 * time.Minute,
        Source:   "prometheus_export",
    },
    Global: calibration.GlobalObservation{
        IngressThroughputRPS: calibration.F64(120),
        RootLatencyP50Ms:     calibration.F64(18),
        RootLatencyP95Ms:     calibration.F64(85),
        RootLatencyP99Ms:     calibration.F64(160),
        IngressErrorRate:     calibration.F64(0.002),
    },
    Services: []calibration.ServiceObservation{
        {ServiceID: "checkout", CPUUtilization: calibration.F64(0.55), MemoryUtilization: calibration.F64(0.40)},
    },
}
```

For a golden run inside the simulator, use:

```go
rm, _ := simd.RunScenarioForMetrics(scenario, duration, seed, false)
obs := calibration.FromRunMetrics(rm, duration)
```

`FromRunMetrics` marks every copied field as present.

### Broker drop rates (validation vs `RunMetrics`)

Align observed denominators with simulator rollups:

- **Queue** (`queue_drop_rate` in `RunMetrics`): `queue_drop_count / queue_publish_attempt_count`. In `QueueBrokerObservation`, set **`QueuePublishAttemptCount`** when you have producer-side attempt totals. If only **`EnqueueCount`** and **`DropCount`** are available, validation uses **`enqueue + drop`** as an **approximate** attempt count and records a **warning** on `ValidationReport.Warnings`. If **`DropCount`** is present but neither attempts nor enqueue can be inferred, **queue_drop_rate** is **not** compared (warning only).
- **Topic** (`topic_drop_rate` in `RunMetrics`): `topic_drop_count / (topic_deliver_count + topic_drop_count)` per subscriber-style attempts. Set **`TopicDeliverCount`** alongside **`DropCount`**. **`PublishCount`** is producer volume only and is **not** used as a drop-rate denominator. Missing deliver counts when drops are observed → skip comparison + warning.

`RunScenarioForMetrics` fills **`RunMetrics.EndpointRequestStats`** so per-endpoint error rates can be validated against **`EndpointObservation`** rows with concrete paths; **`EndpointPath: "*"`** is compared to the **service-level** max predicted rate (see **`ValidationReport.Warnings`**).

## 2. Calibrated scenario (output summary)

Run a **baseline prediction** on the draft scenario, then calibrate toward `obs`:

```go
pred, _ := simd.RunScenarioForMetrics(draft, duration, seed, false)
calibrated, rep, err := calibration.CalibrateScenario(draft, obs, &calibration.CalibrateOptions{
    PredictedRun: pred,
    Overwrite:    calibration.OverwriteWhenHigherConfidence,
})
```

Inspect `rep.Changes` for paths, old/new values, confidence, and reasons. Review `rep.SkippedLowConfidence`, `rep.Warnings`, and `rep.AmbiguousMappings` for fields that were not updated or ambiguous workload-to-observation mappings.

When `predicted_run` is not supplied over HTTP/CLI, calibration can auto-compute a baseline:

- HTTP `POST /v1/calibrate`: set `sim_duration_ms`, `seeds`, and optionally `auto_predict` (default `true` when `predicted_run` is absent).
- CLI `simd calibrate`: use `-sim-duration-ms`, `-seeds`, and `-auto-predict=true` (default).

## 3. Validation report (summary)

Check whether predictions match observations within tolerances:

```go
vr, err := calibration.ValidateScenario(calibrated, obs, int64(duration.Milliseconds()), &calibration.ValidateOptions{
    Seeds:      []int64{1, 2, 3},
    Tolerances: calibration.DefaultValidationTolerances(),
})
```

You can also tune tolerance bands:

- HTTP `POST /v1/validate`: `validate_options.tolerance_profile` (`default|strict|loose`) and `validate_options.tolerances` (partial JSON override).
- CLI `simd validate`: `-tolerance-profile` plus `-tolerances path/to/tolerances.json`.

- If **`vr.Pass`** is true, the simulator is within default bands for the compared metrics (only fields marked **present** in `obs` participate).
- If false, read **`vr.Checks`** and **`vr.LargestErrors`** for the biggest gaps (throughput, tails, utilization, broker gauges).
- Always read **`vr.Warnings`** for skipped checks (missing broker denominators) or **service-level** fallbacks for endpoint error rates.
- `observed_metrics` can include optional `instance_routing` rows (`service_id`, `endpoint_path`, `instance_id`, `request_share`, `request_count`) for routing skew validation against predicted `route_selection_count` distributions.
- If route-selection samples are unavailable for a row (e.g., no traffic for that endpoint), validation skips that row and emits an explicit warning.

Multi-seed runs use **mean** for central metrics and **max across seeds** for stress-oriented quantities (tail latency, broker sums, drops, DLQ, retries), so validation errs on the side of catching worst-case divergence.
