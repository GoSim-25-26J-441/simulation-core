package simd

import (
	"strings"
	"testing"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
)

func TestRunStoreCreateAndGet(t *testing.T) {
	store := NewRunStore()

	rec, err := store.Create("", &simulationv1.RunInput{ScenarioYaml: "hosts: []"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if rec == nil || rec.Run == nil {
		t.Fatalf("Create returned nil record/run")
	}
	if rec.Run.Id == "" {
		t.Fatalf("expected generated run id")
	}
	if rec.Run.Status != simulationv1.RunStatus_RUN_STATUS_PENDING {
		t.Fatalf("expected status pending, got %v", rec.Run.Status)
	}
	if rec.Run.CreatedAtUnixMs == 0 {
		t.Fatalf("expected created_at_unix_ms to be set")
	}

	got, ok := store.Get(rec.Run.Id)
	if !ok {
		t.Fatalf("expected run to exist")
	}
	if got.Run.Id != rec.Run.Id {
		t.Fatalf("expected same run id")
	}
}

func TestRunStoreCreateDuplicate(t *testing.T) {
	store := NewRunStore()
	_, err := store.Create("run-1", &simulationv1.RunInput{ScenarioYaml: "x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = store.Create("run-1", &simulationv1.RunInput{ScenarioYaml: "y"})
	if err == nil {
		t.Fatalf("expected duplicate error")
	}
}

func TestRunStoreSetStatusSetsTimestamps(t *testing.T) {
	store := NewRunStore()
	rec, err := store.Create("run-1", &simulationv1.RunInput{ScenarioYaml: "x"})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	if rec.Run.StartedAtUnixMs != 0 || rec.Run.EndedAtUnixMs != 0 {
		t.Fatalf("expected timestamps not set initially")
	}

	rec, err = store.SetStatus("run-1", simulationv1.RunStatus_RUN_STATUS_RUNNING, "")
	if err != nil {
		t.Fatalf("SetStatus running error: %v", err)
	}
	if rec.Run.StartedAtUnixMs == 0 {
		t.Fatalf("expected started_at_unix_ms set")
	}
	if rec.Run.EndedAtUnixMs != 0 {
		t.Fatalf("did not expect ended_at_unix_ms set for running")
	}

	rec, err = store.SetStatus("run-1", simulationv1.RunStatus_RUN_STATUS_COMPLETED, "")
	if err != nil {
		t.Fatalf("SetStatus completed error: %v", err)
	}
	if rec.Run.EndedAtUnixMs == 0 {
		t.Fatalf("expected ended_at_unix_ms set")
	}
}

func TestRunStoreSetMetrics(t *testing.T) {
	store := NewRunStore()
	_, err := store.Create("run-1", &simulationv1.RunInput{ScenarioYaml: "x"})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	metrics := &simulationv1.RunMetrics{TotalRequests: 123}
	if err := store.SetMetrics("run-1", metrics); err != nil {
		t.Fatalf("SetMetrics error: %v", err)
	}

	rec, ok := store.Get("run-1")
	if !ok {
		t.Fatalf("expected run to exist")
	}
	if rec.Metrics == nil || rec.Metrics.TotalRequests != 123 {
		t.Fatalf("expected metrics to be stored")
	}
}

func TestRunStoreListLimit(t *testing.T) {
	store := NewRunStore()
	for i := 0; i < 10; i++ {
		_, err := store.Create("", &simulationv1.RunInput{ScenarioYaml: "x"})
		if err != nil {
			t.Fatalf("Create error: %v", err)
		}
	}

	recs := store.List(3)
	if len(recs) != 3 {
		t.Fatalf("expected 3 records, got %d", len(recs))
	}
}

func TestRunStoreCreateInvalidRunID(t *testing.T) {
	store := NewRunStore()
	tests := []struct {
		name  string
		runID string
	}{
		{"with colon", "test:stop"},
		{"with slash", "test/metrics"},
		{"with both", "test:stop/metrics"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := store.Create(tt.runID, &simulationv1.RunInput{ScenarioYaml: "x"})
			if err == nil {
				t.Fatalf("expected error for run ID %q", tt.runID)
			}
			if !strings.Contains(err.Error(), "cannot contain") {
				t.Fatalf("expected validation error, got: %v", err)
			}
		})
	}
}

func TestRunStoreSetStatusWithErrorMessage(t *testing.T) {
	store := NewRunStore()
	_, err := store.Create("run-1", &simulationv1.RunInput{ScenarioYaml: "x"})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	rec, err := store.SetStatus("run-1", simulationv1.RunStatus_RUN_STATUS_FAILED, "test error")
	if err != nil {
		t.Fatalf("SetStatus error: %v", err)
	}
	if rec.Run.Error != "test error" {
		t.Fatalf("expected error message, got %q", rec.Run.Error)
	}
	if rec.Run.Status != simulationv1.RunStatus_RUN_STATUS_FAILED {
		t.Fatalf("expected failed status")
	}
}

func TestRunStoreSetStatusOnNonExistentRun(t *testing.T) {
	store := NewRunStore()
	_, err := store.SetStatus("nope", simulationv1.RunStatus_RUN_STATUS_RUNNING, "")
	if err == nil {
		t.Fatalf("expected error for non-existent run")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found error, got: %v", err)
	}
}

func TestRunStoreSetMetricsOnNonExistentRun(t *testing.T) {
	store := NewRunStore()
	metrics := &simulationv1.RunMetrics{TotalRequests: 123}
	err := store.SetMetrics("nope", metrics)
	if err == nil {
		t.Fatalf("expected error for non-existent run")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found error, got: %v", err)
	}
}

func TestRunStoreGetNonExistentRun(t *testing.T) {
	store := NewRunStore()
	_, ok := store.Get("nope")
	if ok {
		t.Fatalf("expected false for non-existent run")
	}
}

func TestRunStoreListWithZeroLimit(t *testing.T) {
	store := NewRunStore()
	for i := 0; i < 5; i++ {
		_, err := store.Create("", &simulationv1.RunInput{ScenarioYaml: "x"})
		if err != nil {
			t.Fatalf("Create error: %v", err)
		}
	}

	recs := store.List(0)
	if len(recs) == 0 {
		t.Fatalf("expected default limit to be applied")
	}
	if len(recs) > 50 {
		t.Fatalf("expected max 50 records, got %d", len(recs))
	}
}

func TestRunStoreSetStatusFailedAndCancelled(t *testing.T) {
	store := NewRunStore()
	_, err := store.Create("run-1", &simulationv1.RunInput{ScenarioYaml: "x"})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	// Test FAILED status
	rec, err := store.SetStatus("run-1", simulationv1.RunStatus_RUN_STATUS_FAILED, "failed")
	if err != nil {
		t.Fatalf("SetStatus failed error: %v", err)
	}
	if rec.Run.EndedAtUnixMs == 0 {
		t.Fatalf("expected ended_at_unix_ms set for failed")
	}

	// Test CANCELLED status
	_, err = store.Create("run-2", &simulationv1.RunInput{ScenarioYaml: "x"})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	rec, err = store.SetStatus("run-2", simulationv1.RunStatus_RUN_STATUS_CANCELLED, "")
	if err != nil {
		t.Fatalf("SetStatus cancelled error: %v", err)
	}
	if rec.Run.EndedAtUnixMs == 0 {
		t.Fatalf("expected ended_at_unix_ms set for cancelled")
	}
}

func TestRunStoreSetCollector(t *testing.T) {
	store := NewRunStore()
	_, err := store.Create("run-1", &simulationv1.RunInput{ScenarioYaml: "x"})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	collector := metrics.NewCollector()
	if err := store.SetCollector("run-1", collector); err != nil {
		t.Fatalf("SetCollector error: %v", err)
	}

	got, ok := store.GetCollector("run-1")
	if !ok {
		t.Fatalf("expected collector to exist")
	}
	if got != collector {
		t.Fatalf("expected same collector reference")
	}
}

func TestRunStoreGetCollectorNonExistent(t *testing.T) {
	store := NewRunStore()
	_, ok := store.GetCollector("nope")
	if ok {
		t.Fatalf("expected false for non-existent run")
	}
}

func TestRunStoreGetCollectorNoCollector(t *testing.T) {
	store := NewRunStore()
	_, err := store.Create("run-1", &simulationv1.RunInput{ScenarioYaml: "x"})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	_, ok := store.GetCollector("run-1")
	if ok {
		t.Fatalf("expected false when collector not set")
	}
}

func TestRunStoreSetCollectorOnNonExistentRun(t *testing.T) {
	store := NewRunStore()
	collector := metrics.NewCollector()
	err := store.SetCollector("nope", collector)
	if err == nil {
		t.Fatalf("expected error for non-existent run")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found error, got: %v", err)
	}
}

func TestRunStoreCollectorPersistsAfterClone(t *testing.T) {
	store := NewRunStore()
	_, err := store.Create("run-1", &simulationv1.RunInput{ScenarioYaml: "x"})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	collector := metrics.NewCollector()
	collector.Start()
	if err := store.SetCollector("run-1", collector); err != nil {
		t.Fatalf("SetCollector error: %v", err)
	}

	// Get should return cloned record with same collector reference
	rec, ok := store.Get("run-1")
	if !ok {
		t.Fatalf("expected run to exist")
	}
	if rec.Collector != collector {
		t.Fatalf("expected same collector reference in cloned record")
	}
}
