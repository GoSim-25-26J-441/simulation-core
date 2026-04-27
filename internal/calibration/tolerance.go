package calibration

import "math"

// ValidationTolerances define pragmatic pass/fail bands for predicted vs observed comparisons.
type ValidationTolerances struct {
	ThroughputRel float64 // e.g. 0.05 = ±5%

	LatencyP50Rel float64
	LatencyP95Rel float64
	LatencyP99Rel float64

	UtilizationAbsPP float64 // CPU/memory: absolute percentage points (0.10 = ±10 points on 0..1 scale)

	IngressErrorRateAbs float64
	IngressErrorRateRel float64
	QueueDropRateAbs    float64
	TopicDropRateAbs    float64
	LocalityRateAbs     float64
	CrossZoneRateAbs    float64
	// CrossZonePenaltyMeanAbs is absolute tolerance (ms) when comparing mean cross-zone latency penalty.
	CrossZonePenaltyMeanAbs float64
	// TopologyPenaltyMeanAbs is absolute tolerance (ms) for optional mean topology network penalty rollups.
	TopologyPenaltyMeanAbs float64

	// QueueDepthAbsSmall: absolute tolerance when both values are small.
	QueueDepthAbsSmall float64
	QueueDepthRel      float64

	TopicLagAbsSmall float64
	TopicLagRel      float64

	// Routing skew tolerances (per-instance request share/count by endpoint).
	RouteShareAbsSmall float64
	RouteShareRel      float64
	RouteCountAbsSmall float64
	RouteCountRel      float64
}

// DefaultValidationTolerances returns pragmatic defaults (not cert-grade strict).
func DefaultValidationTolerances() *ValidationTolerances {
	return &ValidationTolerances{
		ThroughputRel:           0.05,
		LatencyP50Rel:           0.12,
		LatencyP95Rel:           0.15,
		LatencyP99Rel:           0.18,
		UtilizationAbsPP:        0.10,
		IngressErrorRateAbs:     0.02,
		IngressErrorRateRel:     0.50,
		QueueDropRateAbs:        0.03,
		TopicDropRateAbs:        0.03,
		LocalityRateAbs:         0.10,
		CrossZoneRateAbs:        0.10,
		CrossZonePenaltyMeanAbs: 10.0,
		TopologyPenaltyMeanAbs:  10.0,
		QueueDepthAbsSmall:      2.0,
		QueueDepthRel:           0.25,
		TopicLagAbsSmall:        2.0,
		TopicLagRel:             0.30,
		RouteShareAbsSmall:      0.08,
		RouteShareRel:           0.25,
		RouteCountAbsSmall:      10.0,
		RouteCountRel:           0.30,
	}
}

func withinRel(obs, pred float64, rel float64) bool {
	if math.IsNaN(obs) || math.IsNaN(pred) {
		return false
	}
	denom := math.Max(math.Abs(obs), 1e-9)
	return math.Abs(pred-obs)/denom <= rel
}

func withinAbsRatio(obs, pred float64, absSmall, rel float64) bool {
	if math.Abs(obs) < absSmall && math.Abs(pred) < absSmall {
		return math.Abs(pred-obs) <= absSmall
	}
	return withinRel(obs, pred, rel)
}
