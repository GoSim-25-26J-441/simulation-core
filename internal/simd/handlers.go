package simd

import (
	"fmt"
	"strings"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/engine"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
	"github.com/GoSim-25-26J-441/simulation-core/internal/resource"
	"github.com/GoSim-25-26J-441/simulation-core/internal/workload"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/utils"
)

// scenarioState holds scenario data for handlers
type scenarioState struct {
	scenario  *config.Scenario
	services  map[string]*config.Service  // service ID -> service
	endpoints map[string]*config.Endpoint // "serviceID:path" -> endpoint
	rng       *utils.RandSource
	rm        *resource.Manager    // Resource manager for tracking CPU/memory/queueing
	collector *metrics.Collector    // Metrics collector for time-series metrics
}

// newScenarioState creates a new scenario state from a parsed scenario
func newScenarioState(scenario *config.Scenario, rm *resource.Manager, collector *metrics.Collector) *scenarioState {
	state := &scenarioState{
		scenario:  scenario,
		services:  make(map[string]*config.Service),
		endpoints: make(map[string]*config.Endpoint),
		rng:       utils.NewRandSource(time.Now().UnixNano()),
		rm:        rm,
		collector: collector,
	}

	// Build service and endpoint maps
	for i := range scenario.Services {
		svc := &scenario.Services[i]
		state.services[svc.ID] = svc
		for j := range svc.Endpoints {
			ep := &svc.Endpoints[j]
			key := fmt.Sprintf("%s:%s", svc.ID, ep.Path)
			state.endpoints[key] = ep
		}
	}

	return state
}

// parseWorkloadTarget parses "serviceID:path" format
func parseWorkloadTarget(target string) (serviceID, path string, err error) {
	parts := strings.SplitN(target, ":", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid workload target format: %s (expected serviceID:path)", target)
	}

	serviceID = strings.TrimSpace(parts[0])
	path = strings.TrimSpace(parts[1])

	if serviceID == "" {
		return "", "", fmt.Errorf("invalid workload target format: %s (serviceID must be non-empty)", target)
	}
	if path == "" {
		return "", "", fmt.Errorf("invalid workload target format: %s (path must be non-empty)", target)
	}

	return serviceID, path, nil
}

// RegisterHandlers registers all event handlers for the engine
func RegisterHandlers(eng *engine.Engine, state *scenarioState) {
	eng.RegisterHandler(engine.EventTypeRequestArrival, handleRequestArrival(state))
	eng.RegisterHandler(engine.EventTypeRequestStart, handleRequestStart(state))
	eng.RegisterHandler(engine.EventTypeRequestComplete, handleRequestComplete(state, eng))
	eng.RegisterHandler(engine.EventTypeDownstreamCall, handleDownstreamCall(state, eng))
}

// handleRequestArrival creates a new request and schedules it to start
func handleRequestArrival(state *scenarioState) engine.EventHandler {
	return func(eng *engine.Engine, evt *engine.Event) error {
		simTime := eng.GetSimTime()

		// Extract service and endpoint from event data
		serviceID, ok := evt.Data["service_id"].(string)
		if !ok {
			return fmt.Errorf("missing service_id in request arrival event")
		}
		endpointPath, ok := evt.Data["endpoint_path"].(string)
		if !ok {
			return fmt.Errorf("missing endpoint_path in request arrival event")
		}

		// Create request
		requestID := utils.GenerateRequestID()
		traceID := utils.GenerateTraceID()
		request := &models.Request{
			ID:          requestID,
			TraceID:     traceID,
			ServiceName: serviceID,
			Endpoint:    endpointPath,
			Status:      models.RequestStatusPending,
			ArrivalTime: simTime,
			Metadata:    make(map[string]interface{}),
		}

		rm := eng.GetRunManager()
		rm.AddRequest(request)

		// Record request arrival metric
		labels := metrics.CreateEndpointLabels(serviceID, endpointPath)
		metrics.RecordRequestCount(state.collector, 1.0, simTime, labels)

		// Select an instance for this service
		instance, err := state.rm.SelectInstanceForService(serviceID)
		if err != nil {
			// No instances available, mark request as failed
			request.Status = models.RequestStatusFailed
			// Record error
			metrics.RecordErrorCount(state.collector, 1.0, simTime, labels)
			return fmt.Errorf("no instances available for service %s: %w", serviceID, err)
		}

		// Check if instance has capacity
		if !instance.HasCapacity() {
			// Instance is at capacity, enqueue the request
			if err := state.rm.EnqueueRequest(instance.ID(), requestID); err != nil {
				return fmt.Errorf("failed to enqueue request: %w", err)
			}
			// Request will be processed when capacity becomes available
			// Store instance ID in request metadata for later processing
			request.Metadata["instance_id"] = instance.ID()
			return nil
		}

		// Instance has capacity, schedule request start immediately
		// Store instance ID in request metadata
		request.Metadata["instance_id"] = instance.ID()
		eng.ScheduleAt(engine.EventTypeRequestStart, simTime, request, serviceID, map[string]interface{}{
			"endpoint_path": endpointPath,
			"instance_id":   instance.ID(),
		})

		return nil
	}
}

// handleRequestStart processes a request (CPU time, network latency)
func handleRequestStart(state *scenarioState) engine.EventHandler {
	return func(eng *engine.Engine, evt *engine.Event) error {
		if evt.Request == nil {
			return fmt.Errorf("request is nil in request start event")
		}

		simTime := eng.GetSimTime()

		request := evt.Request
		serviceID := request.ServiceName
		endpointPath := request.Endpoint

		// Find endpoint configuration
		endpointKey := fmt.Sprintf("%s:%s", serviceID, endpointPath)
		endpoint, ok := state.endpoints[endpointKey]
		if !ok {
			return fmt.Errorf("endpoint not found: %s", endpointKey)
		}

		// Get instance ID from event data or request metadata
		instanceID, ok := evt.Data["instance_id"].(string)
		if !ok {
			// Fallback to metadata
			if id, ok := request.Metadata["instance_id"].(string); ok {
				instanceID = id
			} else {
				// Select instance if not already assigned
				instance, err := state.rm.SelectInstanceForService(serviceID)
				if err != nil {
					return fmt.Errorf("no instances available for service %s: %w", serviceID, err)
				}
				instanceID = instance.ID()
				request.Metadata["instance_id"] = instanceID
			}
		}

		// Update request status
		request.Status = models.RequestStatusProcessing
		request.StartTime = simTime

		// Calculate CPU time (normal distribution)
		cpuTimeMs := state.rng.NormFloat64(endpoint.MeanCPUMs, endpoint.CPUSigmaMs)
		if cpuTimeMs < 0 {
			cpuTimeMs = 0
		}
		request.CPUTimeMs = cpuTimeMs

		// Calculate network latency (normal distribution)
		netLatencyMs := state.rng.NormFloat64(endpoint.NetLatencyMs.Mean, endpoint.NetLatencyMs.Sigma)
		if netLatencyMs < 0 {
			netLatencyMs = 0
		}
		request.NetworkLatencyMs = netLatencyMs

		// Estimate memory usage (simplified: assume 10MB per request)
		memoryMB := 10.0
		if mem, ok := request.Metadata["memory_mb"].(float64); ok {
			memoryMB = mem
		}

		// Allocate resources
		if err := state.rm.AllocateCPU(instanceID, cpuTimeMs, simTime); err != nil {
			return fmt.Errorf("failed to allocate CPU: %w", err)
		}
		if err := state.rm.AllocateMemory(instanceID, memoryMB); err != nil {
			// If memory allocation fails, release CPU and fail request
			state.rm.ReleaseCPU(instanceID, cpuTimeMs, simTime)
			return fmt.Errorf("failed to allocate memory: %w", err)
		}

		// Store resource allocation in metadata for cleanup
		request.Metadata["allocated_cpu_ms"] = cpuTimeMs
		request.Metadata["allocated_memory_mb"] = memoryMB

		// Record resource utilization metrics
		instance, ok := state.rm.GetServiceInstance(instanceID)
		if ok {
			// Record CPU utilization
			cpuUtil := instance.CPUUtilization()
			instanceLabels := metrics.CreateInstanceLabels(serviceID, instanceID)
			metrics.RecordCPUUtilization(state.collector, cpuUtil, simTime, instanceLabels)

			// Record memory utilization
			memUtil := instance.MemoryUtilization()
			metrics.RecordMemoryUtilization(state.collector, memUtil, simTime, instanceLabels)

			// Record queue length
			queueLength := instance.QueueLength()
			metrics.RecordQueueLength(state.collector, float64(queueLength), simTime, instanceLabels)

			// Model queueing delay: each queued request adds 1ms delay
			queueDelayMs := float64(queueLength) * 1.0
			request.Metadata["queue_delay_ms"] = queueDelayMs
		}

		// Total processing time = CPU time + network latency + queue delay
		var queueDelayMs float64
		if qd, ok := request.Metadata["queue_delay_ms"].(float64); ok {
			queueDelayMs = qd
		}
		processingTime := time.Duration(cpuTimeMs+netLatencyMs+queueDelayMs) * time.Millisecond

		// Schedule completion
		completionTime := simTime.Add(processingTime)
		eng.ScheduleAt(engine.EventTypeRequestComplete, completionTime, request, serviceID, map[string]interface{}{
			"endpoint_path": endpointPath,
			"instance_id":   instanceID,
		})

		return nil
	}
}

// handleRequestComplete records metrics and handles downstream calls
func handleRequestComplete(state *scenarioState, eng *engine.Engine) engine.EventHandler {
	return func(_ *engine.Engine, evt *engine.Event) error {
		if evt.Request == nil {
			return fmt.Errorf("request is nil in request complete event")
		}

		rm := eng.GetRunManager()
		simTime := eng.GetSimTime()

		request := evt.Request
		serviceID := request.ServiceName
		endpointPath := request.Endpoint

		// Get instance ID from metadata
		instanceID, ok := request.Metadata["instance_id"].(string)
		if ok {
			// Release resources
			if cpuMs, ok := request.Metadata["allocated_cpu_ms"].(float64); ok {
				state.rm.ReleaseCPU(instanceID, cpuMs, simTime)
			}
			if memoryMB, ok := request.Metadata["allocated_memory_mb"].(float64); ok {
				state.rm.ReleaseMemory(instanceID, memoryMB)
			}

			// Process next queued request if available
			nextRequestID, hasNext := state.rm.DequeueRequest(instanceID)
			if hasNext {
				// Find the request in the run manager
				// Note: This is a simplified approach - in a real system, we'd maintain a request store
				// For now, we'll schedule a new arrival event for the queued request
				// This will be handled by the arrival handler which will check capacity again
				eng.ScheduleAt(engine.EventTypeRequestArrival, simTime, nil, serviceID, map[string]interface{}{
					"service_id":    serviceID,
					"endpoint_path": endpointPath,
					"queued_id":     nextRequestID,
				})
			}
		}

		// Mark request as completed
		request.Status = models.RequestStatusCompleted
		request.CompletionTime = simTime
		request.Duration = simTime.Sub(request.ArrivalTime)

		// Record latency metric
		totalLatencyMs := float64(request.Duration.Milliseconds())
		labels := metrics.CreateEndpointLabels(serviceID, endpointPath)
		metrics.RecordLatency(state.collector, totalLatencyMs, simTime, labels)

		// Also record in run manager for backward compatibility
		rm.RecordLatency(totalLatencyMs)

		// Find endpoint to check for downstream calls
		endpointKey := fmt.Sprintf("%s:%s", serviceID, endpointPath)
		endpoint, ok := state.endpoints[endpointKey]
		if !ok {
			// Endpoint not found, but request is complete
			return nil
		}

		// Handle downstream calls
		for _, ds := range endpoint.Downstream {
			// Parse downstream target (should be "serviceID:path" or just "serviceID")
			downstreamTarget := ds.To
			var downstreamServiceID, downstreamPath string
			if strings.Contains(downstreamTarget, ":") {
				var err error
				downstreamServiceID, downstreamPath, err = parseWorkloadTarget(downstreamTarget)
				if err != nil {
					// If parsing fails, log a warning and treat entire string as service ID with default path
					fmt.Printf("warning: failed to parse downstream target %q: %v; treating as service ID with default path\n", downstreamTarget, err)
					downstreamServiceID = downstreamTarget
					downstreamPath = "/"
				}
			} else {
				downstreamServiceID = downstreamTarget
				downstreamPath = "/"
			}

			// Check if downstream service exists
			if _, exists := state.services[downstreamServiceID]; !exists {
				continue
			}

			// Schedule downstream call
			// For MVP, we schedule it immediately after current request completes
			eng.ScheduleAt(engine.EventTypeDownstreamCall, simTime, request, downstreamServiceID, map[string]interface{}{
				"endpoint_path":     downstreamPath,
				"parent_request_id": request.ID,
			})
		}

		return nil
	}
}

// handleDownstreamCall creates a new request for a downstream service
func handleDownstreamCall(state *scenarioState, eng *engine.Engine) engine.EventHandler {
	return func(eng *engine.Engine, evt *engine.Event) error {
		if evt.Request == nil {
			return fmt.Errorf("request is nil in downstream call event")
		}

		simTime := eng.GetSimTime()
		parentRequest := evt.Request

		// Extract downstream service and endpoint
		downstreamServiceID := evt.ServiceID
		endpointPath, ok := evt.Data["endpoint_path"].(string)
		if !ok {
			endpointPath = "/"
		}

		// Create new request for downstream service
		requestID := utils.GenerateRequestID()
		downstreamRequest := &models.Request{
			ID:          requestID,
			TraceID:     parentRequest.TraceID, // Same trace
			ParentID:    parentRequest.ID,
			ServiceName: downstreamServiceID,
			Endpoint:    endpointPath,
			Status:      models.RequestStatusPending,
			ArrivalTime: simTime,
			Metadata:    make(map[string]interface{}),
		}

		rm := eng.GetRunManager()
		rm.AddRequest(downstreamRequest)

		// Schedule downstream request start
		eng.ScheduleAt(engine.EventTypeRequestStart, simTime, downstreamRequest, downstreamServiceID, map[string]interface{}{
			"endpoint_path": endpointPath,
		})

		return nil
	}
}

// ScheduleWorkload generates arrival events based on workload patterns
func ScheduleWorkload(eng *engine.Engine, scenario *config.Scenario, duration time.Duration) error {
	startTime := eng.GetSimTime()
	endTime := startTime.Add(duration)
	generator := workload.NewGenerator(time.Now().UnixNano())

	for _, workloadPattern := range scenario.Workload {
		// Parse target: "serviceID:path"
		serviceID, endpointPath, err := parseWorkloadTarget(workloadPattern.To)
		if err != nil {
			return fmt.Errorf("invalid workload target %s: %w", workloadPattern.To, err)
		}

		// Use the new workload generator
		if err := generator.ScheduleArrivals(eng, startTime, endTime, workloadPattern.Arrival, serviceID, endpointPath); err != nil {
			return fmt.Errorf("failed to schedule arrivals for %s: %w", workloadPattern.To, err)
		}
	}

	return nil
}

// Legacy functions removed - now using workload.Generator
// These are kept for backward compatibility but delegate to the new generator
