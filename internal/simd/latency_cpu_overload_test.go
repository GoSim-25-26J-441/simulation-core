package simd

import (
	"testing"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
)

// Regression: CPU capacity and latency metrics must use simulation time. Under-provisioned CPU
// should show elevated tail latency (queueing), and global mean / percentiles must stay consistent
// (same sample set from request_latency_ms in ConvertToRunMetrics).
func TestRunExecutorCPUSaturationRaisesTailLatency(t *testing.T) {
	store := NewRunStore()
	// Long enough and enough arrivals that a tail of requests waits in queue (p99 catches it).
	const durationMs int64 = 2500
	scenario := `
hosts:
  - id: host-1
    cores: 1
services:
  - id: svc1
    replicas: 1
    model: cpu
    cpu_cores: 1
    memory_mb: 512
    endpoints:
      - path: /test
        mean_cpu_ms: 10
        cpu_sigma_ms: 0
        default_memory_mb: 16
        downstream: []
        net_latency_ms: {mean: 5, sigma: 0}
workload:
  - from: client
    to: svc1:/test
    arrival: {type: constant, rate_rps: 300}
`
	_, err := store.Create("run-cpu-sat-latency", &simulationv1.RunInput{
		ScenarioYaml: scenario,
		DurationMs:   durationMs,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	exec := NewRunExecutor(store, nil)
	if _, err := exec.Start("run-cpu-sat-latency"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rec, ok := store.Get("run-cpu-sat-latency")
		if ok && rec.Run.Status == simulationv1.RunStatus_RUN_STATUS_COMPLETED {
			if rec.Metrics == nil {
				t.Fatalf("expected metrics")
			}
			m := rec.Metrics
			if m.LatencyP50Ms > m.LatencyP95Ms || m.LatencyP95Ms > m.LatencyP99Ms {
				t.Fatalf("percentile ordering broken: p50=%v p95=%v p99=%v", m.LatencyP50Ms, m.LatencyP95Ms, m.LatencyP99Ms)
			}
			// ~15ms service baseline; sustained overload on 1 core must queue — tail above baseline.
			if m.LatencyP99Ms < 25 {
				t.Fatalf("expected P99 >= 25ms under CPU saturation (got %v); capacity may be using wall clock", m.LatencyP99Ms)
			}
			// Mean and P99 from the same latency samples should not diverge by orders of magnitude.
			if m.LatencyP99Ms > 0 && m.LatencyMeanMs > 5*m.LatencyP99Ms {
				t.Fatalf("mean %v vs p99 %v looks inconsistent (same sample set)", m.LatencyMeanMs, m.LatencyP99Ms)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("run did not complete")
}
