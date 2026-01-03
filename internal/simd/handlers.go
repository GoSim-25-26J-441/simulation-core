package simd

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/engine"
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
}

// newScenarioState creates a new scenario state from a parsed scenario
func newScenarioState(scenario *config.Scenario) *scenarioState {
	state := &scenarioState{
		scenario:  scenario,
		services:  make(map[string]*config.Service),
		endpoints: make(map[string]*config.Endpoint),
		rng:       utils.NewRandSource(time.Now().UnixNano()),
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

		// Schedule request start immediately (no queue delay for MVP)
		eng.ScheduleAt(engine.EventTypeRequestStart, simTime, request, serviceID, map[string]interface{}{
			"endpoint_path": endpointPath,
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

		// Total processing time = CPU time + network latency
		processingTime := time.Duration(cpuTimeMs+netLatencyMs) * time.Millisecond

		// Schedule completion
		completionTime := simTime.Add(processingTime)
		eng.ScheduleAt(engine.EventTypeRequestComplete, completionTime, request, serviceID, map[string]interface{}{
			"endpoint_path": endpointPath,
		})

		return nil
	}
}

// handleRequestComplete records metrics and handles downstream calls
func handleRequestComplete(state *scenarioState, _ *engine.Engine) engine.EventHandler {
	return func(eng *engine.Engine, evt *engine.Event) error {
		if evt.Request == nil {
			return fmt.Errorf("request is nil in request complete event")
		}

		rm := eng.GetRunManager()
		simTime := eng.GetSimTime()

		request := evt.Request
		serviceID := request.ServiceName
		endpointPath := request.Endpoint

		// Mark request as completed
		request.Status = models.RequestStatusCompleted
		request.CompletionTime = simTime
		request.Duration = simTime.Sub(request.ArrivalTime)

		// Record latency
		totalLatencyMs := float64(request.Duration.Milliseconds())
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
	rng := utils.NewRandSource(time.Now().UnixNano())

	for _, workload := range scenario.Workload {
		// Parse target: "serviceID:path"
		serviceID, endpointPath, err := parseWorkloadTarget(workload.To)
		if err != nil {
			return fmt.Errorf("invalid workload target %s: %w", workload.To, err)
		}

		// Generate arrivals based on arrival type
		switch workload.Arrival.Type {
		case "poisson":
			if err := schedulePoissonArrivals(eng, rng, startTime, endTime, workload.Arrival.RateRPS, serviceID, endpointPath); err != nil {
				return err
			}
		case "uniform":
			if err := scheduleUniformArrivals(eng, rng, startTime, endTime, workload.Arrival.RateRPS, serviceID, endpointPath); err != nil {
				return err
			}
		default:
			// Default to poisson
			if err := schedulePoissonArrivals(eng, rng, startTime, endTime, workload.Arrival.RateRPS, serviceID, endpointPath); err != nil {
				return err
			}
		}
	}

	return nil
}

// schedulePoissonArrivals schedules arrivals using Poisson process
func schedulePoissonArrivals(eng *engine.Engine, rng *utils.RandSource, startTime, endTime time.Time, rateRPS float64, serviceID, endpointPath string) error {
	// Generate inter-arrival times using exponential distribution
	currentTime := startTime
	lambda := rateRPS // rate parameter for exponential distribution

	for currentTime.Before(endTime) {
		// Generate next inter-arrival time (exponential with rate lambda)
		interArrivalSeconds := rng.ExpFloat64(lambda)
		if interArrivalSeconds < 0 {
			interArrivalSeconds = 0
		}
		currentTime = currentTime.Add(time.Duration(interArrivalSeconds * float64(time.Second)))

		if currentTime.After(endTime) {
			break
		}

		// Schedule arrival event
		eng.ScheduleAt(engine.EventTypeRequestArrival, currentTime, nil, serviceID, map[string]interface{}{
			"service_id":    serviceID,
			"endpoint_path": endpointPath,
		})
	}

	return nil
}

// scheduleUniformArrivals schedules arrivals uniformly over the duration
func scheduleUniformArrivals(eng *engine.Engine, rng *utils.RandSource, startTime, endTime time.Time, rateRPS float64, serviceID, endpointPath string) error {
	duration := endTime.Sub(startTime)
	totalSeconds := duration.Seconds()
	expectedArrivals := int64(math.Round(rateRPS * totalSeconds))

	// Distribute arrivals uniformly
	for i := int64(0); i < expectedArrivals; i++ {
		// Uniform distribution over duration
		offsetSeconds := rng.UniformFloat64(0, totalSeconds)
		arrivalTime := startTime.Add(time.Duration(offsetSeconds * float64(time.Second)))

		if arrivalTime.After(endTime) {
			continue
		}

		eng.ScheduleAt(engine.EventTypeRequestArrival, arrivalTime, nil, serviceID, map[string]interface{}{
			"service_id":    serviceID,
			"endpoint_path": endpointPath,
		})
	}

	return nil
}
