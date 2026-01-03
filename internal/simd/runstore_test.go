package simd

import (
	"testing"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
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
