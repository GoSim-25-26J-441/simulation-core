package interaction

import (
	"fmt"
	"strings"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

// ResolvedCall represents a resolved downstream call
type ResolvedCall struct {
	ServiceID string
	Path      string
	Call      config.DownstreamCall
}

// ParseDownstreamTarget parses a downstream target string in "serviceID:path" or "serviceID" format
func ParseDownstreamTarget(target string) (serviceID, path string, err error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", "", fmt.Errorf("downstream target cannot be empty")
	}

	if strings.Contains(target, ":") {
		parts := strings.SplitN(target, ":", 2)
		if len(parts) != 2 {
			return "", "", fmt.Errorf("invalid downstream target format: %s (expected serviceID:path)", target)
		}

		serviceID = strings.TrimSpace(parts[0])
		path = strings.TrimSpace(parts[1])

		if serviceID == "" {
			return "", "", fmt.Errorf("invalid downstream target format: %s (serviceID must be non-empty)", target)
		}
		if path == "" {
			return "", "", fmt.Errorf("invalid downstream target format: %s (path must be non-empty)", target)
		}
	} else {
		// No colon means just service ID, use default path
		serviceID = target
		path = "/"
	}

	return serviceID, path, nil
}

// ResolveDownstreamCalls resolves all downstream calls for a given endpoint
func (g *Graph) ResolveDownstreamCalls(serviceID, path string) ([]ResolvedCall, error) {
	edges := g.GetDownstreamEdges(serviceID, path)
	if len(edges) == 0 {
		return nil, nil
	}

	resolved := make([]ResolvedCall, 0, len(edges))
	for _, edge := range edges {
		// Validate target service exists
		if _, exists := g.GetService(edge.ToServiceID); !exists {
			continue // Skip invalid downstream calls
		}

		resolved = append(resolved, ResolvedCall{
			ServiceID: edge.ToServiceID,
			Path:      edge.ToPath,
			Call:      edge.Call,
		})
	}

	return resolved, nil
}

// ValidateDownstreamTarget validates that a downstream target exists in the graph
func (g *Graph) ValidateDownstreamTarget(target string) error {
	serviceID, path, err := ParseDownstreamTarget(target)
	if err != nil {
		return err
	}

	if _, exists := g.GetService(serviceID); !exists {
		return fmt.Errorf("downstream service %q does not exist", serviceID)
	}

	// Optionally validate that the endpoint exists
	// For now, we allow calls to non-existent endpoints (they'll fail at runtime)
	// This allows more flexible scenarios
	_, _ = g.GetEndpoint(serviceID, path) // Check if endpoint exists, but don't fail if it doesn't

	return nil
}
