package resource

import (
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestGetInstancePlacementsInitialStableOrder(t *testing.T) {
	m := NewManager()
	scenario := &config.Scenario{
		Hosts: []config.Host{
			{ID: "host-b", Cores: 4, Zone: "zone-b", Labels: map[string]string{"rack": "r2"}},
			{ID: "host-a", Cores: 8, Zone: "zone-a", Labels: map[string]string{"rack": "r1"}},
		},
		Services: []config.Service{
			{
				ID:       "svc1",
				Replicas: 2,
				Model:    "cpu",
				Endpoints: []config.Endpoint{
					{Path: "/test", MeanCPUMs: 10, CPUSigmaMs: 2},
				},
			},
		},
	}
	if err := m.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
	}

	t0 := time.Unix(1, 0)
	pl := m.GetInstancePlacements(t0)
	if len(pl) != 2 {
		t.Fatalf("expected 2 placements, got %d", len(pl))
	}
	if pl[0].HostID != "host-a" || pl[1].HostID != "host-b" {
		t.Fatalf("expected host-a then host-b (round-robin among feasible hosts), got %q %q", pl[0].HostID, pl[1].HostID)
	}
	if pl[0].HostZone != "zone-a" || pl[0].HostLabels["rack"] != "r1" {
		t.Fatalf("expected host topology on placement, got %+v", pl[0])
	}
	if pl[0].Lifecycle != "ACTIVE" || pl[1].Lifecycle != "ACTIVE" {
		t.Fatalf("expected ACTIVE lifecycle, got %+v %+v", pl[0].Lifecycle, pl[1].Lifecycle)
	}
}

func TestGetInstancePlacementsScaleOut(t *testing.T) {
	m := NewManager()
	scenario := &config.Scenario{
		Hosts: []config.Host{
			{ID: "host-1", Cores: 4},
			{ID: "host-2", Cores: 8},
		},
		Services: []config.Service{
			{
				ID:       "svc1",
				Replicas: 2,
				Model:    "cpu",
				Endpoints: []config.Endpoint{
					{Path: "/test", MeanCPUMs: 10, CPUSigmaMs: 2},
				},
			},
		},
	}
	if err := m.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
	}
	if err := m.ScaleService("svc1", 4); err != nil {
		t.Fatalf("ScaleService: %v", err)
	}
	pl := m.GetInstancePlacements(time.Unix(0, 0))
	if len(pl) != 4 {
		t.Fatalf("expected 4 placements, got %d", len(pl))
	}
	for i := 1; i < len(pl); i++ {
		prev := pl[i-1].HostID + "\x00" + pl[i-1].InstanceID
		cur := pl[i].HostID + "\x00" + pl[i].InstanceID
		if prev >= cur {
			t.Fatalf("placements not stably sorted at %d: %+v", i, pl)
		}
	}
}

func TestGetInstancePlacementsDrainingAndRemoved(t *testing.T) {
	m := NewManager()
	scenario := &config.Scenario{
		Hosts: []config.Host{
			{ID: "host-1", Cores: 4},
			{ID: "host-2", Cores: 8},
		},
		Services: []config.Service{
			{
				ID:       "svc1",
				Replicas: 2,
				Model:    "cpu",
				Endpoints: []config.Endpoint{
					{Path: "/test", MeanCPUMs: 10, CPUSigmaMs: 2},
				},
			},
		},
	}
	if err := m.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
	}
	if err := m.ScaleService("svc1", 1); err != nil {
		t.Fatalf("ScaleService: %v", err)
	}
	pl := m.GetInstancePlacements(time.Unix(0, 0))
	if len(pl) != 2 {
		t.Fatalf("expected 2 instances while draining, got %d", len(pl))
	}
	var draining int
	for _, p := range pl {
		if p.Lifecycle == "DRAINING" {
			draining++
		}
	}
	if draining != 1 {
		t.Fatalf("expected 1 DRAINING placement, got %d (%+v)", draining, pl)
	}

	m.ProcessDrainingInstances(time.Unix(0, 0))
	pl = m.GetInstancePlacements(time.Unix(0, 0))
	if len(pl) != 1 {
		t.Fatalf("expected 1 placement after idle drain, got %d (%+v)", len(pl), pl)
	}
	if pl[0].Lifecycle != "ACTIVE" {
		t.Fatalf("expected remaining instance ACTIVE, got %+v", pl[0])
	}
}
