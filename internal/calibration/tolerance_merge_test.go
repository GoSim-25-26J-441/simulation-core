package calibration

import (
	"encoding/json"
	"testing"
)

func TestToleranceProfilesResolve(t *testing.T) {
	strict := ResolveToleranceProfile(" strict ")
	loose := ResolveToleranceProfile("LOOSE")
	def := ResolveToleranceProfile("unknown")

	if strict.ThroughputRel >= def.ThroughputRel {
		t.Fatalf("strict throughput tolerance should be tighter than default")
	}
	if loose.ThroughputRel <= def.ThroughputRel {
		t.Fatalf("loose throughput tolerance should be wider than default")
	}
	if strict.LatencyP99Rel >= def.LatencyP99Rel {
		t.Fatalf("strict latency p99 tolerance should be tighter than default")
	}
	if loose.LatencyP99Rel <= def.LatencyP99Rel {
		t.Fatalf("loose latency p99 tolerance should be wider than default")
	}
}

func TestApplyToleranceJSONNilAndEmpty(t *testing.T) {
	got, err := ApplyToleranceJSON(nil, nil)
	if err != nil {
		t.Fatalf("ApplyToleranceJSON(nil,nil) error: %v", err)
	}
	def := DefaultValidationTolerances()
	if got.ThroughputRel != def.ThroughputRel {
		t.Fatalf("expected default throughput_rel %v, got %v", def.ThroughputRel, got.ThroughputRel)
	}

	base := &ValidationTolerances{ThroughputRel: 0.42, LatencyP95Rel: 0.9}
	clone, err := ApplyToleranceJSON(base, json.RawMessage{})
	if err != nil {
		t.Fatalf("ApplyToleranceJSON(base,empty) error: %v", err)
	}
	if clone == base {
		t.Fatalf("expected copy, got same pointer")
	}
	if clone.ThroughputRel != base.ThroughputRel || clone.LatencyP95Rel != base.LatencyP95Rel {
		t.Fatalf("expected copy of base values, got %+v", clone)
	}
}

func TestApplyToleranceJSONOverridesKnownKeys(t *testing.T) {
	raw := json.RawMessage(`{
		"throughput_rel": 0.2,
		"latency_p50_rel": 0.21,
		"latency_p95_rel": 0.22,
		"latency_p99_rel": 0.23,
		"utilization_abs_pp": 0.24,
		"ingress_error_rate_abs": 0.25,
		"ingress_error_rate_rel": 0.26,
		"queue_drop_rate_abs": 0.27,
		"topic_drop_rate_abs": 0.28,
		"queue_depth_abs_small": 3.0,
		"queue_depth_rel": 0.29,
		"topic_lag_abs_small": 4.0,
		"topic_lag_rel": 0.30,
		"route_share_abs_small": 0.31,
		"route_share_rel": 0.32,
		"route_count_abs_small": 12.0,
		"route_count_rel": 0.33,
		"locality_rate_abs": 0.34,
		"cross_zone_rate_abs": 0.35,
		"cross_zone_penalty_mean_abs": 7.0,
		"topology_penalty_mean_abs": 8.0
	}`)

	got, err := ApplyToleranceJSON(DefaultValidationTolerances(), raw)
	if err != nil {
		t.Fatalf("ApplyToleranceJSON error: %v", err)
	}
	if got.TopologyPenaltyMeanAbs != 8.0 || got.CrossZonePenaltyMeanAbs != 7.0 {
		t.Fatalf("expected topology/cross-zone penalties to be overridden, got %+v", got)
	}
	if got.RouteCountRel != 0.33 || got.RouteShareRel != 0.32 || got.ThroughputRel != 0.2 {
		t.Fatalf("expected key overrides to apply, got %+v", got)
	}
}

func TestApplyToleranceJSONErrors(t *testing.T) {
	if _, err := ApplyToleranceJSON(nil, json.RawMessage(`{"unknown_key":1}`)); err == nil {
		t.Fatalf("expected unknown key error")
	}
	if _, err := ApplyToleranceJSON(nil, json.RawMessage(`{"throughput_rel":`)); err == nil {
		t.Fatalf("expected malformed json error")
	}
}
