package policy

import (
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

// Policy represents a generic policy interface
type Policy interface {
	// Enabled returns whether the policy is enabled
	Enabled() bool
	// Name returns the policy name for identification
	Name() string
}

// AutoscalingPolicy handles automatic scaling of service instances
type AutoscalingPolicy interface {
	Policy
	// ShouldScaleUp determines if a service should scale up based on current metrics
	ShouldScaleUp(serviceID string, currentReplicas int, avgCPUUtil float64) bool
	// ShouldScaleDown determines if a service should scale down based on current metrics
	ShouldScaleDown(serviceID string, currentReplicas int, avgCPUUtil float64) bool
	// GetTargetReplicas calculates the target number of replicas
	GetTargetReplicas(serviceID string, currentReplicas int, avgCPUUtil float64) int
}

// RateLimitingPolicy handles rate limiting for requests
type RateLimitingPolicy interface {
	Policy
	// AllowRequest checks if a request should be allowed based on rate limits
	AllowRequest(serviceID, endpointPath string, requestTime time.Time) bool
	// GetRemainingQuota returns the remaining quota for a service/endpoint
	GetRemainingQuota(serviceID, endpointPath string) int
}

// RetryPolicy handles retry logic for failed requests
type RetryPolicy interface {
	Policy
	// ShouldRetry determines if a request should be retried
	ShouldRetry(attempt int, err error) bool
	// GetBackoffDuration calculates the backoff duration for a retry attempt
	GetBackoffDuration(attempt int) time.Duration
	// GetMaxRetries returns the maximum number of retries allowed
	GetMaxRetries() int
}

// CircuitBreakerPolicy handles circuit breaker logic
type CircuitBreakerPolicy interface {
	Policy
	// AllowRequest checks if a request should be allowed (circuit not open)
	AllowRequest(serviceID, endpointPath string) bool
	// RecordSuccess records a successful request
	RecordSuccess(serviceID, endpointPath string)
	// RecordFailure records a failed request
	RecordFailure(serviceID, endpointPath string)
	// GetState returns the current circuit breaker state
	GetState(serviceID, endpointPath string) CircuitState
}

// CircuitState represents the state of a circuit breaker
type CircuitState string

const (
	CircuitStateClosed   CircuitState = "closed"   // Normal operation
	CircuitStateOpen     CircuitState = "open"     // Failing, rejecting requests
	CircuitStateHalfOpen CircuitState = "halfopen" // Testing if service recovered
)

// Manager manages all active policies
type Manager struct {
	autoscaling    AutoscalingPolicy
	rateLimiting   RateLimitingPolicy
	retry          RetryPolicy
	circuitBreaker CircuitBreakerPolicy
}

// NewPolicyManager creates a new policy manager from configuration
func NewPolicyManager(policies *config.Policies) *Manager {
	pm := &Manager{}

	if policies != nil {
		if policies.Autoscaling != nil && policies.Autoscaling.Enabled {
			pm.autoscaling = NewAutoscalingPolicyFromConfig(policies.Autoscaling)
		}
		if policies.Retries != nil && policies.Retries.Enabled {
			pm.retry = NewRetryPolicyFromConfig(policies.Retries)
		}
		// Rate limiting and circuit breaker will be added when config types are extended
	}

	return pm
}

// GetAutoscaling returns the autoscaling policy if enabled
func (pm *Manager) GetAutoscaling() AutoscalingPolicy {
	return pm.autoscaling
}

// GetRateLimiting returns the rate limiting policy if enabled
func (pm *Manager) GetRateLimiting() RateLimitingPolicy {
	return pm.rateLimiting
}

// GetRetry returns the retry policy if enabled
func (pm *Manager) GetRetry() RetryPolicy {
	return pm.retry
}

// GetCircuitBreaker returns the circuit breaker policy if enabled
func (pm *Manager) GetCircuitBreaker() CircuitBreakerPolicy {
	return pm.circuitBreaker
}
