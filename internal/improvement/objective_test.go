package improvement

import (
	"math"
	"testing"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func floatEqual(a, b float64) bool {
	const eps = 1e-9
	return math.Abs(a-b) < eps
}

func TestNewObjectiveFunction(t *testing.T) {
	tests := []struct {
		name      string
		objType   string
		wantErr   bool
		checkName func(ObjectiveFunction) bool
	}{
		{
			name:    "P95 latency",
			objType: "p95_latency_ms",
			wantErr: false,
			checkName: func(obj ObjectiveFunction) bool {
				return obj.Name() == "p95_latency_ms"
			},
		},
		{
			name:    "P99 latency",
			objType: "p99_latency_ms",
			wantErr: false,
			checkName: func(obj ObjectiveFunction) bool {
				return obj.Name() == "p99_latency_ms"
			},
		},
		{
			name:    "mean latency",
			objType: "mean_latency_ms",
			wantErr: false,
			checkName: func(obj ObjectiveFunction) bool {
				return obj.Name() == "mean_latency_ms"
			},
		},
		{
			name:    "throughput",
			objType: "throughput_rps",
			wantErr: false,
			checkName: func(obj ObjectiveFunction) bool {
				return obj.Name() == "throughput_rps"
			},
		},
		{
			name:    "error rate",
			objType: "error_rate",
			wantErr: false,
			checkName: func(obj ObjectiveFunction) bool {
				return obj.Name() == "error_rate"
			},
		},
		{
			name:    "cost",
			objType: "cost",
			wantErr: false,
			checkName: func(obj ObjectiveFunction) bool {
				return obj.Name() == "cost"
			},
		},
		{
			name:    "cpu_utilization",
			objType: "cpu_utilization",
			wantErr: false,
			checkName: func(obj ObjectiveFunction) bool {
				return obj.Name() == "cpu_utilization"
			},
		},
		{
			name:    "memory_utilization",
			objType: "memory_utilization",
			wantErr: false,
			checkName: func(obj ObjectiveFunction) bool {
				return obj.Name() == "memory_utilization"
			},
		},
		{
			name:    "unknown objective",
			objType: "unknown",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj, err := NewObjectiveFunction(tt.objType, nil)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for unknown objective type")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if obj == nil {
				t.Fatalf("expected non-nil objective function")
			}
			if tt.checkName != nil && !tt.checkName(obj) {
				t.Fatalf("objective name check failed")
			}
		})
	}
}

func TestP95LatencyObjective(t *testing.T) {
	obj := &P95LatencyObjective{}
	if obj.Name() != "p95_latency_ms" {
		t.Fatalf("expected name p95_latency_ms, got %s", obj.Name())
	}
	if !obj.Direction() {
		t.Fatalf("expected minimize direction")
	}

	// Test with valid metrics
	metrics := &simulationv1.RunMetrics{
		LatencyP95Ms: 100.5,
	}
	score, err := obj.Evaluate(metrics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score != 100.5 {
		t.Fatalf("expected score 100.5, got %f", score)
	}

	// Test with nil metrics
	_, err = obj.Evaluate(nil)
	if err == nil {
		t.Fatalf("expected error for nil metrics")
	}

	// Test with zero latency (should return high penalty)
	metrics.LatencyP95Ms = 0
	score, err = obj.Evaluate(metrics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score != highPenaltyScore {
		t.Fatalf("expected high penalty for zero latency, got %f", score)
	}
}

func TestP99LatencyObjective(t *testing.T) {
	obj := &P99LatencyObjective{}
	if obj.Name() != "p99_latency_ms" {
		t.Fatalf("expected name p99_latency_ms, got %s", obj.Name())
	}
	if !obj.Direction() {
		t.Fatalf("expected minimize direction")
	}

	// Test with valid metrics
	metrics := &simulationv1.RunMetrics{
		LatencyP99Ms: 150.5,
	}
	score, err := obj.Evaluate(metrics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score != 150.5 {
		t.Fatalf("expected score 150.5, got %f", score)
	}

	// Test with nil metrics
	_, err = obj.Evaluate(nil)
	if err == nil {
		t.Fatalf("expected error for nil metrics")
	}

	// Test with zero latency (should return high penalty)
	metrics.LatencyP99Ms = 0
	score, err = obj.Evaluate(metrics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score != highPenaltyScore {
		t.Fatalf("expected high penalty for zero latency, got %f", score)
	}
}

func TestMeanLatencyObjective(t *testing.T) {
	obj := &MeanLatencyObjective{}
	if obj.Name() != "mean_latency_ms" {
		t.Fatalf("expected name mean_latency_ms, got %s", obj.Name())
	}
	if !obj.Direction() {
		t.Fatalf("expected minimize direction")
	}

	// Test with valid metrics
	metrics := &simulationv1.RunMetrics{
		LatencyMeanMs: 75.5,
	}
	score, err := obj.Evaluate(metrics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score != 75.5 {
		t.Fatalf("expected score 75.5, got %f", score)
	}

	// Test with nil metrics
	_, err = obj.Evaluate(nil)
	if err == nil {
		t.Fatalf("expected error for nil metrics")
	}

	// Test with zero latency (should return high penalty)
	metrics.LatencyMeanMs = 0
	score, err = obj.Evaluate(metrics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score != highPenaltyScore {
		t.Fatalf("expected high penalty for zero latency, got %f", score)
	}
}

func TestThroughputObjective(t *testing.T) {
	obj := &ThroughputObjective{}
	if obj.Name() != "throughput_rps" {
		t.Fatalf("expected name throughput_rps, got %s", obj.Name())
	}
	if obj.Direction() {
		t.Fatalf("expected maximize direction (false)")
	}

	// Test with valid metrics
	metrics := &simulationv1.RunMetrics{
		ThroughputRps: 100.0,
	}
	score, err := obj.Evaluate(metrics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// For maximization, we return negative
	if score != -100.0 {
		t.Fatalf("expected score -100.0, got %f", score)
	}

	// Test with nil metrics
	_, err = obj.Evaluate(nil)
	if err == nil {
		t.Fatalf("expected error for nil metrics")
	}

	// Test with zero throughput (should return high penalty)
	metrics.ThroughputRps = 0
	score, err = obj.Evaluate(metrics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score != highPenaltyScore {
		t.Fatalf("expected high penalty for zero throughput, got %f", score)
	}

	// Test with negative throughput (should return high penalty)
	metrics.ThroughputRps = -10.0
	score, err = obj.Evaluate(metrics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score != highPenaltyScore {
		t.Fatalf("expected high penalty for negative throughput, got %f", score)
	}
}

func TestErrorRateObjective(t *testing.T) {
	obj := &ErrorRateObjective{}

	// Test with valid metrics
	metrics := &simulationv1.RunMetrics{
		TotalRequests:  100,
		FailedRequests: 5,
	}
	score, err := obj.Evaluate(metrics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := 5.0 / 100.0
	if score != expected {
		t.Fatalf("expected score %f, got %f", expected, score)
	}

	// Test with nil metrics
	_, err = obj.Evaluate(nil)
	if err == nil {
		t.Fatalf("expected error for nil metrics")
	}

	// Test with zero requests
	metrics.TotalRequests = 0
	score, err = obj.Evaluate(metrics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score != 0 {
		t.Fatalf("expected score 0 for zero requests, got %f", score)
	}
}

func TestCostObjective(t *testing.T) {
	obj := &CostObjective{}

	// Test with valid metrics
	metrics := &simulationv1.RunMetrics{
		ServiceMetrics: []*simulationv1.ServiceMetrics{
			{
				CpuUtilization:    0.5,
				MemoryUtilization: 0.3,
				ActiveReplicas:    2,
			},
			{
				CpuUtilization:    0.7,
				MemoryUtilization: 0.4,
				ActiveReplicas:    3,
			},
		},
	}
	score, err := obj.Evaluate(metrics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// avgCPU = (0.5 + 0.7) / 2 = 0.6
	// avgMemory = (0.3 + 0.4) / 2 = 0.35
	// totalReplicas = 2 + 3 = 5
	// cost = 0.6 * 0.4 + 0.35 * 0.3 + 5 * 0.3 = 0.24 + 0.105 + 1.5 = 1.845
	expected := 0.6*0.4 + 0.35*0.3 + 5.0*0.3
	if score != expected {
		t.Fatalf("expected score %f, got %f", expected, score)
	}

	// Test with nil metrics
	_, err = obj.Evaluate(nil)
	if err == nil {
		t.Fatalf("expected error for nil metrics")
	}

	// Test with no service metrics
	metrics.ServiceMetrics = nil
	score, err = obj.Evaluate(metrics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score != 0 {
		t.Fatalf("expected score 0 for no services, got %f", score)
	}
}

func TestEvaluateInfrastructureCostSensitivity(t *testing.T) {
	base := &config.Scenario{
		Services: []config.Service{
			{ID: "svc1", Replicas: 2, CPUCores: 1.0, MemoryMB: 1024},
		},
	}
	baseScore := EvaluateInfrastructureCost(base)

	// Lower memory should reduce cost (all else equal)
	lowerMemory := &config.Scenario{
		Services: []config.Service{
			{ID: "svc1", Replicas: 2, CPUCores: 1.0, MemoryMB: 512},
		},
	}
	if score := EvaluateInfrastructureCost(lowerMemory); score >= baseScore {
		t.Fatalf("expected lower memory to reduce cost: base=%f lowerMemory=%f", baseScore, score)
	}

	// Lower CPU should reduce cost (all else equal)
	lowerCPU := &config.Scenario{
		Services: []config.Service{
			{ID: "svc1", Replicas: 2, CPUCores: 0.5, MemoryMB: 1024},
		},
	}
	if score := EvaluateInfrastructureCost(lowerCPU); score >= baseScore {
		t.Fatalf("expected lower CPU to reduce cost: base=%f lowerCPU=%f", baseScore, score)
	}

	// Lower replicas should reduce cost (all else equal)
	lowerReplicas := &config.Scenario{
		Services: []config.Service{
			{ID: "svc1", Replicas: 1, CPUCores: 1.0, MemoryMB: 1024},
		},
	}
	if score := EvaluateInfrastructureCost(lowerReplicas); score >= baseScore {
		t.Fatalf("expected lower replicas to reduce cost: base=%f lowerReplicas=%f", baseScore, score)
	}
}

func TestEvaluateInfrastructureCostDefaults(t *testing.T) {
	// Missing/zero fields should fall back to defaults and still produce finite cost.
	cfg := &config.Scenario{
		Services: []config.Service{
			{ID: "svc1", Replicas: 0, CPUCores: 0, MemoryMB: 0},
		},
	}
	score := EvaluateInfrastructureCost(cfg)
	if score <= 0 || math.IsNaN(score) || math.IsInf(score, 0) {
		t.Fatalf("expected positive finite score with defaults, got %f", score)
	}

	if EvaluateInfrastructureCost(nil) != highPenaltyScore {
		t.Fatalf("expected high penalty for nil scenario")
	}
}

func TestCPUUtilizationObjective(t *testing.T) {
	obj := &CPUUtilizationObjective{}
	if obj.Name() != "cpu_utilization" {
		t.Fatalf("expected name cpu_utilization, got %s", obj.Name())
	}
	if !obj.Direction() {
		t.Fatalf("expected minimize direction")
	}

	// Test with nil metrics
	_, err := obj.Evaluate(nil)
	if err == nil {
		t.Fatalf("expected error for nil metrics")
	}

	// Test with empty ServiceMetrics (no non-client services)
	metrics := &simulationv1.RunMetrics{ServiceMetrics: []*simulationv1.ServiceMetrics{}}
	score, err := obj.Evaluate(metrics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score != highPenaltyScore {
		t.Fatalf("expected high penalty for no services, got %f", score)
	}

	// Test with one non-client service
	metrics.ServiceMetrics = []*simulationv1.ServiceMetrics{
		{ServiceName: "svc1", CpuUtilization: 0.6},
	}
	score, err = obj.Evaluate(metrics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score != 0.6 {
		t.Fatalf("expected score 0.6, got %f", score)
	}

	// Test with multiple services (max is chosen)
	metrics.ServiceMetrics = []*simulationv1.ServiceMetrics{
		{ServiceName: "svc1", CpuUtilization: 0.3},
		{ServiceName: "svc2", CpuUtilization: 0.8},
		{ServiceName: "svc3", CpuUtilization: 0.5},
	}
	score, err = obj.Evaluate(metrics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score != 0.8 {
		t.Fatalf("expected score 0.8 (max), got %f", score)
	}

	// Test that client-prefixed service is skipped (only client-one counts as client)
	metrics.ServiceMetrics = []*simulationv1.ServiceMetrics{
		{ServiceName: "client-one", CpuUtilization: 0.9},
		{ServiceName: "svc1", CpuUtilization: 0.4},
	}
	score, err = obj.Evaluate(metrics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score != 0.4 {
		t.Fatalf("expected score 0.4 (client skipped), got %f", score)
	}

	// All client services: should return high penalty
	metrics.ServiceMetrics = []*simulationv1.ServiceMetrics{
		{ServiceName: "client-a", CpuUtilization: 0.9},
	}
	score, err = obj.Evaluate(metrics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score != highPenaltyScore {
		t.Fatalf("expected high penalty when only client services, got %f", score)
	}
}

func TestCPUUtilizationObjectiveWithBand(t *testing.T) {
	// Band [0.4, 0.7]: score 0 inside, else distance to nearest bound
	band := &UtilizationTarget{Low: 0.4, High: 0.7}
	if !band.Valid() {
		t.Fatalf("expected valid band")
	}
	obj, err := NewObjectiveFunction("cpu_utilization", band)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cpuObj := obj.(*CPUUtilizationObjective)
	if cpuObj.TargetLow != 0.4 || cpuObj.TargetHigh != 0.7 {
		t.Fatalf("expected band 0.4-0.7, got %f-%f", cpuObj.TargetLow, cpuObj.TargetHigh)
	}

	metricsWith := func(u float64) *simulationv1.RunMetrics {
		return &simulationv1.RunMetrics{
			ServiceMetrics: []*simulationv1.ServiceMetrics{
				{ServiceName: "svc1", CpuUtilization: u},
			},
		}
	}

	// Inside band: score 0
	for _, u := range []float64{0.4, 0.5, 0.7} {
		score, err := obj.Evaluate(metricsWith(u))
		if err != nil {
			t.Fatalf("unexpected error for util %f: %v", u, err)
		}
		if !floatEqual(score, 0) {
			t.Errorf("util %f: expected score 0 (in band), got %f", u, score)
		}
	}

	// Below band: score = target_low - u
	score, _ := obj.Evaluate(metricsWith(0.3))
	if !floatEqual(score, 0.1) {
		t.Errorf("util 0.3: expected score 0.1, got %f", score)
	}
	score, _ = obj.Evaluate(metricsWith(0.0))
	if !floatEqual(score, 0.4) {
		t.Errorf("util 0.0: expected score 0.4, got %f", score)
	}

	// Above band: score = u - target_high
	score, _ = obj.Evaluate(metricsWith(0.8))
	if !floatEqual(score, 0.1) {
		t.Errorf("util 0.8: expected score 0.1, got %f", score)
	}
	score, _ = obj.Evaluate(metricsWith(1.0))
	if !floatEqual(score, 0.3) {
		t.Errorf("util 1.0: expected score 0.3, got %f", score)
	}
}

func TestUtilizationTargetValid(t *testing.T) {
	if (&UtilizationTarget{Low: 0.4, High: 0.7}).Valid() != true {
		t.Error("expected valid for 0.4-0.7")
	}
	if (&UtilizationTarget{Low: 0, High: 1}).Valid() != true {
		t.Error("expected valid for 0-1")
	}
	if (*UtilizationTarget)(nil).Valid() != false {
		t.Error("nil should be invalid")
	}
	if (&UtilizationTarget{}).Valid() != false {
		t.Error("zero value: Low >= High should be invalid")
	}
	if (&UtilizationTarget{Low: 0.7, High: 0.4}).Valid() != false {
		t.Error("Low >= High should be invalid")
	}
	if (&UtilizationTarget{Low: -0.1, High: 0.5}).Valid() != false {
		t.Error("Low < 0 should be invalid")
	}
	if (&UtilizationTarget{Low: 0.5, High: 1.1}).Valid() != false {
		t.Error("High > 1 should be invalid")
	}
}

func TestMemoryUtilizationObjective(t *testing.T) {
	obj := &MemoryUtilizationObjective{}
	if obj.Name() != "memory_utilization" {
		t.Fatalf("expected name memory_utilization, got %s", obj.Name())
	}
	if !obj.Direction() {
		t.Fatalf("expected minimize direction")
	}

	// Test with nil metrics
	_, err := obj.Evaluate(nil)
	if err == nil {
		t.Fatalf("expected error for nil metrics")
	}

	// Test with empty ServiceMetrics
	metrics := &simulationv1.RunMetrics{ServiceMetrics: []*simulationv1.ServiceMetrics{}}
	score, err := obj.Evaluate(metrics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score != highPenaltyScore {
		t.Fatalf("expected high penalty for no services, got %f", score)
	}

	// Test with one non-client service
	metrics.ServiceMetrics = []*simulationv1.ServiceMetrics{
		{ServiceName: "svc1", MemoryUtilization: 0.35},
	}
	score, err = obj.Evaluate(metrics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score != 0.35 {
		t.Fatalf("expected score 0.35, got %f", score)
	}

	// Test with multiple services (max is chosen)
	metrics.ServiceMetrics = []*simulationv1.ServiceMetrics{
		{ServiceName: "svc1", MemoryUtilization: 0.2},
		{ServiceName: "svc2", MemoryUtilization: 0.7},
		{ServiceName: "svc3", MemoryUtilization: 0.4},
	}
	score, err = obj.Evaluate(metrics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score != 0.7 {
		t.Fatalf("expected score 0.7 (max), got %f", score)
	}

	// Test that client-prefixed service is skipped
	metrics.ServiceMetrics = []*simulationv1.ServiceMetrics{
		{ServiceName: "client-worker", MemoryUtilization: 0.95},
		{ServiceName: "api", MemoryUtilization: 0.5},
	}
	score, err = obj.Evaluate(metrics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if score != 0.5 {
		t.Fatalf("expected score 0.5 (client skipped), got %f", score)
	}
}
