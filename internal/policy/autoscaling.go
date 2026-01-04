package policy

import (
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

// autoscalingPolicy implements AutoscalingPolicy
type autoscalingPolicy struct {
	enabled       bool
	targetCPUUtil float64
	scaleStep     int
	minReplicas   int
	maxReplicas   int
}

// NewAutoscalingPolicyFromConfig creates an autoscaling policy from config
func NewAutoscalingPolicyFromConfig(cfg *config.AutoscalingPolicy) AutoscalingPolicy {
	return &autoscalingPolicy{
		enabled:       cfg.Enabled,
		targetCPUUtil: cfg.TargetCPUUtil,
		scaleStep:     cfg.ScaleStep,
		minReplicas:   1,   // Default minimum
		maxReplicas:   100, // Default maximum
	}
}

// NewAutoscalingPolicy creates an autoscaling policy with explicit parameters
func NewAutoscalingPolicy(enabled bool, targetCPUUtil float64, scaleStep, minReplicas, maxReplicas int) AutoscalingPolicy {
	return &autoscalingPolicy{
		enabled:       enabled,
		targetCPUUtil: targetCPUUtil,
		scaleStep:     scaleStep,
		minReplicas:   minReplicas,
		maxReplicas:   maxReplicas,
	}
}

func (p *autoscalingPolicy) Enabled() bool {
	return p.enabled
}

func (p *autoscalingPolicy) Name() string {
	return "autoscaling"
}

func (p *autoscalingPolicy) ShouldScaleUp(serviceID string, currentReplicas int, avgCPUUtil float64) bool {
	if !p.enabled {
		return false
	}
	if currentReplicas >= p.maxReplicas {
		return false
	}
	// Scale up if CPU utilization is above target
	return avgCPUUtil > p.targetCPUUtil
}

func (p *autoscalingPolicy) ShouldScaleDown(serviceID string, currentReplicas int, avgCPUUtil float64) bool {
	if !p.enabled {
		return false
	}
	if currentReplicas <= p.minReplicas {
		return false
	}
	// Scale down if CPU utilization is significantly below target (with hysteresis)
	// Use 0.8 * target as threshold to prevent thrashing
	scaleDownThreshold := p.targetCPUUtil * 0.8
	return avgCPUUtil < scaleDownThreshold
}

func (p *autoscalingPolicy) GetTargetReplicas(serviceID string, currentReplicas int, avgCPUUtil float64) int {
	if !p.enabled {
		return currentReplicas
	}

	var targetReplicas int

	if p.ShouldScaleUp(serviceID, currentReplicas, avgCPUUtil) {
		// Calculate target based on CPU utilization
		// target = current * (current_util / target_util)
		targetReplicas = int(float64(currentReplicas) * (avgCPUUtil / p.targetCPUUtil))
		// Round up and add scale step
		targetReplicas = currentReplicas + p.scaleStep
	} else if p.ShouldScaleDown(serviceID, currentReplicas, avgCPUUtil) {
		// Scale down by scale step
		targetReplicas = currentReplicas - p.scaleStep
	} else {
		// No change needed
		return currentReplicas
	}

	// Clamp to min/max bounds
	if targetReplicas < p.minReplicas {
		targetReplicas = p.minReplicas
	}
	if targetReplicas > p.maxReplicas {
		targetReplicas = p.maxReplicas
	}

	return targetReplicas
}
