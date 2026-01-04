// Package interaction provides centralized service interaction management for the simulation.
//
// This package manages service dependency graphs (DAG) and downstream call resolution,
// enabling the simulation to model complex microservice architectures with controlled
// service-to-service communication patterns.
//
// Main Types:
//   - Graph: Represents a directed acyclic graph (DAG) of service dependencies
//   - Manager: Manages service interactions and coordinates downstream calls
//   - BranchingStrategy: Determines which downstream calls to make based on probabilities
//
// Usage:
//
//	// Create a manager from a scenario configuration
//	manager, err := interaction.NewManager(scenario)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Get downstream calls for a completed request
//	calls, err := manager.GetDownstreamCalls("service-a", "/api/endpoint")
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Create downstream requests
//	for _, call := range calls {
//	    req, err := manager.CreateDownstreamRequest(parentReq, call)
//	    // ... schedule the request
//	}
package interaction
