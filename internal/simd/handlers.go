package simd

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/engine"
	"github.com/GoSim-25-26J-441/simulation-core/internal/interaction"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
	"github.com/GoSim-25-26J-441/simulation-core/internal/policy"
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
	collector *metrics.Collector   // Metrics collector for time-series metrics
	policies  *policy.Manager      // Policy manager for autoscaling, rate limiting, retries, circuit breaking
	interact  *interaction.Manager // Interaction manager for service graph and downstream calls
	// simEndTime is the simulation horizon for scheduling drain sweeps (zero disables rescheduling).
	simEndTime time.Time

	pendingSyncMu sync.Mutex
	// pendingSync counts synchronous downstream subtrees not yet reported complete for a request ID.
	pendingSync map[string]int
}

// SetSimEndTime sets the simulation end time used by periodic drain sweeps.
func (s *scenarioState) SetSimEndTime(t time.Time) {
	s.simEndTime = t
}

// newScenarioState creates a new scenario state from a parsed scenario.
// seed 0 selects a non-deterministic RNG base (wall clock); non-zero seeds derive stable RNG streams.
func newScenarioState(scenario *config.Scenario, rm *resource.Manager, collector *metrics.Collector, policies *policy.Manager, seed int64) (*scenarioState, error) {
	rngSeed := seed
	if rngSeed == 0 {
		rngSeed = time.Now().UnixNano()
	}
	interact, err := interaction.NewManagerWithSeed(scenario, rngSeed+2)
	if err != nil {
		return nil, fmt.Errorf("failed to create interaction manager: %w", err)
	}

	state := &scenarioState{
		scenario:  scenario,
		services:  make(map[string]*config.Service),
		endpoints: make(map[string]*config.Endpoint),
		rng:       utils.NewRandSource(rngSeed),
		rm:        rm,
		collector: collector,
		policies:  policies,
		interact:  interact,
		pendingSync: make(map[string]int),
	}

	// Build service and endpoint maps (kept for backward compatibility and quick lookups)
	for i := range scenario.Services {
		svc := &scenario.Services[i]
		state.services[svc.ID] = svc
		for j := range svc.Endpoints {
			ep := &svc.Endpoints[j]
			key := fmt.Sprintf("%s:%s", svc.ID, ep.Path)
			state.endpoints[key] = ep
		}
	}

	return state, nil
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
	eng.RegisterHandler(engine.EventTypeDownstreamRetry, handleDownstreamRetry(state, eng))
	eng.RegisterHandler(engine.EventTypeDownstreamTimeout, handleDownstreamTimeout(state, eng))
	eng.RegisterHandler(engine.EventTypeDrainSweep, handleDrainSweep(state))
}

func recordInstanceAndHostGauges(state *scenarioState, serviceID, instanceID string, simTime time.Time) {
	instance, ok := state.rm.GetServiceInstance(instanceID)
	if !ok {
		return
	}
	instanceLabels := metrics.CreateInstanceLabels(serviceID, instanceID)
	metrics.RecordCPUUtilization(state.collector, instance.CPUUtilizationAt(simTime), simTime, instanceLabels)
	metrics.RecordMemoryUtilization(state.collector, instance.MemoryUtilization(), simTime, instanceLabels)
	metrics.RecordQueueLength(state.collector, float64(instance.QueueLength()), simTime, instanceLabels)
	metrics.RecordConcurrentRequests(state.collector, float64(instance.ActiveRequests()), simTime, instanceLabels)

	hostID := instance.HostID()
	if host, hostOk := state.rm.GetHost(hostID); hostOk {
		hostLabels := metrics.CreateHostLabels(hostID)
		metrics.RecordCPUUtilization(state.collector, host.CPUUtilization(), simTime, hostLabels)
		metrics.RecordMemoryUtilization(state.collector, host.MemoryUtilization(), simTime, hostLabels)
	}
}

const drainSweepInterval = 100 * time.Millisecond

func metadataInt(m map[string]interface{}, key string) int {
	if m == nil {
		return 0
	}
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch t := v.(type) {
	case int:
		return t
	case int32:
		return int(t)
	case int64:
		return int(t)
	case float64:
		return int(t)
	default:
		return 0
	}
}

func metadataBool(m map[string]interface{}, key string) bool {
	if m == nil {
		return false
	}
	v, ok := m[key]
	if !ok {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}

func metadataFloat64(m map[string]interface{}, key string) float64 {
	if m == nil {
		return 0
	}
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	case int64:
		return float64(t)
	default:
		return 0
	}
}

func labelsForRequestMetrics(req *models.Request, serviceID, endpointPath string) map[string]string {
	var origin string
	if req.ParentID == "" {
		origin = metrics.OriginIngress
	} else {
		origin = metrics.OriginDownstream
	}
	lbl := metrics.EndpointLabelsWithOrigin(serviceID, endpointPath, origin)
	if req.Metadata != nil {
		if tc, ok := req.Metadata["workload_traffic_class"].(string); ok && tc != "" {
			lbl[metrics.LabelTrafficClass] = tc
		}
		if sk, ok := req.Metadata["workload_source_kind"].(string); ok && sk != "" {
			lbl[metrics.LabelSourceKind] = sk
		}
		if b, ok := req.Metadata[metaIsRetry].(bool); ok && b {
			lbl[metrics.LabelIsRetry] = "true"
			lbl[metrics.LabelRetryAttempt] = strconv.Itoa(metadataInt(req.Metadata, metaRetryAttempt))
		}
	}
	return lbl
}

func localServiceLatencyMs(request *models.Request, simTime time.Time) float64 {
	if !request.StartTime.IsZero() {
		return float64(simTime.Sub(request.StartTime).Milliseconds())
	}
	return float64(simTime.Sub(request.ArrivalTime).Milliseconds())
}

func partitionDownstreamFiltered(state *scenarioState, downstreamCalls []interaction.ResolvedCall, td, ad int) (asyncCalls, syncCalls []interaction.ResolvedCall) {
	lim := state.scenario.SimulationLimits
	for _, downstreamCall := range downstreamCalls {
		nextTD := td + 1
		nextAD := ad
		if downstreamCall.Call.IsAsync() {
			nextAD++
		}
		if lim != nil {
			if lim.MaxTraceDepth > 0 && nextTD > lim.MaxTraceDepth {
				continue
			}
			if lim.MaxAsyncHops > 0 && downstreamCall.Call.IsAsync() && nextAD > lim.MaxAsyncHops {
				continue
			}
		}
		if downstreamCall.Call.IsAsync() {
			asyncCalls = append(asyncCalls, downstreamCall)
		} else {
			syncCalls = append(syncCalls, downstreamCall)
		}
	}
	return asyncCalls, syncCalls
}

func scheduleDownstreamCallEvent(state *scenarioState, eng *engine.Engine, parentRequest *models.Request, downstreamCall interaction.ResolvedCall, simTime time.Time, nextTD, nextAD int, isAsync bool) {
	callLatencyMs := 0.0
	if downstreamCall.Call.CallLatencyMs.Mean > 0 {
		sigma := downstreamCall.Call.CallLatencyMs.Sigma
		if sigma < 0 {
			sigma = 0
		}
		callLatencyMs = state.rng.NormFloat64(downstreamCall.Call.CallLatencyMs.Mean, sigma)
		if callLatencyMs < 0 {
			callLatencyMs = 0
		}
	}
	scheduleTime := simTime.Add(time.Duration(callLatencyMs * float64(time.Millisecond)))
	eng.ScheduleAt(engine.EventTypeDownstreamCall, scheduleTime, parentRequest, downstreamCall.ServiceID, map[string]interface{}{
		"endpoint_path":         downstreamCall.Path,
		"parent_request_id":     parentRequest.ID,
		"trace_depth":           nextTD,
		"async_depth":           nextAD,
		"is_async_downstream":   isAsync,
		"downstream_timeout_ms": downstreamCall.Call.TimeoutMs,
	})
}

// ScheduleDrainSweepKickoff schedules the first simulated-time drain sweep so draining
// replicas are processed even when request traffic stops.
func ScheduleDrainSweepKickoff(eng *engine.Engine, startTime time.Time) {
	eng.ScheduleAt(engine.EventTypeDrainSweep, startTime.Add(50*time.Millisecond), nil, "", nil)
}

// failDroppedQueueRequests marks queued (pending) requests failed when their instance
// queue was cleared by a hard drain timeout eviction.
func failDroppedQueueRequests(eng *engine.Engine, state *scenarioState, simTime time.Time, dropped []string) {
	if len(dropped) == 0 {
		return
	}
	rm := eng.GetRunManager()
	for _, reqID := range dropped {
		req, ok := rm.GetRequest(reqID)
		if !ok {
			continue
		}
		if req.Status != models.RequestStatusPending {
			continue
		}
		req.Status = models.RequestStatusFailed
		labels := metrics.CreateEndpointLabels(req.ServiceName, req.Endpoint)
		metrics.RecordErrorCount(state.collector, 1.0, simTime, labels)
	}
}

func handleDrainSweep(state *scenarioState) engine.EventHandler {
	return func(eng *engine.Engine, evt *engine.Event) error {
		simTime := eng.GetSimTime()
		state.rm.NoteSimTime(simTime)
		dropped := state.rm.ProcessDrainingInstances(simTime)
		failDroppedQueueRequests(eng, state, simTime, dropped)
		next := simTime.Add(drainSweepInterval)
		if state.simEndTime.IsZero() || next.Before(state.simEndTime) {
			eng.ScheduleAt(engine.EventTypeDrainSweep, next, nil, "", nil)
		}
		return nil
	}
}

// handleRequestArrival creates a new request and schedules it to start
func handleRequestArrival(state *scenarioState) engine.EventHandler {
	return func(eng *engine.Engine, evt *engine.Event) error {
		simTime := eng.GetSimTime()
		state.rm.NoteSimTime(simTime)
		dropped := state.rm.ProcessDrainingInstances(simTime)
		failDroppedQueueRequests(eng, state, simTime, dropped)

		// Extract service and endpoint from event data
		serviceID, ok := evt.Data["service_id"].(string)
		if !ok {
			return fmt.Errorf("missing service_id in request arrival event")
		}
		endpointPath, ok := evt.Data["endpoint_path"].(string)
		if !ok {
			return fmt.Errorf("missing endpoint_path in request arrival event")
		}

		ingressLabels := metrics.EndpointLabelsWithOrigin(serviceID, endpointPath, metrics.OriginIngress)

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
		request.Metadata["trace_depth"] = 0
		request.Metadata["async_depth"] = 0
		if v, ok := evt.Data["from"]; ok {
			request.Metadata["workload_from"] = v
		}
		if v, ok := evt.Data["source_kind"]; ok {
			request.Metadata["workload_source_kind"] = v
		}
		if v, ok := evt.Data["traffic_class"]; ok {
			request.Metadata["workload_traffic_class"] = v
		}

		rm := eng.GetRunManager()
		rm.AddRequest(request)

		// Check rate limiting policy
		if state.policies != nil {
			rateLimiting := state.policies.GetRateLimiting()
			if rateLimiting != nil && !rateLimiting.AllowRequest(serviceID, endpointPath, simTime) {
				request.Status = models.RequestStatusFailed
				metrics.RecordErrorCount(state.collector, 1.0, simTime, metrics.EndpointErrorLabels(ingressLabels, metrics.ReasonRateLimited))
				return fmt.Errorf("rate limit exceeded for %s:%s", serviceID, endpointPath)
			}

			circuitBreaker := state.policies.GetCircuitBreaker()
			if circuitBreaker != nil && !circuitBreaker.AllowRequest(serviceID, endpointPath, simTime) {
				request.Status = models.RequestStatusFailed
				metrics.RecordErrorCount(state.collector, 1.0, simTime, metrics.EndpointErrorLabels(ingressLabels, metrics.ReasonCircuitOpen))
				return fmt.Errorf("circuit breaker open for %s:%s", serviceID, endpointPath)
			}
		}

		// Record request arrival metric
		metrics.RecordRequestCount(state.collector, 1.0, simTime, ingressLabels)

		// Select an instance for this service
		instance, err := state.rm.SelectInstanceForService(serviceID)
		if err != nil {
			request.Status = models.RequestStatusFailed
			metrics.RecordErrorCount(state.collector, 1.0, simTime, metrics.EndpointErrorLabels(ingressLabels, metrics.ReasonNoInstance))
			return fmt.Errorf("no instances available for service %s: %w", serviceID, err)
		}

		// Check if instance has capacity (must use simulation time, not wall clock)
		if !instance.HasCapacityAt(simTime) {
			// Instance is at capacity, enqueue the request
			if err := state.rm.EnqueueRequest(instance.ID(), requestID); err != nil {
				return fmt.Errorf("failed to enqueue request: %w", err)
			}
			// Request will be processed when capacity becomes available
			// Store instance ID in request metadata for later processing
			request.Metadata["instance_id"] = instance.ID()
			// Refresh gauges so queue_length / concurrent_requests match backlog immediately
			// (otherwise they stay stale until a later start/complete path records them).
			recordInstanceAndHostGauges(state, serviceID, instance.ID(), simTime)
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
		state.rm.NoteSimTime(simTime)
		dropped := state.rm.ProcessDrainingInstances(simTime)
		failDroppedQueueRequests(eng, state, simTime, dropped)

		request := evt.Request
		serviceID := request.ServiceName
		endpointPath := request.Endpoint

		// Find endpoint configuration
		endpointKey := fmt.Sprintf("%s:%s", serviceID, endpointPath)
		endpoint, ok := state.endpoints[endpointKey]
		if !ok {
			return fmt.Errorf("endpoint not found: %s", endpointKey)
		}
		svc, ok := state.services[serviceID]
		if !ok {
			return fmt.Errorf("service not found: %s", serviceID)
		}
		prof := resolveServiceExecutionProfile(svc, endpoint, nil, state.rng)

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

		cpuTimeMs := prof.CPUTimeMs
		request.CPUTimeMs = cpuTimeMs

		netLatencyMs := prof.NetworkLatencyMs
		request.NetworkLatencyMs = netLatencyMs

		memoryMB := prof.MemoryMB
		// Allow override from metadata
		if mem, ok := request.Metadata["memory_mb"].(float64); ok {
			memoryMB = mem
		}

		// Allocate resources
		if err := state.rm.AllocateCPU(instanceID, cpuTimeMs, simTime); err != nil {
			request.Status = models.RequestStatusFailed
			lbl := labelsForRequestMetricsWithRetry(request, serviceID, endpointPath)
			metrics.RecordErrorCount(state.collector, 1.0, simTime, metrics.EndpointErrorLabels(lbl, metrics.ReasonCPUCapacity))
			if state.policies != nil {
				circuitBreaker := state.policies.GetCircuitBreaker()
				if circuitBreaker != nil {
					circuitBreaker.RecordFailure(serviceID, endpointPath, simTime)
				}
			}
			rm := eng.GetRunManager()
			if maybeRetrySyncStartFailure(state, eng, rm, request, simTime, metrics.ReasonCPUCapacity) {
				return nil
			}
			propagateSyncChildFailureFromStartFailure(state, eng, request, simTime, metrics.ReasonCPUCapacity)
			return fmt.Errorf("failed to allocate CPU: %w", err)
		}
		if err := state.rm.AllocateMemory(instanceID, memoryMB); err != nil {
			state.rm.ReleaseCPU(instanceID, cpuTimeMs, simTime)
			request.Status = models.RequestStatusFailed
			lbl := labelsForRequestMetricsWithRetry(request, serviceID, endpointPath)
			reason := metrics.ReasonMemoryCapacity
			if errors.Is(err, resource.ErrHostMemoryCapacity) {
				reason = metrics.ReasonMemoryCapacity
			}
			metrics.RecordErrorCount(state.collector, 1.0, simTime, metrics.EndpointErrorLabels(lbl, reason))
			if state.policies != nil {
				circuitBreaker := state.policies.GetCircuitBreaker()
				if circuitBreaker != nil {
					circuitBreaker.RecordFailure(serviceID, endpointPath, simTime)
				}
			}
			rm := eng.GetRunManager()
			if maybeRetrySyncStartFailure(state, eng, rm, request, simTime, reason) {
				if errors.Is(err, resource.ErrHostMemoryCapacity) {
					return nil
				}
				return fmt.Errorf("failed to allocate memory: %w", err)
			}
			propagateSyncChildFailureFromStartFailure(state, eng, request, simTime, reason)
			if errors.Is(err, resource.ErrHostMemoryCapacity) {
				return nil
			}
			return fmt.Errorf("failed to allocate memory: %w", err)
		}

		// Store resource allocation in metadata for cleanup
		request.Metadata["allocated_cpu_ms"] = cpuTimeMs
		request.Metadata["allocated_memory_mb"] = memoryMB

		// Record resource utilization metrics
		instance, ok := state.rm.GetServiceInstance(instanceID)
		if ok {
			recordInstanceAndHostGauges(state, serviceID, instanceID, simTime)
			queueLength := instance.QueueLength()
			// Model queueing delay based on mean service time
			// Queue delay is estimated as the sum of expected service times for all queued requests.
			// This assumes:
			// - FIFO queue processing
			// - Independent, identically distributed service times
			// - Mean service time ≈ mean CPU time + mean network latency
			// - No variance in service times (uses mean values)
			//
			// Limitations:
			// - Does not account for actual variability in service times
			// - Assumes all queued requests have similar characteristics
			// - Does not model complex queueing effects (e.g., head-of-line blocking)
			//
			// For more accurate modeling, consider implementing a detailed queueing theory model
			// (e.g., M/M/1, M/G/1) with actual service time distributions.
			queueDelayMs := 0.0
			if queueLength > 0 {
				meanServiceTimeMs := prof.QueueMeanWorkMs
				if meanServiceTimeMs < 0 {
					meanServiceTimeMs = 0
				}
				queueDelayMs = float64(queueLength) * meanServiceTimeMs
			}
			if prof.QueueClass != "" {
				request.Metadata["queue_class"] = prof.QueueClass
			}
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
		state.rm.NoteSimTime(simTime)
		dropped := state.rm.ProcessDrainingInstances(simTime)
		failDroppedQueueRequests(eng, state, simTime, dropped)

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
				rm := eng.GetRunManager()
				nextRequest, found := rm.GetRequest(nextRequestID)
				if !found {
					return fmt.Errorf("queued request %s not found in run manager", nextRequestID)
				}
				eng.ScheduleAt(engine.EventTypeRequestStart, simTime, nextRequest, serviceID, map[string]interface{}{
					"service_id":    serviceID,
					"endpoint_path": endpointPath,
					"instance_id":   instanceID,
				})
			}
			// Refresh gauges after release/dequeue to avoid stale service-level snapshots.
			recordInstanceAndHostGauges(state, serviceID, instanceID, simTime)
		}

		labels := labelsForRequestMetrics(request, serviceID, endpointPath)
		svcLocalMs := localServiceLatencyMs(request, simTime)
		metrics.RecordServiceRequestLatency(state.collector, svcLocalMs, simTime, labels)

		downstreamCalls, err := state.interact.GetDownstreamCalls(serviceID, endpointPath)
		if err != nil {
			return fmt.Errorf("failed to get downstream calls for %s:%s: %w", serviceID, endpointPath, err)
		}

		td := metadataInt(request.Metadata, "trace_depth")
		ad := metadataInt(request.Metadata, "async_depth")
		asyncCalls, syncCalls := partitionDownstreamFiltered(state, downstreamCalls, td, ad)

		for _, downstreamCall := range asyncCalls {
			nextTD := td + 1
			nextAD := ad + 1
			scheduleDownstreamCallEvent(state, eng, request, downstreamCall, simTime, nextTD, nextAD, true)
		}

		if len(syncCalls) == 0 {
			finalizeRequestCompletion(state, eng, rm, request, simTime, labels)
			return nil
		}

		state.pendingSyncMu.Lock()
		state.pendingSync[request.ID] = len(syncCalls)
		state.pendingSyncMu.Unlock()

		for _, downstreamCall := range syncCalls {
			nextTD := td + 1
			nextAD := ad
			scheduleDownstreamCallEvent(state, eng, request, downstreamCall, simTime, nextTD, nextAD, false)
		}

		return nil
	}
}

// handleDownstreamCall creates a new request for a downstream service
func handleDownstreamCall(state *scenarioState, _ *engine.Engine) engine.EventHandler {
	return func(eng *engine.Engine, evt *engine.Event) error {
		if evt.Request == nil {
			return fmt.Errorf("request is nil in downstream call event")
		}

		simTime := eng.GetSimTime()
		state.rm.NoteSimTime(simTime)
		parentRequest := evt.Request

		// First attempt: ensure retry_attempt defaults to 0 (logical_call_id set in execDownstreamSpawn)
		if evt.Data == nil {
			evt.Data = make(map[string]interface{})
		}
		if _, ok := evt.Data[metaRetryAttempt]; !ok {
			evt.Data[metaRetryAttempt] = 0
		}

		return execDownstreamSpawnFromEvent(state, eng, parentRequest, evt)
	}
}

// ScheduleWorkload generates arrival events based on workload patterns
func ScheduleWorkload(eng *engine.Engine, scenario *config.Scenario, duration time.Duration) error {
	startTime := eng.GetSimTime()
	endTime := startTime.Add(duration)
	generator := workload.NewGenerator(time.Now().UnixNano())

	for _, workloadPattern := range scenario.Workload {
		// Parse target: "serviceID:path" using interaction resolver
		serviceID, endpointPath, err := interaction.ParseDownstreamTarget(workloadPattern.To)
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
