package simd

import (
	"strings"

	"github.com/GoSim-25-26J-441/simulation-core/internal/resource"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

// ScenarioValidationIssue is one validation finding (error or warning).
type ScenarioValidationIssue struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	ServiceID string `json:"service_id,omitempty"`
	Hint      string `json:"hint,omitempty"`
}

// ScenarioValidationSummary provides basic parsed scenario counts.
type ScenarioValidationSummary struct {
	Hosts     int `json:"hosts"`
	Services  int `json:"services"`
	Workloads int `json:"workloads"`
}

// ScenarioValidationResult contains parse + preflight feasibility outcomes.
type ScenarioValidationResult struct {
	Valid    bool                      `json:"valid"`
	Errors   []ScenarioValidationIssue `json:"errors"`
	Warnings []ScenarioValidationIssue `json:"warnings"`
	Summary  ScenarioValidationSummary `json:"summary"`
}

const placementHint = "Check required_zones, required_host_labels, max_replicas_per_host, and available host CPU/memory."

// ValidateScenarioPreflight validates scenario YAML for both config semantics and
// resource/placement feasibility. It is side-effect free.
func ValidateScenarioPreflight(scenarioYAML string) *ScenarioValidationResult {
	result := &ScenarioValidationResult{
		Valid:    false,
		Errors:   make([]ScenarioValidationIssue, 0),
		Warnings: make([]ScenarioValidationIssue, 0),
	}

	scenario, err := config.ParseScenarioYAMLString(scenarioYAML)
	if err != nil {
		result.Errors = append(result.Errors, ScenarioValidationIssue{
			Code:    "SCENARIO_PARSE_INVALID",
			Message: err.Error(),
		})
		return result
	}

	result.Summary = ScenarioValidationSummary{
		Hosts:     len(scenario.Hosts),
		Services:  len(scenario.Services),
		Workloads: len(scenario.Workload),
	}

	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		msg := err.Error()
		issue := ScenarioValidationIssue{
			Code:    "PLACEMENT_INFEASIBLE",
			Message: msg,
			Hint:    placementHint,
		}
		if sid := extractServiceIDFromPlacementError(msg); sid != "" {
			issue.ServiceID = sid
		}
		result.Errors = append(result.Errors, issue)
		return result
	}

	result.Valid = true
	return result
}

func extractServiceIDFromPlacementError(msg string) string {
	const prefix = "cannot place service "
	if !strings.HasPrefix(msg, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(msg, prefix)
	if rest == "" {
		return ""
	}
	if idx := strings.Index(rest, ":"); idx > 0 {
		return strings.TrimSpace(rest[:idx])
	}
	return strings.TrimSpace(rest)
}
