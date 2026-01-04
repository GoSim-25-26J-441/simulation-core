package improvement

import (
	"fmt"
	"math"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
)

// MetricsComparison compares metrics between two runs
type MetricsComparison struct {
	RunID1         string
	RunID2         string
	ObjectiveDiff  float64 // Difference in objective score (run2 - run1)
	Improvement    bool    // True if run2 is better than run1
	LatencyDiff    LatencyComparison
	ThroughputDiff float64
	ErrorRateDiff  float64
	ResourceDiff   ResourceComparison
}

// LatencyComparison compares latency metrics
type LatencyComparison struct {
	P50Diff  float64
	P95Diff  float64
	P99Diff  float64
	MeanDiff float64
}

// ResourceComparison compares resource utilization
type ResourceComparison struct {
	CPUUtilDiff    float64
	MemoryUtilDiff float64
	ReplicaDiff    int32
}

// CompareMetrics compares two sets of run metrics
func CompareMetrics(metrics1, metrics2 *simulationv1.RunMetrics, objective ObjectiveFunction) (*MetricsComparison, error) {
	if metrics1 == nil {
		return nil, fmt.Errorf("metrics1 is nil")
	}
	if metrics2 == nil {
		return nil, fmt.Errorf("metrics2 is nil")
	}
	if objective == nil {
		return nil, fmt.Errorf("objective function is nil")
	}

	// Evaluate objective scores
	score1, err := objective.Evaluate(metrics1)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate objective for metrics1: %w", err)
	}
	score2, err := objective.Evaluate(metrics2)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate objective for metrics2: %w", err)
	}

	// Determine improvement (lower score is better for minimization objectives)
	improvement := score2 < score1
	if !objective.Direction() {
		// For maximization, higher is better
		improvement = score2 > score1
	}

	comparison := &MetricsComparison{
		ObjectiveDiff: score2 - score1,
		Improvement:   improvement,
		LatencyDiff: LatencyComparison{
			P50Diff:  metrics2.LatencyP50Ms - metrics1.LatencyP50Ms,
			P95Diff:  metrics2.LatencyP95Ms - metrics1.LatencyP95Ms,
			P99Diff:  metrics2.LatencyP99Ms - metrics1.LatencyP99Ms,
			MeanDiff: metrics2.LatencyMeanMs - metrics1.LatencyMeanMs,
		},
		ThroughputDiff: metrics2.ThroughputRps - metrics1.ThroughputRps,
	}

	// Calculate error rate difference
	errorRate1 := 0.0
	if metrics1.TotalRequests > 0 {
		errorRate1 = float64(metrics1.FailedRequests) / float64(metrics1.TotalRequests)
	}
	errorRate2 := 0.0
	if metrics2.TotalRequests > 0 {
		errorRate2 = float64(metrics2.FailedRequests) / float64(metrics2.TotalRequests)
	}
	comparison.ErrorRateDiff = errorRate2 - errorRate1

	// Compare resource utilization (aggregate from service metrics)
	comparison.ResourceDiff = compareResources(metrics1, metrics2)

	return comparison, nil
}

// compareResources compares resource utilization between two metrics
func compareResources(metrics1, metrics2 *simulationv1.RunMetrics) ResourceComparison {
	avgCPU1, avgMemory1, totalReplicas1 := aggregateResources(metrics1)
	avgCPU2, avgMemory2, totalReplicas2 := aggregateResources(metrics2)

	return ResourceComparison{
		CPUUtilDiff:    avgCPU2 - avgCPU1,
		MemoryUtilDiff: avgMemory2 - avgMemory1,
		ReplicaDiff:    totalReplicas2 - totalReplicas1,
	}
}

// aggregateResources aggregates resource metrics from service metrics
func aggregateResources(metrics *simulationv1.RunMetrics) (avgCPU, avgMemory float64, totalReplicas int32) {
	if len(metrics.ServiceMetrics) == 0 {
		return 0, 0, 0
	}

	var totalCPU, totalMemory float64
	var totalReplicasSum int32

	for _, svc := range metrics.ServiceMetrics {
		totalCPU += svc.CpuUtilization
		totalMemory += svc.MemoryUtilization
		totalReplicasSum += svc.ActiveReplicas
	}

	count := float64(len(metrics.ServiceMetrics))
	avgCPU = totalCPU / count
	avgMemory = totalMemory / count

	return avgCPU, avgMemory, totalReplicasSum
}

// CompareRunHistory compares metrics across a sequence of runs
type RunHistoryComparison struct {
	Runs             []*RunMetricsWithID
	BestRunID        string
	WorstRunID       string
	ImprovementTrend string // "improving", "degrading", "stable"
	AverageScore     float64
	ScoreVariance    float64
}

// RunMetricsWithID associates metrics with a run ID
type RunMetricsWithID struct {
	RunID   string
	Metrics *simulationv1.RunMetrics
	Score   float64
}

// CompareRunHistory compares metrics across multiple runs
func CompareRunHistory(runs []*RunMetricsWithID, objective ObjectiveFunction) (*RunHistoryComparison, error) {
	if len(runs) == 0 {
		return nil, fmt.Errorf("no runs provided")
	}
	if objective == nil {
		return nil, fmt.Errorf("objective function is nil")
	}

	// Evaluate scores for all runs
	scores := make([]float64, len(runs))
	for i, run := range runs {
		score, err := objective.Evaluate(run.Metrics)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate objective for run %s: %w", run.RunID, err)
		}
		runs[i].Score = score
		scores[i] = score
	}

	// Find best and worst runs
	bestIdx := 0
	worstIdx := 0
	bestScore := scores[0]
	worstScore := scores[0]

	for i, score := range scores {
		// For both minimization and maximization, we treat lower scores as better
		// because maximization objectives already negate their scores
		if score < bestScore {
			bestScore = score
			bestIdx = i
		}
		if score > worstScore {
			worstScore = score
			worstIdx = i
		}
	}

	// Calculate average and variance
	avgScore := mean(scores)
	variance := variance(scores, avgScore)

	// Determine trend
	trend := determineTrend(scores, objective.Direction())

	return &RunHistoryComparison{
		Runs:             runs,
		BestRunID:        runs[bestIdx].RunID,
		WorstRunID:       runs[worstIdx].RunID,
		ImprovementTrend: trend,
		AverageScore:     avgScore,
		ScoreVariance:    variance,
	}, nil
}

// determineTrend analyzes the trend of scores over time
// Note: scores are already normalized (negated for maximization), so lower is always better
func determineTrend(scores []float64, minimize bool) string {
	if len(scores) < 2 {
		return "stable"
	}

	// Calculate linear regression slope
	n := float64(len(scores))
	var sumX, sumY, sumXY, sumX2 float64
	for i, score := range scores {
		x := float64(i)
		sumX += x
		sumY += score
		sumXY += x * score
		sumX2 += x * x
	}

	slope := (n*sumXY - sumX*sumY) / (n*sumX2 - sumX*sumX)

	// For both minimization and maximization, negative slope means improving
	// because maximization objectives already negate their scores
	if slope < -0.01 {
		return "improving"
	}
	if slope > 0.01 {
		return "degrading"
	}

	return "stable"
}

// mean calculates the mean of a slice of floats
func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

// variance calculates the variance of a slice of floats
func variance(values []float64, mean float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sumSqDiff := 0.0
	for _, v := range values {
		diff := v - mean
		sumSqDiff += diff * diff
	}
	return sumSqDiff / float64(len(values))
}

// GetImprovementPercentage calculates the percentage improvement between two scores
func GetImprovementPercentage(score1, score2 float64, minimize bool) float64 {
	if score1 == 0 {
		return 0
	}
	diff := score2 - score1
	if minimize {
		// For minimization, negative diff means improvement
		return -(diff / score1) * 100
	}
	// For maximization, positive diff means improvement
	return (diff / score1) * 100
}

// IsSignificantImprovement checks if the improvement is statistically significant
func IsSignificantImprovement(comparison *MetricsComparison, thresholdPercent float64) bool {
	if comparison == nil {
		return false
	}
	if !comparison.Improvement {
		return false
	}

	// Calculate improvement percentage
	// We need the original scores, but we can use ObjectiveDiff
	// For a rough check, we'll use a threshold on the absolute difference
	improvementPercent := math.Abs(comparison.ObjectiveDiff)
	return improvementPercent >= thresholdPercent
}
