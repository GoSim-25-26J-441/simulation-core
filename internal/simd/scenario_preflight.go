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
	Path      string `json:"path,omitempty"`
	ServiceID string `json:"service_id,omitempty"`
	Hint      string `json:"hint,omitempty"`
}

// ScenarioValidationSummary provides basic parsed scenario counts.
type ScenarioValidationSummary struct {
	Hosts     int `json:"hosts"`
	Services  int `json:"services"`
	Workloads int `json:"workloads"`
}

// ScenarioValidationResult contains parse + semantic + placement outcomes.
type ScenarioValidationResult struct {
	Valid    bool                       `json:"valid"`
	Errors   []ScenarioValidationIssue  `json:"errors"`
	Warnings []ScenarioValidationIssue  `json:"warnings"`
	Summary  *ScenarioValidationSummary `json:"summary,omitempty"`
}

const placementHint = "Check required_zones, required_host_labels, max_replicas_per_host, and available host CPU/memory."

// validateScenarioYAMLThroughSemantics parses YAML and runs config.ValidateScenario (graph/schema/reference checks).
// On failure it returns (nil, result) with Valid=false and the same issue mapping as preflight/draft APIs.
// On success it returns (scenario, nil).
func validateScenarioYAMLThroughSemantics(scenarioYAML string) (*config.Scenario, *ScenarioValidationResult) {
	scenario, err := config.UnmarshalScenarioYAMLString(scenarioYAML)
	if err != nil {
		z := ScenarioValidationSummary{}
		return nil, &ScenarioValidationResult{
			Valid: false,
			Errors: []ScenarioValidationIssue{{
				Code:    "SCENARIO_PARSE_INVALID",
				Message: err.Error(),
			}},
			Warnings: []ScenarioValidationIssue{},
			Summary:  &z,
		}
	}

	if err := config.ValidateScenario(scenario); err != nil {
		code, path, msg := config.SemanticIssueFromValidateError(err)
		return nil, &ScenarioValidationResult{
			Valid: false,
			Errors: []ScenarioValidationIssue{{
				Code:    code,
				Message: msg,
				Path:    path,
			}},
			Warnings: []ScenarioValidationIssue{},
			Summary:  summaryPtr(scenario),
		}
	}

	return scenario, nil
}

func summaryPtr(s *config.Scenario) *ScenarioValidationSummary {
	if s == nil {
		return nil
	}
	return &ScenarioValidationSummary{
		Hosts:     len(s.Hosts),
		Services:  len(s.Services),
		Workloads: len(s.Workload),
	}
}

// ValidateScenarioDraft validates YAML parse and semantic/schema/reference checks via [config.ValidateScenario]
// without resource placement or capacity initialization.
func ValidateScenarioDraft(scenarioYAML string) *ScenarioValidationResult {
	if _, bad := validateScenarioYAMLThroughSemantics(scenarioYAML); bad != nil {
		return bad
	}
	return &ScenarioValidationResult{
		Valid:    true,
		Errors:   []ScenarioValidationIssue{},
		Warnings: []ScenarioValidationIssue{},
		Summary:  nil,
	}
}

// ValidateScenarioPreflight validates scenario YAML: YAML syntax, [config.ValidateScenario]
// semantic graph checks, then resource/placement initialization. Side-effect free.
func ValidateScenarioPreflight(scenarioYAML string) *ScenarioValidationResult {
	scenario, bad := validateScenarioYAMLThroughSemantics(scenarioYAML)
	if bad != nil {
		return bad
	}

	result := &ScenarioValidationResult{
		Valid:    false,
		Errors:   make([]ScenarioValidationIssue, 0),
		Warnings: make([]ScenarioValidationIssue, 0),
	}

	result.Summary = summaryPtr(scenario)

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
	result.Summary = nil
	result.Errors = []ScenarioValidationIssue{}
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
