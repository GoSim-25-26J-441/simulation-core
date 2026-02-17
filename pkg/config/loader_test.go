package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	// Test loading the actual config file
	cfg, err := LoadConfig("../../config/config.yaml")
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Validate basic structure
	if cfg.LogLevel != "info" {
		t.Errorf("Expected log_level 'info', got '%s'", cfg.LogLevel)
	}

	if len(cfg.Clusters) != 2 {
		t.Errorf("Expected 2 clusters, got %d", len(cfg.Clusters))
	}

	// Validate cluster-a
	clusterA := cfg.Clusters[0]
	if clusterA.Name != "cluster-a" {
		t.Errorf("Expected cluster name 'cluster-a', got '%s'", clusterA.Name)
	}
	if clusterA.NetworkRTTMs != 1.2 {
		t.Errorf("Expected network RTT 1.2, got %f", clusterA.NetworkRTTMs)
	}
	if clusterA.Capacity.CPUCores != 32 {
		t.Errorf("Expected 32 CPU cores, got %d", clusterA.Capacity.CPUCores)
	}

	// Validate service graph
	if cfg.ServiceGraph == nil {
		t.Fatal("ServiceGraph should not be nil")
	}
	if len(cfg.ServiceGraph.Nodes) != 4 {
		t.Errorf("Expected 4 service nodes, got %d", len(cfg.ServiceGraph.Nodes))
	}
	if len(cfg.ServiceGraph.Edges) != 3 {
		t.Errorf("Expected 3 service edges, got %d", len(cfg.ServiceGraph.Edges))
	}

	// Validate workload
	if cfg.Workload == nil {
		t.Fatal("Workload should not be nil")
	}
	if cfg.Workload.Arrival != "poisson" {
		t.Errorf("Expected arrival 'poisson', got '%s'", cfg.Workload.Arrival)
	}
	if cfg.Workload.RateRPS != 500 {
		t.Errorf("Expected rate 500 RPS, got %d", cfg.Workload.RateRPS)
	}

	// Validate duration parsing
	duration, err := cfg.Workload.GetDuration()
	if err != nil {
		t.Errorf("Failed to parse duration: %v", err)
	}
	if duration.Seconds() != 60 {
		t.Errorf("Expected 60s duration, got %v", duration)
	}

	// Validate policies
	if cfg.Policies == nil {
		t.Fatal("Policies should not be nil")
	}
	if cfg.Policies.Autoscaling == nil {
		t.Fatal("Autoscaling policy should not be nil")
	}
	if cfg.Policies.Autoscaling.TargetCPUUtil != 0.7 {
		t.Errorf("Expected target CPU util 0.7, got %f", cfg.Policies.Autoscaling.TargetCPUUtil)
	}

	// Validate optimization
	if cfg.Optimization == nil {
		t.Fatal("Optimization should not be nil")
	}
	if cfg.Optimization.Objective != "p95_latency_ms" {
		t.Errorf("Expected objective 'p95_latency_ms', got '%s'", cfg.Optimization.Objective)
	}
}

func TestLoadScenario(t *testing.T) {
	// Test loading the actual scenario file
	scenario, err := LoadScenario("../../config/scenario.yaml")
	if err != nil {
		t.Fatalf("Failed to load scenario: %v", err)
	}

	// Validate hosts
	if len(scenario.Hosts) != 1 {
		t.Errorf("Expected 1 host, got %d", len(scenario.Hosts))
	}
	if scenario.Hosts[0].ID != "host-1" {
		t.Errorf("Expected host ID 'host-1', got '%s'", scenario.Hosts[0].ID)
	}
	if scenario.Hosts[0].Cores != 2 {
		t.Errorf("Expected 2 cores, got %d", scenario.Hosts[0].Cores)
	}

	// Validate services
	if len(scenario.Services) != 3 {
		t.Errorf("Expected 3 services, got %d", len(scenario.Services))
	}

	// Validate auth service
	authService := scenario.Services[0]
	if authService.ID != "auth" {
		t.Errorf("Expected service ID 'auth', got '%s'", authService.ID)
	}
	if authService.Replicas != 2 {
		t.Errorf("Expected 2 replicas, got %d", authService.Replicas)
	}
	if authService.Model != "cpu" {
		t.Errorf("Expected model 'cpu', got '%s'", authService.Model)
	}
	if len(authService.Endpoints) != 2 {
		t.Errorf("Expected 2 endpoints, got %d", len(authService.Endpoints))
	}

	// Validate endpoint
	loginEndpoint := authService.Endpoints[0]
	if loginEndpoint.Path != "/auth/login" {
		t.Errorf("Expected path '/auth/login', got '%s'", loginEndpoint.Path)
	}
	if loginEndpoint.MeanCPUMs != 50 {
		t.Errorf("Expected mean CPU 50ms, got %f", loginEndpoint.MeanCPUMs)
	}

	// Validate workload
	if len(scenario.Workload) != 2 {
		t.Errorf("Expected 2 workload patterns, got %d", len(scenario.Workload))
	}
	if scenario.Workload[0].To != "auth:/auth/login" {
		t.Errorf("Expected workload to 'auth:/auth/login', got '%s'", scenario.Workload[0].To)
	}
	if scenario.Workload[0].Arrival.RateRPS != 20 {
		t.Errorf("Expected rate 20 RPS, got %f", scenario.Workload[0].Arrival.RateRPS)
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name        string
		config      *Config
		expectError bool
	}{
		{
			name: "Valid config",
			config: &Config{
				LogLevel: "info",
				Clusters: []Cluster{
					{Name: "test", NetworkRTTMs: 1.0, Capacity: Capacity{CPUCores: 4, MemGB: 8}},
				},
			},
			expectError: false,
		},
		{
			name: "Invalid log level",
			config: &Config{
				LogLevel: "invalid",
				Clusters: []Cluster{
					{Name: "test", NetworkRTTMs: 1.0, Capacity: Capacity{CPUCores: 4, MemGB: 8}},
				},
			},
			expectError: true,
		},
		{
			name: "No clusters",
			config: &Config{
				LogLevel: "info",
				Clusters: []Cluster{},
			},
			expectError: true,
		},
		{
			name: "Negative network RTT",
			config: &Config{
				LogLevel: "info",
				Clusters: []Cluster{
					{Name: "test", NetworkRTTMs: -1.0, Capacity: Capacity{CPUCores: 4, MemGB: 8}},
				},
			},
			expectError: true,
		},
		{
			name: "Empty cluster name",
			config: &Config{
				LogLevel: "info",
				Clusters: []Cluster{
					{Name: "", NetworkRTTMs: 1.0, Capacity: Capacity{CPUCores: 4, MemGB: 8}},
				},
			},
			expectError: true,
		},
		{
			name: "Duplicate cluster name",
			config: &Config{
				LogLevel: "info",
				Clusters: []Cluster{
					{Name: "dup", NetworkRTTMs: 1.0, Capacity: Capacity{CPUCores: 4, MemGB: 8}},
					{Name: "dup", NetworkRTTMs: 1.0, Capacity: Capacity{CPUCores: 4, MemGB: 8}},
				},
			},
			expectError: true,
		},
		{
			name: "Zero mem_gb",
			config: &Config{
				LogLevel: "info",
				Clusters: []Cluster{
					{Name: "test", NetworkRTTMs: 1.0, Capacity: Capacity{CPUCores: 4, MemGB: 0}},
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfig(tt.config)
			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
		})
	}
}

func TestScenarioValidation(t *testing.T) {
	tests := []struct {
		name        string
		scenario    *Scenario
		expectError bool
	}{
		{
			name: "Valid scenario",
			scenario: &Scenario{
				Hosts: []Host{{ID: "h1", Cores: 4}},
				Services: []Service{
					{
						ID:       "svc1",
						Replicas: 1,
						Model:    "cpu",
						Endpoints: []Endpoint{
							{Path: "/test", MeanCPUMs: 10, CPUSigmaMs: 2, NetLatencyMs: LatencySpec{Mean: 1, Sigma: 0.5}},
						},
					},
				},
				Workload: []WorkloadPattern{
					{From: "client", To: "svc1:/test", Arrival: ArrivalSpec{Type: "poisson", RateRPS: 10}},
				},
			},
			expectError: false,
		},
		{
			name: "No hosts",
			scenario: &Scenario{
				Services: []Service{
					{ID: "svc1", Replicas: 1, Model: "cpu", Endpoints: []Endpoint{{Path: "/test"}}},
				},
				Workload: []WorkloadPattern{{From: "client", To: "svc1:/test", Arrival: ArrivalSpec{Type: "poisson", RateRPS: 10}}},
			},
			expectError: true,
		},
		{
			name: "Invalid service model",
			scenario: &Scenario{
				Hosts: []Host{{ID: "h1", Cores: 4}},
				Services: []Service{
					{ID: "svc1", Replicas: 1, Model: "invalid", Endpoints: []Endpoint{{Path: "/test"}}},
				},
				Workload: []WorkloadPattern{{From: "client", To: "svc1:/test", Arrival: ArrivalSpec{Type: "poisson", RateRPS: 10}}},
			},
			expectError: true,
		},
		{
			name: "Empty host id",
			scenario: &Scenario{
				Hosts: []Host{{ID: "", Cores: 4}},
				Services: []Service{
					{ID: "svc1", Replicas: 1, Model: "cpu", Endpoints: []Endpoint{{Path: "/test"}}},
				},
				Workload: []WorkloadPattern{{From: "client", To: "svc1:/test", Arrival: ArrivalSpec{Type: "poisson", RateRPS: 10}}},
			},
			expectError: true,
		},
		{
			name: "Duplicate host id",
			scenario: &Scenario{
				Hosts: []Host{{ID: "h1", Cores: 4}, {ID: "h1", Cores: 2}},
				Services: []Service{
					{ID: "svc1", Replicas: 1, Model: "cpu", Endpoints: []Endpoint{{Path: "/test"}}},
				},
				Workload: []WorkloadPattern{{From: "client", To: "svc1:/test", Arrival: ArrivalSpec{Type: "poisson", RateRPS: 10}}},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateScenario(tt.scenario)
			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
		})
	}
}

func TestLoadInvalidFile(t *testing.T) {
	_, err := LoadConfig("/nonexistent/path/config.yaml")
	if err == nil {
		t.Error("Expected error when loading nonexistent file")
	}
}

func TestValidateServiceGraph(t *testing.T) {
	cfg := &Config{
		LogLevel: "info",
		Clusters: []Cluster{
			{Name: "c1", NetworkRTTMs: 1.0, Capacity: Capacity{CPUCores: 4, MemGB: 8}},
		},
		ServiceGraph: &ServiceGraph{
			Nodes: []ServiceNode{{Name: "svc1", Cluster: "c1", CPUCostMs: 10}},
			Edges: []ServiceEdge{{From: "svc1", To: "svc2", Mode: "sync", P: 1.0}},
		},
	}
	err := validateConfig(cfg)
	if err == nil {
		t.Error("expected error when edge references non-existent node svc2")
	}
}

func TestValidateConfigEmptyClusterName(t *testing.T) {
	cfg := &Config{
		LogLevel: "info",
		Clusters: []Cluster{
			{Name: "", NetworkRTTMs: 1.0, Capacity: Capacity{CPUCores: 4, MemGB: 8}},
		},
	}
	err := validateConfig(cfg)
	if err == nil {
		t.Error("expected error for empty cluster name")
	}
}

func TestLoadScenarioInvalidFile(t *testing.T) {
	_, err := LoadScenario("/nonexistent/path/scenario.yaml")
	if err == nil {
		t.Error("Expected error when loading nonexistent scenario file")
	}
}

func TestLoadMalformedYAML(t *testing.T) {
	// Create a temporary malformed YAML file
	tmpDir := t.TempDir()
	malformedFile := filepath.Join(tmpDir, "malformed.yaml")

	content := `
log_level: info
clusters:
  - name: test
    invalid_yaml: [unclosed
`
	if err := os.WriteFile(malformedFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	_, err := LoadConfig(malformedFile)
	if err == nil {
		t.Error("Expected error when parsing malformed YAML")
	}
}
