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
	eng.RegisterHandler(engine.EventTypeDownstreamCallerOverheadStart, handleDownstreamCallerOverheadStart(state, eng))
	eng.RegisterHandler(engine.EventTypeDownstreamCallerOverheadEnd, handleDownstreamCallerOverheadEnd(state, eng))
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

// CPU scheduler metadata (DES): deferred RequestStart until cpu_service_start simulation time.
const (
	metaCpuDeferredStart = "cpu_deferred_start"
	metaCpuServiceStart  = "cpu_service_start"
	metaCpuServiceEnd    = "cpu_service_end"
)

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

func metadataTime(m map[string]interface{}, key string) (time.Time, bool) {
	if m == nil {
		return time.Time{}, false
	}
	v, ok := m[key]
	if !ok {
		return time.Time{}, false
	}
	t, ok := v.(time.Time)
	return t, ok
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

// labelsForQueueWaitMetrics extends endpoint request labels with instance for queue_wait_ms.
func labelsForQueueWaitMetrics(req *models.Request, serviceID, endpointPath, instanceID string) map[string]string {
	lbl := labelsForRequestMetricsWithRetry(req, serviceID, endpointPath)
	if instanceID != "" {
		lbl["instance"] = instanceID
	}
	return lbl
}

// localServiceHopLatencyMs is total time at this hop from request.ArrivalTime to completion (queue + processing).
func localServiceHopLatencyMs(request *models.Request, simTime time.Time) float64 {
	return float64(simTime.Sub(request.ArrivalTime).Milliseconds())
}

// localServiceProcessingLatencyMs is StartTime to completion (CPU + network service time only).
func localServiceProcessingLatencyMs(request *models.Request, simTime time.Time) float64 {
	if request.StartTime.IsZero() {
		return localServiceHopLatencyMs(request, simTime)
	}
	return float64(simTime.Sub(request.StartTime).Milliseconds())
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
		lbl := labelsForRequestMetrics(req, req.ServiceName, req.Endpoint)
		el := metrics.EndpointErrorLabels(lbl, metrics.ReasonDrainEvicted)
		metrics.RecordErrorCount(state.collector, 1.0, simTime, el)
		if req.ParentID == "" {
			metrics.RecordIngressLogicalFailure(state.collector, 1.0, simTime, el)
		}
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

		// Count every workload arrival as ingress (including those rejected below) so ingress_error_rate has a correct denominator.
		metrics.RecordRequestCount(state.collector, 1.0, simTime, ingressLabels)

		// Check rate limiting policy
		if state.policies != nil {
			rateLimiting := state.policies.GetRateLimiting()
			if rateLimiting != nil && !rateLimiting.AllowRequest(serviceID, endpointPath, simTime) {
				request.Status = models.RequestStatusFailed
				el := metrics.EndpointErrorLabels(ingressLabels, metrics.ReasonRateLimited)
				metrics.RecordErrorCount(state.collector, 1.0, simTime, el)
				metrics.RecordIngressLogicalFailure(state.collector, 1.0, simTime, el)
				return fmt.Errorf("rate limit exceeded for %s:%s", serviceID, endpointPath)
			}

			circuitBreaker := state.policies.GetCircuitBreaker()
			if circuitBreaker != nil && !circuitBreaker.AllowRequest(serviceID, endpointPath, simTime) {
				request.Status = models.RequestStatusFailed
				el := metrics.EndpointErrorLabels(ingressLabels, metrics.ReasonCircuitOpen)
				metrics.RecordErrorCount(state.collector, 1.0, simTime, el)
				metrics.RecordIngressLogicalFailure(state.collector, 1.0, simTime, el)
				return fmt.Errorf("circuit breaker open for %s:%s", serviceID, endpointPath)
			}
		}

		// Select an instance for this service
		instance, err := state.rm.SelectInstanceForService(serviceID)
		if err != nil {
			request.Status = models.RequestStatusFailed
			el := metrics.EndpointErrorLabels(ingressLabels, metrics.ReasonNoInstance)
			metrics.RecordErrorCount(state.collector, 1.0, simTime, el)
			metrics.RecordIngressLogicalFailure(state.collector, 1.0, simTime, el)
			return fmt.Errorf("no instances available for service %s: %w", serviceID, err)
		}

		// CPU admission is serialized in handleRequestStart via per-instance ReserveCPUWork
		// (FIFO). Same-timestamp arrivals schedule RequestStart at the same sim time; the
		// scheduler orders work without relying on HasCapacityAt + enqueue.
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

		if request.Metadata == nil {
			request.Metadata = make(map[string]interface{})
		}

		cpuTimeMs := prof.CPUTimeMs
		netLatencyMs := prof.NetworkLatencyMs
		memoryMB := prof.MemoryMB
		if mem, ok := request.Metadata["memory_mb"].(float64); ok {
			memoryMB = mem
		}

		if inst, ok := state.rm.GetServiceInstance(instanceID); ok && svc.Behavior != nil && svc.Behavior.SaturationLatencyFactor > 0 {
			u := inst.CPUUtilizationAt(simTime)
			f := 1.0 + svc.Behavior.SaturationLatencyFactor*u
			cpuTimeMs *= f
			netLatencyMs *= f
		}

		if svc.Behavior != nil && svc.Behavior.Cache != nil {
			c := svc.Behavior.Cache
			if state.rng.Float64() < c.HitRate {
				request.Metadata["cache_hit"] = true
				hitTotal := state.rng.NormFloat64(c.HitLatencyMs.Mean, c.HitLatencyMs.Sigma)
				if hitTotal < 0 {
					hitTotal = 0
				}
				cpuTimeMs = hitTotal * 0.4
				netLatencyMs = hitTotal * 0.6
				memoryMB *= 0.85
			} else {
				request.Metadata["cache_miss"] = true
				miss := state.rng.NormFloat64(c.MissLatencyMs.Mean, c.MissLatencyMs.Sigma)
				if miss < 0 {
					miss = 0
				}
				cpuTimeMs += miss * 0.5
				netLatencyMs += miss * 0.5
			}
		}

		pLocal := mergedLocalFailureRate(svc, endpoint)
		if pLocal > 0 && state.rng.Float64() < pLocal {
			request.Status = models.RequestStatusFailed
			lbl := labelsForRequestMetricsWithRetry(request, serviceID, endpointPath)
			rm := eng.GetRunManager()
			if maybeRetrySyncStartFailure(state, eng, rm, request, simTime, metrics.ReasonLocalFailure) {
				el := metrics.EndpointErrorLabels(lbl, metrics.ReasonLocalFailure)
				metrics.RecordErrorCount(state.collector, 1.0, simTime, el)
				return nil
			}
			finalizeRequestFailure(state, eng, rm, request, simTime, lbl, metrics.ReasonLocalFailure)
			return nil
		}

		request.CPUTimeMs = cpuTimeMs
		request.NetworkLatencyMs = netLatencyMs

		var cpuStart, cpuEnd time.Time
		deferredExec := false
		if b, ok := request.Metadata[metaCpuDeferredStart].(bool); ok && b {
			t0, ok0 := metadataTime(request.Metadata, metaCpuServiceStart)
			t1, ok1 := metadataTime(request.Metadata, metaCpuServiceEnd)
			if ok0 && ok1 {
				cpuStart, cpuEnd = t0, t1
				deferredExec = true
				delete(request.Metadata, metaCpuDeferredStart)
				delete(request.Metadata, metaCpuServiceStart)
				delete(request.Metadata, metaCpuServiceEnd)
			}
		}
		if !deferredExec {
			var err error
			cpuStart, cpuEnd, err = state.rm.ReserveCPUWork(instanceID, request.ArrivalTime, cpuTimeMs)
			if err != nil {
				request.Status = models.RequestStatusFailed
				lbl := labelsForRequestMetricsWithRetry(request, serviceID, endpointPath)
				el := metrics.EndpointErrorLabels(lbl, metrics.ReasonNoInstance)
				metrics.RecordErrorCount(state.collector, 1.0, simTime, el)
				if request.ParentID == "" {
					metrics.RecordIngressLogicalFailure(state.collector, 1.0, simTime, el)
				}
				if state.policies != nil {
					circuitBreaker := state.policies.GetCircuitBreaker()
					if circuitBreaker != nil {
						circuitBreaker.RecordFailure(serviceID, endpointPath, simTime)
					}
				}
				rm := eng.GetRunManager()
				if maybeRetrySyncStartFailure(state, eng, rm, request, simTime, metrics.ReasonNoInstance) {
					return nil
				}
				propagateSyncChildFailureFromStartFailure(state, eng, request, simTime, metrics.ReasonNoInstance)
				return nil
			}
			if cpuStart.After(simTime) {
				request.Metadata[metaCpuDeferredStart] = true
				request.Metadata[metaCpuServiceStart] = cpuStart
				request.Metadata[metaCpuServiceEnd] = cpuEnd
				eng.ScheduleAt(engine.EventTypeRequestStart, cpuStart, request, serviceID, map[string]interface{}{
					"endpoint_path": endpointPath,
					"instance_id":   instanceID,
				})
				return nil
			}
		}

		// CPU service interval begins at cpuStart (simTime matches for this invocation).
		request.Status = models.RequestStatusProcessing
		request.StartTime = cpuStart

		queueWait := cpuStart.Sub(request.ArrivalTime)
		if queueWait < 0 {
			queueWait = 0
		}
		request.QueueTimeMs = float64(queueWait.Nanoseconds()) / 1e6
		request.Metadata["queue_wait_ms"] = request.QueueTimeMs
		metrics.RecordQueueWait(state.collector, request.QueueTimeMs, simTime, labelsForQueueWaitMetrics(request, serviceID, endpointPath, instanceID))

		if err := state.rm.AllocateMemory(instanceID, memoryMB); err != nil {
			state.rm.RollbackCPUTailReservation(instanceID, cpuStart, cpuEnd)
			request.Status = models.RequestStatusFailed
			lbl := labelsForRequestMetricsWithRetry(request, serviceID, endpointPath)
			reason := metrics.ReasonMemoryCapacity
			if errors.Is(err, resource.ErrHostMemoryCapacity) {
				reason = metrics.ReasonMemoryCapacity
			}
			el := metrics.EndpointErrorLabels(lbl, reason)
			metrics.RecordErrorCount(state.collector, 1.0, simTime, el)
			if request.ParentID == "" {
				metrics.RecordIngressLogicalFailure(state.collector, 1.0, simTime, el)
			}
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

		if err := state.rm.AllocateCPU(instanceID, cpuTimeMs, cpuStart); err != nil {
			state.rm.ReleaseMemory(instanceID, memoryMB)
			state.rm.RollbackCPUTailReservation(instanceID, cpuStart, cpuEnd)
			request.Status = models.RequestStatusFailed
			lbl := labelsForRequestMetricsWithRetry(request, serviceID, endpointPath)
			el := metrics.EndpointErrorLabels(lbl, metrics.ReasonCPUCapacity)
			metrics.RecordErrorCount(state.collector, 1.0, simTime, el)
			if request.ParentID == "" {
				metrics.RecordIngressLogicalFailure(state.collector, 1.0, simTime, el)
			}
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

		request.Metadata["allocated_cpu_ms"] = cpuTimeMs
		request.Metadata["allocated_memory_mb"] = memoryMB

		if _, ok := state.rm.GetServiceInstance(instanceID); ok {
			recordInstanceAndHostGauges(state, serviceID, instanceID, simTime)
			if prof.QueueClass != "" {
				request.Metadata["queue_class"] = prof.QueueClass
			}
		}

		ioEnd := cpuEnd
		if isDatastoreWorkload(svc, endpoint) {
			maxConn := effectiveDBMaxConnections(svc, endpoint)
			ioDur := sampleEndpointIOWorkloadMs(endpoint, state.rng)
			ioStart, ioEndSlot, _, dbWaitMs, err := state.rm.ReserveDBWork(instanceID, cpuEnd, ioDur, maxConn)
			if err != nil {
				return err
			}
			_ = ioStart
			ioEnd = ioEndSlot
			if dbWaitMs > 0 {
				metrics.RecordDbWait(state.collector, dbWaitMs, simTime, labelsForQueueWaitMetrics(request, serviceID, endpointPath, instanceID))
			}
			request.Metadata["db_wait_ms"] = dbWaitMs
			if maxConn > 0 {
				request.Metadata["db_reserved"] = true
			}
			if inst, ok := state.rm.GetServiceInstance(instanceID); ok {
				metrics.RecordActiveConnections(state.collector, float64(inst.ActiveDBConnections()), simTime, metrics.CreateInstanceLabels(serviceID, instanceID))
			}
		}

		// Completion after CPU + optional datastore IO, then network latency (same hop).
		completionTime := ioEnd.Add(time.Duration(netLatencyMs * float64(time.Millisecond)))
		if endpoint.TimeoutMs > 0 {
			deadline := cpuStart.Add(time.Duration(endpoint.TimeoutMs) * time.Millisecond)
			if completionTime.After(deadline) {
				request.Metadata["local_timeout"] = true
				completionTime = deadline
			}
		}
		eng.ScheduleAt(engine.EventTypeRequestComplete, completionTime, request, serviceID, map[string]interface{}{
			"endpoint_path": endpointPath,
			"instance_id":   instanceID,
		})

		return nil
	}
}

// dequeueNextRequestForInstance schedules the next queued request after the instance is free until scheduleAt
// (caller downstream CPU overhead is ordered before the next hop start).
func dequeueNextRequestForInstance(state *scenarioState, eng *engine.Engine, rm *engine.RunManager, instanceID, serviceID, endpointPath string, scheduleAt time.Time) error {
	nextRequestID, hasNext := state.rm.DequeueRequest(instanceID)
	if !hasNext {
		return nil
	}
	nextRequest, found := rm.GetRequest(nextRequestID)
	if !found {
		return fmt.Errorf("queued request %s not found in run manager", nextRequestID)
	}
	eng.ScheduleAt(engine.EventTypeRequestStart, scheduleAt, nextRequest, serviceID, map[string]interface{}{
		"service_id":    serviceID,
		"endpoint_path": endpointPath,
		"instance_id":   instanceID,
	})
	recordInstanceAndHostGauges(state, serviceID, instanceID, scheduleAt)
	return nil
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

		instanceID, hasInstance := request.Metadata["instance_id"].(string)
		if hasInstance {
			if cpuMs, ok := request.Metadata["allocated_cpu_ms"].(float64); ok {
				state.rm.ReleaseCPU(instanceID, cpuMs, simTime)
			}
			if memoryMB, ok := request.Metadata["allocated_memory_mb"].(float64); ok {
				state.rm.ReleaseMemory(instanceID, memoryMB)
			}
			if metadataBool(request.Metadata, "db_reserved") {
				state.rm.ReleaseDBConnection(instanceID)
			}
			recordInstanceAndHostGauges(state, serviceID, instanceID, simTime)
		}

		labels := labelsForRequestMetrics(request, serviceID, endpointPath)

		if metadataBool(request.Metadata, "local_timeout") {
			hopMs := localServiceHopLatencyMs(request, simTime)
			procMs := localServiceProcessingLatencyMs(request, simTime)
			metrics.RecordServiceRequestLatency(state.collector, hopMs, simTime, labels)
			metrics.RecordServiceProcessingLatency(state.collector, procMs, simTime, labels)
			if metadataBool(request.Metadata, "cache_hit") {
				metrics.RecordCacheHitCount(state.collector, 1.0, simTime, labels)
			}
			if metadataBool(request.Metadata, "cache_miss") {
				metrics.RecordCacheMissCount(state.collector, 1.0, simTime, labels)
			}
			finalizeRequestFailure(state, eng, rm, request, simTime, labels, metrics.ReasonLocalFailure)
			if hasInstance {
				if err := dequeueNextRequestForInstance(state, eng, rm, instanceID, serviceID, endpointPath, simTime); err != nil {
					return err
				}
			}
			return nil
		}

		if metadataBool(request.Metadata, "cache_hit") {
			hopMs := localServiceHopLatencyMs(request, simTime)
			procMs := localServiceProcessingLatencyMs(request, simTime)
			metrics.RecordServiceRequestLatency(state.collector, hopMs, simTime, labels)
			metrics.RecordServiceProcessingLatency(state.collector, procMs, simTime, labels)
			metrics.RecordCacheHitCount(state.collector, 1.0, simTime, labels)
			finalizeRequestCompletion(state, eng, rm, request, simTime, labels)
			if hasInstance {
				if err := dequeueNextRequestForInstance(state, eng, rm, instanceID, serviceID, endpointPath, simTime); err != nil {
					return err
				}
			}
			return nil
		}

		downstreamCalls, err := state.interact.GetDownstreamCalls(serviceID, endpointPath)
		if err != nil {
			return fmt.Errorf("failed to get downstream calls for %s:%s: %w", serviceID, endpointPath, err)
		}

		td := metadataInt(request.Metadata, "trace_depth")
		ad := metadataInt(request.Metadata, "async_depth")
		asyncCalls, syncCalls := partitionDownstreamFiltered(state, downstreamCalls, td, ad)

		callerExtraMs := 0.0
		for _, dc := range asyncCalls {
			callerExtraMs += computeDownstreamCallerCPU(state, dc)
		}
		for _, dc := range syncCalls {
			callerExtraMs += computeDownstreamCallerCPU(state, dc)
		}

		hopMs := localServiceHopLatencyMs(request, simTime) + callerExtraMs
		procMs := localServiceProcessingLatencyMs(request, simTime) + callerExtraMs
		metrics.RecordServiceRequestLatency(state.collector, hopMs, simTime, labels)
		metrics.RecordServiceProcessingLatency(state.collector, procMs, simTime, labels)

		if metadataBool(request.Metadata, "cache_miss") {
			metrics.RecordCacheMissCount(state.collector, 1.0, simTime, labels)
		}

		tAfter := simTime
		for _, downstreamCall := range asyncCalls {
			nextTD := td + 1
			nextAD := ad + 1
			tAfter = scheduleDownstreamWithCallerOverhead(state, eng, request, downstreamCall, tAfter, nextTD, nextAD, true, false, 0, "")
		}

		if len(syncCalls) == 0 {
			finalizeRequestCompletion(state, eng, rm, request, simTime, labels)
			if hasInstance {
				if err := dequeueNextRequestForInstance(state, eng, rm, instanceID, serviceID, endpointPath, tAfter); err != nil {
					return err
				}
			}
			return nil
		}

		state.pendingSyncMu.Lock()
		state.pendingSync[request.ID] = len(syncCalls)
		state.pendingSyncMu.Unlock()

		for _, downstreamCall := range syncCalls {
			nextTD := td + 1
			nextAD := ad
			tAfter = scheduleDownstreamWithCallerOverhead(state, eng, request, downstreamCall, tAfter, nextTD, nextAD, false, false, 0, "")
		}
		if hasInstance {
			if err := dequeueNextRequestForInstance(state, eng, rm, instanceID, serviceID, endpointPath, tAfter); err != nil {
				return err
			}
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
