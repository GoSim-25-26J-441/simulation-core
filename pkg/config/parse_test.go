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
	tests := []struct {
		name     string
		yamlText string
	}{
		{
			name:     "Missing hosts",
			yamlText: `services: []`,
		},
		{
			name: "Missing services",
			yamlText: `
hosts:
  - id: host-1
    cores: 2
workload: []`,
		},
		{
			name: "Missing workload",
			yamlText: `
hosts:
  - id: host-1
    cores: 2
services:
  - id: svc1
    replicas: 1
    model: cpu
    endpoints: []`,
		},
		{
			name: "Empty service ID",
			yamlText: `
hosts:
  - id: host-1
    cores: 2
services:
  - id: ""
    replicas: 1
    model: cpu
    endpoints: []
workload: []`,
		},
		{
			name: "Invalid service model",
			yamlText: `
hosts:
  - id: host-1
    cores: 2
services:
  - id: svc1
    replicas: 1
    model: invalid_model
    endpoints: []
workload: []`,
		},
		{
			name: "Negative cores",
			yamlText: `
hosts:
  - id: host-1
    cores: -1
services:
  - id: svc1
    replicas: 1
    model: cpu
    endpoints: []
workload: []`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseScenarioYAMLString(tt.yamlText)
			if err == nil {
				t.Fatalf("expected validation error for %s", tt.name)
			}
		})
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
	tests := []struct {
		name     string
		yamlText string
	}{
		{
			name: "Invalid log level",
			yamlText: `
log_level: nope
clusters:
  - name: cluster-a
    network_rtt_ms: 1.0
    capacity:
      cpu_cores: 4
      mem_gb: 8`,
		},
		{
			name: "Missing clusters",
			yamlText: `
log_level: info
clusters: []`,
		},
		{
			name: "Negative network RTT",
			yamlText: `
log_level: info
clusters:
  - name: cluster-a
    network_rtt_ms: -1.0
    capacity:
      cpu_cores: 4
      mem_gb: 8`,
		},
		{
			name: "Invalid CPU cores",
			yamlText: `
log_level: info
clusters:
  - name: cluster-a
    network_rtt_ms: 1.0
    capacity:
      cpu_cores: 0
      mem_gb: 8`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseConfigYAMLString(tt.yamlText)
			if err == nil {
				t.Fatalf("expected validation error for %s", tt.name)
			}
		})
	}
}

func TestParseScenarioYAMLStringMalformed(t *testing.T) {
	tests := []struct {
		name     string
		yamlText string
	}{
		{
			name:     "Unclosed bracket",
			yamlText: `hosts: [unclosed`,
		},
		{
			name: "Invalid indentation",
			yamlText: `
hosts:
- id: host-1
  cores: 2
 services:
  - id: svc1`,
		},
		{
			name:     "Invalid YAML syntax",
			yamlText: `hosts: {{{invalid}}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseScenarioYAMLString(tt.yamlText)
			if err == nil {
				t.Fatalf("expected error when parsing malformed YAML")
			}
		})
	}
}

func TestParseConfigYAMLStringMalformed(t *testing.T) {
	tests := []struct {
		name     string
		yamlText string
	}{
		{
			name:     "Unclosed bracket",
			yamlText: `clusters: [unclosed`,
		},
		{
			name: "Invalid indentation",
			yamlText: `
log_level: info
 clusters:
  - name: test`,
		},
		{
			name:     "Invalid YAML syntax",
			yamlText: `log_level: {{{invalid}}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseConfigYAMLString(tt.yamlText)
			if err == nil {
				t.Fatalf("expected error when parsing malformed YAML")
			}
		})
	}
}

func TestParseScenarioYAML(t *testing.T) {
	yamlBytes := []byte(`
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
`)

	scenario, err := ParseScenarioYAML(yamlBytes)
	if err != nil {
		t.Fatalf("ParseScenarioYAML failed: %v", err)
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

func TestParseScenarioYAMLInvalid(t *testing.T) {
	yamlBytes := []byte(`services: []`)
	_, err := ParseScenarioYAML(yamlBytes)
	if err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestParseScenarioYAMLMalformed(t *testing.T) {
	yamlBytes := []byte(`hosts: [unclosed`)
	_, err := ParseScenarioYAML(yamlBytes)
	if err == nil {
		t.Fatalf("expected error when parsing malformed YAML")
	}
}

func TestParseConfigYAML(t *testing.T) {
	yamlBytes := []byte(`
log_level: info
clusters:
  - name: cluster-a
    network_rtt_ms: 1.0
    capacity:
      cpu_cores: 4
      mem_gb: 8
`)

	cfg, err := ParseConfigYAML(yamlBytes)
	if err != nil {
		t.Fatalf("ParseConfigYAML failed: %v", err)
	}
	if cfg == nil {
		t.Fatalf("expected non-nil config")
	}
	if cfg.LogLevel != "info" {
		t.Fatalf("expected log_level info, got %q", cfg.LogLevel)
	}
}

func TestParseConfigYAMLInvalid(t *testing.T) {
	yamlBytes := []byte(`
log_level: invalid
clusters:
  - name: test
    network_rtt_ms: 1.0
    capacity:
      cpu_cores: 4
      mem_gb: 8
`)
	_, err := ParseConfigYAML(yamlBytes)
	if err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestParseConfigYAMLMalformed(t *testing.T) {
	yamlBytes := []byte(`clusters: [unclosed`)
	_, err := ParseConfigYAML(yamlBytes)
	if err == nil {
		t.Fatalf("expected error when parsing malformed YAML")
	}
}
