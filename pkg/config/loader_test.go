package config

import (
	"math"
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

	if scenario.Metadata == nil || scenario.Metadata.SchemaVersion != "0.2.0" {
		t.Fatalf("Expected scenario schema_version 0.2.0, got %+v", scenario.Metadata)
	}

	// Validate hosts
	if len(scenario.Hosts) != 3 {
		t.Errorf("Expected 3 hosts, got %d", len(scenario.Hosts))
	}
	if scenario.Hosts[0].ID != "host-1" {
		t.Errorf("Expected host ID 'host-1', got '%s'", scenario.Hosts[0].ID)
	}
	if scenario.Hosts[0].Cores != 8 {
		t.Errorf("Expected 8 cores, got %d", scenario.Hosts[0].Cores)
	}
	if scenario.Hosts[0].MemoryGB != 32 {
		t.Errorf("Expected host memory_gb 32, got %d", scenario.Hosts[0].MemoryGB)
	}

	// Validate services
	if len(scenario.Services) != 15 {
		t.Errorf("Expected 15 services, got %d", len(scenario.Services))
	}

	// Validate ingress service and v2 fields
	gateway := scenario.Services[0]
	if gateway.ID != "api-gateway" {
		t.Errorf("Expected service ID 'api-gateway', got '%s'", gateway.ID)
	}
	if gateway.Kind != "api_gateway" || gateway.Role != "ingress" {
		t.Errorf("Expected api_gateway ingress service, got kind=%q role=%q", gateway.Kind, gateway.Role)
	}
	if gateway.Scaling == nil || !gateway.Scaling.Horizontal || !gateway.Scaling.VerticalCPU || !gateway.Scaling.VerticalMemory {
		t.Fatalf("Expected gateway scaling policy to allow horizontal and vertical scaling, got %+v", gateway.Scaling)
	}
	if gateway.Model != "cpu" {
		t.Errorf("Expected model 'cpu', got '%s'", gateway.Model)
	}

	// Validate endpoint
	restEndpoint := gateway.Endpoints[0]
	if restEndpoint.Path != "/REST" {
		t.Errorf("Expected path '/REST', got '%s'", restEndpoint.Path)
	}
	if len(restEndpoint.Downstream) != 3 {
		t.Fatalf("Expected 3 gateway downstream calls, got %d", len(restEndpoint.Downstream))
	}
	if restEndpoint.Downstream[0].Mode != "sync" || restEndpoint.Downstream[1].Mode != "async" || restEndpoint.Downstream[2].Mode != "async" {
		t.Errorf("Expected sync then two async downstream modes, got %+v", restEndpoint.Downstream)
	}
	if restEndpoint.Downstream[2].Kind != "queue" {
		t.Errorf("Expected third downstream kind queue, got %q", restEndpoint.Downstream[2].Kind)
	}

	// Validate workload
	if len(scenario.Workload) != 3 {
		t.Errorf("Expected 3 workload patterns, got %d", len(scenario.Workload))
	}
	if scenario.Workload[0].To != "api-gateway:/REST" {
		t.Errorf("Expected workload to 'api-gateway:/REST', got '%s'", scenario.Workload[0].To)
	}
	if scenario.Workload[0].TrafficClass != "ingress" || scenario.Workload[0].SourceKind != "client" {
		t.Errorf("Expected ingress client workload, got traffic_class=%q source_kind=%q", scenario.Workload[0].TrafficClass, scenario.Workload[0].SourceKind)
	}
	if scenario.Workload[0].Arrival.RateRPS != 80 {
		t.Errorf("Expected rate 80 RPS, got %f", scenario.Workload[0].Arrival.RateRPS)
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
		{
			name: "Negative host memory_gb",
			scenario: &Scenario{
				Hosts: []Host{{ID: "h1", Cores: 4, MemoryGB: -1}},
				Services: []Service{
					{ID: "svc1", Replicas: 1, Model: "cpu", Endpoints: []Endpoint{{Path: "/test"}}},
				},
				Workload: []WorkloadPattern{{From: "client", To: "svc1:/test", Arrival: ArrivalSpec{Type: "poisson", RateRPS: 10}}},
			},
			expectError: true,
		},
		{
			name: "Invalid arrival type",
			scenario: &Scenario{
				Hosts: []Host{{ID: "h1", Cores: 4}},
				Services: []Service{
					{ID: "svc1", Replicas: 1, Model: "cpu", Endpoints: []Endpoint{{Path: "/test"}}},
				},
				Workload: []WorkloadPattern{{From: "client", To: "svc1:/test", Arrival: ArrivalSpec{Type: "burst_typo", RateRPS: 10}}},
			},
			expectError: true,
		},
		{
			name: "Workload metadata empty key rejected",
			scenario: &Scenario{
				Hosts: []Host{{ID: "h1", Cores: 4}},
				Services: []Service{
					{ID: "svc1", Replicas: 1, Model: "cpu", Endpoints: []Endpoint{{Path: "/test"}}},
				},
				Workload: []WorkloadPattern{{From: "client", To: "svc1:/test", Metadata: map[string]string{"": "zone-a"}, Arrival: ArrivalSpec{Type: "poisson", RateRPS: 10}}},
			},
			expectError: true,
		},
		{
			name: "Invalid service routing strategy",
			scenario: &Scenario{
				Hosts: []Host{{ID: "h1", Cores: 4}},
				Services: []Service{
					{
						ID:       "svc1",
						Replicas: 1,
						Model:    "cpu",
						Routing:  &RoutingPolicy{Strategy: "maglev"},
						Endpoints: []Endpoint{
							{Path: "/test", MeanCPUMs: 10, CPUSigmaMs: 2, NetLatencyMs: LatencySpec{Mean: 1, Sigma: 0.5}},
						},
					},
				},
				Workload: []WorkloadPattern{{From: "client", To: "svc1:/test", Arrival: ArrivalSpec{Type: "poisson", RateRPS: 10}}},
			},
			expectError: true,
		},
		{
			name: "Negative routing weight rejected",
			scenario: &Scenario{
				Hosts: []Host{{ID: "h1", Cores: 4}},
				Services: []Service{
					{
						ID:       "svc1",
						Replicas: 1,
						Model:    "cpu",
						Routing:  &RoutingPolicy{Strategy: "weighted_round_robin", Weights: map[string]float64{"svc1-instance-0": -0.1}},
						Endpoints: []Endpoint{
							{Path: "/test", MeanCPUMs: 10, CPUSigmaMs: 2, NetLatencyMs: LatencySpec{Mean: 1, Sigma: 0.5}},
						},
					},
				},
				Workload: []WorkloadPattern{{From: "client", To: "svc1:/test", Arrival: ArrivalSpec{Type: "poisson", RateRPS: 10}}},
			},
			expectError: true,
		},
		{
			name: "Sticky requires sticky_key_from",
			scenario: &Scenario{
				Hosts: []Host{{ID: "h1", Cores: 4}},
				Services: []Service{
					{
						ID:       "svc1",
						Replicas: 1,
						Model:    "cpu",
						Routing:  &RoutingPolicy{Strategy: "sticky"},
						Endpoints: []Endpoint{
							{Path: "/test", MeanCPUMs: 10, CPUSigmaMs: 2, NetLatencyMs: LatencySpec{Mean: 1, Sigma: 0.5}},
						},
					},
				},
				Workload: []WorkloadPattern{{From: "client", To: "svc1:/test", Arrival: ArrivalSpec{Type: "poisson", RateRPS: 10}}},
			},
			expectError: true,
		},
		{
			name: "Endpoint routing override valid",
			scenario: &Scenario{
				Hosts: []Host{{ID: "h1", Cores: 4}},
				Services: []Service{
					{
						ID:       "svc1",
						Replicas: 1,
						Model:    "cpu",
						Routing:  &RoutingPolicy{Strategy: "round_robin"},
						Endpoints: []Endpoint{
							{
								Path: "/test", MeanCPUMs: 10, CPUSigmaMs: 2, NetLatencyMs: LatencySpec{Mean: 1, Sigma: 0.5},
								Routing: &RoutingPolicy{Strategy: "least_queue"},
							},
						},
					},
				},
				Workload: []WorkloadPattern{{From: "client", To: "svc1:/test", Arrival: ArrivalSpec{Type: "poisson", RateRPS: 10}}},
			},
			expectError: false,
		},
		{
			name: "Infinite routing weight rejected",
			scenario: &Scenario{
				Hosts: []Host{{ID: "h1", Cores: 4}},
				Services: []Service{
					{
						ID:       "svc1",
						Replicas: 1,
						Model:    "cpu",
						Routing:  &RoutingPolicy{Strategy: "weighted_round_robin", Weights: map[string]float64{"svc1-instance-0": math.Inf(1)}},
						Endpoints: []Endpoint{
							{Path: "/test", MeanCPUMs: 10, CPUSigmaMs: 2, NetLatencyMs: LatencySpec{Mean: 1, Sigma: 0.5}},
						},
					},
				},
				Workload: []WorkloadPattern{{From: "client", To: "svc1:/test", Arrival: ArrivalSpec{Type: "poisson", RateRPS: 10}}},
			},
			expectError: true,
		},
		{
			name: "NaN routing weight rejected",
			scenario: &Scenario{
				Hosts: []Host{{ID: "h1", Cores: 4}},
				Services: []Service{
					{
						ID:       "svc1",
						Replicas: 1,
						Model:    "cpu",
						Routing:  &RoutingPolicy{Strategy: "weighted_round_robin", Weights: map[string]float64{"svc1-instance-0": math.NaN()}},
						Endpoints: []Endpoint{
							{Path: "/test", MeanCPUMs: 10, CPUSigmaMs: 2, NetLatencyMs: LatencySpec{Mean: 1, Sigma: 0.5}},
						},
					},
				},
				Workload: []WorkloadPattern{{From: "client", To: "svc1:/test", Arrival: ArrivalSpec{Type: "poisson", RateRPS: 10}}},
			},
			expectError: true,
		},
		{
			name: "Negative max_replicas_per_host rejected",
			scenario: &Scenario{
				Hosts: []Host{{ID: "h1", Cores: 4}},
				Services: []Service{
					{
						ID:       "svc1",
						Replicas: 1,
						Model:    "cpu",
						Placement: &PlacementPolicy{
							MaxReplicasPerHost: -1,
						},
						Endpoints: []Endpoint{
							{Path: "/test", MeanCPUMs: 10, CPUSigmaMs: 2, NetLatencyMs: LatencySpec{Mean: 1, Sigma: 0.5}},
						},
					},
				},
				Workload: []WorkloadPattern{{From: "client", To: "svc1:/test", Arrival: ArrivalSpec{Type: "poisson", RateRPS: 10}}},
			},
			expectError: true,
		},
		{
			name: "Preferred placement fields valid",
			scenario: &Scenario{
				Hosts: []Host{{ID: "h1", Cores: 4, Zone: "zone-a", Labels: map[string]string{"rack": "r1"}}},
				Services: []Service{
					{
						ID:       "svc1",
						Replicas: 1,
						Model:    "cpu",
						Placement: &PlacementPolicy{
							RequiredZones:       []string{"zone-a"},
							PreferredZones:      []string{"zone-b"},
							RequiredHostLabels:  map[string]string{"rack": "r1"},
							PreferredHostLabels: map[string]string{"tier": "gold"},
							SpreadAcrossZones:   true,
							MaxReplicasPerHost:  1,
						},
						Endpoints: []Endpoint{
							{Path: "/test", MeanCPUMs: 10, CPUSigmaMs: 2, NetLatencyMs: LatencySpec{Mean: 1, Sigma: 0.5}},
						},
					},
				},
				Workload: []WorkloadPattern{{From: "client", To: "svc1:/test", Arrival: ArrivalSpec{Type: "poisson", RateRPS: 10}}},
			},
			expectError: false,
		},
		{
			name: "queue service requires behavior.queue",
			scenario: &Scenario{
				Hosts: []Host{{ID: "h1", Cores: 4}},
				Services: []Service{
					{ID: "q", Kind: "queue", Replicas: 1, Model: "cpu", Endpoints: []Endpoint{{Path: "/test"}}},
				},
				Workload: []WorkloadPattern{{From: "client", To: "q:/test", Arrival: ArrivalSpec{Type: "poisson", RateRPS: 10}}},
			},
			expectError: true,
		},
		{
			name: "Invalid downstream_fraction_cpu high",
			scenario: &Scenario{
				Hosts: []Host{{ID: "h1", Cores: 4}},
				Services: []Service{
					{
						ID: "svc1", Replicas: 1, Model: "cpu",
						Endpoints: []Endpoint{
							{
								Path: "/a",
								Downstream: []DownstreamCall{
									{To: "svc2:/b", DownstreamFractionCPU: 1.1},
								},
							},
						},
					},
					{ID: "svc2", Replicas: 1, Model: "cpu", Endpoints: []Endpoint{{Path: "/b"}}},
				},
				Workload: []WorkloadPattern{{From: "client", To: "svc1:/a", Arrival: ArrivalSpec{Type: "poisson", RateRPS: 10}}},
			},
			expectError: true,
		},
		{
			name: "Invalid downstream_fraction_cpu negative",
			scenario: &Scenario{
				Hosts: []Host{{ID: "h1", Cores: 4}},
				Services: []Service{
					{
						ID: "svc1", Replicas: 1, Model: "cpu",
						Endpoints: []Endpoint{
							{
								Path: "/a",
								Downstream: []DownstreamCall{
									{To: "svc2:/b", DownstreamFractionCPU: -0.01},
								},
							},
						},
					},
					{ID: "svc2", Replicas: 1, Model: "cpu", Endpoints: []Endpoint{{Path: "/b"}}},
				},
				Workload: []WorkloadPattern{{From: "client", To: "svc1:/a", Arrival: ArrivalSpec{Type: "poisson", RateRPS: 10}}},
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

func TestValidateScenarioBurstAliasNormalizes(t *testing.T) {
	s := &Scenario{
		Hosts: []Host{{ID: "h1", Cores: 4}},
		Services: []Service{
			{ID: "svc1", Replicas: 1, Model: "cpu", Endpoints: []Endpoint{{Path: "/test"}}},
		},
		Workload: []WorkloadPattern{
			{From: "client", To: "svc1:/test", Arrival: ArrivalSpec{Type: "burst", RateRPS: 10}},
		},
	}
	if err := validateScenario(s); err != nil {
		t.Fatalf("validateScenario: %v", err)
	}
	if s.Workload[0].Arrival.Type != "bursty" {
		t.Fatalf("burst alias: got type %q want bursty", s.Workload[0].Arrival.Type)
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

func TestValidateScenarioQueueServiceWithBehavior(t *testing.T) {
	s := &Scenario{
		Hosts: []Host{{ID: "h1", Cores: 4}},
		Services: []Service{
			{ID: "consumer", Replicas: 1, Model: "cpu", Endpoints: []Endpoint{{Path: "/handle"}}},
			{
				ID: "mq", Kind: "queue", Replicas: 1, Model: "cpu",
				Behavior: &ServiceBehavior{
					Queue: &QueueBehavior{ConsumerTarget: "consumer:/handle"},
				},
				Endpoints: []Endpoint{{Path: "/orders"}},
			},
		},
		Workload: []WorkloadPattern{{From: "client", To: "mq:/orders", Arrival: ArrivalSpec{Type: "poisson", RateRPS: 1}}},
	}
	if err := validateScenario(s); err != nil {
		t.Fatalf("expected valid queue service: %v", err)
	}
}

func TestValidateScenarioQueueInvalidDropPolicy(t *testing.T) {
	s := &Scenario{
		Hosts: []Host{{ID: "h1", Cores: 4}},
		Services: []Service{
			{ID: "consumer", Replicas: 1, Model: "cpu", Endpoints: []Endpoint{{Path: "/handle"}}},
			{
				ID: "mq", Kind: "queue", Replicas: 1, Model: "cpu",
				Behavior: &ServiceBehavior{
					Queue: &QueueBehavior{ConsumerTarget: "consumer:/handle", DropPolicy: "drop_all"},
				},
				Endpoints: []Endpoint{{Path: "/orders"}},
			},
		},
		Workload: []WorkloadPattern{{From: "client", To: "mq:/orders", Arrival: ArrivalSpec{Type: "poisson", RateRPS: 1}}},
	}
	if err := validateScenario(s); err == nil {
		t.Fatal("expected error for invalid drop_policy")
	}
}

func TestValidateScenarioTopicServiceValid(t *testing.T) {
	s := &Scenario{
		Hosts: []Host{{ID: "h1", Cores: 4}},
		Services: []Service{
			{ID: "consumer", Replicas: 1, Model: "cpu", Endpoints: []Endpoint{{Path: "/handle", MeanCPUMs: 1, NetLatencyMs: LatencySpec{Mean: 0, Sigma: 0}}}},
			{
				ID: "evt", Kind: "topic", Replicas: 1, Model: "cpu",
				Behavior: &ServiceBehavior{
					Topic: &TopicBehavior{
						Subscribers: []TopicSubscriber{
							{ConsumerGroup: "g1", ConsumerTarget: "consumer:/handle", DropPolicy: "reject"},
						},
					},
				},
				Endpoints: []Endpoint{{Path: "/events", MeanCPUMs: 1, NetLatencyMs: LatencySpec{Mean: 0, Sigma: 0}}},
			},
		},
		Workload: []WorkloadPattern{{From: "client", To: "consumer:/handle", Arrival: ArrivalSpec{Type: "poisson", RateRPS: 1}}},
	}
	if err := validateScenario(s); err != nil {
		t.Fatalf("expected valid topic service: %v", err)
	}
}

func TestValidateScenarioTopicInvalidDropPolicy(t *testing.T) {
	s := &Scenario{
		Hosts: []Host{{ID: "h1", Cores: 4}},
		Services: []Service{
			{ID: "consumer", Replicas: 1, Model: "cpu", Endpoints: []Endpoint{{Path: "/handle", MeanCPUMs: 1, NetLatencyMs: LatencySpec{Mean: 0, Sigma: 0}}}},
			{
				ID: "evt", Kind: "topic", Replicas: 1, Model: "cpu",
				Behavior: &ServiceBehavior{
					Topic: &TopicBehavior{
						Subscribers: []TopicSubscriber{
							{ConsumerGroup: "g1", ConsumerTarget: "consumer:/handle", DropPolicy: "drop_all"},
						},
					},
				},
				Endpoints: []Endpoint{{Path: "/events", MeanCPUMs: 1, NetLatencyMs: LatencySpec{Mean: 0, Sigma: 0}}},
			},
		},
		Workload: []WorkloadPattern{{From: "client", To: "consumer:/handle", Arrival: ArrivalSpec{Type: "poisson", RateRPS: 1}}},
	}
	if err := validateScenario(s); err == nil {
		t.Fatal("expected error for invalid topic drop_policy")
	}
}

func TestValidateScenarioTopicDuplicateConsumerGroup(t *testing.T) {
	s := &Scenario{
		Hosts: []Host{{ID: "h1", Cores: 4}},
		Services: []Service{
			{ID: "consumer", Replicas: 1, Model: "cpu", Endpoints: []Endpoint{{Path: "/handle", MeanCPUMs: 1, NetLatencyMs: LatencySpec{Mean: 0, Sigma: 0}}}},
			{
				ID: "evt", Kind: "topic", Replicas: 1, Model: "cpu",
				Behavior: &ServiceBehavior{
					Topic: &TopicBehavior{
						Subscribers: []TopicSubscriber{
							{ConsumerGroup: "g1", ConsumerTarget: "consumer:/handle"},
							{ConsumerGroup: "g1", ConsumerTarget: "consumer:/handle"},
						},
					},
				},
				Endpoints: []Endpoint{{Path: "/events", MeanCPUMs: 1, NetLatencyMs: LatencySpec{Mean: 0, Sigma: 0}}},
			},
		},
		Workload: []WorkloadPattern{{From: "client", To: "consumer:/handle", Arrival: ArrivalSpec{Type: "poisson", RateRPS: 1}}},
	}
	if err := validateScenario(s); err == nil {
		t.Fatal("expected error for duplicate consumer_group")
	}
}

func TestValidateScenarioQueueInvalidConcurrencyBounds(t *testing.T) {
	s := &Scenario{
		Hosts: []Host{{ID: "h1", Cores: 4}},
		Services: []Service{
			{ID: "consumer", Replicas: 1, Model: "cpu", Endpoints: []Endpoint{{Path: "/handle", MeanCPUMs: 1, NetLatencyMs: LatencySpec{Mean: 0, Sigma: 0}}}},
			{
				ID: "mq", Kind: "queue", Replicas: 1, Model: "cpu",
				Behavior: &ServiceBehavior{Queue: &QueueBehavior{
					ConsumerTarget: "consumer:/handle", ConsumerConcurrency: 2, MinConsumerConcurrency: 3, MaxConsumerConcurrency: 2,
				}},
				Endpoints: []Endpoint{{Path: "/orders", MeanCPUMs: 1, NetLatencyMs: LatencySpec{Mean: 0, Sigma: 0}}},
			},
		},
		Workload: []WorkloadPattern{{From: "client", To: "mq:/orders", Arrival: ArrivalSpec{Type: "poisson", RateRPS: 1}}},
	}
	if err := validateScenario(s); err == nil {
		t.Fatal("expected error for invalid queue consumer concurrency bounds")
	}
}

func TestValidateScenarioTopicInvalidConcurrencyBounds(t *testing.T) {
	s := &Scenario{
		Hosts: []Host{{ID: "h1", Cores: 4}},
		Services: []Service{
			{ID: "consumer", Replicas: 1, Model: "cpu", Endpoints: []Endpoint{{Path: "/handle", MeanCPUMs: 1, NetLatencyMs: LatencySpec{Mean: 0, Sigma: 0}}}},
			{
				ID: "evt", Kind: "topic", Replicas: 1, Model: "cpu",
				Behavior: &ServiceBehavior{
					Topic: &TopicBehavior{
						Subscribers: []TopicSubscriber{
							{ConsumerGroup: "g1", ConsumerTarget: "consumer:/handle", ConsumerConcurrency: 2, MinConsumerConcurrency: 3, MaxConsumerConcurrency: 2},
						},
					},
				},
				Endpoints: []Endpoint{{Path: "/events", MeanCPUMs: 1, NetLatencyMs: LatencySpec{Mean: 0, Sigma: 0}}},
			},
		},
		Workload: []WorkloadPattern{{From: "client", To: "consumer:/handle", Arrival: ArrivalSpec{Type: "poisson", RateRPS: 1}}},
	}
	if err := validateScenario(s); err == nil {
		t.Fatal("expected error for invalid topic subscriber concurrency bounds")
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

func TestValidateScenarioQueueNegativeAckTimeoutRawField(t *testing.T) {
	s := &Scenario{
		Hosts: []Host{{ID: "h1", Cores: 4}},
		Services: []Service{
			{ID: "worker", Replicas: 1, Model: "cpu", Endpoints: []Endpoint{{Path: "/handle", MeanCPUMs: 1, NetLatencyMs: LatencySpec{Mean: 0, Sigma: 0}}}},
			{
				ID: "mq", Kind: "queue", Replicas: 1, Model: "cpu",
				Behavior: &ServiceBehavior{Queue: &QueueBehavior{
					ConsumerTarget: "worker:/handle", AckTimeoutMs: -1,
				}},
				Endpoints: []Endpoint{{Path: "/orders", MeanCPUMs: 1, NetLatencyMs: LatencySpec{Mean: 0, Sigma: 0}}},
			},
		},
		Workload: []WorkloadPattern{{From: "client", To: "mq:/orders", Arrival: ArrivalSpec{Type: "poisson", RateRPS: 1}}},
	}
	if err := validateScenario(s); err == nil {
		t.Fatal("expected error for negative queue ack_timeout_ms")
	}
}

func TestValidateScenarioQueueNegativeDeliveryLatencyRawField(t *testing.T) {
	s := &Scenario{
		Hosts: []Host{{ID: "h1", Cores: 4}},
		Services: []Service{
			{ID: "worker", Replicas: 1, Model: "cpu", Endpoints: []Endpoint{{Path: "/handle", MeanCPUMs: 1, NetLatencyMs: LatencySpec{Mean: 0, Sigma: 0}}}},
			{
				ID: "mq", Kind: "queue", Replicas: 1, Model: "cpu",
				Behavior: &ServiceBehavior{Queue: &QueueBehavior{
					ConsumerTarget: "worker:/handle", DeliveryLatencyMs: LatencySpec{Mean: -1, Sigma: 0},
				}},
				Endpoints: []Endpoint{{Path: "/orders", MeanCPUMs: 1, NetLatencyMs: LatencySpec{Mean: 0, Sigma: 0}}},
			},
		},
		Workload: []WorkloadPattern{{From: "client", To: "mq:/orders", Arrival: ArrivalSpec{Type: "poisson", RateRPS: 1}}},
	}
	if err := validateScenario(s); err == nil {
		t.Fatal("expected error for negative queue delivery latency")
	}
}

func TestValidateScenarioTopicNegativeCapacityRawField(t *testing.T) {
	s := &Scenario{
		Hosts: []Host{{ID: "h1", Cores: 4}},
		Services: []Service{
			{ID: "consumer", Replicas: 1, Model: "cpu", Endpoints: []Endpoint{{Path: "/handle", MeanCPUMs: 1, NetLatencyMs: LatencySpec{Mean: 0, Sigma: 0}}}},
			{
				ID: "evt", Kind: "topic", Replicas: 1, Model: "cpu",
				Behavior: &ServiceBehavior{Topic: &TopicBehavior{
					Capacity: -2,
					Subscribers: []TopicSubscriber{
						{ConsumerGroup: "g1", ConsumerTarget: "consumer:/handle", ConsumerConcurrency: 1},
					},
				}},
				Endpoints: []Endpoint{{Path: "/events", MeanCPUMs: 1, NetLatencyMs: LatencySpec{Mean: 0, Sigma: 0}}},
			},
		},
		Workload: []WorkloadPattern{{From: "client", To: "consumer:/handle", Arrival: ArrivalSpec{Type: "poisson", RateRPS: 1}}},
	}
	if err := validateScenario(s); err == nil {
		t.Fatal("expected error for negative topic capacity")
	}
}

func TestValidateScenarioTopicNegativeDeliveryLatencyRawField(t *testing.T) {
	s := &Scenario{
		Hosts: []Host{{ID: "h1", Cores: 4}},
		Services: []Service{
			{ID: "consumer", Replicas: 1, Model: "cpu", Endpoints: []Endpoint{{Path: "/handle", MeanCPUMs: 1, NetLatencyMs: LatencySpec{Mean: 0, Sigma: 0}}}},
			{
				ID: "evt", Kind: "topic", Replicas: 1, Model: "cpu",
				Behavior: &ServiceBehavior{Topic: &TopicBehavior{
					DeliveryLatencyMs: LatencySpec{Mean: -1, Sigma: 0},
					Subscribers: []TopicSubscriber{
						{ConsumerGroup: "g1", ConsumerTarget: "consumer:/handle", ConsumerConcurrency: 1},
					},
				}},
				Endpoints: []Endpoint{{Path: "/events", MeanCPUMs: 1, NetLatencyMs: LatencySpec{Mean: 0, Sigma: 0}}},
			},
		},
		Workload: []WorkloadPattern{{From: "client", To: "consumer:/handle", Arrival: ArrivalSpec{Type: "poisson", RateRPS: 1}}},
	}
	if err := validateScenario(s); err == nil {
		t.Fatal("expected error for negative topic delivery latency")
	}
}

func TestValidateNetworkConfigRejectsNegativePairLatency(t *testing.T) {
	s := &Scenario{
		Hosts: []Host{{ID: "h1", Cores: 4, MemoryGB: 8, Zone: "a"}},
		Services: []Service{{
			ID: "svc", Replicas: 1, Model: "cpu", Endpoints: []Endpoint{{Path: "/p", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: LatencySpec{Mean: 0, Sigma: 0}}},
		}},
		Network: &NetworkConfig{
			CrossZoneLatencyMs: map[string]map[string]LatencySpec{
				"zone-a": {"zone-b": {Mean: -1, Sigma: 0}},
			},
		},
		Workload: []WorkloadPattern{{From: "c", To: "svc:/p", Arrival: ArrivalSpec{Type: "poisson", RateRPS: 1}}},
	}
	if err := validateScenario(s); err == nil {
		t.Fatal("expected error for negative cross_zone_latency_ms pair")
	}
}

func TestValidateScenarioRejectsExternalNetworkLatencyOnNonExternalKind(t *testing.T) {
	ls := LatencySpec{Mean: 5, Sigma: 0}
	s := &Scenario{
		Hosts: []Host{{ID: "h1", Cores: 4}},
		Services: []Service{{
			ID: "svc1", Kind: "service", Replicas: 1, Model: "cpu",
			ExternalNetworkLatencyMs: &ls,
			Endpoints: []Endpoint{{Path: "/p", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: LatencySpec{Mean: 0, Sigma: 0}}},
		}},
		Workload: []WorkloadPattern{{From: "c", To: "svc1:/p", Arrival: ArrivalSpec{Type: "poisson", RateRPS: 1}}},
	}
	if err := validateScenario(s); err == nil {
		t.Fatal("expected error for external_network_latency_ms on non-external service")
	}
}
