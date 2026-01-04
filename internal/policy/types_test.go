package policy

import (
	"testing"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestNewPolicyManager(t *testing.T) {
	// Test with nil policies
	pm := NewPolicyManager(nil)
	if pm == nil {
		t.Fatalf("expected PolicyManager to be created")
	}
	if pm.GetAutoscaling() != nil {
		t.Fatalf("expected no autoscaling policy when nil")
	}
	if pm.GetRetry() != nil {
		t.Fatalf("expected no retry policy when nil")
	}

	// Test with empty policies
	pm = NewPolicyManager(&config.Policies{})
	if pm == nil {
		t.Fatalf("expected PolicyManager to be created")
	}

	// Test with autoscaling enabled
	policies := &config.Policies{
		Autoscaling: &config.AutoscalingPolicy{
			Enabled:       true,
			TargetCPUUtil: 0.7,
			ScaleStep:     1,
		},
	}
	pm = NewPolicyManager(policies)
	if pm.GetAutoscaling() == nil {
		t.Fatalf("expected autoscaling policy to be created")
	}
	if !pm.GetAutoscaling().Enabled() {
		t.Fatalf("expected autoscaling to be enabled")
	}

	// Test with retry enabled
	policies = &config.Policies{
		Retries: &config.RetryPolicy{
			Enabled:    true,
			MaxRetries: 3,
			Backoff:    "exponential",
			BaseMs:     10,
		},
	}
	pm = NewPolicyManager(policies)
	if pm.GetRetry() == nil {
		t.Fatalf("expected retry policy to be created")
	}
	if !pm.GetRetry().Enabled() {
		t.Fatalf("expected retry to be enabled")
	}

	// Test with both enabled
	policies = &config.Policies{
		Autoscaling: &config.AutoscalingPolicy{
			Enabled:       true,
			TargetCPUUtil: 0.7,
			ScaleStep:     1,
		},
		Retries: &config.RetryPolicy{
			Enabled:    true,
			MaxRetries: 3,
			Backoff:    "exponential",
			BaseMs:     10,
		},
	}
	pm = NewPolicyManager(policies)
	if pm.GetAutoscaling() == nil {
		t.Fatalf("expected autoscaling policy to be created")
	}
	if pm.GetRetry() == nil {
		t.Fatalf("expected retry policy to be created")
	}
}

func TestNewPolicyManagerWithDisabledPolicies(t *testing.T) {
	policies := &config.Policies{
		Autoscaling: &config.AutoscalingPolicy{
			Enabled:       false,
			TargetCPUUtil: 0.7,
			ScaleStep:     1,
		},
		Retries: &config.RetryPolicy{
			Enabled:    false,
			MaxRetries: 3,
			Backoff:    "exponential",
			BaseMs:     10,
		},
	}
	pm := NewPolicyManager(policies)
	if pm.GetAutoscaling() != nil {
		t.Fatalf("expected no autoscaling policy when disabled")
	}
	if pm.GetRetry() != nil {
		t.Fatalf("expected no retry policy when disabled")
	}
}
