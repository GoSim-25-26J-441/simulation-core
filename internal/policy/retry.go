package policy

import (
	"math"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

// retryPolicy implements RetryPolicy
type retryPolicy struct {
	enabled    bool
	maxRetries int
	backoff    string // exponential, linear, constant
	baseMs     int
}

// NewRetryPolicyFromConfig creates a retry policy from config
func NewRetryPolicyFromConfig(cfg *config.RetryPolicy) RetryPolicy {
	return &retryPolicy{
		enabled:    cfg.Enabled,
		maxRetries: cfg.MaxRetries,
		backoff:    cfg.Backoff,
		baseMs:     cfg.BaseMs,
	}
}

// NewRetryPolicy creates a retry policy with explicit parameters
func NewRetryPolicy(enabled bool, maxRetries int, backoff string, baseMs int) RetryPolicy {
	return &retryPolicy{
		enabled:    enabled,
		maxRetries: maxRetries,
		backoff:    backoff,
		baseMs:     baseMs,
	}
}

func (p *retryPolicy) Enabled() bool {
	return p.enabled
}

func (p *retryPolicy) Name() string {
	return "retry"
}

func (p *retryPolicy) ShouldRetry(attempt int, err error) bool {
	if !p.enabled {
		return false
	}
	if attempt >= p.maxRetries {
		return false
	}
	// Retry on any error (can be extended to filter specific error types)
	return err != nil
}

func (p *retryPolicy) GetBackoffDuration(attempt int) time.Duration {
	if !p.enabled || attempt <= 0 {
		return 0
	}

	var durationMs int

	switch p.backoff {
	case "exponential":
		// Exponential backoff: baseMs * 2^(attempt-1)
		durationMs = p.baseMs * int(math.Pow(2, float64(attempt-1)))
	case "linear":
		// Linear backoff: baseMs * attempt
		durationMs = p.baseMs * attempt
	case "constant":
		// Constant backoff: baseMs
		durationMs = p.baseMs
	default:
		// Default to exponential
		durationMs = p.baseMs * int(math.Pow(2, float64(attempt-1)))
	}

	return time.Duration(durationMs) * time.Millisecond
}

func (p *retryPolicy) GetMaxRetries() int {
	return p.maxRetries
}
