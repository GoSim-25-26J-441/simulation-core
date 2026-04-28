package calibration

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ToleranceProfile selects a preset band; empty string means default.
type ToleranceProfile string

const (
	ToleranceProfileDefault ToleranceProfile = "default"
	ToleranceProfileStrict  ToleranceProfile = "strict"
	ToleranceProfileLoose   ToleranceProfile = "loose"
)

// StrictValidationTolerances tightens relative bands (stricter pass/fail).
func StrictValidationTolerances() *ValidationTolerances {
	return &ValidationTolerances{
		ThroughputRel:           0.03,
		LatencyP50Rel:           0.08,
		LatencyP95Rel:           0.10,
		LatencyP99Rel:           0.12,
		UtilizationAbsPP:        0.06,
		IngressErrorRateAbs:     0.01,
		IngressErrorRateRel:     0.35,
		QueueDropRateAbs:        0.02,
		TopicDropRateAbs:        0.02,
		QueueDepthAbsSmall:      1.0,
		QueueDepthRel:           0.15,
		TopicLagAbsSmall:        1.0,
		TopicLagRel:             0.20,
		RouteShareAbsSmall:      0.05,
		RouteShareRel:           0.15,
		RouteCountAbsSmall:      5.0,
		RouteCountRel:           0.20,
		LocalityRateAbs:         0.06,
		CrossZoneRateAbs:        0.06,
		CrossZonePenaltyMeanAbs: 6.0,
		TopologyPenaltyMeanAbs:  6.0,
	}
}

// LooseValidationTolerances widens bands (more permissive).
func LooseValidationTolerances() *ValidationTolerances {
	return &ValidationTolerances{
		ThroughputRel:       0.10,
		LatencyP50Rel:       0.18,
		LatencyP95Rel:       0.22,
		LatencyP99Rel:       0.28,
		UtilizationAbsPP:    0.15,
		IngressErrorRateAbs: 0.04,
		IngressErrorRateRel: 0.60,
		QueueDropRateAbs:    0.05,
		TopicDropRateAbs:    0.05,
		QueueDepthAbsSmall:  4.0,
		QueueDepthRel:       0.40,
		TopicLagAbsSmall:    4.0,
		TopicLagRel:         0.45,
		RouteShareAbsSmall:  0.12,
		RouteShareRel:       0.35,
		RouteCountAbsSmall:  20.0,
		RouteCountRel:       0.45,
	}
}

// ResolveToleranceProfile returns tolerances for a profile name (default if unknown).
func ResolveToleranceProfile(name string) *ValidationTolerances {
	switch ToleranceProfile(strings.ToLower(strings.TrimSpace(name))) {
	case ToleranceProfileStrict:
		return StrictValidationTolerances()
	case ToleranceProfileLoose:
		return LooseValidationTolerances()
	default:
		return DefaultValidationTolerances()
	}
}

// ApplyToleranceJSON merges JSON object keys (snake_case) onto a copy of base.
// Supported keys match ValidationTolerances json tags conceptually: throughput_rel, latency_p50_rel, ...
func ApplyToleranceJSON(base *ValidationTolerances, raw json.RawMessage) (*ValidationTolerances, error) {
	if len(raw) == 0 {
		if base == nil {
			return DefaultValidationTolerances(), nil
		}
		out := *base
		return &out, nil
	}
	var m map[string]float64
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("tolerances JSON: %w", err)
	}
	if base == nil {
		base = DefaultValidationTolerances()
	}
	out := *base
	for k, v := range m {
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "throughput_rel":
			out.ThroughputRel = v
		case "latency_p50_rel":
			out.LatencyP50Rel = v
		case "latency_p95_rel":
			out.LatencyP95Rel = v
		case "latency_p99_rel":
			out.LatencyP99Rel = v
		case "utilization_abs_pp":
			out.UtilizationAbsPP = v
		case "ingress_error_rate_abs":
			out.IngressErrorRateAbs = v
		case "ingress_error_rate_rel":
			out.IngressErrorRateRel = v
		case "queue_drop_rate_abs":
			out.QueueDropRateAbs = v
		case "topic_drop_rate_abs":
			out.TopicDropRateAbs = v
		case "queue_depth_abs_small":
			out.QueueDepthAbsSmall = v
		case "queue_depth_rel":
			out.QueueDepthRel = v
		case "topic_lag_abs_small":
			out.TopicLagAbsSmall = v
		case "topic_lag_rel":
			out.TopicLagRel = v
		case "route_share_abs_small":
			out.RouteShareAbsSmall = v
		case "route_share_rel":
			out.RouteShareRel = v
		case "route_count_abs_small":
			out.RouteCountAbsSmall = v
		case "route_count_rel":
			out.RouteCountRel = v
		case "locality_rate_abs":
			out.LocalityRateAbs = v
		case "cross_zone_rate_abs":
			out.CrossZoneRateAbs = v
		case "cross_zone_penalty_mean_abs":
			out.CrossZonePenaltyMeanAbs = v
		case "topology_penalty_mean_abs":
			out.TopologyPenaltyMeanAbs = v
		default:
			return nil, fmt.Errorf("tolerances: unknown key %q", k)
		}
	}
	return &out, nil
}
