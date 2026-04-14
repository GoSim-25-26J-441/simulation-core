package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestScenarioV2YAMLRoundTrip(t *testing.T) {
	y := `
metadata:
  schema_version: "0.2.0"
hosts:
  - id: host-1
    cores: 8
    memory_gb: 32
services:
  - id: api-gateway
    kind: api_gateway
    role: ingress
    model: cpu
    replicas: 2
    cpu_cores: 1
    memory_mb: 512
    scaling:
      horizontal: true
      vertical_cpu: true
      vertical_memory: true
    endpoints:
      - path: /REST
        mean_cpu_ms: 4
        cpu_sigma_ms: 1
        net_latency_ms: { mean: 2, sigma: 0.5 }
        downstream:
          - to: customer-core:/default
            mode: sync
            kind: rest
            probability: 1.0
            timeout_ms: 500
workload:
  - from: external
    source_kind: client
    traffic_class: ingress
    to: api-gateway:/REST
    arrival:
      type: poisson
      rate_rps: 680
`
	var s Scenario
	if err := yaml.NewDecoder(strings.NewReader(y)).Decode(&s); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if s.Metadata == nil || s.Metadata.SchemaVersion != "0.2.0" {
		t.Fatalf("metadata: %+v", s.Metadata)
	}
	if len(s.Services) != 1 || s.Services[0].Kind != "api_gateway" || s.Services[0].Scaling == nil || !s.Services[0].Scaling.Horizontal {
		t.Fatalf("service: %+v", s.Services[0])
	}
	if len(s.Services[0].Endpoints[0].Downstream) != 1 || s.Services[0].Endpoints[0].Downstream[0].Mode != "sync" {
		t.Fatalf("downstream: %+v", s.Services[0].Endpoints[0].Downstream)
	}
	if s.Workload[0].TrafficClass != "ingress" || s.Workload[0].SourceKind != "client" {
		t.Fatalf("workload: %+v", s.Workload[0])
	}
}
