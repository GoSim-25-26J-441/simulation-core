package simd

import (
	"strings"
	"testing"
)

func TestValidateScenarioPreflight_ParseInvalidZeroSummary(t *testing.T) {
	res := ValidateScenarioPreflight("hosts: [")
	if res.Valid {
		t.Fatal("expected invalid")
	}
	if len(res.Errors) != 1 || res.Errors[0].Code != "SCENARIO_PARSE_INVALID" {
		t.Fatalf("errors: %#v", res.Errors)
	}
	if res.Summary == nil {
		t.Fatal("expected summary on parse failure")
	}
	if res.Summary.Hosts != 0 || res.Summary.Services != 0 || res.Summary.Workloads != 0 {
		t.Fatalf("summary: %#v", res.Summary)
	}
}

func TestValidateScenarioPreflight_ValidMinimal(t *testing.T) {
	yaml := strings.TrimSpace(testScenarioYAML)
	res := ValidateScenarioPreflight(yaml)
	if !res.Valid {
		t.Fatalf("expected valid, errors=%#v", res.Errors)
	}
	if res.Summary != nil {
		t.Fatalf("expected summary omitted for valid scenario, got %#v", res.Summary)
	}
}

func TestValidateScenarioPreflight_WorkloadUnknownEndpoint(t *testing.T) {
	res := ValidateScenarioPreflight(strings.TrimSpace(workloadMissingEndpointYAML))
	if res.Valid {
		t.Fatal("expected invalid")
	}
	if len(res.Errors) != 1 || res.Errors[0].Code != "UNKNOWN_WORKLOAD_ENDPOINT" {
		t.Fatalf("errors: %#v", res.Errors)
	}
	if res.Errors[0].Path != "workload[0].to" {
		t.Fatalf("path=%q", res.Errors[0].Path)
	}
}

func TestValidateScenarioPreflight_PlacementInfeasible(t *testing.T) {
	res := ValidateScenarioPreflight(strings.TrimSpace(infeasiblePlacementScenarioYAML))
	if res.Valid {
		t.Fatal("expected invalid")
	}
	if len(res.Errors) != 1 || res.Errors[0].Code != "PLACEMENT_INFEASIBLE" {
		t.Fatalf("errors: %#v", res.Errors)
	}
	if res.Summary == nil {
		t.Fatal("expected summary")
	}
	if res.Summary.Hosts != 1 || res.Summary.Services != 1 || res.Summary.Workloads != 1 {
		t.Fatalf("expected parsed counts before placement failure, got %#v", res.Summary)
	}
}

func TestValidateScenarioDraft_ValidMinimal(t *testing.T) {
	yaml := strings.TrimSpace(testScenarioYAML)
	res := ValidateScenarioDraft(yaml)
	if !res.Valid {
		t.Fatalf("expected valid draft, errors=%#v", res.Errors)
	}
	if len(res.Errors) != 0 || len(res.Warnings) != 0 {
		t.Fatalf("expected empty errors/warnings, got errors=%#v warnings=%#v", res.Errors, res.Warnings)
	}
	if res.Summary != nil {
		t.Fatalf("expected summary omitted for valid draft, got %#v", res.Summary)
	}
}

func TestExtractServiceIDFromPlacementError_WrappedMessage(t *testing.T) {
	msg := "placement infeasible: cannot place service api-1: insufficient host capacity"
	if got := extractServiceIDFromPlacementError(msg); got != "api-1" {
		t.Fatalf("got %q want api-1", got)
	}
}

func TestValidateScenarioDraft_PlacementInfeasibleSkipped(t *testing.T) {
	yaml := strings.TrimSpace(infeasiblePlacementScenarioYAML)
	draft := ValidateScenarioDraft(yaml)
	if !draft.Valid {
		t.Fatalf("draft should skip placement and accept semantically valid scenario, got %#v", draft)
	}
	pre := ValidateScenarioPreflight(yaml)
	if pre.Valid {
		t.Fatal("preflight must still fail placement for same YAML")
	}
	if len(pre.Errors) != 1 || pre.Errors[0].Code != "PLACEMENT_INFEASIBLE" {
		t.Fatalf("preflight errors: %#v", pre.Errors)
	}
}
