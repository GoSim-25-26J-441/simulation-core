package config

import "testing"

func TestParseScenarioYAMLString(t *testing.T) {
	yamlText := `
hosts:
  - id: host-1
    cores: 2
services:
  - id: svc1
    replicas: 1
    model: cpu
    endpoints:
      - path: /test
        mean_cpu_ms: 10
        cpu_sigma_ms: 2
        downstream: []
        net_latency_ms: {mean: 1, sigma: 0.5}
workload:
  - from: client
    to: svc1:/test
    arrival: {type: poisson, rate_rps: 10}
`

	scenario, err := ParseScenarioYAMLString(yamlText)
	if err != nil {
		t.Fatalf("ParseScenarioYAMLString failed: %v", err)
	}
	if scenario == nil {
		t.Fatalf("expected non-nil scenario")
	}
	if len(scenario.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(scenario.Services))
	}
	if scenario.Services[0].ID != "svc1" {
		t.Fatalf("expected service id svc1, got %q", scenario.Services[0].ID)
	}
}

func TestParseScenarioYAMLStringInvalid(t *testing.T) {
	// Missing hosts/services/workload should fail validation.
	yamlText := `services: []`
	_, err := ParseScenarioYAMLString(yamlText)
	if err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestParseConfigYAMLString(t *testing.T) {
	yamlText := `
log_level: info
clusters:
  - name: cluster-a
    network_rtt_ms: 1.0
    capacity:
      cpu_cores: 4
      mem_gb: 8
`

	cfg, err := ParseConfigYAMLString(yamlText)
	if err != nil {
		t.Fatalf("ParseConfigYAMLString failed: %v", err)
	}
	if cfg == nil {
		t.Fatalf("expected non-nil config")
	}
	if cfg.LogLevel != "info" {
		t.Fatalf("expected log_level info, got %q", cfg.LogLevel)
	}
}

func TestParseConfigYAMLStringInvalid(t *testing.T) {
	// Invalid log level should fail validation.
	yamlText := `
log_level: nope
clusters:
  - name: cluster-a
    network_rtt_ms: 1.0
    capacity:
      cpu_cores: 4
      mem_gb: 8
`
	_, err := ParseConfigYAMLString(yamlText)
	if err == nil {
		t.Fatalf("expected validation error")
	}
}
