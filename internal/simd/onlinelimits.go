package simd

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
)

// ErrInvalidOnlineRunInput is returned when online optimization inputs fail server validation.
var ErrInvalidOnlineRunInput = errors.New("invalid online run input")

// Well-known values for Run.online_completion_reason when status is COMPLETED.
const (
	OnlineCompletionDurationLimit    = "duration_limit"
	OnlineCompletionControllerSteps  = "controller_steps_limit"
	OnlineCompletionConverged        = "converged"
	OnlineCompletionHeartbeatExpired = "heartbeat_expired"
)

// OnlineRunLimits are server-enforced caps and defaults for online optimization runs.
type OnlineRunLimits struct {
	ServerMaxOnlineDuration        time.Duration // hard cap on requested wall duration; 0 disables cap
	ServerMaxControllerSteps       int32         // 0 = no server cap on controller steps
	MaxConcurrentOnlineRuns        int           // 0 = unlimited
	DefaultMaxOnlineDuration       time.Duration // applied when client omits max_online_duration_ms
	HeartbeatRequiredAfterDuration time.Duration // lease_ttl_ms required when duration exceeds this
}

// DefaultOnlineRunLimits returns the built-in policy (10m default session, 30m server cap, 10m lease threshold).
func DefaultOnlineRunLimits() OnlineRunLimits {
	return OnlineRunLimits{
		ServerMaxOnlineDuration:        30 * time.Minute,
		ServerMaxControllerSteps:       0,
		MaxConcurrentOnlineRuns:        0,
		DefaultMaxOnlineDuration:       10 * time.Minute,
		HeartbeatRequiredAfterDuration: 10 * time.Minute,
	}
}

func parseDurationMsEnv(key string, def time.Duration) time.Duration {
	s := os.Getenv(key)
	if s == "" {
		return def
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return def
	}
	return time.Duration(n) * time.Millisecond
}

func parseIntEnv(key string, def int) int {
	s := os.Getenv(key)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return def
	}
	return n
}

func parseInt32Env(key string, def int32) int32 {
	s := os.Getenv(key)
	if s == "" {
		return def
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return def
	}
	if n > int64(^uint32(0)>>1) {
		return def
	}
	return int32(n)
}

// OnlineRunLimitsFromEnv reads SIMD_SERVER_* and related env vars; unset keys use DefaultOnlineRunLimits().
func OnlineRunLimitsFromEnv() OnlineRunLimits {
	d := DefaultOnlineRunLimits()
	d.ServerMaxOnlineDuration = parseDurationMsEnv("SIMD_SERVER_MAX_ONLINE_DURATION_MS", d.ServerMaxOnlineDuration)
	d.DefaultMaxOnlineDuration = parseDurationMsEnv("SIMD_DEFAULT_MAX_ONLINE_DURATION_MS", d.DefaultMaxOnlineDuration)
	d.HeartbeatRequiredAfterDuration = parseDurationMsEnv("SIMD_HEARTBEAT_REQUIRED_AFTER_MS", d.HeartbeatRequiredAfterDuration)
	d.ServerMaxControllerSteps = parseInt32Env("SIMD_SERVER_MAX_CONTROLLER_STEPS", d.ServerMaxControllerSteps)
	d.MaxConcurrentOnlineRuns = parseIntEnv("SIMD_SERVER_MAX_CONCURRENT_ONLINE_RUNS", d.MaxConcurrentOnlineRuns)
	return d
}

// PrepareOnlineRunInput applies server defaults and caps to OptimizationConfig for online runs.
// It mutates input.Optimization in place (input must be a dedicated clone per Create).
func PrepareOnlineRunInput(input *simulationv1.RunInput, limits OnlineRunLimits) error {
	if input == nil || input.Optimization == nil || !input.Optimization.Online {
		return nil
	}
	opt := input.Optimization

	intervalMs := opt.ControlIntervalMs
	if intervalMs <= 0 {
		intervalMs = 1000
		opt.ControlIntervalMs = intervalMs
	}

	allowUnbounded := opt.GetAllowUnboundedOnline()
	maxDurMs := opt.GetMaxOnlineDurationMs()
	maxSteps := opt.GetMaxControllerSteps()

	if !allowUnbounded && maxDurMs == 0 {
		maxDurMs = limits.DefaultMaxOnlineDuration.Milliseconds()
		opt.MaxOnlineDurationMs = maxDurMs
	}

	if limits.ServerMaxOnlineDuration > 0 && maxDurMs > 0 {
		capMs := limits.ServerMaxOnlineDuration.Milliseconds()
		if maxDurMs > capMs {
			opt.MaxOnlineDurationMs = capMs
			maxDurMs = capMs
		}
	}

	if maxDurMs > limits.HeartbeatRequiredAfterDuration.Milliseconds() && !allowUnbounded && opt.GetLeaseTtlMs() <= 0 {
		return fmt.Errorf("%w: lease_ttl_ms is required when max_online_duration_ms exceeds %d ms (server heartbeat threshold)",
			ErrInvalidOnlineRunInput, limits.HeartbeatRequiredAfterDuration.Milliseconds())
	}

	if maxSteps == 0 && maxDurMs > 0 {
		steps := (maxDurMs + intervalMs - 1) / intervalMs
		if steps < 1 {
			steps = 1
		}
		if steps > math.MaxInt32 {
			steps = math.MaxInt32
		}
		opt.MaxControllerSteps = int32(steps) //nolint:gosec // clamped to MaxInt32
	}

	if limits.ServerMaxControllerSteps > 0 && opt.MaxControllerSteps > limits.ServerMaxControllerSteps {
		opt.MaxControllerSteps = limits.ServerMaxControllerSteps
	}

	noop := opt.GetMaxNoopIntervals()
	if noop == 0 {
		if allowUnbounded && maxDurMs == 0 {
			opt.MaxNoopIntervals = -1
		} else {
			const minWallSec int64 = 60
			ticks := (minWallSec*1000 + intervalMs - 1) / intervalMs
			if ticks < 20 {
				ticks = 20
			}
			if ticks > math.MaxInt32 {
				ticks = math.MaxInt32
			}
			opt.MaxNoopIntervals = int32(ticks) //nolint:gosec // clamped to MaxInt32
		}
	}

	return nil
}
