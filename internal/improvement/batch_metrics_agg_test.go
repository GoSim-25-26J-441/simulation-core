package improvement

import (
	"testing"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
)

func TestAggregateRunMetricsMerge(t *testing.T) {
	a := &simulationv1.RunMetrics{
		LatencyP50Ms:         10,
		LatencyP95Ms:         100,
		LatencyP99Ms:         200,
		LatencyMeanMs:        50,
		ThroughputRps:        10,
		TotalRequests:        100,
		SuccessfulRequests:   100,
		ServiceMetrics: []*simulationv1.ServiceMetrics{
			{ServiceName: "svc1", RequestCount: 100, ErrorCount: 0, LatencyP95Ms: 100, LatencyP99Ms: 200, LatencyMeanMs: 50, CpuUtilization: 0.4, MemoryUtilization: 0.5},
		},
	}
	b := &simulationv1.RunMetrics{
		LatencyP50Ms:         12,
		LatencyP95Ms:         300,
		LatencyP99Ms:         400,
		LatencyMeanMs:        800,
		ThroughputRps:        20,
		TotalRequests:        200,
		SuccessfulRequests:   200,
		ServiceMetrics: []*simulationv1.ServiceMetrics{
			{ServiceName: "svc1", RequestCount: 200, ErrorCount: 0, LatencyP95Ms: 300, LatencyP99Ms: 400, LatencyMeanMs: 800, CpuUtilization: 0.6, MemoryUtilization: 0.7},
		},
	}
	out := AggregateRunMetrics([]*simulationv1.RunMetrics{a, b})
	// Percentiles: max across runs (not average)
	if out.GetLatencyP50Ms() != 12 || out.GetLatencyP95Ms() != 300 || out.GetLatencyP99Ms() != 400 {
		t.Fatalf("latency percentiles want max across runs, got p50=%v p95=%v p99=%v", out.GetLatencyP50Ms(), out.GetLatencyP95Ms(), out.GetLatencyP99Ms())
	}
	// Mean: weighted by successful requests (100*50 + 200*800) / 300
	wantMean := (100*50 + 200*800) / 300.0
	if out.GetLatencyMeanMs() < wantMean-0.01 || out.GetLatencyMeanMs() > wantMean+0.01 {
		t.Fatalf("latency mean: got %v want %v", out.GetLatencyMeanMs(), wantMean)
	}
	if out.GetThroughputRps() != 15 {
		t.Fatalf("tput: %v", out.GetThroughputRps())
	}
	if len(out.ServiceMetrics) != 1 || out.ServiceMetrics[0].GetLatencyP95Ms() != 300 {
		t.Fatalf("service p95 merge: %+v", out.ServiceMetrics[0])
	}
	if out.ServiceMetrics[0].GetCpuUtilization() != 0.5 {
		t.Fatalf("service cpu merge: %+v", out.ServiceMetrics[0])
	}
}
