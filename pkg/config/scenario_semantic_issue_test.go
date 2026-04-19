package config

import (
	"strings"
	"testing"
)

func TestSemanticIssueFromValidateError_WorkloadMissingEndpoint(t *testing.T) {
	yaml := `
hosts:
  - id: host-1
    cores: 2
services:
  - id: checkout
    replicas: 1
    model: cpu
    endpoints:
      - path: /read
        mean_cpu_ms: 10
        cpu_sigma_ms: 2
        downstream: []
        net_latency_ms: {mean: 1, sigma: 0.5}
workload:
  - from: client
    to: checkout:/write
    arrival: {type: poisson, rate_rps: 10}
`
	s, err := UnmarshalScenarioYAMLString(strings.TrimSpace(yaml))
	if err != nil {
		t.Fatal(err)
	}
	vErr := ValidateScenario(s)
	if vErr == nil {
		t.Fatal("expected validation error")
	}
	code, path, msg := SemanticIssueFromValidateError(vErr)
	if code != "UNKNOWN_WORKLOAD_ENDPOINT" {
		t.Fatalf("code=%q want UNKNOWN_WORKLOAD_ENDPOINT", code)
	}
	if path != "workload[0].to" {
		t.Fatalf("path=%q", path)
	}
	if !strings.Contains(msg, "checkout") || !strings.Contains(msg, "/write") {
		t.Fatalf("message=%q", msg)
	}
}

func TestSemanticIssueFromValidateError_DownstreamMissingService(t *testing.T) {
	yaml := `
hosts:
  - id: host-1
    cores: 4
services:
  - id: a
    replicas: 1
    model: cpu
    endpoints:
      - path: /x
        mean_cpu_ms: 1
        cpu_sigma_ms: 0
        downstream:
          - to: "nosuch:/y"
            mode: sync
        net_latency_ms: {mean: 1, sigma: 0}
  - id: b
    replicas: 1
    model: cpu
    endpoints:
      - path: /y
        mean_cpu_ms: 1
        cpu_sigma_ms: 0
        downstream: []
        net_latency_ms: {mean: 1, sigma: 0}
workload:
  - from: c
    to: "a:/x"
    arrival: {type: poisson, rate_rps: 1}
`
	s, err := UnmarshalScenarioYAMLString(strings.TrimSpace(yaml))
	if err != nil {
		t.Fatal(err)
	}
	vErr := ValidateScenario(s)
	if vErr == nil {
		t.Fatal("expected error")
	}
	code, _, _ := SemanticIssueFromValidateError(vErr)
	if code != "UNKNOWN_DOWNSTREAM_SERVICE" {
		t.Fatalf("code=%q err=%v", code, vErr)
	}
}

func TestSemanticIssueFromValidateError_DownstreamMissingEndpoint(t *testing.T) {
	yaml := `
hosts:
  - id: host-1
    cores: 4
services:
  - id: a
    replicas: 1
    model: cpu
    endpoints:
      - path: /x
        mean_cpu_ms: 1
        cpu_sigma_ms: 0
        downstream:
          - to: "b:/missing"
            mode: sync
        net_latency_ms: {mean: 1, sigma: 0}
  - id: b
    replicas: 1
    model: cpu
    endpoints:
      - path: /ok
        mean_cpu_ms: 1
        cpu_sigma_ms: 0
        downstream: []
        net_latency_ms: {mean: 1, sigma: 0}
workload:
  - from: c
    to: "a:/x"
    arrival: {type: poisson, rate_rps: 1}
`
	s, err := UnmarshalScenarioYAMLString(strings.TrimSpace(yaml))
	if err != nil {
		t.Fatal(err)
	}
	vErr := ValidateScenario(s)
	if vErr == nil {
		t.Fatal("expected error")
	}
	code, _, _ := SemanticIssueFromValidateError(vErr)
	if code != "UNKNOWN_DOWNSTREAM_ENDPOINT" {
		t.Fatalf("code=%q err=%v", code, vErr)
	}
}
