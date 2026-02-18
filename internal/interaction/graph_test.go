package interaction

import (
	"testing"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestNewGraph(t *testing.T) {
	scenario := &config.Scenario{
		Services: []config.Service{
			{
				ID: "svc1",
				Endpoints: []config.Endpoint{
					{
						Path: "/test",
						Downstream: []config.DownstreamCall{
							{To: "svc2:/api"},
						},
					},
				},
			},
			{
				ID: "svc2",
				Endpoints: []config.Endpoint{
					{Path: "/api"},
				},
			},
		},
	}

	graph, err := NewGraph(scenario)
	if err != nil {
		t.Fatalf("failed to create graph: %v", err)
	}

	if graph == nil {
		t.Fatalf("expected non-nil graph")
	}

	// Test GetService
	svc, ok := graph.GetService("svc1")
	if !ok {
		t.Fatalf("expected service svc1 to exist")
	}
	if svc.ID != "svc1" {
		t.Fatalf("expected service ID svc1, got %s", svc.ID)
	}

	// Test GetEndpoint
	ep, ok := graph.GetEndpoint("svc1", "/test")
	if !ok {
		t.Fatalf("expected endpoint svc1:/test to exist")
	}
	if ep.Path != "/test" {
		t.Fatalf("expected endpoint path /test, got %s", ep.Path)
	}

	// Test GetDownstreamEdges
	edges := graph.GetDownstreamEdges("svc1", "/test")
	if len(edges) != 1 {
		t.Fatalf("expected 1 downstream edge, got %d", len(edges))
	}
	if edges[0].ToServiceID != "svc2" {
		t.Fatalf("expected downstream service svc2, got %s", edges[0].ToServiceID)
	}
	if edges[0].ToPath != "/api" {
		t.Fatalf("expected downstream path /api, got %s", edges[0].ToPath)
	}
}

func TestNewGraphWithCycle(t *testing.T) {
	scenario := &config.Scenario{
		Services: []config.Service{
			{
				ID: "svc1",
				Endpoints: []config.Endpoint{
					{
						Path: "/test",
						Downstream: []config.DownstreamCall{
							{To: "svc2:/api"},
						},
					},
				},
			},
			{
				ID: "svc2",
				Endpoints: []config.Endpoint{
					{
						Path: "/api",
						Downstream: []config.DownstreamCall{
							{To: "svc1:/test"}, // Creates a cycle
						},
					},
				},
			},
		},
	}

	_, err := NewGraph(scenario)
	if err == nil {
		t.Fatalf("expected error for cyclic graph, got nil")
	}
}

func TestGraphGetDownstreamEdgesEmpty(t *testing.T) {
	scenario := &config.Scenario{
		Services: []config.Service{
			{ID: "svc1", Endpoints: []config.Endpoint{{Path: "/test"}}},
		},
	}
	graph, err := NewGraph(scenario)
	if err != nil {
		t.Fatalf("failed to create graph: %v", err)
	}
	edges := graph.GetDownstreamEdges("svc1", "/test")
	if edges != nil {
		t.Fatalf("expected nil for endpoint with no downstream, got %v", edges)
	}
	edges = graph.GetDownstreamEdges("nonexistent", "/path")
	if edges != nil {
		t.Fatalf("expected nil for nonexistent endpoint")
	}
}

func TestGraphGetAllServices(t *testing.T) {
	scenario := &config.Scenario{
		Services: []config.Service{
			{ID: "svc1", Endpoints: []config.Endpoint{{Path: "/test"}}},
			{ID: "svc2", Endpoints: []config.Endpoint{{Path: "/api"}}},
		},
	}
	graph, err := NewGraph(scenario)
	if err != nil {
		t.Fatalf("failed to create graph: %v", err)
	}
	services := graph.GetAllServices()
	if len(services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(services))
	}
	if _, ok := services["svc1"]; !ok {
		t.Fatalf("expected svc1 in services")
	}
}

func TestNewGraphWithInvalidDownstream(t *testing.T) {
	scenario := &config.Scenario{
		Services: []config.Service{
			{
				ID: "svc1",
				Endpoints: []config.Endpoint{
					{
						Path: "/test",
						Downstream: []config.DownstreamCall{
							{To: "nonexistent:/api"},
						},
					},
				},
			},
		},
	}

	_, err := NewGraph(scenario)
	if err == nil {
		t.Fatalf("expected error for invalid downstream service, got nil")
	}
}
