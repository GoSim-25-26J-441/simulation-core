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

func TestComputeBatchScoreUsesIngressErrorRateNotAttemptRate(t *testing.T) {
	base := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 32}},
		Services: []config.Service{
			{ID: "svc1", Replicas: 1, CPUCores: 1, MemoryMB: 512, Model: "cpu"},
		},
	}
	pb := &simulationv1.BatchOptimizationConfig{MaxErrorRate: 0.05}
	spec, err := batchspec.ParseBatchSpec(pb, base)
	if err != nil {
		t.Fatal(err)
	}
	// Many attempt-level errors but 0 user-visible ingress failures.
	m := &simulationv1.RunMetrics{
		LatencyP95Ms:          1,
		LatencyP99Ms:          1,
		IngressRequests:       100,
		IngressFailedRequests: 0,
		FailedRequests:        80,
		TotalRequests:         1000,
		ServiceMetrics: []*simulationv1.ServiceMetrics{
			{ServiceName: "svc1", CpuUtilization: 0.5, MemoryUtilization: 0.5},
		},
	}
	sc := ComputeBatchScore(spec, base, base, m)
	if !sc.Feasible {
		t.Fatalf("expected feasible: ingress error rate 0 but attempt errors high, got %+v", sc)
	}
}

func TestComputeBatchScoreIngressErrorRateViolation(t *testing.T) {
	base := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 32}},
		Services: []config.Service{
			{ID: "svc1", Replicas: 1, CPUCores: 1, MemoryMB: 512, Model: "cpu"},
		},
	}
	pb := &simulationv1.BatchOptimizationConfig{MaxErrorRate: 0.05}
	spec, err := batchspec.ParseBatchSpec(pb, base)
	if err != nil {
		t.Fatal(err)
	}
	m := &simulationv1.RunMetrics{
		LatencyP95Ms:          1,
		LatencyP99Ms:          1,
		IngressRequests:       100,
		IngressFailedRequests: 10,
		FailedRequests:        10,
		TotalRequests:         100,
		ServiceMetrics: []*simulationv1.ServiceMetrics{
			{ServiceName: "svc1", CpuUtilization: 0.5, MemoryUtilization: 0.5},
		},
	}
	sc := ComputeBatchScore(spec, base, base, m)
	if sc.Feasible || sc.ErrViolation <= 0 {
		t.Fatalf("expected infeasible error-rate from ingress 10 percent, got feasible=%v errV=%v", sc.Feasible, sc.ErrViolation)
	}
}

func TestComputeBatchScoreTopicLagViolation(t *testing.T) {
	base := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 32}},
		Services: []config.Service{
			{ID: "svc1", Replicas: 1, CPUCores: 1, MemoryMB: 512, Model: "cpu"},
		},
	}
	pb := &simulationv1.BatchOptimizationConfig{MaxTopicConsumerLagSum: 5}
	spec, err := batchspec.ParseBatchSpec(pb, base)
	if err != nil {
		t.Fatal(err)
	}
	m := &simulationv1.RunMetrics{
		LatencyP95Ms:        1,
		LatencyP99Ms:        1,
		TopicConsumerLagSum: 20,
		ServiceMetrics: []*simulationv1.ServiceMetrics{
			{ServiceName: "svc1", CpuUtilization: 0.5, MemoryUtilization: 0.5},
		},
	}
	sc := ComputeBatchScore(spec, base, base, m)
	if sc.Feasible || sc.TopicLagViolation <= 0 {
		t.Fatalf("expected infeasible topic lag violation, got %+v", sc)
	}
}

func TestComputeBatchScoreTopologyGuardrailViolation(t *testing.T) {
	base := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 32}},
		Services: []config.Service{
			{ID: "svc1", Replicas: 1, CPUCores: 1, MemoryMB: 512, Model: "cpu"},
		},
	}
	pb := &simulationv1.BatchOptimizationConfig{
		MinLocalityHitRate:              0.9,
		MaxCrossZoneRequestFraction:     0.1,
		MaxTopologyLatencyPenaltyMeanMs: 10,
	}
	spec, err := batchspec.ParseBatchSpec(pb, base)
	if err != nil {
		t.Fatal(err)
	}
	m := &simulationv1.RunMetrics{
		LatencyP95Ms:                 1,
		LatencyP99Ms:                 1,
		LocalityHitRate:              0.5,
		CrossZoneRequestFraction:     0.4,
		TopologyLatencyPenaltyMsMean: 40,
		ServiceMetrics: []*simulationv1.ServiceMetrics{
			{ServiceName: "svc1", CpuUtilization: 0.5, MemoryUtilization: 0.5},
		},
	}
	sc := ComputeBatchScore(spec, base, base, m)
	if sc.Feasible || sc.LocalityViolation <= 0 || sc.CrossZoneViolation <= 0 || sc.TopologyLatencyViolation <= 0 {
		t.Fatalf("expected topology guardrail violations, got %+v", sc)
	}
}

func TestCompareBatchScoresPrefersLowerTopologyPenaltyWhenFeasible(t *testing.T) {
	a := BatchScore{Feasible: true, ViolationScore: 0, EfficiencyScore: 10}
	b := BatchScore{Feasible: true, ViolationScore: 0, EfficiencyScore: 12}
	if !CompareBatchScores(a, b, 1, 2) {
		t.Fatal("expected lower efficiency score candidate to win when both feasible")
	}
}
