package policy

import (
	"sync"
	"time"
)

// circuitBreakerPolicy implements CircuitBreakerPolicy
type circuitBreakerPolicy struct {
	enabled bool
	// failureThreshold is the number of failures before opening the circuit
	failureThreshold int
	// successThreshold is the number of successes needed in half-open state to close
	successThreshold int
	// timeout is how long the circuit stays open before transitioning to half-open
	timeout time.Duration
	// circuits tracks circuit state per service/endpoint
	circuits map[string]*circuitState
	mu       sync.RWMutex
}

// circuitState tracks the state of a circuit breaker for a service/endpoint
type circuitState struct {
	state           CircuitState
	failureCount    int
	successCount    int
	lastFailureTime time.Time
	lastStateChange time.Time
	mu              sync.Mutex
}

// NewCircuitBreakerPolicy creates a new circuit breaker policy
func NewCircuitBreakerPolicy(enabled bool, failureThreshold, successThreshold int, timeout time.Duration) CircuitBreakerPolicy {
	return &circuitBreakerPolicy{
		enabled:          enabled,
		failureThreshold: failureThreshold,
		successThreshold: successThreshold,
		timeout:          timeout,
		circuits:         make(map[string]*circuitState),
	}
}

func (p *circuitBreakerPolicy) Enabled() bool {
	return p.enabled
}

func (p *circuitBreakerPolicy) Name() string {
	return "circuit_breaker"
}

func (p *circuitBreakerPolicy) AllowRequest(serviceID, endpointPath string) bool {
	if !p.enabled {
		return true
	}

	key := serviceID + ":" + endpointPath
	p.mu.RLock()
	circuit, exists := p.circuits[key]
	p.mu.RUnlock()

	if !exists {
		p.mu.Lock()
		if circuit, exists = p.circuits[key]; !exists {
			circuit = &circuitState{
				state:           CircuitStateClosed,
				lastStateChange: time.Now(),
			}
			p.circuits[key] = circuit
		}
		p.mu.Unlock()
	}

	circuit.mu.Lock()
	defer circuit.mu.Unlock()

	// Check if we should transition from open to half-open
	if circuit.state == CircuitStateOpen {
		if time.Since(circuit.lastStateChange) >= p.timeout {
			circuit.state = CircuitStateHalfOpen
			circuit.successCount = 0
			circuit.lastStateChange = time.Now()
		} else {
			return false // Circuit is open, reject request
		}
	}

	return true // Circuit is closed or half-open, allow request
}

func (p *circuitBreakerPolicy) RecordSuccess(serviceID, endpointPath string) {
	if !p.enabled {
		return
	}

	key := serviceID + ":" + endpointPath
	p.mu.RLock()
	circuit, exists := p.circuits[key]
	p.mu.RUnlock()

	if !exists {
		return
	}

	circuit.mu.Lock()
	defer circuit.mu.Unlock()

	if circuit.state == CircuitStateHalfOpen {
		circuit.successCount++
		if circuit.successCount >= p.successThreshold {
			circuit.state = CircuitStateClosed
			circuit.failureCount = 0
			circuit.lastStateChange = time.Now()
		}
	} else if circuit.state == CircuitStateClosed {
		// Reset failure count on success
		circuit.failureCount = 0
	}
}

func (p *circuitBreakerPolicy) RecordFailure(serviceID, endpointPath string) {
	if !p.enabled {
		return
	}

	key := serviceID + ":" + endpointPath
	p.mu.RLock()
	circuit, exists := p.circuits[key]
	p.mu.RUnlock()

	if !exists {
		p.mu.Lock()
		if circuit, exists = p.circuits[key]; !exists {
			circuit = &circuitState{
				state:           CircuitStateClosed,
				lastStateChange: time.Now(),
			}
			p.circuits[key] = circuit
		}
		p.mu.Unlock()
	}

	circuit.mu.Lock()
	defer circuit.mu.Unlock()

	circuit.failureCount++
	circuit.lastFailureTime = time.Now()

	if circuit.state == CircuitStateHalfOpen {
		// Any failure in half-open state immediately opens the circuit
		circuit.state = CircuitStateOpen
		circuit.successCount = 0
		circuit.lastStateChange = time.Now()
	} else if circuit.state == CircuitStateClosed {
		// Check if we've exceeded the failure threshold
		if circuit.failureCount >= p.failureThreshold {
			circuit.state = CircuitStateOpen
			circuit.lastStateChange = time.Now()
		}
	}
}

func (p *circuitBreakerPolicy) GetState(serviceID, endpointPath string) CircuitState {
	if !p.enabled {
		return CircuitStateClosed
	}

	key := serviceID + ":" + endpointPath
	p.mu.RLock()
	circuit, exists := p.circuits[key]
	p.mu.RUnlock()

	if !exists {
		return CircuitStateClosed
	}

	circuit.mu.Lock()
	defer circuit.mu.Unlock()

	// Check if we should transition from open to half-open
	if circuit.state == CircuitStateOpen {
		if time.Since(circuit.lastStateChange) >= p.timeout {
			circuit.state = CircuitStateHalfOpen
			circuit.successCount = 0
			circuit.lastStateChange = time.Now()
		}
	}

	return circuit.state
}
