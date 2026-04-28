package improvement

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// OptimizationSafetyConfig bounds optimizer runtime behavior.
type OptimizationSafetyConfig struct {
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

func DefaultOptimizationSafetyConfig() OptimizationSafetyConfig {
	return OptimizationSafetyConfig{
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

func OptimizationSafetyConfigFromEnv() OptimizationSafetyConfig {
	cfg := DefaultOptimizationSafetyConfig()
	if v := parseInt32Env("SIMD_OPTIMIZATION_MAX_EVALUATIONS"); v > 0 {
		cfg.MaxEvaluations = v
	}
	if v := parseInt32Env("SIMD_OPTIMIZATION_MAX_ITERATIONS"); v > 0 {
		cfg.MaxIterations = v
	}
	if v := parseIntEnv("SIMD_OPTIMIZATION_MAX_CONCURRENT_CANDIDATES"); v > 0 {
		cfg.MaxConcurrentCandidates = v
	}
	if v := parseDurationEnv("SIMD_OPTIMIZATION_MAX_WALL_CLOCK_RUNTIME"); v > 0 {
		cfg.MaxWallClockRuntime = v
	}
	if v := parseDurationEnv("SIMD_OPTIMIZATION_CANDIDATE_MAX_WALL_CLOCK_RUNTIME"); v > 0 {
		cfg.CandidateMaxWallClock = v
	}
	if v := parseIntEnv("SIMD_OPTIMIZATION_MAX_FAILED_CANDIDATES"); v > 0 {
		cfg.MaxFailedCandidates = v
	}
	if v := parseIntEnv("SIMD_OPTIMIZATION_MAX_RETAINED_CANDIDATES"); v > 0 {
		cfg.MaxRetainedCandidates = v
	}
	if v := parseInt64Env("SIMD_OPTIMIZATION_DEFAULT_EVALUATION_DURATION"); v > 0 {
		cfg.DefaultEvaluationDuration = v
	}
	if v, ok := parseBoolEnv("SIMD_OPTIMIZATION_ALLOW_BATCH"); ok {
		cfg.AllowBatch = v
	}
	return cfg
}

func parseInt32Env(key string) int32 {
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

func parseIntEnv(key string) int {
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

func parseInt64Env(key string) int64 {
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

func parseDurationEnv(key string) time.Duration {
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

func parseBoolEnv(key string) (value bool, ok bool) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return false, false
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		return false, false
	}
	return b, true
}
