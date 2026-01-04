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

	// Initially should be closed and allow requests
	if !policy.AllowRequest("svc1", "/endpoint1") {
		t.Fatalf("expected request to be allowed in closed state")
	}
	if policy.GetState("svc1", "/endpoint1") != CircuitStateClosed {
		t.Fatalf("expected state to be closed, got %s", policy.GetState("svc1", "/endpoint1"))
	}
}

func TestCircuitBreakerPolicyOpenState(t *testing.T) {
	policy := NewCircuitBreakerPolicy(true, 3, 2, 5*time.Second)

	// Record failures until threshold
	policy.RecordFailure("svc1", "/endpoint1")
	policy.RecordFailure("svc1", "/endpoint1")
	policy.RecordFailure("svc1", "/endpoint1")

	// Circuit should be open
	if policy.GetState("svc1", "/endpoint1") != CircuitStateOpen {
		t.Fatalf("expected state to be open after 3 failures, got %s", policy.GetState("svc1", "/endpoint1"))
	}

	// Requests should be rejected
	if policy.AllowRequest("svc1", "/endpoint1") {
		t.Fatalf("expected request to be rejected in open state")
	}
}

func TestCircuitBreakerPolicyHalfOpenState(t *testing.T) {
	policy := NewCircuitBreakerPolicy(true, 3, 2, 100*time.Millisecond)

	// Open the circuit
	policy.RecordFailure("svc1", "/endpoint1")
	policy.RecordFailure("svc1", "/endpoint1")
	policy.RecordFailure("svc1", "/endpoint1")

	// Wait for timeout
	time.Sleep(150 * time.Millisecond)

	// Circuit should transition to half-open
	state := policy.GetState("svc1", "/endpoint1")
	if state != CircuitStateHalfOpen {
		t.Fatalf("expected state to be half-open after timeout, got %s", state)
	}

	// Requests should be allowed in half-open state
	if !policy.AllowRequest("svc1", "/endpoint1") {
		t.Fatalf("expected request to be allowed in half-open state")
	}
}

func TestCircuitBreakerPolicyRecovery(t *testing.T) {
	policy := NewCircuitBreakerPolicy(true, 3, 2, 100*time.Millisecond)

	// Open the circuit
	policy.RecordFailure("svc1", "/endpoint1")
	policy.RecordFailure("svc1", "/endpoint1")
	policy.RecordFailure("svc1", "/endpoint1")

	// Wait for timeout to enter half-open
	time.Sleep(150 * time.Millisecond)

	// GetState will transition to half-open
	state := policy.GetState("svc1", "/endpoint1")
	if state != CircuitStateHalfOpen {
		t.Fatalf("expected state to be half-open after timeout, got %s", state)
	}

	// Record successes to close the circuit
	policy.RecordSuccess("svc1", "/endpoint1")
	policy.RecordSuccess("svc1", "/endpoint1")

	// Circuit should be closed
	if policy.GetState("svc1", "/endpoint1") != CircuitStateClosed {
		t.Fatalf("expected state to be closed after 2 successes, got %s", policy.GetState("svc1", "/endpoint1"))
	}
}

func TestCircuitBreakerPolicyFailureInHalfOpen(t *testing.T) {
	policy := NewCircuitBreakerPolicy(true, 3, 2, 100*time.Millisecond)

	// Open the circuit
	policy.RecordFailure("svc1", "/endpoint1")
	policy.RecordFailure("svc1", "/endpoint1")
	policy.RecordFailure("svc1", "/endpoint1")

	// Wait for timeout
	time.Sleep(150 * time.Millisecond)

	// GetState will transition to half-open
	state := policy.GetState("svc1", "/endpoint1")
	if state != CircuitStateHalfOpen {
		t.Fatalf("expected state to be half-open after timeout, got %s", state)
	}

	// Record a failure in half-open state
	policy.RecordFailure("svc1", "/endpoint1")

	// Circuit should immediately open again
	if policy.GetState("svc1", "/endpoint1") != CircuitStateOpen {
		t.Fatalf("expected state to be open after failure in half-open state, got %s", policy.GetState("svc1", "/endpoint1"))
	}
}

func TestCircuitBreakerPolicySuccessResetsFailureCount(t *testing.T) {
	policy := NewCircuitBreakerPolicy(true, 3, 2, 5*time.Second)

	// Record some failures
	policy.RecordFailure("svc1", "/endpoint1")
	policy.RecordFailure("svc1", "/endpoint1")

	// Record a success
	policy.RecordSuccess("svc1", "/endpoint1")

	// Failure count should be reset, so one more failure shouldn't open circuit
	policy.RecordFailure("svc1", "/endpoint1")
	if policy.GetState("svc1", "/endpoint1") != CircuitStateClosed {
		t.Fatalf("expected state to remain closed after success reset failure count")
	}
}

func TestCircuitBreakerPolicyDifferentKeys(t *testing.T) {
	policy := NewCircuitBreakerPolicy(true, 2, 1, 5*time.Second)

	// Open circuit for svc1
	policy.RecordFailure("svc1", "/endpoint1")
	policy.RecordFailure("svc1", "/endpoint1")

	// svc2 should still be closed
	if policy.GetState("svc2", "/endpoint1") != CircuitStateClosed {
		t.Fatalf("expected svc2 to be closed")
	}

	// Different endpoint for same service should be separate
	if policy.GetState("svc1", "/endpoint2") != CircuitStateClosed {
		t.Fatalf("expected different endpoint to be closed")
	}
}

func TestCircuitBreakerPolicyWhenDisabled(t *testing.T) {
	policy := NewCircuitBreakerPolicy(false, 3, 2, 5*time.Second)

	// All requests should be allowed
	if !policy.AllowRequest("svc1", "/endpoint1") {
		t.Fatalf("expected request to be allowed when disabled")
	}

	// State should always be closed
	if policy.GetState("svc1", "/endpoint1") != CircuitStateClosed {
		t.Fatalf("expected state to be closed when disabled, got %s", policy.GetState("svc1", "/endpoint1"))
	}

	// Failures should not affect state
	policy.RecordFailure("svc1", "/endpoint1")
	policy.RecordFailure("svc1", "/endpoint1")
	policy.RecordFailure("svc1", "/endpoint1")
	if policy.GetState("svc1", "/endpoint1") != CircuitStateClosed {
		t.Fatalf("expected state to remain closed when disabled")
	}
}
