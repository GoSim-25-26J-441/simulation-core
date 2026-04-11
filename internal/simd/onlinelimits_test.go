package simd

import (
	"errors"
	"testing"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
)

func TestPrepareOnlineRunInputLeaseRequiredForLongDuration(t *testing.T) {
	limits := DefaultOnlineRunLimits()
	input := &simulationv1.RunInput{
		Optimization: &simulationv1.OptimizationConfig{
			Online:              true,
			TargetP95LatencyMs:  50,
			ControlIntervalMs:   1000,
			MaxOnlineDurationMs: 15 * 60 * 1000, // 15m > default 10m heartbeat threshold
			MaxControllerSteps:  100,
		},
	}
	err := PrepareOnlineRunInput(input, limits)
	if !errors.Is(err, ErrInvalidOnlineRunInput) {
		t.Fatalf("expected ErrInvalidOnlineRunInput, got %v", err)
	}
}

func TestPrepareOnlineRunInputAppliesDefaultDurationAndDerivesSteps(t *testing.T) {
	limits := DefaultOnlineRunLimits()
	input := &simulationv1.RunInput{
		Optimization: &simulationv1.OptimizationConfig{
			Online:             true,
			TargetP95LatencyMs: 50,
			ControlIntervalMs:  1000,
		},
	}
	if err := PrepareOnlineRunInput(input, limits); err != nil {
		t.Fatal(err)
	}
	opt := input.Optimization
	if opt.GetMaxOnlineDurationMs() != limits.DefaultMaxOnlineDuration.Milliseconds() {
		t.Fatalf("max_online_duration_ms: got %d want %d", opt.GetMaxOnlineDurationMs(), limits.DefaultMaxOnlineDuration.Milliseconds())
	}
	// 10m / 1s = 600 steps
	if opt.GetMaxControllerSteps() != 600 {
		t.Fatalf("max_controller_steps: got %d want 600", opt.GetMaxControllerSteps())
	}
}
