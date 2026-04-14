package simd

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/engine"
	"github.com/GoSim-25-26J-441/simulation-core/internal/interaction"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
	"github.com/GoSim-25-26J-441/simulation-core/internal/policy"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

// Retry / logical-call metadata (stable across physical attempts).
const (
	metaLogicalCallID        = "logical_call_id"
	metaRetryAttempt         = "retry_attempt"
	metaIsRetry              = "is_retry"
	metaAsyncAttemptAbandoned = "async_attempt_abandoned"
)

func metadataString(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func retryReasonError(reason string) error {
	if reason == "" {
		return errors.New("retryable_failure")
	}
	return errors.New(reason)
}

func getRetryPolicy(pm *policy.Manager) policy.RetryPolicy {
	if pm == nil {
		return nil
	}
	rp := pm.GetRetry()
	if rp == nil || !rp.Enabled() {
		return nil
	}
	return rp
}

// isolateFailedSyncAttempt marks the child so late completion cannot notify the sync parent,
// without decrementing pendingSync (used when a retry will replace the logical attempt).
func isolateFailedSyncAttempt(child *models.Request) {
	if child.Metadata == nil {
		child.Metadata = make(map[string]interface{})
	}
	child.Metadata[metaCallerSyncResolved] = true
}

func labelsForRequestMetricsWithRetry(req *models.Request, serviceID, endpointPath string) map[string]string {
	return labelsForRequestMetrics(req, serviceID, endpointPath)
}

func resolveDownstreamCallSpec(state *scenarioState, parent *models.Request, childSvc, childPath string) (config.DownstreamCall, bool) {
	if parent == nil || state == nil {
		return config.DownstreamCall{}, false
	}
	edges := state.interact.GetGraph().GetDownstreamEdges(parent.ServiceName, parent.Endpoint)
	for _, e := range edges {
		if e.ToServiceID == childSvc && e.ToPath == childPath {
			return e.Call, true
		}
	}
	return config.DownstreamCall{}, false
}

// maybeRetrySyncDependencyFailure schedules a downstream retry after transport/dependency failure (respects retryable=false).
func maybeRetrySyncDependencyFailure(state *scenarioState, eng *engine.Engine, rm *engine.RunManager, child *models.Request, simTime time.Time, reason string, dsCall config.DownstreamCall) bool {
	if !dsCall.IsRetryable() {
		return false
	}
	return maybeRetrySyncStartFailure(state, eng, rm, child, simTime, reason)
}

// execDownstreamSpawn creates a downstream request, records request_count, schedules start and optional timeout.
func execDownstreamSpawn(state *scenarioState, eng *engine.Engine, parentRequest *models.Request, downstreamServiceID, endpointPath string, traceDepth, asyncDepth int, isAsync bool, timeoutMs float64, retryAttempt int, logicalCallID string) error {
	simTime := eng.GetSimTime()
	resolvedCall := interaction.ResolvedCall{ServiceID: downstreamServiceID, Path: endpointPath}
	downstreamRequest, err := state.interact.CreateDownstreamRequest(parentRequest, resolvedCall)
	if err != nil {
		return err
	}

	dsCall, _ := resolveDownstreamCallSpec(state, parentRequest, downstreamServiceID, endpointPath)
	tgtSvc := state.services[downstreamServiceID]
	pFail := mergedDependencyFailureRate(dsCall, tgtSvc)
	if pFail > 0 && state.rng.Float64() < pFail {
		downstreamRequest.Metadata["trace_depth"] = traceDepth
		downstreamRequest.Metadata["async_depth"] = asyncDepth
		downstreamRequest.Metadata[metaDownstreamAsync] = isAsync
		downstreamRequest.Metadata[metaRetryAttempt] = retryAttempt
		if logicalCallID != "" {
			downstreamRequest.Metadata[metaLogicalCallID] = logicalCallID
		} else {
			downstreamRequest.Metadata[metaLogicalCallID] = downstreamRequest.ID
		}
		if retryAttempt > 0 {
			downstreamRequest.Metadata[metaIsRetry] = true
		}
		for _, k := range []string{"workload_from", "workload_source_kind", "workload_traffic_class"} {
			if v, ok := parentRequest.Metadata[k]; ok {
				downstreamRequest.Metadata[k] = v
			}
		}
		downstreamRequest.ArrivalTime = simTime
		downstreamRequest.Status = models.RequestStatusFailed
		if timeoutMs > 0 {
			downstreamRequest.Metadata["downstream_timeout_ms"] = timeoutMs
		}
		reason := metrics.ReasonDependencyFailure
		if tgtSvc != nil && strings.ToLower(strings.TrimSpace(tgtSvc.Kind)) == "external" {
			reason = metrics.ReasonExternalFailure
		}
		rm := eng.GetRunManager()
		rm.AddRequest(downstreamRequest)
		dsLabels := labelsForRequestMetricsWithRetry(downstreamRequest, downstreamServiceID, endpointPath)
		metrics.RecordRequestCount(state.collector, 1.0, simTime, dsLabels)
		if maybeRetrySyncDependencyFailure(state, eng, rm, downstreamRequest, simTime, reason, dsCall) {
			el := metrics.EndpointErrorLabels(dsLabels, reason)
			metrics.RecordErrorCount(state.collector, 1.0, simTime, el)
			return nil
		}
		finalizeRequestFailure(state, eng, rm, downstreamRequest, simTime, dsLabels, reason)
		return nil
	}

	downstreamRequest.Metadata["trace_depth"] = traceDepth
	downstreamRequest.Metadata["async_depth"] = asyncDepth
	downstreamRequest.Metadata[metaDownstreamAsync] = isAsync
	downstreamRequest.Metadata[metaRetryAttempt] = retryAttempt
	if logicalCallID != "" {
		downstreamRequest.Metadata[metaLogicalCallID] = logicalCallID
	} else {
		downstreamRequest.Metadata[metaLogicalCallID] = downstreamRequest.ID
	}
	if retryAttempt > 0 {
		downstreamRequest.Metadata[metaIsRetry] = true
	}
	for _, k := range []string{"workload_from", "workload_source_kind", "workload_traffic_class"} {
		if v, ok := parentRequest.Metadata[k]; ok {
			downstreamRequest.Metadata[k] = v
		}
	}
	downstreamRequest.ArrivalTime = simTime
	if timeoutMs > 0 {
		downstreamRequest.Metadata["downstream_timeout_ms"] = timeoutMs
	}

	dsLabels := labelsForRequestMetricsWithRetry(downstreamRequest, downstreamServiceID, endpointPath)
	metrics.RecordRequestCount(state.collector, 1.0, simTime, dsLabels)

	rm := eng.GetRunManager()
	rm.AddRequest(downstreamRequest)

	eng.ScheduleAt(engine.EventTypeRequestStart, simTime, downstreamRequest, downstreamServiceID, map[string]interface{}{
		"endpoint_path": endpointPath,
	})

	if timeoutMs > 0 {
		deadline := simTime.Add(time.Duration(timeoutMs) * time.Millisecond)
		eng.ScheduleAtPriority(engine.EventTypeDownstreamTimeout, deadline, 1, nil, "", map[string]interface{}{
			"child_request_id":      downstreamRequest.ID,
			"parent_request_id":     parentRequest.ID,
			"is_async_downstream":   isAsync,
		})
	}
	return nil
}

func execDownstreamSpawnFromEvent(state *scenarioState, eng *engine.Engine, parentRequest *models.Request, evt *engine.Event) error {
	downstreamServiceID := evt.ServiceID
	endpointPath, ok := evt.Data["endpoint_path"].(string)
	if !ok {
		endpointPath = "/"
	}
	traceDepth := metadataInt(evt.Data, "trace_depth")
	asyncDepth := metadataInt(evt.Data, "async_depth")
	isAsync := false
	if v, ok := evt.Data["is_async_downstream"].(bool); ok {
		isAsync = v
	}
	timeoutMs := metadataFloat64(evt.Data, "downstream_timeout_ms")
	retryAttempt := metadataInt(evt.Data, metaRetryAttempt)
	logicalID, _ := evt.Data[metaLogicalCallID].(string)
	return execDownstreamSpawn(state, eng, parentRequest, downstreamServiceID, endpointPath, traceDepth, asyncDepth, isAsync, timeoutMs, retryAttempt, logicalID)
}

func scheduleDownstreamRetryEvent(state *scenarioState, eng *engine.Engine, parentRequest *models.Request, downstreamServiceID, endpointPath string, traceDepth, asyncDepth int, isAsync bool, timeoutMs float64, nextRetryAttempt int, logicalCallID string, delay time.Duration) {
	t := eng.GetSimTime().Add(delay)
	data := map[string]interface{}{
		"endpoint_path":           endpointPath,
		"parent_request_id":       parentRequest.ID,
		"trace_depth":             traceDepth,
		"async_depth":             asyncDepth,
		"is_async_downstream":     isAsync,
		"downstream_timeout_ms":   timeoutMs,
		metaRetryAttempt:          nextRetryAttempt,
		metaLogicalCallID:         logicalCallID,
	}
	eng.ScheduleAt(engine.EventTypeDownstreamRetry, t, parentRequest, downstreamServiceID, data)
}

func handleDownstreamRetry(state *scenarioState, eng *engine.Engine) engine.EventHandler {
	return func(eng *engine.Engine, evt *engine.Event) error {
		if evt.Request == nil {
			return fmt.Errorf("request is nil in downstream retry event")
		}
		simTime := eng.GetSimTime()
		state.rm.NoteSimTime(simTime)
		parentRequest := evt.Request
		childSvc := evt.ServiceID
		childPath := metadataString(evt.Data, "endpoint_path")
		dsCall, _ := resolveDownstreamCallSpec(state, parentRequest, childSvc, childPath)
		resolved := interaction.ResolvedCall{ServiceID: childSvc, Path: childPath, Call: dsCall}
		traceDepth := metadataInt(evt.Data, "trace_depth")
		asyncDepth := metadataInt(evt.Data, "async_depth")
		isAsync := metadataBool(evt.Data, "is_async_downstream")
		retryAttempt := metadataInt(evt.Data, metaRetryAttempt)
		logicalID := metadataString(evt.Data, metaLogicalCallID)
		scheduleDownstreamWithCallerOverhead(state, eng, parentRequest, resolved, simTime, traceDepth, asyncDepth, isAsync, true, retryAttempt, logicalID)
		return nil
	}
}

// maybeRetrySyncTimeout handles sync downstream timeout when retries may apply.
func maybeRetrySyncTimeout(state *scenarioState, eng *engine.Engine, rm *engine.RunManager, child *models.Request, parentID string, simTime time.Time) bool {
	rp := getRetryPolicy(state.policies)
	if rp == nil {
		return false
	}
	attempt := metadataInt(child.Metadata, metaRetryAttempt)
	if !rp.ShouldRetry(attempt, retryReasonError(metrics.ReasonTimeout)) {
		return false
	}
	parent, ok := rm.GetRequest(parentID)
	if !ok {
		return false
	}
	logical := metadataString(child.Metadata, metaLogicalCallID)
	if logical == "" {
		logical = child.ID
	}
	nextAttempt := attempt + 1
	delay := rp.GetBackoffDuration(nextAttempt)
	isolateFailedSyncAttempt(child)
	scheduleDownstreamRetryEvent(state, eng, parent, child.ServiceName, child.Endpoint,
		metadataInt(child.Metadata, "trace_depth"),
		metadataInt(child.Metadata, "async_depth"),
		false,
		metadataFloat64(child.Metadata, "downstream_timeout_ms"),
		nextAttempt,
		logical,
		delay,
	)
	return true
}

// maybeRetryAsyncTimeout schedules a downstream retry for async children without blocking parents.
func maybeRetryAsyncTimeout(state *scenarioState, eng *engine.Engine, rm *engine.RunManager, child *models.Request, parentID string, simTime time.Time) bool {
	rp := getRetryPolicy(state.policies)
	if rp == nil {
		return false
	}
	attempt := metadataInt(child.Metadata, metaRetryAttempt)
	if !rp.ShouldRetry(attempt, retryReasonError(metrics.ReasonTimeout)) {
		return false
	}
	parent, ok := rm.GetRequest(parentID)
	if !ok {
		return false
	}
	if child.Metadata == nil {
		child.Metadata = make(map[string]interface{})
	}
	child.Metadata[metaAsyncAttemptAbandoned] = true
	logical := metadataString(child.Metadata, metaLogicalCallID)
	if logical == "" {
		logical = child.ID
	}
	nextAttempt := attempt + 1
	delay := rp.GetBackoffDuration(nextAttempt)
	scheduleDownstreamRetryEvent(state, eng, parent, child.ServiceName, child.Endpoint,
		metadataInt(child.Metadata, "trace_depth"),
		metadataInt(child.Metadata, "async_depth"),
		true,
		metadataFloat64(child.Metadata, "downstream_timeout_ms"),
		nextAttempt,
		logical,
		delay,
	)
	return true
}

// maybeRetrySyncStartFailure schedules a downstream retry after CPU/memory allocation failure on a sync child.
// maybeRetrySyncCallerOverheadFailure schedules a downstream retry after CPU reservation failure on caller-side
// downstream overhead (no child request exists yet).
func maybeRetrySyncCallerOverheadFailure(state *scenarioState, eng *engine.Engine, rm *engine.RunManager, parent *models.Request, evt *engine.Event, simTime time.Time, reason string) bool {
	rp := getRetryPolicy(state.policies)
	if rp == nil || evt == nil || evt.Data == nil {
		return false
	}
	if metadataBool(evt.Data, "is_async_downstream") {
		return false
	}
	attempt := metadataInt(evt.Data, metaRetryAttempt)
	if !rp.ShouldRetry(attempt, retryReasonError(reason)) {
		return false
	}
	childSvc := evt.ServiceID
	childPath := metadataString(evt.Data, "child_endpoint_path")
	logical := metadataString(evt.Data, metaLogicalCallID)
	if logical == "" {
		logical = parent.ID + ":" + childSvc + ":" + childPath
	}
	nextAttempt := attempt + 1
	delay := rp.GetBackoffDuration(nextAttempt)
	scheduleDownstreamRetryEvent(state, eng, parent, childSvc, childPath,
		metadataInt(evt.Data, "trace_depth"),
		metadataInt(evt.Data, "async_depth"),
		false,
		metadataFloat64(evt.Data, "downstream_timeout_ms"),
		nextAttempt,
		logical,
		delay,
	)
	return true
}

func maybeRetrySyncStartFailure(state *scenarioState, eng *engine.Engine, rm *engine.RunManager, child *models.Request, simTime time.Time, reason string) bool {
	rp := getRetryPolicy(state.policies)
	if rp == nil {
		return false
	}
	if child.ParentID == "" || metadataBool(child.Metadata, metaDownstreamAsync) {
		return false
	}
	attempt := metadataInt(child.Metadata, metaRetryAttempt)
	if !rp.ShouldRetry(attempt, retryReasonError(reason)) {
		return false
	}
	parent, ok := rm.GetRequest(child.ParentID)
	if !ok {
		return false
	}
	logical := metadataString(child.Metadata, metaLogicalCallID)
	if logical == "" {
		logical = child.ID
	}
	nextAttempt := attempt + 1
	delay := rp.GetBackoffDuration(nextAttempt)
	isolateFailedSyncAttempt(child)
	scheduleDownstreamRetryEvent(state, eng, parent, child.ServiceName, child.Endpoint,
		metadataInt(child.Metadata, "trace_depth"),
		metadataInt(child.Metadata, "async_depth"),
		false,
		metadataFloat64(child.Metadata, "downstream_timeout_ms"),
		nextAttempt,
		logical,
		delay,
	)
	return true
}
