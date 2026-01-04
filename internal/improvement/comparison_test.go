package improvement

import (
	"math"
	"testing"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
)

func TestCompareMetrics(t *testing.T) {
	objective := &P95LatencyObjective{}

	metrics1 := &simulationv1.RunMetrics{
		TotalRequests:  100,
		FailedRequests: 5,
		LatencyP50Ms:   10,
		LatencyP95Ms:   50,
		LatencyP99Ms:   100,
		LatencyMeanMs:  25,
		ThroughputRps:  10,
		ServiceMetrics: []*simulationv1.ServiceMetrics{
			{
				CpuUtilization:    0.5,
				MemoryUtilization: 0.3,
				ActiveReplicas:    2,
			},
		},
	}

	metrics2 := &simulationv1.RunMetrics{
		TotalRequests:  100,
		FailedRequests: 2,
		LatencyP50Ms:   8,
		LatencyP95Ms:   40,
		LatencyP99Ms:   80,
		LatencyMeanMs:  20,
		ThroughputRps:  12,
		ServiceMetrics: []*simulationv1.ServiceMetrics{
			{
				CpuUtilization:    0.4,
				MemoryUtilization: 0.25,
				ActiveReplicas:    3,
			},
		},
	}

	comparison, err := CompareMetrics(metrics1, metrics2, objective)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if comparison == nil {
		t.Fatalf("expected non-nil comparison")
	}

	// metrics2 should be better (lower P95 latency)
	if !comparison.Improvement {
		t.Fatalf("expected improvement (metrics2 should be better)")
	}

	// Check latency differences
	if comparison.LatencyDiff.P95Diff != -10.0 { // 40 - 50 = -10
		t.Fatalf("expected P95 diff -10, got %f", comparison.LatencyDiff.P95Diff)
	}

	// Check throughput improvement
	if comparison.ThroughputDiff != 2.0 { // 12 - 10 = 2
		t.Fatalf("expected throughput diff 2, got %f", comparison.ThroughputDiff)
	}

	// Check error rate improvement
	expectedErrorRateDiff := (2.0 / 100.0) - (5.0 / 100.0) // -0.03
	if math.Abs(comparison.ErrorRateDiff-expectedErrorRateDiff) > 0.0001 {
		t.Fatalf("expected error rate diff %f, got %f", expectedErrorRateDiff, comparison.ErrorRateDiff)
	}

	// Check resource differences
	if comparison.ResourceDiff.CPUUtilDiff != -0.1 { // 0.4 - 0.5 = -0.1
		t.Fatalf("expected CPU util diff -0.1, got %f", comparison.ResourceDiff.CPUUtilDiff)
	}
	if comparison.ResourceDiff.ReplicaDiff != 1 { // 3 - 2 = 1
		t.Fatalf("expected replica diff 1, got %d", comparison.ResourceDiff.ReplicaDiff)
	}
}

func TestCompareMetricsWithNil(t *testing.T) {
	objective := &P95LatencyObjective{}
	metrics := &simulationv1.RunMetrics{LatencyP95Ms: 50}

	// Test nil metrics1
	_, err := CompareMetrics(nil, metrics, objective)
	if err == nil {
		t.Fatalf("expected error for nil metrics1")
	}

	// Test nil metrics2
	_, err = CompareMetrics(metrics, nil, objective)
	if err == nil {
		t.Fatalf("expected error for nil metrics2")
	}

	// Test nil objective
	_, err = CompareMetrics(metrics, metrics, nil)
	if err == nil {
		t.Fatalf("expected error for nil objective")
	}
}

func TestCompareRunHistory(t *testing.T) {
	objective := &P95LatencyObjective{}

	runs := []*RunMetricsWithID{
		{
			RunID: "run1",
			Metrics: &simulationv1.RunMetrics{
				LatencyP95Ms: 100,
			},
		},
		{
			RunID: "run2",
			Metrics: &simulationv1.RunMetrics{
				LatencyP95Ms: 80,
			},
		},
		{
			RunID: "run3",
			Metrics: &simulationv1.RunMetrics{
				LatencyP95Ms: 60,
			},
		},
	}

	comparison, err := CompareRunHistory(runs, objective)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if comparison == nil {
		t.Fatalf("expected non-nil comparison")
	}

	// Best run should be run3 (lowest latency)
	if comparison.BestRunID != "run3" {
		t.Fatalf("expected best run to be run3, got %s", comparison.BestRunID)
	}

	// Worst run should be run1 (highest latency)
	if comparison.WorstRunID != "run1" {
		t.Fatalf("expected worst run to be run1, got %s", comparison.WorstRunID)
	}

	// Trend should be improving (scores decreasing)
	if comparison.ImprovementTrend != "improving" {
		t.Fatalf("expected improving trend, got %s", comparison.ImprovementTrend)
	}

	// Average should be (100 + 80 + 60) / 3 = 80
	if comparison.AverageScore != 80.0 {
		t.Fatalf("expected average score 80, got %f", comparison.AverageScore)
	}
}

func TestCompareRunHistoryWithMaximization(t *testing.T) {
	objective := &ThroughputObjective{}

	runs := []*RunMetricsWithID{
		{
			RunID: "run1",
			Metrics: &simulationv1.RunMetrics{
				ThroughputRps: 10,
			},
		},
		{
			RunID: "run2",
			Metrics: &simulationv1.RunMetrics{
				ThroughputRps: 15,
			},
		},
		{
			RunID: "run3",
			Metrics: &simulationv1.RunMetrics{
				ThroughputRps: 20,
			},
		},
	}

	comparison, err := CompareRunHistory(runs, objective)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Best run should be run3 (highest throughput, but remember we negate for maximization)
	// The score will be negative, so the most negative (closest to 0) is best
	if comparison.BestRunID != "run3" {
		t.Fatalf("expected best run to be run3, got %s", comparison.BestRunID)
	}

	// Trend should be improving (throughput increasing)
	if comparison.ImprovementTrend != "improving" {
		t.Fatalf("expected improving trend, got %s", comparison.ImprovementTrend)
	}
}

func TestCompareRunHistoryEmpty(t *testing.T) {
	objective := &P95LatencyObjective{}

	_, err := CompareRunHistory(nil, objective)
	if err == nil {
		t.Fatalf("expected error for empty runs")
	}

	_, err = CompareRunHistory([]*RunMetricsWithID{}, objective)
	if err == nil {
		t.Fatalf("expected error for empty runs slice")
	}
}

func TestGetImprovementPercentage(t *testing.T) {
	// Test minimization (lower is better)
	percent := GetImprovementPercentage(100, 80, true)
	expected := 20.0 // (100-80)/100 * 100 = 20%
	if percent != expected {
		t.Fatalf("expected improvement %f%%, got %f%%", expected, percent)
	}

	// Test maximization (higher is better)
	percent = GetImprovementPercentage(10, 15, false)
	expected = 50.0 // (15-10)/10 * 100 = 50%
	if percent != expected {
		t.Fatalf("expected improvement %f%%, got %f%%", expected, percent)
	}

	// Test no change
	percent = GetImprovementPercentage(100, 100, true)
	if percent != 0 {
		t.Fatalf("expected 0%% improvement, got %f%%", percent)
	}

	// Test zero score
	percent = GetImprovementPercentage(0, 10, true)
	if percent != 0 {
		t.Fatalf("expected 0%% improvement for zero score, got %f%%", percent)
	}
}

func TestIsSignificantImprovement(t *testing.T) {
	comparison := &MetricsComparison{
		Improvement:   true,
		ObjectiveDiff: -10.0, // Improvement of 10
	}

	// Test with threshold
	if !IsSignificantImprovement(comparison, 5.0) {
		t.Fatalf("expected significant improvement")
	}

	if IsSignificantImprovement(comparison, 15.0) {
		t.Fatalf("expected not significant improvement with higher threshold")
	}

	// Test with no improvement
	comparison.Improvement = false
	if IsSignificantImprovement(comparison, 5.0) {
		t.Fatalf("expected not significant when no improvement")
	}

	// Test with nil
	if IsSignificantImprovement(nil, 5.0) {
		t.Fatalf("expected false for nil comparison")
	}
}

func TestAggregateResources(t *testing.T) {
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

	avgCPU, avgMemory, totalReplicas := aggregateResources(metrics)

	expectedAvgCPU := (0.5 + 0.7) / 2.0
	if avgCPU != expectedAvgCPU {
		t.Fatalf("expected avg CPU %f, got %f", expectedAvgCPU, avgCPU)
	}

	expectedAvgMemory := (0.3 + 0.4) / 2.0
	if avgMemory != expectedAvgMemory {
		t.Fatalf("expected avg memory %f, got %f", expectedAvgMemory, avgMemory)
	}

	if totalReplicas != 5 {
		t.Fatalf("expected total replicas 5, got %d", totalReplicas)
	}

	// Test with empty metrics
	emptyMetrics := &simulationv1.RunMetrics{}
	avgCPU, avgMemory, totalReplicas = aggregateResources(emptyMetrics)
	if avgCPU != 0 || avgMemory != 0 || totalReplicas != 0 {
		t.Fatalf("expected zero values for empty metrics")
	}
}
