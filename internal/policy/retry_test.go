package policy

import (
	"errors"
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestNewRetryPolicyFromConfig(t *testing.T) {
	cfg := &config.RetryPolicy{
		Enabled:    true,
		MaxRetries: 3,
		Backoff:    "exponential",
		BaseMs:     10,
	}
	policy := NewRetryPolicyFromConfig(cfg)
	if policy == nil {
		t.Fatalf("expected policy to be created")
	}
	if !policy.Enabled() {
		t.Fatalf("expected policy to be enabled")
	}
	if policy.Name() != "retry" {
		t.Fatalf("expected name to be 'retry', got %s", policy.Name())
	}
	if policy.GetMaxRetries() != 3 {
		t.Fatalf("expected max retries 3, got %d", policy.GetMaxRetries())
	}
}

func TestRetryPolicyShouldRetry(t *testing.T) {
	policy := NewRetryPolicy(true, 3, "exponential", 10)

	// Should retry on error within max retries
	if !policy.ShouldRetry(0, errors.New("test error")) {
		t.Fatalf("expected should retry on first attempt with error")
	}

	if !policy.ShouldRetry(1, errors.New("test error")) {
		t.Fatalf("expected should retry on second attempt with error")
	}

	if !policy.ShouldRetry(2, errors.New("test error")) {
		t.Fatalf("expected should retry on third attempt with error")
	}

	// Should not retry when at max retries
	if policy.ShouldRetry(3, errors.New("test error")) {
		t.Fatalf("expected should not retry when at max retries")
	}

	// Should not retry when no error
	if policy.ShouldRetry(0, nil) {
		t.Fatalf("expected should not retry when no error")
	}

	// Should not retry when disabled
	disabledPolicy := NewRetryPolicy(false, 3, "exponential", 10)
	if disabledPolicy.ShouldRetry(0, errors.New("test error")) {
		t.Fatalf("expected should not retry when disabled")
	}
}

func TestRetryPolicyGetBackoffDurationExponential(t *testing.T) {
	policy := NewRetryPolicy(true, 3, "exponential", 10)

	// Exponential: baseMs * 2^(attempt-1)
	duration := policy.GetBackoffDuration(1)
	expected := 10 * time.Millisecond // 10 * 2^0
	if duration != expected {
		t.Fatalf("expected duration %v, got %v", expected, duration)
	}

	duration = policy.GetBackoffDuration(2)
	expected = 20 * time.Millisecond // 10 * 2^1
	if duration != expected {
		t.Fatalf("expected duration %v, got %v", expected, duration)
	}

	duration = policy.GetBackoffDuration(3)
	expected = 40 * time.Millisecond // 10 * 2^2
	if duration != expected {
		t.Fatalf("expected duration %v, got %v", expected, duration)
	}

	// Zero or negative attempt should return 0
	duration = policy.GetBackoffDuration(0)
	if duration != 0 {
		t.Fatalf("expected duration 0 for attempt 0, got %v", duration)
	}

	duration = policy.GetBackoffDuration(-1)
	if duration != 0 {
		t.Fatalf("expected duration 0 for negative attempt, got %v", duration)
	}
}

func TestRetryPolicyGetBackoffDurationLinear(t *testing.T) {
	policy := NewRetryPolicy(true, 3, "linear", 10)

	// Linear: baseMs * attempt
	duration := policy.GetBackoffDuration(1)
	expected := 10 * time.Millisecond // 10 * 1
	if duration != expected {
		t.Fatalf("expected duration %v, got %v", expected, duration)
	}

	duration = policy.GetBackoffDuration(2)
	expected = 20 * time.Millisecond // 10 * 2
	if duration != expected {
		t.Fatalf("expected duration %v, got %v", expected, duration)
	}

	duration = policy.GetBackoffDuration(3)
	expected = 30 * time.Millisecond // 10 * 3
	if duration != expected {
		t.Fatalf("expected duration %v, got %v", expected, duration)
	}
}

func TestRetryPolicyGetBackoffDurationConstant(t *testing.T) {
	policy := NewRetryPolicy(true, 3, "constant", 10)

	// Constant: always baseMs
	duration := policy.GetBackoffDuration(1)
	expected := 10 * time.Millisecond
	if duration != expected {
		t.Fatalf("expected duration %v, got %v", expected, duration)
	}

	duration = policy.GetBackoffDuration(2)
	if duration != expected {
		t.Fatalf("expected duration %v, got %v", expected, duration)
	}

	duration = policy.GetBackoffDuration(3)
	if duration != expected {
		t.Fatalf("expected duration %v, got %v", expected, duration)
	}
}

func TestRetryPolicyGetBackoffDurationDefault(t *testing.T) {
	// Unknown backoff type should default to exponential
	policy := NewRetryPolicy(true, 3, "unknown", 10)

	duration := policy.GetBackoffDuration(1)
	expected := 10 * time.Millisecond // Should use exponential
	if duration != expected {
		t.Fatalf("expected duration %v (exponential default), got %v", expected, duration)
	}
}

func TestRetryPolicyWhenDisabled(t *testing.T) {
	policy := NewRetryPolicy(false, 3, "exponential", 10)

	duration := policy.GetBackoffDuration(1)
	if duration != 0 {
		t.Fatalf("expected duration 0 when disabled, got %v", duration)
	}
}
