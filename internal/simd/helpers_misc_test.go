package simd

import (
	"testing"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
)

func TestMetadataHelpersHandleSupportedAndFallbackTypes(t *testing.T) {
	now := time.Now()
	meta := map[string]interface{}{
		"i":   int(7),
		"i32": int32(8),
		"i64": int64(9),
		"f":   float64(10.8),
		"f32": float32(11.2),
		"b":   true,
		"t":   now,
		"bad": "x",
	}

	if got := metadataInt(meta, "i"); got != 7 {
		t.Fatalf("metadataInt int: got %d", got)
	}
	if got := metadataInt(meta, "i32"); got != 8 {
		t.Fatalf("metadataInt int32: got %d", got)
	}
	if got := metadataInt(meta, "i64"); got != 9 {
		t.Fatalf("metadataInt int64: got %d", got)
	}
	if got := metadataInt(meta, "f"); got != 10 {
		t.Fatalf("metadataInt float64 cast: got %d", got)
	}
	if got := metadataInt(meta, "missing"); got != 0 {
		t.Fatalf("metadataInt missing: got %d", got)
	}
	if got := metadataInt(nil, "i"); got != 0 {
		t.Fatalf("metadataInt nil map: got %d", got)
	}

	if !metadataBool(meta, "b") {
		t.Fatal("metadataBool expected true for bool value")
	}
	if metadataBool(meta, "bad") {
		t.Fatal("metadataBool expected false for non-bool value")
	}
	if metadataBool(nil, "b") {
		t.Fatal("metadataBool expected false for nil map")
	}

	if got, ok := metadataTime(meta, "t"); !ok || !got.Equal(now) {
		t.Fatalf("metadataTime expected existing timestamp, got=%v ok=%v", got, ok)
	}
	if got, ok := metadataTime(meta, "bad"); ok || !got.IsZero() {
		t.Fatalf("metadataTime expected zero,false for wrong type, got=%v ok=%v", got, ok)
	}

	if got := metadataFloat64(meta, "f"); got != 10.8 {
		t.Fatalf("metadataFloat64 float64: got %f", got)
	}
	if got := metadataFloat64(meta, "f32"); got != float64(float32(11.2)) {
		t.Fatalf("metadataFloat64 float32: got %f", got)
	}
	if got := metadataFloat64(meta, "i"); got != 7 {
		t.Fatalf("metadataFloat64 int: got %f", got)
	}
	if got := metadataFloat64(meta, "i64"); got != 9 {
		t.Fatalf("metadataFloat64 int64: got %f", got)
	}
	if got := metadataFloat64(meta, "bad"); got != 0 {
		t.Fatalf("metadataFloat64 fallback: got %f", got)
	}

	if got := metadataInt64(meta, "i64"); got != 9 {
		t.Fatalf("metadataInt64 int64: got %d", got)
	}
	if got := metadataInt64(meta, "i"); got != 7 {
		t.Fatalf("metadataInt64 int: got %d", got)
	}
	if got := metadataInt64(meta, "f"); got != 10 {
		t.Fatalf("metadataInt64 float64 cast: got %d", got)
	}
	if got := metadataInt64(meta, "f32"); got != 11 {
		t.Fatalf("metadataInt64 float32 cast: got %d", got)
	}
	if got := metadataInt64(map[string]interface{}{"n": nil}, "n"); got != 0 {
		t.Fatalf("metadataInt64 nil value: got %d", got)
	}
}

func TestParseRunStatusCoversKnownAndUnknownValues(t *testing.T) {
	cases := map[string]simulationv1.RunStatus{
		"pending":   simulationv1.RunStatus_RUN_STATUS_PENDING,
		"RUNNING":   simulationv1.RunStatus_RUN_STATUS_RUNNING,
		"Completed": simulationv1.RunStatus_RUN_STATUS_COMPLETED,
		"FAILED":    simulationv1.RunStatus_RUN_STATUS_FAILED,
		"cancelled": simulationv1.RunStatus_RUN_STATUS_CANCELLED,
		"stopped":   simulationv1.RunStatus_RUN_STATUS_STOPPED,
		"weird":     simulationv1.RunStatus_RUN_STATUS_UNSPECIFIED,
	}
	for in, want := range cases {
		if got := parseRunStatus(in); got != want {
			t.Fatalf("parseRunStatus(%q): got %v want %v", in, got, want)
		}
	}
}

func TestObjectiveAndUnitForProgressMappings(t *testing.T) {
	if obj, unit := ObjectiveAndUnitForProgress(nil); obj != "p95_latency" || unit != "ms" {
		t.Fatalf("nil optimization mapping mismatch: %q %q", obj, unit)
	}

	online := &simulationv1.OptimizationConfig{Online: true, OptimizationTargetPrimary: " cpu_utilization "}
	if obj, unit := ObjectiveAndUnitForProgress(online); obj != "cpu_utilization" || unit != "ratio" {
		t.Fatalf("online cpu mapping mismatch: %q %q", obj, unit)
	}
	online.OptimizationTargetPrimary = ""
	if obj, unit := ObjectiveAndUnitForProgress(online); obj != "p95_latency" || unit != "ms" {
		t.Fatalf("online default mapping mismatch: %q %q", obj, unit)
	}

	cases := map[string][2]string{
		"p95_latency_ms":     {"p95_latency", "ms"},
		"p99_latency_ms":     {"p99_latency", "ms"},
		"mean_latency_ms":    {"mean_latency", "ms"},
		"cpu_utilization":    {"cpu_utilization", "ratio"},
		"memory_utilization": {"memory_utilization", "ratio"},
		"error_rate":         {"error_rate", "ratio"},
		"throughput_rps":     {"throughput_rps", "rps"},
		"custom_cost":        {"custom_cost", "ms"},
		"":                   {"p95_latency", "ms"},
	}
	for objective, want := range cases {
		opt := &simulationv1.OptimizationConfig{Objective: objective}
		obj, unit := ObjectiveAndUnitForProgress(opt)
		if obj != want[0] || unit != want[1] {
			t.Fatalf("batch objective %q: got (%q,%q) want (%q,%q)", objective, obj, unit, want[0], want[1])
		}
	}
}
