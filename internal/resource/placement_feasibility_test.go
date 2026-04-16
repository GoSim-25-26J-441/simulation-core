package resource

import (
	"strings"
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

func TestInitializeFromScenarioRespectsPlacementZonesAndLabels(t *testing.T) {
	m := NewManager()
	scenario := &config.Scenario{
		Hosts: []config.Host{
			{ID: "h1", Cores: 2, Zone: "zone-a", Labels: map[string]string{"rack": "r1"}},
			{ID: "h2", Cores: 2, Zone: "zone-b", Labels: map[string]string{"rack": "r2"}},
		},
		Services: []config.Service{
			{
				ID:       "svc1",
				Replicas: 1,
				CPUCores: 1,
				MemoryMB: 64,
				Model:    "cpu",
				Placement: &config.PlacementPolicy{
					AffinityZones:      []string{"zone-b"},
					RequiredHostLabels: map[string]string{"rack": "r2"},
				},
				Endpoints: []config.Endpoint{{Path: "/x", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}},
			},
		},
	}
	if err := m.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
	}
	inst := m.GetInstancesForService("svc1")
	if len(inst) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(inst))
	}
	if inst[0].HostID() != "h2" {
		t.Fatalf("expected placement on h2, got %s", inst[0].HostID())
	}
}

func TestScaleServiceRespectsAntiAffinityServices(t *testing.T) {
	m := NewManager()
	scenario := &config.Scenario{
		Hosts: []config.Host{
			{ID: "h1", Cores: 2},
			{ID: "h2", Cores: 2},
		},
		Services: []config.Service{
			{
				ID:       "svcA",
				Replicas: 1,
				CPUCores: 1,
				MemoryMB: 64,
				Model:    "cpu",
				Endpoints: []config.Endpoint{{Path: "/a", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}},
			},
			{
				ID:       "svcB",
				Replicas: 1,
				CPUCores: 1,
				MemoryMB: 64,
				Model:    "cpu",
				Placement: &config.PlacementPolicy{
					AntiAffinityServices: []string{"svcA", "svcB"},
				},
				Endpoints: []config.Endpoint{{Path: "/b", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}},
			},
		},
	}
	if err := m.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
	}
	if err := m.ScaleService("svcB", 2); err == nil {
		t.Fatal("expected scale-out to fail due to anti-affinity host exhaustion")
	}
}

func TestScaleServiceRespectsMaxReplicasPerHostAndSpreadAcrossZones(t *testing.T) {
	m := NewManager()
	scenario := &config.Scenario{
		Hosts: []config.Host{
			{ID: "h1", Cores: 4, Zone: "zone-a"},
			{ID: "h2", Cores: 4, Zone: "zone-b"},
		},
		Services: []config.Service{
			{
				ID:       "svc",
				Replicas: 1,
				CPUCores: 1,
				MemoryMB: 64,
				Model:    "cpu",
				Placement: &config.PlacementPolicy{
					RequiredZones:      []string{"zone-a", "zone-b"},
					SpreadAcrossZones:  true,
					MaxReplicasPerHost: 1,
				},
				Endpoints: []config.Endpoint{{Path: "/x", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}},
			},
		},
	}
	if err := m.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
	}
	if err := m.ScaleService("svc", 2); err != nil {
		t.Fatalf("ScaleService: %v", err)
	}
	instances := m.GetInstancesForService("svc")
	if len(instances) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(instances))
	}
	seenZones := map[string]bool{}
	for _, inst := range instances {
		host, ok := m.GetHost(inst.HostID())
		if !ok {
			t.Fatalf("host missing for %s", inst.ID())
		}
		seenZones[host.Zone()] = true
	}
	if !seenZones["zone-a"] || !seenZones["zone-b"] {
		t.Fatalf("expected spread across zone-a/zone-b, got %+v", seenZones)
	}
}

func TestScaleOutHostsForServiceUsesPreferredZoneTemplate(t *testing.T) {
	m := NewManager()
	scenario := &config.Scenario{
		Hosts: []config.Host{
			{ID: "h1", Cores: 2, Zone: "zone-a", Labels: map[string]string{"rack": "r1"}},
			{ID: "h2", Cores: 2, Zone: "zone-b", Labels: map[string]string{"rack": "r2"}},
		},
		Services: []config.Service{
			{
				ID:       "svc",
				Replicas: 1,
				CPUCores: 1,
				MemoryMB: 64,
				Model:    "cpu",
				Placement: &config.PlacementPolicy{
					PreferredZones:      []string{"zone-b"},
					PreferredHostLabels: map[string]string{"rack": "r2"},
				},
				Endpoints: []config.Endpoint{{Path: "/x", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}},
			},
		},
	}
	if err := m.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
	}
	if err := m.ScaleOutHostsForService("svc", 3); err != nil {
		t.Fatalf("ScaleOutHostsForService: %v", err)
	}
	var autoHost *Host
	for _, hid := range m.HostIDs() {
		if strings.HasPrefix(hid, "host-auto-") {
			h, _ := m.GetHost(hid)
			autoHost = h
			break
		}
	}
	if autoHost == nil {
		t.Fatal("expected auto host to be created")
	}
	if autoHost.Zone() != "zone-b" {
		t.Fatalf("expected auto host in preferred zone-b, got %s", autoHost.Zone())
	}
	if autoHost.Labels()["rack"] != "r2" {
		t.Fatalf("expected auto host labels copied from preferred template, got %+v", autoHost.Labels())
	}
}
