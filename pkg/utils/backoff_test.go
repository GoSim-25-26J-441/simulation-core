package utils

import (
	"testing"
	"time"
)

func TestConstantBackoff(t *testing.T) {
	delay := 100 * time.Millisecond
	backoff := NewConstantBackoff(delay)

	for i := 0; i < 10; i++ {
		nextDelay := backoff.NextDelay(i)
		if nextDelay != delay {
			t.Errorf("Attempt %d: expected %v, got %v", i, delay, nextDelay)
		}
	}
}

func TestLinearBackoff(t *testing.T) {
	baseDelay := 100 * time.Millisecond
	maxDelay := 1 * time.Second
	backoff := NewLinearBackoff(baseDelay, maxDelay)

	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{0, 100 * time.Millisecond},
		{1, 200 * time.Millisecond},
		{2, 300 * time.Millisecond},
		{3, 400 * time.Millisecond},
		{9, 1000 * time.Millisecond},  // at max
		{10, 1000 * time.Millisecond}, // capped at max
		{20, 1000 * time.Millisecond}, // capped at max
	}

	for _, tt := range tests {
		delay := backoff.NextDelay(tt.attempt)
		if delay != tt.expected {
			t.Errorf("Attempt %d: expected %v, got %v", tt.attempt, tt.expected, delay)
		}
	}
}

func TestExponentialBackoff(t *testing.T) {
	baseDelay := 100 * time.Millisecond
	maxDelay := 10 * time.Second
	multiplier := 2.0
	backoff := NewExponentialBackoff(baseDelay, maxDelay, multiplier, false)

	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{0, 100 * time.Millisecond},  // 100 * 2^0
		{1, 200 * time.Millisecond},  // 100 * 2^1
		{2, 400 * time.Millisecond},  // 100 * 2^2
		{3, 800 * time.Millisecond},  // 100 * 2^3
		{4, 1600 * time.Millisecond}, // 100 * 2^4
		{10, 10 * time.Second},       // capped at max
	}

	for _, tt := range tests {
		delay := backoff.NextDelay(tt.attempt)
		if delay != tt.expected {
			t.Errorf("Attempt %d: expected %v, got %v", tt.attempt, tt.expected, delay)
		}
	}
}

func TestExponentialBackoffWithJitter(t *testing.T) {
	baseDelay := 100 * time.Millisecond
	maxDelay := 10 * time.Second
	multiplier := 2.0
	backoff := NewExponentialBackoff(baseDelay, maxDelay, multiplier, true)

	// With jitter, we can't test exact values, but we can verify ranges
	for attempt := 0; attempt < 5; attempt++ {
		delay := backoff.NextDelay(attempt)

		// Calculate expected base delay without jitter
		expectedBase := float64(baseDelay) * float64(uint(1)<<uint(attempt)) // 2^attempt
		if expectedBase > float64(maxDelay) {
			expectedBase = float64(maxDelay)
		}

		// With jitter factor between 0.5 and 1.5
		minExpected := time.Duration(expectedBase * 0.5)
		maxExpected := time.Duration(expectedBase * 1.5)

		if delay < minExpected || delay > maxExpected {
			t.Errorf("Attempt %d: delay %v outside expected range [%v, %v]",
				attempt, delay, minExpected, maxExpected)
		}
	}
}

func TestExponentialBackoffDefaultMultiplier(t *testing.T) {
	baseDelay := 100 * time.Millisecond
	maxDelay := 10 * time.Second
	backoff := NewExponentialBackoff(baseDelay, maxDelay, 0, false) // 0 should default to 2.0

	delay1 := backoff.NextDelay(1)
	expected := 200 * time.Millisecond

	if delay1 != expected {
		t.Errorf("With default multiplier, attempt 1 should give %v, got %v", expected, delay1)
	}
}

func TestBackoffFromConfig(t *testing.T) {
	tests := []struct {
		name        string
		backoffType string
		baseMs      int
		maxMs       int
		attempt     int
		checkFunc   func(time.Duration) bool
	}{
		{
			name:        "Constant backoff",
			backoffType: "constant",
			baseMs:      100,
			maxMs:       1000,
			attempt:     5,
			checkFunc:   func(d time.Duration) bool { return d == 100*time.Millisecond },
		},
		{
			name:        "Linear backoff",
			backoffType: "linear",
			baseMs:      100,
			maxMs:       1000,
			attempt:     2,
			checkFunc:   func(d time.Duration) bool { return d == 300*time.Millisecond },
		},
		{
			name:        "Exponential backoff",
			backoffType: "exponential",
			baseMs:      100,
			maxMs:       10000,
			attempt:     0,
			checkFunc: func(d time.Duration) bool {
				// With jitter, should be between 50ms and 150ms
				return d >= 50*time.Millisecond && d <= 150*time.Millisecond
			},
		},
		{
			name:        "Default (unknown type)",
			backoffType: "unknown",
			baseMs:      100,
			maxMs:       10000,
			attempt:     0,
			checkFunc: func(d time.Duration) bool {
				// Should default to exponential with jitter
				return d >= 50*time.Millisecond && d <= 150*time.Millisecond
			},
		},
		{
			name:        "Zero maxMs defaults to 30s",
			backoffType: "constant",
			baseMs:      100,
			maxMs:       0,
			attempt:     0,
			checkFunc:   func(d time.Duration) bool { return d == 100*time.Millisecond },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backoff := BackoffFromConfig(tt.backoffType, tt.baseMs, tt.maxMs)
			if backoff == nil {
				t.Fatal("BackoffFromConfig returned nil")
			}

			delay := backoff.NextDelay(tt.attempt)
			if !tt.checkFunc(delay) {
				t.Errorf("Delay %v failed check function", delay)
			}
		})
	}
}

func TestBackoffProgression(t *testing.T) {
	baseDelay := 10 * time.Millisecond
	maxDelay := 1 * time.Second
	backoff := NewExponentialBackoff(baseDelay, maxDelay, 2.0, false)

	var lastDelay time.Duration
	for i := 0; i < 10; i++ {
		delay := backoff.NextDelay(i)

		// Delays should be non-decreasing (allowing for max cap)
		if i > 0 && delay < lastDelay {
			t.Errorf("Attempt %d: delay %v less than previous %v", i, delay, lastDelay)
		}

		// Should not exceed max
		if delay > maxDelay {
			t.Errorf("Attempt %d: delay %v exceeds max %v", i, delay, maxDelay)
		}

		lastDelay = delay
	}
}
