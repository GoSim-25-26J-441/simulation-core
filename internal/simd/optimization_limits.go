package simd

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
)

type OptimizationSafetyLimits struct {
	MaxEvaluations            int32
	MaxIterations             int32
	MaxConcurrentCandidates   int
	MaxWallClockRuntime       time.Duration
	CandidateMaxWallClock     time.Duration
	MaxFailedCandidates       int
	MaxRetainedCandidates     int
	DefaultEvaluationDuration int64
	AllowBatch                bool
}

func defaultOptimizationSafetyLimits() OptimizationSafetyLimits {
	return OptimizationSafetyLimits{
		MaxEvaluations:            128,
		MaxIterations:             20,
		MaxConcurrentCandidates:   2,
		MaxWallClockRuntime:       20 * time.Minute,
		CandidateMaxWallClock:     90 * time.Second,
		MaxFailedCandidates:       16,
		MaxRetainedCandidates:     64,
		DefaultEvaluationDuration: 10_000,
		AllowBatch:                true,
	}
}

func optimizationSafetyLimitsFromEnv() OptimizationSafetyLimits {
	l := defaultOptimizationSafetyLimits()
	if v := getenvInt32("SIMD_OPTIMIZATION_MAX_EVALUATIONS"); v > 0 {
		l.MaxEvaluations = v
	}
	if v := getenvInt32("SIMD_OPTIMIZATION_MAX_ITERATIONS"); v > 0 {
		l.MaxIterations = v
	}
	if v := getenvInt("SIMD_OPTIMIZATION_MAX_CONCURRENT_CANDIDATES"); v > 0 {
		l.MaxConcurrentCandidates = v
	}
	if v := getenvDuration("SIMD_OPTIMIZATION_MAX_WALL_CLOCK_RUNTIME"); v > 0 {
		l.MaxWallClockRuntime = v
	}
	if v := getenvDuration("SIMD_OPTIMIZATION_CANDIDATE_MAX_WALL_CLOCK_RUNTIME"); v > 0 {
		l.CandidateMaxWallClock = v
	}
	if v := getenvInt("SIMD_OPTIMIZATION_MAX_FAILED_CANDIDATES"); v > 0 {
		l.MaxFailedCandidates = v
	}
	if v := getenvInt("SIMD_OPTIMIZATION_MAX_RETAINED_CANDIDATES"); v > 0 {
		l.MaxRetainedCandidates = v
	}
	if v := getenvInt64("SIMD_OPTIMIZATION_DEFAULT_EVALUATION_DURATION"); v > 0 {
		l.DefaultEvaluationDuration = v
	}
	if v, ok := getenvBool("SIMD_OPTIMIZATION_ALLOW_BATCH"); ok {
		l.AllowBatch = v
	}
	return l
}

func validateOptimizationPreStart(input *simulationv1.RunInput, lim OptimizationSafetyLimits) error {
	if input == nil || input.GetOptimization() == nil {
		return nil
	}
	opt := input.GetOptimization()
	if !opt.GetOnline() {
		if opt.GetBatch() != nil && !lim.AllowBatch {
			return fmt.Errorf("run rejected: batch optimization disabled by server")
		}
		if opt.GetMaxEvaluations() > lim.MaxEvaluations {
			return fmt.Errorf("run rejected: optimization max_evaluations %d exceeds server limit %d", opt.GetMaxEvaluations(), lim.MaxEvaluations)
		}
		if opt.GetMaxIterations() > lim.MaxIterations {
			return fmt.Errorf("run rejected: optimization max_iterations %d exceeds server limit %d", opt.GetMaxIterations(), lim.MaxIterations)
		}
		if opt.GetEvaluationDurationMs() > 0 && opt.GetEvaluationDurationMs() > int64(lim.CandidateMaxWallClock/time.Millisecond) {
			return fmt.Errorf("run rejected: evaluation_duration_ms %d exceeds server candidate runtime limit %d", opt.GetEvaluationDurationMs(), int64(lim.CandidateMaxWallClock/time.Millisecond))
		}
	}
	obj := strings.TrimSpace(strings.ToLower(opt.GetObjective()))
	if obj != "" {
		switch obj {
		case "p95_latency_ms", "p99_latency_ms", "mean_latency_ms", "throughput_rps", "error_rate", "cost", "cpu_utilization", "memory_utilization":
		default:
			return fmt.Errorf("run rejected: unsupported optimization objective %q", obj)
		}
	}
	return nil
}

func getenvInt32(key string) int32 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return 0
	}
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return 0
	}
	return int32(n)
}
func getenvInt(key string) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return n
}
func getenvInt64(key string) int64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return 0
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
func getenvDuration(key string) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0
	}
	return d
}
func getenvBool(key string) (value bool, ok bool) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return false, false
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, false
	}
	return v, true
}
