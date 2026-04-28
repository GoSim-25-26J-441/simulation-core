package config

import "testing"

func TestValidateWorkloadBranches(t *testing.T) {
	t.Run("normalizes arrival aliases", func(t *testing.T) {
		w := &Workload{Arrival: "burst", RateRPS: 1, Duration: "10s", Warmup: "1s"}
		if err := validateWorkload(w); err != nil {
			t.Fatalf("validateWorkload error: %v", err)
		}
		if w.Arrival != "bursty" {
			t.Fatalf("expected burst alias to normalize to bursty, got %q", w.Arrival)
		}
	})

	t.Run("rejects invalid arrival", func(t *testing.T) {
		w := &Workload{Arrival: "not-a-type", RateRPS: 1, Duration: "10s", Warmup: "1s"}
		if err := validateWorkload(w); err == nil {
			t.Fatalf("expected invalid arrival error")
		}
	})

	t.Run("rejects non-positive rate", func(t *testing.T) {
		w := &Workload{Arrival: "poisson", RateRPS: 0, Duration: "10s", Warmup: "1s"}
		if err := validateWorkload(w); err == nil {
			t.Fatalf("expected non-positive rate error")
		}
	})

	t.Run("rejects invalid duration", func(t *testing.T) {
		w := &Workload{Arrival: "poisson", RateRPS: 1, Duration: "bad", Warmup: "1s"}
		if err := validateWorkload(w); err == nil {
			t.Fatalf("expected invalid duration error")
		}
	})

	t.Run("rejects invalid warmup", func(t *testing.T) {
		w := &Workload{Arrival: "poisson", RateRPS: 1, Duration: "10s", Warmup: "bad"}
		if err := validateWorkload(w); err == nil {
			t.Fatalf("expected invalid warmup error")
		}
	})
}

func TestValidatePoliciesBranches(t *testing.T) {
	valid := &Policies{
		Autoscaling: &AutoscalingPolicy{TargetCPUUtil: 0.6, ScaleStep: 1},
		Retries:     &RetryPolicy{MaxRetries: 3, Backoff: "exponential", BaseMs: 10},
	}
	if err := validatePolicies(valid); err != nil {
		t.Fatalf("expected valid policies, got %v", err)
	}

	tests := []struct {
		name string
		p    *Policies
	}{
		{
			name: "autoscaling target out of range",
			p:    &Policies{Autoscaling: &AutoscalingPolicy{TargetCPUUtil: 1.5, ScaleStep: 1}},
		},
		{
			name: "autoscaling non-positive scale step",
			p:    &Policies{Autoscaling: &AutoscalingPolicy{TargetCPUUtil: 0.5, ScaleStep: 0}},
		},
		{
			name: "retries negative max",
			p:    &Policies{Retries: &RetryPolicy{MaxRetries: -1, Backoff: "exponential", BaseMs: 10}},
		},
		{
			name: "retries invalid backoff",
			p:    &Policies{Retries: &RetryPolicy{MaxRetries: 1, Backoff: "jitter", BaseMs: 10}},
		},
		{
			name: "retries negative base",
			p:    &Policies{Retries: &RetryPolicy{MaxRetries: 1, Backoff: "linear", BaseMs: -1}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validatePolicies(tt.p); err == nil {
				t.Fatalf("expected validation error")
			}
		})
	}
}

func TestValidateOptimizationBranches(t *testing.T) {
	if err := validateOptimization(&Optimization{Objective: "p95_latency_ms", MaxIterations: 5}); err != nil {
		t.Fatalf("expected valid optimization, got %v", err)
	}
	if err := validateOptimization(&Optimization{Objective: "", MaxIterations: 5}); err == nil {
		t.Fatalf("expected empty objective error")
	}
	if err := validateOptimization(&Optimization{Objective: "p95_latency_ms", MaxIterations: 0}); err == nil {
		t.Fatalf("expected max iterations error")
	}
}
