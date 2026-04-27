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
