package policy

import (
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

const (
	// scaleDownHysteresisFactor is the multiplier for the target CPU utilization
	// to determine the scale-down threshold. This prevents thrashing by requiring
	// CPU utilization to be significantly below target before scaling down.
	scaleDownHysteresisFactor = 0.8
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
	// Validate targetCPUUtil to prevent division by zero
	targetCPU := cfg.TargetCPUUtil
	if targetCPU <= 0 {
		targetCPU = 0.7 // Default to 70% if invalid
	}

	return &autoscalingPolicy{
		enabled:       cfg.Enabled,
		targetCPUUtil: targetCPU,
		scaleStep:     cfg.ScaleStep,
		minReplicas:   1,   // Default minimum
		maxReplicas:   100, // Default maximum
	}
}

// NewAutoscalingPolicy creates an autoscaling policy with explicit parameters
func NewAutoscalingPolicy(enabled bool, targetCPUUtil float64, scaleStep, minReplicas, maxReplicas int) AutoscalingPolicy {
	// Validate targetCPUUtil to prevent division by zero
	if targetCPUUtil <= 0 {
		targetCPUUtil = 0.7 // Default to 70% if invalid
	}

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
	scaleDownThreshold := p.targetCPUUtil * scaleDownHysteresisFactor
	return avgCPUUtil < scaleDownThreshold
}

func (p *autoscalingPolicy) GetTargetReplicas(serviceID string, currentReplicas int, avgCPUUtil float64) int {
	if !p.enabled {
		return currentReplicas
	}

	var targetReplicas int
	shouldScaleUp := p.ShouldScaleUp(serviceID, currentReplicas, avgCPUUtil)
	shouldScaleDown := p.ShouldScaleDown(serviceID, currentReplicas, avgCPUUtil)

	switch {
	case shouldScaleUp:
		// Scale up by scale step
		targetReplicas = currentReplicas + p.scaleStep
	case shouldScaleDown:
		// Scale down by scale step
		targetReplicas = currentReplicas - p.scaleStep
	default:
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
