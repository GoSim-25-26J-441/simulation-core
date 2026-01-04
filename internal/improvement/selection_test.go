package improvement

import (
	"testing"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestBestScoreStrategy(t *testing.T) {
	strategy := &BestScoreStrategy{}
	objective := &P95LatencyObjective{}

	candidates := []*ConfigurationCandidate{
		{
			Config: &config.Scenario{Services: []config.Service{{ID: "svc1", Replicas: 2}}},
			RunID:  "run1",
			Metrics: &simulationv1.RunMetrics{
				LatencyP95Ms: 100,
			},
			Score:     100,
			Evaluated: true,
		},
		{
			Config: &config.Scenario{Services: []config.Service{{ID: "svc1", Replicas: 3}}},
			RunID:  "run2",
			Metrics: &simulationv1.RunMetrics{
				LatencyP95Ms: 80,
			},
			Score:     80,
			Evaluated: true,
		},
		{
			Config: &config.Scenario{Services: []config.Service{{ID: "svc1", Replicas: 4}}},
			RunID:  "run3",
			Metrics: &simulationv1.RunMetrics{
				LatencyP95Ms: 90,
			},
			Score:     90,
			Evaluated: true,
		},
	}

	best, err := strategy.SelectBest(candidates, objective)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if best == nil {
		t.Fatalf("expected non-nil best candidate")
	}

	if best.RunID != "run2" {
		t.Fatalf("expected best run to be run2 (score 80), got %s (score %f)", best.RunID, best.Score)
	}
}

func TestBestScoreStrategyWithUnevaluated(t *testing.T) {
	strategy := &BestScoreStrategy{}
	objective := &P95LatencyObjective{}

	candidates := []*ConfigurationCandidate{
		{
			Config:    &config.Scenario{},
			RunID:     "run1",
			Evaluated: false, // Not evaluated
		},
		{
			Config: &config.Scenario{},
			RunID:  "run2",
			Metrics: &simulationv1.RunMetrics{
				LatencyP95Ms: 80,
			},
			Score:     80,
			Evaluated: true,
		},
	}

	best, err := strategy.SelectBest(candidates, objective)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if best.RunID != "run2" {
		t.Fatalf("expected best run to be run2, got %s", best.RunID)
	}
}

func TestBestScoreStrategyErrors(t *testing.T) {
	strategy := &BestScoreStrategy{}
	objective := &P95LatencyObjective{}

	// Test empty candidates
	_, err := strategy.SelectBest(nil, objective)
	if err == nil {
		t.Fatalf("expected error for nil candidates")
	}

	_, err = strategy.SelectBest([]*ConfigurationCandidate{}, objective)
	if err == nil {
		t.Fatalf("expected error for empty candidates")
	}

	// Test nil objective
	candidates := []*ConfigurationCandidate{
		{
			Metrics:   &simulationv1.RunMetrics{LatencyP95Ms: 100},
			Score:     100,
			Evaluated: true,
		},
	}
	_, err = strategy.SelectBest(candidates, nil)
	if err == nil {
		t.Fatalf("expected error for nil objective")
	}

	// Test no evaluated candidates
	_, err = strategy.SelectBest([]*ConfigurationCandidate{
		{Evaluated: false},
	}, objective)
	if err == nil {
		t.Fatalf("expected error for no evaluated candidates")
	}
}

func TestEvaluateCandidate(t *testing.T) {
	objective := &P95LatencyObjective{}

	candidate := &ConfigurationCandidate{
		Metrics: &simulationv1.RunMetrics{
			LatencyP95Ms: 100,
		},
		Evaluated: false,
	}

	err := EvaluateCandidate(candidate, objective)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !candidate.Evaluated {
		t.Fatalf("expected candidate to be evaluated")
	}

	if candidate.Score != 100 {
		t.Fatalf("expected score 100, got %f", candidate.Score)
	}
}

func TestEvaluateCandidateErrors(t *testing.T) {
	objective := &P95LatencyObjective{}

	// Test nil candidate
	err := EvaluateCandidate(nil, objective)
	if err == nil {
		t.Fatalf("expected error for nil candidate")
	}

	// Test nil metrics
	err = EvaluateCandidate(&ConfigurationCandidate{}, objective)
	if err == nil {
		t.Fatalf("expected error for nil metrics")
	}

	// Test nil objective
	err = EvaluateCandidate(&ConfigurationCandidate{
		Metrics: &simulationv1.RunMetrics{LatencyP95Ms: 100},
	}, nil)
	if err == nil {
		t.Fatalf("expected error for nil objective")
	}
}

func TestSelectBestConfiguration(t *testing.T) {
	objective := &P95LatencyObjective{}

	candidates := []*ConfigurationCandidate{
		{
			Config: &config.Scenario{},
			RunID:  "run1",
			Metrics: &simulationv1.RunMetrics{
				LatencyP95Ms: 100,
			},
			Score:     100,
			Evaluated: true,
		},
		{
			Config: &config.Scenario{},
			RunID:  "run2",
			Metrics: &simulationv1.RunMetrics{
				LatencyP95Ms: 80,
			},
			Score:     80,
			Evaluated: true,
		},
	}

	best, err := SelectBestConfiguration(candidates, objective)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if best.RunID != "run2" {
		t.Fatalf("expected best run to be run2, got %s", best.RunID)
	}
}

func TestBalancedStrategy(t *testing.T) {
	weights := map[string]float64{
		"p95_latency_ms": 0.7,
		"error_rate":     0.3,
	}
	strategy := NewBalancedStrategy(weights)
	objective := &P95LatencyObjective{}

	candidates := []*ConfigurationCandidate{
		{
			Config: &config.Scenario{},
			RunID:  "run1",
			Metrics: &simulationv1.RunMetrics{
				LatencyP95Ms:   100,
				TotalRequests:  100,
				FailedRequests: 10, // 10% error rate
			},
			Score:     100,
			Evaluated: true,
		},
		{
			Config: &config.Scenario{},
			RunID:  "run2",
			Metrics: &simulationv1.RunMetrics{
				LatencyP95Ms:   110, // Slightly worse latency
				TotalRequests:  100,
				FailedRequests: 2, // 2% error rate (much better)
			},
			Score:     110,
			Evaluated: true,
		},
	}

	best, err := strategy.SelectBest(candidates, objective)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if best == nil {
		t.Fatalf("expected non-nil best candidate")
	}
}
