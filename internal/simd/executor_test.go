package simd

import (
	"testing"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
)

func TestRunExecutorStartTransitionsToRunning(t *testing.T) {
	store := NewRunStore()
	_, err := store.Create("run-1", &simulationv1.RunInput{ScenarioYaml: "x"})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	exec := NewRunExecutor(store)
	rec, err := exec.Start("run-1")
	if err != nil {
		t.Fatalf("Start error: %v", err)
	}
	if rec.Run.Status != simulationv1.RunStatus_RUN_STATUS_RUNNING {
		t.Fatalf("expected running, got %v", rec.Run.Status)
	}
}

func TestRunExecutorStopPreventsCompletion(t *testing.T) {
	store := NewRunStore()
	_, err := store.Create("run-1", &simulationv1.RunInput{ScenarioYaml: "x"})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	exec := NewRunExecutor(store)
	_, err = exec.Start("run-1")
	if err != nil {
		t.Fatalf("Start error: %v", err)
	}

	_, err = exec.Stop("run-1")
	if err != nil {
		t.Fatalf("Stop error: %v", err)
	}

	// Wait longer than the skeleton completion delay.
	time.Sleep(25 * time.Millisecond)

	rec, ok := store.Get("run-1")
	if !ok {
		t.Fatalf("expected run to exist")
	}
	if rec.Run.Status != simulationv1.RunStatus_RUN_STATUS_CANCELLED {
		t.Fatalf("expected cancelled, got %v", rec.Run.Status)
	}
}

func TestRunExecutorStartOnMissingRun(t *testing.T) {
	exec := NewRunExecutor(NewRunStore())
	_, err := exec.Start("nope")
	if err == nil {
		t.Fatalf("expected error")
	}
}
