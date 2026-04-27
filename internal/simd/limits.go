package simd

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/engine"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

// SimulationLimits defines server-side guardrails for both preflight validation and runtime enforcement.
type SimulationLimits struct {
	MaxStandardDuration        time.Duration
	MaxEventsScheduled         int64
	MaxEventsProcessed         int64
	MaxEventQueueSize          int64
	MaxRequestsTracked         int
	MaxMetricPoints            int
	MaxWallClockRuntime        time.Duration
	MaxOptimizationEvaluations int32
}

func defaultSimulationLimits() SimulationLimits {
	return SimulationLimits{
		MaxStandardDuration:        30 * time.Minute,
		MaxEventsScheduled:         2_000_000,
		MaxEventsProcessed:         2_000_000,
		MaxEventQueueSize:          200_000,
		MaxRequestsTracked:         200_000,
		MaxMetricPoints:            1_000_000,
		MaxWallClockRuntime:        15 * time.Minute,
		MaxOptimizationEvaluations: 1_000,
	}
}

func simulationLimitsFromEnv() (SimulationLimits, error) {
	limits := defaultSimulationLimits()
	var err error

	if limits.MaxStandardDuration, err = parsePositiveDurationEnv("SIMD_MAX_STANDARD_DURATION", limits.MaxStandardDuration); err != nil {
		return SimulationLimits{}, err
	}
	if limits.MaxEventsScheduled, err = parsePositiveInt64Env("SIMD_MAX_EVENTS_SCHEDULED", limits.MaxEventsScheduled); err != nil {
		return SimulationLimits{}, err
	}
	if limits.MaxEventsProcessed, err = parsePositiveInt64Env("SIMD_MAX_EVENTS_PROCESSED", limits.MaxEventsProcessed); err != nil {
		return SimulationLimits{}, err
	}
	if limits.MaxEventQueueSize, err = parsePositiveInt64Env("SIMD_MAX_EVENT_QUEUE_SIZE", limits.MaxEventQueueSize); err != nil {
		return SimulationLimits{}, err
	}
	if limits.MaxRequestsTracked, err = parsePositiveIntEnv("SIMD_MAX_REQUESTS_TRACKED", limits.MaxRequestsTracked); err != nil {
		return SimulationLimits{}, err
	}
	if limits.MaxMetricPoints, err = parsePositiveIntEnv("SIMD_MAX_METRIC_POINTS", limits.MaxMetricPoints); err != nil {
		return SimulationLimits{}, err
	}
	if limits.MaxWallClockRuntime, err = parsePositiveDurationEnv("SIMD_MAX_WALL_CLOCK_RUNTIME", limits.MaxWallClockRuntime); err != nil {
		return SimulationLimits{}, err
	}
	if limits.MaxOptimizationEvaluations, err = parsePositiveInt32Env("SIMD_MAX_OPTIMIZATION_EVALUATIONS", limits.MaxOptimizationEvaluations); err != nil {
		return SimulationLimits{}, err
	}
	return limits, nil
}

func parsePositiveDurationEnv(key string, def time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid duration %q: %w", key, raw, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s: value must be > 0", key)
	}
	return d, nil
}

func parsePositiveInt64Env(key string, def int64) (int64, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid integer %q: %w", key, raw, err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("%s: value must be > 0", key)
	}
	return n, nil
}

func parsePositiveIntEnv(key string, def int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid integer %q: %w", key, raw, err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("%s: value must be > 0", key)
	}
	return n, nil
}

func parsePositiveInt32Env(key string, def int32) (int32, error) {
	n, err := parsePositiveInt64Env(key, int64(def))
	if err != nil {
		return 0, err
	}
	return int32(n), nil //nolint:gosec // bounded by default values and env parse for positive int64
}

func (l SimulationLimits) toEngineRuntimeLimits() engine.RuntimeLimits {
	return engine.RuntimeLimits{
		MaxEventsScheduled: l.MaxEventsScheduled,
		MaxEventsProcessed: l.MaxEventsProcessed,
		MaxEventQueueSize:  l.MaxEventQueueSize,
		MaxWallClockRun:    l.MaxWallClockRuntime,
	}
}

func (l SimulationLimits) validatePreStart(input *simulationv1.RunInput) error {
	if input == nil {
		return nil
	}
	duration := time.Duration(input.GetDurationMs()) * time.Millisecond
	if duration <= 0 {
		duration = 10 * time.Second
	}
	if input.GetOptimization() == nil && l.MaxStandardDuration > 0 && duration > l.MaxStandardDuration {
		return fmt.Errorf("run rejected: duration %s exceeds max standard duration %s", duration, l.MaxStandardDuration)
	}
	if opt := input.GetOptimization(); opt != nil && !opt.GetOnline() && opt.GetMaxEvaluations() > l.MaxOptimizationEvaluations {
		return fmt.Errorf("run rejected: optimization max_evaluations %d exceeds server limit %d", opt.GetMaxEvaluations(), l.MaxOptimizationEvaluations)
	}
	scenario, err := config.ParseScenarioYAMLString(input.GetScenarioYaml())
	if err != nil {
		return nil
	}
	est := estimateWorkloadArrivals(scenario, duration)
	if l.MaxEventsScheduled > 0 && est > l.MaxEventsScheduled {
		return fmt.Errorf("run rejected: estimated arrivals %d exceeds max events scheduled %d", est, l.MaxEventsScheduled)
	}
	return nil
}

func estimateWorkloadArrivals(scenario *config.Scenario, duration time.Duration) int64 {
	if scenario == nil || duration <= 0 {
		return 0
	}
	seconds := duration.Seconds()
	var total float64
	for _, wl := range scenario.Workload {
		rate := wl.Arrival.RateRPS
		if rate <= 0 {
			continue
		}
		total += rate * seconds
	}
	if total < 0 {
		return 0
	}
	return int64(total)
}
