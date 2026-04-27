package simd

import (
	"errors"
	"testing"
	"time"

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

func TestOnlineRunLimitsFromEnvParsesAndFallsBack(t *testing.T) {
	t.Setenv("SIMD_SERVER_MAX_ONLINE_DURATION_MS", "120000")
	t.Setenv("SIMD_DEFAULT_MAX_ONLINE_DURATION_MS", "45000")
	t.Setenv("SIMD_HEARTBEAT_REQUIRED_AFTER_MS", "30000")
	t.Setenv("SIMD_SERVER_MAX_CONTROLLER_STEPS", "250")
	t.Setenv("SIMD_SERVER_MAX_CONCURRENT_ONLINE_RUNS", "4")

	limits := OnlineRunLimitsFromEnv()
	if limits.ServerMaxOnlineDuration != 120*time.Second {
		t.Fatalf("ServerMaxOnlineDuration: got %v", limits.ServerMaxOnlineDuration)
	}
	if limits.DefaultMaxOnlineDuration != 45*time.Second {
		t.Fatalf("DefaultMaxOnlineDuration: got %v", limits.DefaultMaxOnlineDuration)
	}
	if limits.HeartbeatRequiredAfterDuration != 30*time.Second {
		t.Fatalf("HeartbeatRequiredAfterDuration: got %v", limits.HeartbeatRequiredAfterDuration)
	}
	if limits.ServerMaxControllerSteps != 250 {
		t.Fatalf("ServerMaxControllerSteps: got %d", limits.ServerMaxControllerSteps)
	}
	if limits.MaxConcurrentOnlineRuns != 4 {
		t.Fatalf("MaxConcurrentOnlineRuns: got %d", limits.MaxConcurrentOnlineRuns)
	}

	t.Setenv("SIMD_SERVER_MAX_ONLINE_DURATION_MS", "-1")
	t.Setenv("SIMD_DEFAULT_MAX_ONLINE_DURATION_MS", "bad")
	t.Setenv("SIMD_HEARTBEAT_REQUIRED_AFTER_MS", "-9")
	t.Setenv("SIMD_SERVER_MAX_CONTROLLER_STEPS", "99999999999")
	t.Setenv("SIMD_SERVER_MAX_CONCURRENT_ONLINE_RUNS", "-7")

	fallback := OnlineRunLimitsFromEnv()
	def := DefaultOnlineRunLimits()
	if fallback.ServerMaxOnlineDuration != def.ServerMaxOnlineDuration {
		t.Fatalf("expected fallback ServerMaxOnlineDuration=%v, got %v", def.ServerMaxOnlineDuration, fallback.ServerMaxOnlineDuration)
	}
	if fallback.DefaultMaxOnlineDuration != def.DefaultMaxOnlineDuration {
		t.Fatalf("expected fallback DefaultMaxOnlineDuration=%v, got %v", def.DefaultMaxOnlineDuration, fallback.DefaultMaxOnlineDuration)
	}
	if fallback.HeartbeatRequiredAfterDuration != def.HeartbeatRequiredAfterDuration {
		t.Fatalf("expected fallback HeartbeatRequiredAfterDuration=%v, got %v", def.HeartbeatRequiredAfterDuration, fallback.HeartbeatRequiredAfterDuration)
	}
	if fallback.ServerMaxControllerSteps != def.ServerMaxControllerSteps {
		t.Fatalf("expected fallback ServerMaxControllerSteps=%d, got %d", def.ServerMaxControllerSteps, fallback.ServerMaxControllerSteps)
	}
	if fallback.MaxConcurrentOnlineRuns != def.MaxConcurrentOnlineRuns {
		t.Fatalf("expected fallback MaxConcurrentOnlineRuns=%d, got %d", def.MaxConcurrentOnlineRuns, fallback.MaxConcurrentOnlineRuns)
	}
}

func TestPrepareOnlineRunInputAppliesServerCapsAndNoopDefaults(t *testing.T) {
	limits := OnlineRunLimits{
		ServerMaxOnlineDuration:        30 * time.Second,
		ServerMaxControllerSteps:       200,
		MaxConcurrentOnlineRuns:        0,
		DefaultMaxOnlineDuration:       10 * time.Second,
		HeartbeatRequiredAfterDuration: 5 * time.Second,
	}
	input := &simulationv1.RunInput{
		Optimization: &simulationv1.OptimizationConfig{
			Online:               true,
			TargetP95LatencyMs:   50,
			ControlIntervalMs:    100,
			MaxOnlineDurationMs:  120000, // should be capped to 30s
			MaxControllerSteps:   300,    // should be capped to 200
			MaxNoopIntervals:     0,      // should be defaulted
			AllowUnboundedOnline: false,
			LeaseTtlMs:           1000,
		},
	}
	if err := PrepareOnlineRunInput(input, limits); err != nil {
		t.Fatalf("PrepareOnlineRunInput error: %v", err)
	}
	opt := input.Optimization
	if opt.MaxOnlineDurationMs != 30000 {
		t.Fatalf("expected max_online_duration_ms capped to 30000, got %d", opt.MaxOnlineDurationMs)
	}
	if opt.MaxControllerSteps != 200 {
		t.Fatalf("expected max_controller_steps capped to 200, got %d", opt.MaxControllerSteps)
	}
	if opt.MaxNoopIntervals < 20 {
		t.Fatalf("expected default max_noop_intervals >= 20, got %d", opt.MaxNoopIntervals)
	}
}

func TestPrepareOnlineRunInputAllowUnboundedDefaultsNoopToNegativeOne(t *testing.T) {
	limits := DefaultOnlineRunLimits()
	input := &simulationv1.RunInput{
		Optimization: &simulationv1.OptimizationConfig{
			Online:               true,
			TargetP95LatencyMs:   50,
			ControlIntervalMs:    100,
			AllowUnboundedOnline: true,
			MaxNoopIntervals:     0,
		},
	}
	if err := PrepareOnlineRunInput(input, limits); err != nil {
		t.Fatalf("PrepareOnlineRunInput error: %v", err)
	}
	if input.Optimization.MaxNoopIntervals != -1 {
		t.Fatalf("expected max_noop_intervals=-1 for unbounded online mode, got %d", input.Optimization.MaxNoopIntervals)
	}
}
