package interaction

import (
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

func TestNewManager(t *testing.T) {
	scenario := &config.Scenario{
		Services: []config.Service{
			{
				ID: "svc1",
				Endpoints: []config.Endpoint{
					{Path: "/test"},
				},
			},
		},
	}

	manager, err := NewManager(scenario)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	if manager == nil {
		t.Fatalf("expected non-nil manager")
	}

	if manager.GetGraph() == nil {
		t.Fatalf("expected non-nil graph")
	}
}

func TestManagerGetDownstreamCalls(t *testing.T) {
	scenario := &config.Scenario{
		Services: []config.Service{
			{
				ID: "svc1",
				Endpoints: []config.Endpoint{
					{
						Path: "/test",
						Downstream: []config.DownstreamCall{
							{To: "svc2:/api", CallCountMean: 1.0},
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

	manager, err := NewManager(scenario)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	calls, err := manager.GetDownstreamCalls("svc1", "/test")
	if err != nil {
		t.Fatalf("failed to get downstream calls: %v", err)
	}

	if len(calls) == 0 {
		t.Fatalf("expected at least one downstream call")
	}

	if calls[0].ServiceID != "svc2" {
		t.Fatalf("expected downstream service svc2, got %s", calls[0].ServiceID)
	}
}

func TestManagerCreateDownstreamRequest(t *testing.T) {
	scenario := &config.Scenario{
		Services: []config.Service{
			{
				ID: "svc1",
				Endpoints: []config.Endpoint{
					{Path: "/test"},
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

	manager, err := NewManager(scenario)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	parentRequest := &models.Request{
		ID:          "req-1",
		TraceID:     "trace-1",
		ServiceName: "svc1",
		Endpoint:    "/test",
		Status:      models.RequestStatusCompleted,
		ArrivalTime: time.Now(),
		Metadata:    make(map[string]interface{}),
	}

	resolvedCall := ResolvedCall{
		ServiceID: "svc2",
		Path:      "/api",
	}

	downstreamRequest, err := manager.CreateDownstreamRequest(parentRequest, resolvedCall)
	if err != nil {
		t.Fatalf("failed to create downstream request: %v", err)
	}

	if downstreamRequest == nil {
		t.Fatalf("expected non-nil downstream request")
	}

	if downstreamRequest.TraceID != parentRequest.TraceID {
		t.Fatalf("expected same trace ID")
	}

	if downstreamRequest.ParentID != parentRequest.ID {
		t.Fatalf("expected parent ID to be set")
	}

	if downstreamRequest.ServiceName != "svc2" {
		t.Fatalf("expected service name svc2, got %s", downstreamRequest.ServiceName)
	}

	if downstreamRequest.Endpoint != "/api" {
		t.Fatalf("expected endpoint /api, got %s", downstreamRequest.Endpoint)
	}
}

func TestManagerValidateEndpoint(t *testing.T) {
	scenario := &config.Scenario{
		Services: []config.Service{
			{
				ID: "svc1",
				Endpoints: []config.Endpoint{
					{Path: "/test"},
				},
			},
		},
	}

	manager, err := NewManager(scenario)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	err = manager.ValidateEndpoint("svc1", "/test")
	if err != nil {
		t.Fatalf("expected valid endpoint, got error: %v", err)
	}

	err = manager.ValidateEndpoint("svc1", "/nonexistent")
	if err == nil {
		t.Fatalf("expected error for nonexistent endpoint")
	}
}

