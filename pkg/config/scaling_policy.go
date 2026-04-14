package config

import (
	"strings"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
)

// ServiceAllowsBatchScalingAction returns whether a batch neighbor action is allowed for svc
// given service kind and scaling policy (Scenario v2). When Scaling is nil, non-database
// services allow all actions; database-like kinds require an explicit policy.
func ServiceAllowsBatchScalingAction(svc *Service, act simulationv1.BatchScalingAction) bool {
	if svc == nil {
		return false
	}
	p := svc.Scaling
	if p == nil {
		return !strings.EqualFold(strings.TrimSpace(svc.Kind), "database")
	}
	switch act {
	case simulationv1.BatchScalingAction_SERVICE_SCALE_OUT,
		simulationv1.BatchScalingAction_SERVICE_SCALE_IN:
		return p.Horizontal
	case simulationv1.BatchScalingAction_SERVICE_SCALE_UP_CPU,
		simulationv1.BatchScalingAction_SERVICE_SCALE_DOWN_CPU:
		return p.VerticalCPU
	case simulationv1.BatchScalingAction_SERVICE_SCALE_UP_MEMORY,
		simulationv1.BatchScalingAction_SERVICE_SCALE_DOWN_MEMORY:
		return p.VerticalMemory
	default:
		return true
	}
}

// ServiceAllowsHorizontalScaling reports whether replica count may change for svc.
func ServiceAllowsHorizontalScaling(svc *Service) bool {
	return ServiceAllowsBatchScalingAction(svc, simulationv1.BatchScalingAction_SERVICE_SCALE_OUT)
}

// ServiceAllowsVerticalCPU reports whether per-instance CPU may change for svc.
func ServiceAllowsVerticalCPU(svc *Service) bool {
	return ServiceAllowsBatchScalingAction(svc, simulationv1.BatchScalingAction_SERVICE_SCALE_UP_CPU)
}

// ServiceAllowsVerticalMemory reports whether per-instance memory may change for svc.
func ServiceAllowsVerticalMemory(svc *Service) bool {
	return ServiceAllowsBatchScalingAction(svc, simulationv1.BatchScalingAction_SERVICE_SCALE_UP_MEMORY)
}
