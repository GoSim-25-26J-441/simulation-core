package policy

import (
	"testing"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestNewAutoscalingPolicyFromConfig(t *testing.T) {
	cfg := &config.AutoscalingPolicy{
		Enabled:       true,
		TargetCPUUtil: 0.7,
		ScaleStep:     2,
	}
	policy := NewAutoscalingPolicyFromConfig(cfg)
	if policy == nil {
		t.Fatalf("expected policy to be created")
	}
	if !policy.Enabled() {
		t.Fatalf("expected policy to be enabled")
	}
	if policy.Name() != "autoscaling" {
		t.Fatalf("expected name to be 'autoscaling', got %s", policy.Name())
	}
}

func TestAutoscalingPolicyShouldScaleUp(t *testing.T) {
	policy := NewAutoscalingPolicy(true, 0.7, 1, 1, 10)

	// Should scale up when CPU is above target
	if !policy.ShouldScaleUp("svc1", 2, 0.8) {
		t.Fatalf("expected should scale up when CPU > target")
	}

	// Should not scale up when CPU is below target
	if policy.ShouldScaleUp("svc1", 2, 0.5) {
		t.Fatalf("expected should not scale up when CPU < target")
	}

	// Should not scale up when at max replicas
	if policy.ShouldScaleUp("svc1", 10, 0.9) {
		t.Fatalf("expected should not scale up when at max replicas")
	}

	// Should not scale up when disabled
	disabledPolicy := NewAutoscalingPolicy(false, 0.7, 1, 1, 10)
	if disabledPolicy.ShouldScaleUp("svc1", 2, 0.9) {
		t.Fatalf("expected should not scale up when disabled")
	}
}

func TestAutoscalingPolicyShouldScaleDown(t *testing.T) {
	policy := NewAutoscalingPolicy(true, 0.7, 1, 1, 10)

	// Should scale down when CPU is significantly below target (0.8 * 0.7 = 0.56)
	if !policy.ShouldScaleDown("svc1", 5, 0.5) {
		t.Fatalf("expected should scale down when CPU < 0.8 * target")
	}

	// Should not scale down when CPU is above threshold
	if policy.ShouldScaleDown("svc1", 5, 0.6) {
		t.Fatalf("expected should not scale down when CPU > threshold")
	}

	// Should not scale down when at min replicas
	if policy.ShouldScaleDown("svc1", 1, 0.3) {
		t.Fatalf("expected should not scale down when at min replicas")
	}

	// Should not scale down when disabled
	disabledPolicy := NewAutoscalingPolicy(false, 0.7, 1, 1, 10)
	if disabledPolicy.ShouldScaleDown("svc1", 5, 0.3) {
		t.Fatalf("expected should not scale down when disabled")
	}
}

func TestAutoscalingPolicyGetTargetReplicas(t *testing.T) {
	policy := NewAutoscalingPolicy(true, 0.7, 2, 1, 10)

	// Scale up case
	target := policy.GetTargetReplicas("svc1", 3, 0.9)
	if target != 5 {
		t.Fatalf("expected target replicas 5 (3 + 2), got %d", target)
	}

	// Scale down case
	target = policy.GetTargetReplicas("svc1", 5, 0.4)
	if target != 3 {
		t.Fatalf("expected target replicas 3 (5 - 2), got %d", target)
	}

	// No change case (CPU at target)
	target = policy.GetTargetReplicas("svc1", 5, 0.7)
	if target != 5 {
		t.Fatalf("expected target replicas 5 (no change), got %d", target)
	}

	// Clamp to max
	target = policy.GetTargetReplicas("svc1", 9, 0.9)
	if target != 10 {
		t.Fatalf("expected target replicas 10 (clamped to max), got %d", target)
	}

	// Clamp to min
	target = policy.GetTargetReplicas("svc1", 2, 0.3)
	if target != 1 {
		t.Fatalf("expected target replicas 1 (clamped to min), got %d", target)
	}

	// When disabled, should return current
	disabledPolicy := NewAutoscalingPolicy(false, 0.7, 2, 1, 10)
	target = disabledPolicy.GetTargetReplicas("svc1", 5, 0.9)
	if target != 5 {
		t.Fatalf("expected target replicas 5 (disabled, no change), got %d", target)
	}
}

func TestAutoscalingPolicyEdgeCases(t *testing.T) {
	policy := NewAutoscalingPolicy(true, 0.7, 1, 1, 10)

	// Test with zero CPU utilization
	target := policy.GetTargetReplicas("svc1", 5, 0.0)
	if target != 4 {
		t.Fatalf("expected target replicas 4 (scale down), got %d", target)
	}

	// Test with very high CPU utilization
	target = policy.GetTargetReplicas("svc1", 5, 1.0)
	if target != 6 {
		t.Fatalf("expected target replicas 6 (scale up), got %d", target)
	}

	// Test with exactly at threshold
	target = policy.GetTargetReplicas("svc1", 5, 0.56) // 0.8 * 0.7
	if target != 5 {
		t.Fatalf("expected target replicas 5 (at threshold, no change), got %d", target)
	}
}
