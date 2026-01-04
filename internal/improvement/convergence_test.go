package improvement

import (
	"testing"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestNoImprovementStrategy(t *testing.T) {
	config := &ConvergenceConfig{
		NoImprovementIterations: 3,
		MinIterations:           2,
	}
	strategy := NewNoImprovementStrategy(config)

	// Test with no improvement for 3 iterations
	history := []OptimizationStep{
		{Iteration: 0, Score: 100.0, Config: &config.Scenario{Services: []config.Service{}}},
		{Iteration: 1, Score: 100.0, Config: &config.Scenario{Services: []config.Service{}}},
		{Iteration: 2, Score: 100.0, Config: &config.Scenario{Services: []config.Service{}}},
		{Iteration: 3, Score: 100.0, Config: &config.Scenario{Services: []config.Service{}}},
		{Iteration: 4, Score: 100.0, Config: &config.Scenario{Services: []config.Service{}}},
	}

	converged, reason := strategy.CheckConvergence(history)
	if !converged {
		t.Fatalf("expected convergence, got false")
	}
	if reason == "" {
		t.Fatalf("expected convergence reason")
	}

	// Test with recent improvement
	history2 := []OptimizationStep{
		{Iteration: 0, Score: 100.0, Config: &config.Scenario{Services: []config.Service{}}},
		{Iteration: 1, Score: 90.0, Config: &config.Scenario{Services: []config.Service{}}},
		{Iteration: 2, Score: 90.0, Config: &config.Scenario{Services: []config.Service{}}},
		{Iteration: 3, Score: 90.0, Config: &config.Scenario{Services: []config.Service{}}},
	}

	converged, _ = strategy.CheckConvergence(history2)
	if converged {
		t.Fatalf("expected no convergence (recent improvement), got true")
	}
}

func TestPlateauStrategy(t *testing.T) {
	config := &ConvergenceConfig{
		PlateauIterations: 3,
		ScoreTolerance:    0.01,
		MinIterations:     2,
	}
	strategy := NewPlateauStrategy(config)

	// Test with plateau (similar scores)
	history := []OptimizationStep{
		{Iteration: 0, Score: 100.0, Config: &config.Scenario{}},
		{Iteration: 1, Score: 100.01, Config: &config.Scenario{}},
		{Iteration: 2, Score: 100.005, Config: &config.Scenario{}},
		{Iteration: 3, Score: 100.002, Config: &config.Scenario{}},
	}

	converged, reason := strategy.CheckConvergence(history)
	if !converged {
		t.Fatalf("expected convergence (plateau), got false")
	}
	if reason == "" {
		t.Fatalf("expected convergence reason")
	}

	// Test with varying scores
	history2 := []OptimizationStep{
		{Iteration: 0, Score: 100.0, Config: &config.Scenario{}},
		{Iteration: 1, Score: 90.0, Config: &config.Scenario{}},
		{Iteration: 2, Score: 95.0, Config: &config.Scenario{}},
		{Iteration: 3, Score: 85.0, Config: &config.Scenario{}},
	}

	converged, _ = strategy.CheckConvergence(history2)
	if converged {
		t.Fatalf("expected no convergence (varying scores), got true")
	}
}

func TestImprovementThresholdStrategy(t *testing.T) {
	config := &ConvergenceConfig{
		NoImprovementIterations: 3,
		ImprovementThreshold:    0.01, // 1%
		MinIterations:           2,
	}
	strategy := NewImprovementThresholdStrategy(config)

	// Test with improvements below threshold
	history := []OptimizationStep{
		{Iteration: 0, Score: 100.0, Config: &config.Scenario{Services: []config.Service{}}},
		{Iteration: 1, Score: 99.5, Config: &config.Scenario{Services: []config.Service{}}}, // 0.5% improvement
		{Iteration: 2, Score: 99.3, Config: &config.Scenario{Services: []config.Service{}}}, // 0.2% improvement
		{Iteration: 3, Score: 99.2, Config: &config.Scenario{Services: []config.Service{}}}, // 0.1% improvement
	}

	converged, reason := strategy.CheckConvergence(history)
	if !converged {
		t.Fatalf("expected convergence (improvements below threshold), got false")
	}
	if reason == "" {
		t.Fatalf("expected convergence reason")
	}

	// Test with significant improvement
	history2 := []OptimizationStep{
		{Iteration: 0, Score: 100.0, Config: &config.Scenario{Services: []config.Service{}}},
		{Iteration: 1, Score: 90.0, Config: &config.Scenario{Services: []config.Service{}}}, // 10% improvement
		{Iteration: 2, Score: 89.5, Config: &config.Scenario{Services: []config.Service{}}}, // 0.5% improvement
	}

	converged, _ = strategy.CheckConvergence(history2)
	if converged {
		t.Fatalf("expected no convergence (significant improvement), got true")
	}
}

func TestCombinedStrategy(t *testing.T) {
	config := &ConvergenceConfig{
		NoImprovementIterations: 3,
		PlateauIterations:       3,
		ScoreTolerance:          0.01,
		MinIterations:           2,
	}
	strategy := NewCombinedStrategy(config)

	// Test with plateau (should trigger convergence)
	history := []OptimizationStep{
		{Iteration: 0, Score: 100.0, Config: &config.Scenario{}},
		{Iteration: 1, Score: 100.01, Config: &config.Scenario{}},
		{Iteration: 2, Score: 100.005, Config: &config.Scenario{}},
		{Iteration: 3, Score: 100.002, Config: &config.Scenario{}},
	}

	converged, reason := strategy.CheckConvergence(history)
	if !converged {
		t.Fatalf("expected convergence, got false")
	}
	if reason == "" {
		t.Fatalf("expected convergence reason")
	}
}

func TestVarianceStrategy(t *testing.T) {
	config := &ConvergenceConfig{
		PlateauIterations:    3,
		ImprovementThreshold: 0.01, // 1% relative variance
		MinIterations:        2,
	}
	strategy := NewVarianceStrategy(config)

	// Test with low variance
	history := []OptimizationStep{
		{Iteration: 0, Score: 100.0, Config: &config.Scenario{Services: []config.Service{}}},
		{Iteration: 1, Score: 100.1, Config: &config.Scenario{Services: []config.Service{}}},
		{Iteration: 2, Score: 100.05, Config: &config.Scenario{Services: []config.Service{}}},
		{Iteration: 3, Score: 100.02, Config: &config.Scenario{Services: []config.Service{}}},
	}

	converged, reason := strategy.CheckConvergence(history)
	if !converged {
		t.Fatalf("expected convergence (low variance), got false")
	}
	if reason == "" {
		t.Fatalf("expected convergence reason")
	}

	// Test with high variance
	history2 := []OptimizationStep{
		{Iteration: 0, Score: 100.0, Config: &config.Scenario{}},
		{Iteration: 1, Score: 90.0, Config: &config.Scenario{}},
		{Iteration: 2, Score: 95.0, Config: &config.Scenario{}},
		{Iteration: 3, Score: 85.0, Config: &config.Scenario{}},
	}

	converged, _ = strategy.CheckConvergence(history2)
	if converged {
		t.Fatalf("expected no convergence (high variance), got true")
	}
}

func TestConvergenceStrategiesName(t *testing.T) {
	config := DefaultConvergenceConfig()

	if NewNoImprovementStrategy(config).Name() != "no_improvement" {
		t.Fatalf("unexpected name for NoImprovementStrategy")
	}
	if NewPlateauStrategy(config).Name() != "plateau" {
		t.Fatalf("unexpected name for PlateauStrategy")
	}
	if NewImprovementThresholdStrategy(config).Name() != "improvement_threshold" {
		t.Fatalf("unexpected name for ImprovementThresholdStrategy")
	}
	if NewCombinedStrategy(config).Name() != "combined" {
		t.Fatalf("unexpected name for CombinedStrategy")
	}
	if NewVarianceStrategy(config).Name() != "variance" {
		t.Fatalf("unexpected name for VarianceStrategy")
	}
}

func TestDefaultConvergenceConfig(t *testing.T) {
	config := DefaultConvergenceConfig()

	if config.NoImprovementIterations <= 0 {
		t.Fatalf("expected positive NoImprovementIterations")
	}
	if config.ImprovementThreshold <= 0 {
		t.Fatalf("expected positive ImprovementThreshold")
	}
	if config.ScoreTolerance <= 0 {
		t.Fatalf("expected positive ScoreTolerance")
	}
	if config.MinIterations <= 0 {
		t.Fatalf("expected positive MinIterations")
	}
}
