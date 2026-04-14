package resource

import (
	"testing"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestInitializeFromScenarioRejectsOverCommittedSingleHost(t *testing.T) {
	m := NewManager()
	scenario := &config.Scenario{
		Hosts: []config.Host{
			{ID: "h1", Cores: 1},
		},
		Services: []config.Service{
			{
				ID:        "svc1",
				Replicas:  2,
				CPUCores:  1,
				MemoryMB:  128,
				Model:     "cpu",
				Endpoints: []config.Endpoint{{Path: "/x", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}},
			},
		},
	}
	if err := m.InitializeFromScenario(scenario); err == nil {
		t.Fatal("expected placement failure when two 1-core replicas exceed 1 core host")
	}
}

func TestInitializeFromScenarioFitsWithSecondHost(t *testing.T) {
	m := NewManager()
	scenario := &config.Scenario{
		Hosts: []config.Host{
			{ID: "h1", Cores: 1},
			{ID: "h2", Cores: 1},
		},
		Services: []config.Service{
			{
				ID:        "svc1",
				Replicas:  2,
				CPUCores:  1,
				MemoryMB:  128,
				Model:     "cpu",
				Endpoints: []config.Endpoint{{Path: "/x", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}},
			},
		},
	}
	if err := m.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
	}
	if n := len(m.instances); n != 2 {
		t.Fatalf("expected 2 instances, got %d", n)
	}
}

func TestScaleServiceOutFailsWhenNoHostFits(t *testing.T) {
	m := NewManager()
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 1}},
		Services: []config.Service{
			{
				ID:        "svc1",
				Replicas:  1,
				CPUCores:  1,
				MemoryMB:  128,
				Model:     "cpu",
				Endpoints: []config.Endpoint{{Path: "/x", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}},
			},
		},
	}
	if err := m.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
	}
	if err := m.ScaleService("svc1", 2); err == nil {
		t.Fatal("expected scale-out to fail when host is full")
	}
}

func TestScaleOutAfterHostScaleOut(t *testing.T) {
	m := NewManager()
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 1}},
		Services: []config.Service{
			{
				ID:        "svc1",
				Replicas:  1,
				CPUCores:  1,
				MemoryMB:  128,
				Model:     "cpu",
				Endpoints: []config.Endpoint{{Path: "/x", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}},
			},
		},
	}
	if err := m.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
	}
	if err := m.ScaleService("svc1", 2); err == nil {
		t.Fatal("expected scale-out to fail before adding host")
	}
	if err := m.ScaleOutHosts(2); err != nil {
		t.Fatalf("ScaleOutHosts: %v", err)
	}
	if err := m.ScaleService("svc1", 2); err != nil {
		t.Fatalf("ScaleService after host scale-out: %v", err)
	}
	if m.ActiveReplicas("svc1") != 2 {
		t.Fatalf("expected 2 active replicas, got %d", m.ActiveReplicas("svc1"))
	}
}
