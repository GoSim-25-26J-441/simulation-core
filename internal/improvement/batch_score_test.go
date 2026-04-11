package improvement

import (
	"testing"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/batchspec"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestComputeBatchScoreFeasible(t *testing.T) {
	base := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 32}},
		Services: []config.Service{
			{ID: "svc1", Replicas: 1, CPUCores: 1, MemoryMB: 512, Model: "cpu"},
		},
	}
	pb := &simulationv1.BatchOptimizationConfig{}
	spec, err := batchspec.ParseBatchSpec(pb, base)
	if err != nil {
		t.Fatal(err)
	}
	m := &simulationv1.RunMetrics{
		LatencyP95Ms:   100,
		LatencyP99Ms:   200,
		ThroughputRps:  100,
		TotalRequests:  1000,
		FailedRequests: 0,
		ServiceMetrics: []*simulationv1.ServiceMetrics{
			{ServiceName: "svc1", CpuUtilization: 0.55, MemoryUtilization: 0.5},
		},
	}
	sc := ComputeBatchScore(spec, base, base, m)
	if !sc.Feasible {
		t.Fatalf("expected feasible score, got %+v", sc)
	}
}

func TestCompareBatchScoresFeasibleFirst(t *testing.T) {
	a := BatchScore{Feasible: true, ViolationScore: 10, EfficiencyScore: 5}
	b := BatchScore{Feasible: false, ViolationScore: 0, EfficiencyScore: 0}
	if !CompareBatchScores(a, b, 1, 2) {
		t.Fatal("feasible should beat infeasible")
	}
}

func TestHostBandUsesHostMetricsWhenPresent(t *testing.T) {
	base := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 32}},
		Services: []config.Service{
			{ID: "svc1", Replicas: 1, CPUCores: 1, MemoryMB: 512, Model: "cpu"},
		},
	}
	pb := &simulationv1.BatchOptimizationConfig{}
	spec, err := batchspec.ParseBatchSpec(pb, base)
	if err != nil {
		t.Fatal(err)
	}
	mSvc := &simulationv1.RunMetrics{
		LatencyP95Ms:  100,
		LatencyP99Ms:  200,
		TotalRequests: 100,
		ServiceMetrics: []*simulationv1.ServiceMetrics{
			{ServiceName: "svc1", CpuUtilization: 0.55, MemoryUtilization: 0.55},
		},
	}
	mHost := &simulationv1.RunMetrics{
		LatencyP95Ms:  100,
		LatencyP99Ms:  200,
		TotalRequests: 100,
		ServiceMetrics: []*simulationv1.ServiceMetrics{
			{ServiceName: "svc1", CpuUtilization: 0.55, MemoryUtilization: 0.55},
		},
		HostMetrics: []*simulationv1.HostMetrics{
			{HostId: "h1", CpuUtilization: 0.95, MemoryUtilization: 0.95},
		},
	}
	s1 := ComputeBatchScore(spec, base, base, mSvc)
	s2 := ComputeBatchScore(spec, base, base, mHost)
	if s1.HostCPUBal == s2.HostCPUBal {
		t.Fatalf("expected host balance term to change when host_metrics diverge from service aggregate, got %v vs %v", s1.HostCPUBal, s2.HostCPUBal)
	}
}
