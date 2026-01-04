package improvement

import (
	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
)

// ObjectiveFunction evaluates a run's metrics and returns a score.
// Lower scores are better (for minimization objectives).
type ObjectiveFunction interface {
	// Evaluate computes the objective value from run metrics.
	// Returns the score and an error if evaluation fails.
	Evaluate(metrics *simulationv1.RunMetrics) (float64, error)

	// Name returns the name of the objective function.
	Name() string

	// Direction returns whether we're minimizing (true) or maximizing (false).
	Direction() bool // true = minimize, false = maximize
}

// ObjectiveType represents the type of objective function
type ObjectiveType string

const (
	// ObjectiveMinimizeP95Latency minimizes P95 latency
	ObjectiveMinimizeP95Latency ObjectiveType = "p95_latency_ms"
	// ObjectiveMinimizeP99Latency minimizes P99 latency
	ObjectiveMinimizeP99Latency ObjectiveType = "p99_latency_ms"
	// ObjectiveMinimizeMeanLatency minimizes mean latency
	ObjectiveMinimizeMeanLatency ObjectiveType = "mean_latency_ms"
	// ObjectiveMaximizeThroughput maximizes throughput (requests per second)
	ObjectiveMaximizeThroughput ObjectiveType = "throughput_rps"
	// ObjectiveMinimizeErrorRate minimizes error rate
	ObjectiveMinimizeErrorRate ObjectiveType = "error_rate"
	// ObjectiveMinimizeCost minimizes cost (weighted combination of resources)
	ObjectiveMinimizeCost ObjectiveType = "cost"
)

// NewObjectiveFunction creates an objective function from a type string
func NewObjectiveFunction(objType string) (ObjectiveFunction, error) {
	switch ObjectiveType(objType) {
	case ObjectiveMinimizeP95Latency:
		return &P95LatencyObjective{}, nil
	case ObjectiveMinimizeP99Latency:
		return &P99LatencyObjective{}, nil
	case ObjectiveMinimizeMeanLatency:
		return &MeanLatencyObjective{}, nil
	case ObjectiveMaximizeThroughput:
		return &ThroughputObjective{}, nil
	case ObjectiveMinimizeErrorRate:
		return &ErrorRateObjective{}, nil
	case ObjectiveMinimizeCost:
		return &CostObjective{}, nil
	default:
		return nil, &UnknownObjectiveError{ObjectiveType: objType}
	}
}

// P95LatencyObjective minimizes P95 latency
type P95LatencyObjective struct{}

func (o *P95LatencyObjective) Name() string {
	return string(ObjectiveMinimizeP95Latency)
}

func (o *P95LatencyObjective) Direction() bool {
	return true // minimize
}

func (o *P95LatencyObjective) Evaluate(metrics *simulationv1.RunMetrics) (float64, error) {
	if metrics == nil {
		return 0, &InvalidMetricsError{Reason: "metrics is nil"}
	}
	if metrics.LatencyP95Ms <= 0 {
		// If no latency data, return a high penalty
		return 1e9, nil
	}
	return metrics.LatencyP95Ms, nil
}

// P99LatencyObjective minimizes P99 latency
type P99LatencyObjective struct{}

func (o *P99LatencyObjective) Name() string {
	return string(ObjectiveMinimizeP99Latency)
}

func (o *P99LatencyObjective) Direction() bool {
	return true // minimize
}

func (o *P99LatencyObjective) Evaluate(metrics *simulationv1.RunMetrics) (float64, error) {
	if metrics == nil {
		return 0, &InvalidMetricsError{Reason: "metrics is nil"}
	}
	if metrics.LatencyP99Ms <= 0 {
		// If no latency data, return a high penalty
		return 1e9, nil
	}
	return metrics.LatencyP99Ms, nil
}

// MeanLatencyObjective minimizes mean latency
type MeanLatencyObjective struct{}

func (o *MeanLatencyObjective) Name() string {
	return string(ObjectiveMinimizeMeanLatency)
}

func (o *MeanLatencyObjective) Direction() bool {
	return true // minimize
}

func (o *MeanLatencyObjective) Evaluate(metrics *simulationv1.RunMetrics) (float64, error) {
	if metrics == nil {
		return 0, &InvalidMetricsError{Reason: "metrics is nil"}
	}
	if metrics.LatencyMeanMs <= 0 {
		// If no latency data, return a high penalty
		return 1e9, nil
	}
	return metrics.LatencyMeanMs, nil
}

// ThroughputObjective maximizes throughput
type ThroughputObjective struct{}

func (o *ThroughputObjective) Name() string {
	return string(ObjectiveMaximizeThroughput)
}

func (o *ThroughputObjective) Direction() bool {
	return false // maximize (so we negate the value)
}

func (o *ThroughputObjective) Evaluate(metrics *simulationv1.RunMetrics) (float64, error) {
	if metrics == nil {
		return 0, &InvalidMetricsError{Reason: "metrics is nil"}
	}
	if metrics.ThroughputRps <= 0 {
		// If no throughput data, return a high penalty (low score)
		return 1e9, nil
	}
	// For maximization, we return negative so that lower is better
	return -metrics.ThroughputRps, nil
}

// ErrorRateObjective minimizes error rate
type ErrorRateObjective struct{}

func (o *ErrorRateObjective) Name() string {
	return string(ObjectiveMinimizeErrorRate)
}

func (o *ErrorRateObjective) Direction() bool {
	return true // minimize
}

func (o *ErrorRateObjective) Evaluate(metrics *simulationv1.RunMetrics) (float64, error) {
	if metrics == nil {
		return 0, &InvalidMetricsError{Reason: "metrics is nil"}
	}
	if metrics.TotalRequests == 0 {
		// If no requests, return 0 error rate
		return 0, nil
	}
	errorRate := float64(metrics.FailedRequests) / float64(metrics.TotalRequests)
	return errorRate, nil
}

// CostObjective minimizes cost (weighted combination of resources)
// Cost = average CPU utilization + average Memory utilization + (total replicas * cost per replica)
type CostObjective struct{}

func (o *CostObjective) Name() string {
	return string(ObjectiveMinimizeCost)
}

func (o *CostObjective) Direction() bool {
	return true // minimize
}

func (o *CostObjective) Evaluate(metrics *simulationv1.RunMetrics) (float64, error) {
	if metrics == nil {
		return 0, &InvalidMetricsError{Reason: "metrics is nil"}
	}
	// Simple cost model: weighted sum of resource utilization and replica count
	// Aggregate CPU and memory utilization from service metrics
	var totalCPU, totalMemory float64
	var totalReplicas int32
	serviceCount := 0
	for _, svc := range metrics.ServiceMetrics {
		totalCPU += svc.CpuUtilization
		totalMemory += svc.MemoryUtilization
		totalReplicas += svc.ActiveReplicas
		serviceCount++
	}
	avgCPU := 0.0
	avgMemory := 0.0
	if serviceCount > 0 {
		avgCPU = totalCPU / float64(serviceCount)
		avgMemory = totalMemory / float64(serviceCount)
	}
	cpuCost := avgCPU * 0.4
	memoryCost := avgMemory * 0.3
	replicaCost := float64(totalReplicas) * 0.3
	return cpuCost + memoryCost + replicaCost, nil
}

// UnknownObjectiveError indicates an unknown objective type
type UnknownObjectiveError struct {
	ObjectiveType string
}

func (e *UnknownObjectiveError) Error() string {
	return "unknown objective type: " + e.ObjectiveType
}

// InvalidMetricsError indicates invalid metrics for evaluation
type InvalidMetricsError struct {
	Reason string
}

func (e *InvalidMetricsError) Error() string {
	return "invalid metrics: " + e.Reason
}
