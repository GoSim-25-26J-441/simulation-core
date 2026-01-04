package interaction

import (
	"fmt"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

// Graph represents a service dependency graph (DAG)
type Graph struct {
	services  map[string]*config.Service
	endpoints map[string]*config.Endpoint // "serviceID:path" -> endpoint
	edges     map[string][]Edge           // "serviceID:path" -> []Edge
}

// Edge represents a directed edge in the service graph
type Edge struct {
	FromServiceID string
	FromPath      string
	ToServiceID   string
	ToPath        string
	Call          config.DownstreamCall
}

// NewGraph creates a new service graph from a scenario
func NewGraph(scenario *config.Scenario) (*Graph, error) {
	g := &Graph{
		services:  make(map[string]*config.Service),
		endpoints: make(map[string]*config.Endpoint),
		edges:     make(map[string][]Edge),
	}

	// First pass: Build service and endpoint maps
	for i := range scenario.Services {
		svc := &scenario.Services[i]
		g.services[svc.ID] = svc

		for j := range svc.Endpoints {
			ep := &svc.Endpoints[j]
			key := endpointKey(svc.ID, ep.Path)
			g.endpoints[key] = ep
		}
	}

	// Second pass: Build edges for downstream calls (now services are in the map)
	for i := range scenario.Services {
		svc := &scenario.Services[i]
		for j := range svc.Endpoints {
			ep := &svc.Endpoints[j]
			key := endpointKey(svc.ID, ep.Path)

			// Build edges for downstream calls
			for k := range ep.Downstream {
				ds := ep.Downstream[k]
				edge, err := g.createEdge(svc.ID, ep.Path, ds)
				if err != nil {
					return nil, fmt.Errorf("failed to create edge from %s:%s: %w", svc.ID, ep.Path, err)
				}
				g.edges[key] = append(g.edges[key], edge)
			}
		}
	}

	// Validate graph is acyclic
	if err := g.validateAcyclic(); err != nil {
		return nil, fmt.Errorf("service graph contains cycles: %w", err)
	}

	return g, nil
}

// createEdge creates an edge from a downstream call specification
func (g *Graph) createEdge(fromServiceID, fromPath string, call config.DownstreamCall) (Edge, error) {
	toServiceID, toPath, err := ParseDownstreamTarget(call.To)
	if err != nil {
		return Edge{}, fmt.Errorf("invalid downstream target %q: %w", call.To, err)
	}

	// Validate that target service exists
	if _, exists := g.services[toServiceID]; !exists {
		return Edge{}, fmt.Errorf("downstream service %q does not exist", toServiceID)
	}

	return Edge{
		FromServiceID: fromServiceID,
		FromPath:      fromPath,
		ToServiceID:   toServiceID,
		ToPath:        toPath,
		Call:          call,
	}, nil
}

// validateAcyclic checks if the graph is acyclic (DAG)
func (g *Graph) validateAcyclic() error {
	visited := make(map[string]bool)
	recStack := make(map[string]bool)

	// Iterate over all endpoints to ensure complete graph traversal
	for key := range g.endpoints {
		if !visited[key] {
			if err := g.dfs(key, visited, recStack); err != nil {
				return err
			}
		}
	}

	return nil
}

// dfs performs depth-first search to detect cycles
func (g *Graph) dfs(key string, visited, recStack map[string]bool) error {
	visited[key] = true
	recStack[key] = true

	for _, edge := range g.edges[key] {
		toKey := endpointKey(edge.ToServiceID, edge.ToPath)
		if !visited[toKey] {
			if err := g.dfs(toKey, visited, recStack); err != nil {
				return err
			}
		} else if recStack[toKey] {
			return fmt.Errorf("cycle detected: %s -> %s", key, toKey)
		}
	}

	recStack[key] = false
	return nil
}

// GetService returns a service by ID
func (g *Graph) GetService(serviceID string) (*config.Service, bool) {
	svc, ok := g.services[serviceID]
	return svc, ok
}

// GetEndpoint returns an endpoint by service ID and path
func (g *Graph) GetEndpoint(serviceID, path string) (*config.Endpoint, bool) {
	key := endpointKey(serviceID, path)
	ep, ok := g.endpoints[key]
	return ep, ok
}

// GetDownstreamEdges returns all downstream edges from a given endpoint
func (g *Graph) GetDownstreamEdges(serviceID, path string) []Edge {
	key := endpointKey(serviceID, path)
	return g.edges[key]
}

// GetAllServices returns all services in the graph
func (g *Graph) GetAllServices() map[string]*config.Service {
	return g.services
}

// endpointKey creates a key for an endpoint
func endpointKey(serviceID, path string) string {
	return fmt.Sprintf("%s:%s", serviceID, path)
}
