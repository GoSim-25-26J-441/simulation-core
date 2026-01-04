package policy

import (
	"testing"
	"time"
)

func TestNewCircuitBreakerPolicy(t *testing.T) {
	policy := NewCircuitBreakerPolicy(true, 3, 2, 5*time.Second)
	if policy == nil {
		t.Fatalf("expected policy to be created")
	}
	if !policy.Enabled() {
		t.Fatalf("expected policy to be enabled")
	}
	if policy.Name() != "circuit_breaker" {
		t.Fatalf("expected name to be 'circuit_breaker', got %s", policy.Name())
	}
}

func TestCircuitBreakerPolicyClosedState(t *testing.T) {
	policy := NewCircuitBreakerPolicy(true, 3, 2, 5*time.Second)
	now := time.Now()

	// Initially should be closed and allow requests
	if !policy.AllowRequest("svc1", "/endpoint1", now) {
		t.Fatalf("expected request to be allowed in closed state")
	}
	if policy.CheckAndGetState("svc1", "/endpoint1", now) != CircuitStateClosed {
		t.Fatalf("expected state to be closed, got %s", policy.CheckAndGetState("svc1", "/endpoint1", now))
	}
}

func TestCircuitBreakerPolicyOpenState(t *testing.T) {
	policy := NewCircuitBreakerPolicy(true, 3, 2, 5*time.Second)
	now := time.Now()

	// Record failures until threshold
	policy.RecordFailure("svc1", "/endpoint1", now)
	policy.RecordFailure("svc1", "/endpoint1", now)
	policy.RecordFailure("svc1", "/endpoint1", now)

	// Circuit should be open
	if policy.CheckAndGetState("svc1", "/endpoint1", now) != CircuitStateOpen {
		t.Fatalf("expected state to be open after 3 failures, got %s", policy.CheckAndGetState("svc1", "/endpoint1", now))
	}

	// Requests should be rejected
	if policy.AllowRequest("svc1", "/endpoint1", now) {
		t.Fatalf("expected request to be rejected in open state")
	}
}

func TestCircuitBreakerPolicyHalfOpenState(t *testing.T) {
	policy := NewCircuitBreakerPolicy(true, 3, 2, 100*time.Millisecond)
	now := time.Now()

	// Open the circuit
	policy.RecordFailure("svc1", "/endpoint1", now)
	policy.RecordFailure("svc1", "/endpoint1", now)
	policy.RecordFailure("svc1", "/endpoint1", now)

	// Simulate time passing by timeout duration
	futureTime := now.Add(150 * time.Millisecond)

	// Circuit should transition to half-open
	state := policy.CheckAndGetState("svc1", "/endpoint1", futureTime)
	if state != CircuitStateHalfOpen {
		t.Fatalf("expected state to be half-open after timeout, got %s", state)
	}

	// Requests should be allowed in half-open state
	if !policy.AllowRequest("svc1", "/endpoint1", futureTime) {
		t.Fatalf("expected request to be allowed in half-open state")
	}
}

func TestCircuitBreakerPolicyRecovery(t *testing.T) {
	policy := NewCircuitBreakerPolicy(true, 3, 2, 100*time.Millisecond)
	now := time.Now()

	// Open the circuit
	policy.RecordFailure("svc1", "/endpoint1", now)
	policy.RecordFailure("svc1", "/endpoint1", now)
	policy.RecordFailure("svc1", "/endpoint1", now)

	// Simulate time passing to enter half-open
	futureTime := now.Add(150 * time.Millisecond)

	// CheckAndGetState will transition to half-open
	state := policy.CheckAndGetState("svc1", "/endpoint1", futureTime)
	if state != CircuitStateHalfOpen {
		t.Fatalf("expected state to be half-open after timeout, got %s", state)
	}

	// Record successes to close the circuit
	policy.RecordSuccess("svc1", "/endpoint1", futureTime)
	policy.RecordSuccess("svc1", "/endpoint1", futureTime)

	// Circuit should be closed
	if policy.CheckAndGetState("svc1", "/endpoint1", futureTime) != CircuitStateClosed {
		t.Fatalf("expected state to be closed after 2 successes, got %s", policy.CheckAndGetState("svc1", "/endpoint1", futureTime))
	}
}

func TestCircuitBreakerPolicyFailureInHalfOpen(t *testing.T) {
	policy := NewCircuitBreakerPolicy(true, 3, 2, 100*time.Millisecond)
	now := time.Now()

	// Open the circuit
	policy.RecordFailure("svc1", "/endpoint1", now)
	policy.RecordFailure("svc1", "/endpoint1", now)
	policy.RecordFailure("svc1", "/endpoint1", now)

	// Simulate time passing
	futureTime := now.Add(150 * time.Millisecond)

	// CheckAndGetState will transition to half-open
	state := policy.CheckAndGetState("svc1", "/endpoint1", futureTime)
	if state != CircuitStateHalfOpen {
		t.Fatalf("expected state to be half-open after timeout, got %s", state)
	}

	// Record a failure in half-open state
	policy.RecordFailure("svc1", "/endpoint1", futureTime)

	// Circuit should immediately open again
	if policy.CheckAndGetState("svc1", "/endpoint1", futureTime) != CircuitStateOpen {
		t.Fatalf("expected state to be open after failure in half-open state, got %s", policy.CheckAndGetState("svc1", "/endpoint1", futureTime))
	}
}

func TestCircuitBreakerPolicySuccessResetsFailureCount(t *testing.T) {
	policy := NewCircuitBreakerPolicy(true, 3, 2, 5*time.Second)
	now := time.Now()

	// Record some failures
	policy.RecordFailure("svc1", "/endpoint1", now)
	policy.RecordFailure("svc1", "/endpoint1", now)

	// Record a success
	policy.RecordSuccess("svc1", "/endpoint1", now)

	// Failure count should be reset, so one more failure shouldn't open circuit
	policy.RecordFailure("svc1", "/endpoint1", now)
	if policy.CheckAndGetState("svc1", "/endpoint1", now) != CircuitStateClosed {
		t.Fatalf("expected state to remain closed after success reset failure count")
	}
}

func TestCircuitBreakerPolicyDifferentKeys(t *testing.T) {
	policy := NewCircuitBreakerPolicy(true, 2, 1, 5*time.Second)
	now := time.Now()

	// Open circuit for svc1
	policy.RecordFailure("svc1", "/endpoint1", now)
	policy.RecordFailure("svc1", "/endpoint1", now)

	// svc2 should still be closed
	if policy.CheckAndGetState("svc2", "/endpoint1", now) != CircuitStateClosed {
		t.Fatalf("expected svc2 to be closed")
	}

	// Different endpoint for same service should be separate
	if policy.CheckAndGetState("svc1", "/endpoint2", now) != CircuitStateClosed {
		t.Fatalf("expected different endpoint to be closed")
	}
}

func TestCircuitBreakerPolicyWhenDisabled(t *testing.T) {
	policy := NewCircuitBreakerPolicy(false, 3, 2, 5*time.Second)
	now := time.Now()

	// All requests should be allowed
	if !policy.AllowRequest("svc1", "/endpoint1", now) {
		t.Fatalf("expected request to be allowed when disabled")
	}

	// State should always be closed
	if policy.CheckAndGetState("svc1", "/endpoint1", now) != CircuitStateClosed {
		t.Fatalf("expected state to be closed when disabled, got %s", policy.CheckAndGetState("svc1", "/endpoint1", now))
	}

	// Failures should not affect state
	policy.RecordFailure("svc1", "/endpoint1", now)
	policy.RecordFailure("svc1", "/endpoint1", now)
	policy.RecordFailure("svc1", "/endpoint1", now)
	if policy.CheckAndGetState("svc1", "/endpoint1", now) != CircuitStateClosed {
		t.Fatalf("expected state to remain closed when disabled")
	}
}
