package utils

import (
	"math"
	"time"
)

// BackoffStrategy represents a retry backoff strategy
type BackoffStrategy interface {
	// NextDelay returns the delay for the given attempt number (0-indexed)
	NextDelay(attempt int) time.Duration
}

// ConstantBackoff implements a constant backoff strategy
type ConstantBackoff struct {
	Delay time.Duration
}

// NewConstantBackoff creates a new constant backoff strategy
func NewConstantBackoff(delay time.Duration) *ConstantBackoff {
	return &ConstantBackoff{Delay: delay}
}

// NextDelay returns the constant delay
func (cb *ConstantBackoff) NextDelay(attempt int) time.Duration {
	return cb.Delay
}

// LinearBackoff implements a linear backoff strategy
type LinearBackoff struct {
	BaseDelay time.Duration
	MaxDelay  time.Duration
}

// NewLinearBackoff creates a new linear backoff strategy
func NewLinearBackoff(baseDelay, maxDelay time.Duration) *LinearBackoff {
	return &LinearBackoff{
		BaseDelay: baseDelay,
		MaxDelay:  maxDelay,
	}
}

// NextDelay returns the linearly increasing delay
func (lb *LinearBackoff) NextDelay(attempt int) time.Duration {
	delay := lb.BaseDelay * time.Duration(attempt+1)
	if delay > lb.MaxDelay {
		return lb.MaxDelay
	}
	return delay
}

// ExponentialBackoff implements an exponential backoff strategy
type ExponentialBackoff struct {
	BaseDelay  time.Duration
	Multiplier float64
	MaxDelay   time.Duration
	Jitter     bool
}

// NewExponentialBackoff creates a new exponential backoff strategy
func NewExponentialBackoff(baseDelay, maxDelay time.Duration, multiplier float64, jitter bool) *ExponentialBackoff {
	if multiplier <= 0 {
		multiplier = 2.0
	}
	return &ExponentialBackoff{
		BaseDelay:  baseDelay,
		Multiplier: multiplier,
		MaxDelay:   maxDelay,
		Jitter:     jitter,
	}
}

// NextDelay returns the exponentially increasing delay
func (eb *ExponentialBackoff) NextDelay(attempt int) time.Duration {
	delay := float64(eb.BaseDelay) * math.Pow(eb.Multiplier, float64(attempt))

	if delay > float64(eb.MaxDelay) {
		delay = float64(eb.MaxDelay)
	}

	if eb.Jitter {
		// Add jitter: random value between 0.5*delay and 1.5*delay
		jitterFactor := 0.5 + Float64()
		delay *= jitterFactor
	}

	return time.Duration(delay)
}

// BackoffFromConfig creates a backoff strategy from config parameters
func BackoffFromConfig(backoffType string, baseMs int, maxMs int) BackoffStrategy {
	baseDelay := time.Duration(baseMs) * time.Millisecond
	maxDelay := time.Duration(maxMs) * time.Millisecond

	if maxDelay == 0 {
		maxDelay = 30 * time.Second
	}

	switch backoffType {
	case "constant":
		return NewConstantBackoff(baseDelay)
	case "linear":
		return NewLinearBackoff(baseDelay, maxDelay)
	case "exponential":
		return NewExponentialBackoff(baseDelay, maxDelay, 2.0, true)
	default:
		// Default to exponential with jitter
		return NewExponentialBackoff(baseDelay, maxDelay, 2.0, true)
	}
}
