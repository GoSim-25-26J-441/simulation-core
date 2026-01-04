package interaction

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/utils"
)

// Manager manages service interactions and downstream calls
type Manager struct {
	graph             *Graph
	branchingStrategy BranchingStrategy
	rng               *rand.Rand
}

// NewManager creates a new interaction manager with a deterministic seed
func NewManager(scenario *config.Scenario) (*Manager, error) {
	return NewManagerWithSeed(scenario, 0)
}

// NewManagerWithSeed creates a new interaction manager with a custom seed
func NewManagerWithSeed(scenario *config.Scenario, seed int64) (*Manager, error) {
	graph, err := NewGraph(scenario)
	if err != nil {
		return nil, fmt.Errorf("failed to create service graph: %w", err)
	}

	return &Manager{
		graph:             graph,
		branchingStrategy: &DefaultBranchingStrategy{},
		rng:               rand.New(rand.NewSource(seed)),
	}, nil
}

// WithBranchingStrategy sets a custom branching strategy
func (m *Manager) WithBranchingStrategy(strategy BranchingStrategy) *Manager {
	m.branchingStrategy = strategy
	return m
}

// GetGraph returns the service graph
func (m *Manager) GetGraph() *Graph {
	return m.graph
}

// GetDownstreamCalls returns the downstream calls that should be made for a completed request
func (m *Manager) GetDownstreamCalls(serviceID, endpointPath string) ([]ResolvedCall, error) {
	// Resolve all possible downstream calls
	calls, err := m.graph.ResolveDownstreamCalls(serviceID, endpointPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve downstream calls: %w", err)
	}

	if len(calls) == 0 {
		return nil, nil
	}

	// Apply branching strategy to select which calls to make
	selected := m.branchingStrategy.SelectCalls(calls, m.rng)

	return selected, nil
}

// CreateDownstreamRequest creates a new request for a downstream service call
func (m *Manager) CreateDownstreamRequest(parentRequest *models.Request, downstreamCall ResolvedCall) (*models.Request, error) {
	if parentRequest == nil {
		return nil, fmt.Errorf("parent request cannot be nil")
	}

	// Validate downstream service exists
	if _, exists := m.graph.GetService(downstreamCall.ServiceID); !exists {
		return nil, fmt.Errorf("downstream service %q does not exist", downstreamCall.ServiceID)
	}

	// Create new request for downstream service
	requestID := utils.GenerateRequestID()
	downstreamRequest := &models.Request{
		ID:          requestID,
		TraceID:     parentRequest.TraceID, // Same trace
		ParentID:    parentRequest.ID,
		ServiceName: downstreamCall.ServiceID,
		Endpoint:    downstreamCall.Path,
		Status:      models.RequestStatusPending,
		ArrivalTime: time.Time{}, // Will be set to simulation time by caller
		Metadata:    make(map[string]interface{}),
	}

	// Copy relevant metadata from parent
	if parentRequest.Metadata != nil {
		// Copy trace-related metadata
		if instanceID, ok := parentRequest.Metadata["instance_id"].(string); ok {
			downstreamRequest.Metadata["parent_instance_id"] = instanceID
		}
	}

	return downstreamRequest, nil
}

// ValidateEndpoint validates that an endpoint exists in the graph
func (m *Manager) ValidateEndpoint(serviceID, path string) error {
	if _, exists := m.graph.GetEndpoint(serviceID, path); !exists {
		return fmt.Errorf("endpoint %s:%s does not exist", serviceID, path)
	}
	return nil
}

// ValidateService validates that a service exists in the graph
func (m *Manager) ValidateService(serviceID string) error {
	if _, exists := m.graph.GetService(serviceID); !exists {
		return fmt.Errorf("service %q does not exist", serviceID)
	}
	return nil
}
