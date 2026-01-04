package improvement

import (
	"testing"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
)

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
			name:    "unknown objective",
			objType: "unknown",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj, err := NewObjectiveFunction(tt.objType)
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
	if score != 1e9 {
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
