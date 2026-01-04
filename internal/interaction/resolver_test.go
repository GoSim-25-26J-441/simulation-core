package interaction

import (
	"testing"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestParseDownstreamTarget(t *testing.T) {
	tests := []struct {
		name        string
		target      string
		expectError bool
		serviceID   string
		path        string
	}{
		{"valid with path", "svc1:/path", false, "svc1", "/path"},
		{"valid with colon in path", "svc1:/api/v1/test", false, "svc1", "/api/v1/test"},
		{"valid without path", "svc1", false, "svc1", "/"},
		{"invalid empty", "", true, "", ""},
		{"invalid empty service ID", ":path", true, "", ""},
		{"invalid empty path", "service:", true, "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serviceID, path, err := ParseDownstreamTarget(tt.target)
			if tt.expectError {
				if err == nil {
					t.Fatalf("expected error")
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if serviceID != tt.serviceID {
					t.Fatalf("expected serviceID %s, got %s", tt.serviceID, serviceID)
				}
				if path != tt.path {
					t.Fatalf("expected path %s, got %s", tt.path, path)
				}
			}
		})
	}
}

func TestGraphResolveDownstreamCalls(t *testing.T) {
	scenario := &config.Scenario{
		Services: []config.Service{
			{
				ID: "svc1",
				Endpoints: []config.Endpoint{
					{
						Path: "/test",
						Downstream: []config.DownstreamCall{
							{To: "svc2:/api"},
							{To: "svc3"},
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
			{
				ID: "svc3",
				Endpoints: []config.Endpoint{
					{Path: "/"},
				},
			},
		},
	}

	graph, err := NewGraph(scenario)
	if err != nil {
		t.Fatalf("failed to create graph: %v", err)
	}

	calls, err := graph.ResolveDownstreamCalls("svc1", "/test")
	if err != nil {
		t.Fatalf("failed to resolve downstream calls: %v", err)
	}

	if len(calls) != 2 {
		t.Fatalf("expected 2 downstream calls, got %d", len(calls))
	}

	if calls[0].ServiceID != "svc2" || calls[0].Path != "/api" {
		t.Fatalf("expected first call to be svc2:/api, got %s:%s", calls[0].ServiceID, calls[0].Path)
	}

	if calls[1].ServiceID != "svc3" || calls[1].Path != "/" {
		t.Fatalf("expected second call to be svc3:/, got %s:%s", calls[1].ServiceID, calls[1].Path)
	}
}
