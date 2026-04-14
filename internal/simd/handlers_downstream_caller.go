package simd

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/engine"
	"github.com/GoSim-25-26J-441/simulation-core/internal/interaction"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

const (
	metaOverheadFromRetry = "overhead_from_retry"
)

// referenceCPUForDownstream picks a deterministic CPU reference (ms) for downstream_fraction_cpu.
func referenceCPUForDownstream(state *scenarioState, downstreamCall interaction.ResolvedCall) float64 {
	if downstreamCall.Call.CallLatencyMs.Mean > 0 {
		return downstreamCall.Call.CallLatencyMs.Mean
	}
	tgtEp, ok := state.endpoints[fmt.Sprintf("%s:%s", downstreamCall.ServiceID, downstreamCall.Path)]
	if ok && tgtEp.MeanCPUMs > 0 {
		return tgtEp.MeanCPUMs
	}
	return 0
}

func clampDownstreamFractionCPU(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

func computeDownstreamCallerCPU(state *scenarioState, downstreamCall interaction.ResolvedCall) float64 {
	fr := clampDownstreamFractionCPU(downstreamCall.Call.DownstreamFractionCPU)
	if fr <= 0 {
		return 0
	}
	return fr * referenceCPUForDownstream(state, downstreamCall)
}

func labelsDownstreamCallerCPU(state *scenarioState, parent *models.Request, callerSvc, callerEp string, downstreamCall interaction.ResolvedCall, data map[string]interface{}) map[string]string {
	lbl := labelsForRequestMetrics(parent, callerSvc, callerEp)
	lbl["downstream_service"] = downstreamCall.ServiceID
	lbl["downstream_endpoint"] = downstreamCall.Path
	lbl["downstream_mode"] = strings.TrimSpace(downstreamCall.Call.Mode)
	if lbl["downstream_mode"] == "" {
		lbl["downstream_mode"] = "sync"
	}
	if data != nil {
		if metadataBool(data, metaOverheadFromRetry) {
			lbl[metrics.LabelIsRetry] = "true"
			lbl[metrics.LabelRetryAttempt] = strconv.Itoa(metadataInt(data, metaRetryAttempt))
		}
	}
	return lbl
}

// scheduleDownstreamWithCallerOverhead reserves caller CPU for downstream_fraction_cpu, then schedules spawn (or retry spawn).
// Returns the simulation time cursor after this edge (cpuEnd when overhead > 0, else tCursor).
func scheduleDownstreamWithCallerOverhead(state *scenarioState, eng *engine.Engine, parent *models.Request, downstreamCall interaction.ResolvedCall, tCursor time.Time, nextTD, nextAD int, isAsync bool, fromRetry bool, retryAttempt int, logicalID string) time.Time {
	if usesQueueBroker(state, downstreamCall) {
		overhead := computeDownstreamCallerCPU(state, downstreamCall)
		if overhead <= 0 {
			return scheduleQueuePublishFromOverhead(state, eng, parent, downstreamCall, tCursor, nextTD, nextAD, fromRetry, retryAttempt, logicalID, -1)
		}
		instanceID, ok := parent.Metadata["instance_id"].(string)
		if !ok || instanceID == "" {
			return scheduleQueuePublishFromOverhead(state, eng, parent, downstreamCall, tCursor, nextTD, nextAD, fromRetry, retryAttempt, logicalID, -1)
		}
		cpuStart, cpuEnd, err := state.rm.ReserveCPUWork(instanceID, tCursor, overhead)
		if err != nil {
			return scheduleQueuePublishFromOverhead(state, eng, parent, downstreamCall, tCursor, nextTD, nextAD, fromRetry, retryAttempt, logicalID, -1)
		}
		deliveryMs := sampleQueueDeliveryMs(state, downstreamCall.ServiceID)
		callerSvc := parent.ServiceName
		callerEp := parent.Endpoint
		dataCommon := map[string]interface{}{
			"instance_id":           instanceID,
			"cpu_ms":                overhead,
			"caller_service":        callerSvc,
			"caller_endpoint":       callerEp,
			"child_endpoint_path":   downstreamCall.Path,
			"trace_depth":           nextTD,
			"async_depth":           nextAD,
			"is_async_downstream":   isAsync,
			"downstream_timeout_ms": downstreamCall.Call.TimeoutMs,
			metaOverheadFromRetry:   fromRetry,
			metaRetryAttempt:        retryAttempt,
			metaLogicalCallID:       logicalID,
			"reserved_cpu_start":    cpuStart,
			"reserved_cpu_end":      cpuEnd,
			metaQueueEdge:           true,
			"queue_delivery_ms":     deliveryMs,
		}
		eng.ScheduleAt(engine.EventTypeDownstreamCallerOverheadStart, cpuStart, parent, downstreamCall.ServiceID, dataCommon)
		eng.ScheduleAt(engine.EventTypeDownstreamCallerOverheadEnd, cpuEnd, parent, downstreamCall.ServiceID, dataCommon)
		ackTime := cpuEnd.Add(time.Duration(deliveryMs * float64(time.Millisecond)))
		return ackTime
	}

	overhead := computeDownstreamCallerCPU(state, downstreamCall)
	if overhead <= 0 {
		if fromRetry {
			execRetrySpawnImmediate(state, eng, parent, downstreamCall, nextTD, nextAD, isAsync, retryAttempt, logicalID)
			return tCursor
		}
		scheduleDownstreamCallEvent(state, eng, parent, downstreamCall, tCursor, nextTD, nextAD, isAsync)
		return tCursor
	}
	instanceID, ok := parent.Metadata["instance_id"].(string)
	if !ok || instanceID == "" {
		if fromRetry {
			execRetrySpawnImmediate(state, eng, parent, downstreamCall, nextTD, nextAD, isAsync, retryAttempt, logicalID)
			return tCursor
		}
		scheduleDownstreamCallEvent(state, eng, parent, downstreamCall, tCursor, nextTD, nextAD, isAsync)
		return tCursor
	}
	cpuStart, cpuEnd, err := state.rm.ReserveCPUWork(instanceID, tCursor, overhead)
	if err != nil {
		if fromRetry {
			execRetrySpawnImmediate(state, eng, parent, downstreamCall, nextTD, nextAD, isAsync, retryAttempt, logicalID)
			return tCursor
		}
		scheduleDownstreamCallEvent(state, eng, parent, downstreamCall, tCursor, nextTD, nextAD, isAsync)
		return tCursor
	}
	callerSvc := parent.ServiceName
	callerEp := parent.Endpoint
	dataCommon := map[string]interface{}{
		"instance_id":         instanceID,
		"cpu_ms":              overhead,
		"caller_service":      callerSvc,
		"caller_endpoint":     callerEp,
		"child_endpoint_path": downstreamCall.Path,
		"trace_depth":         nextTD,
		"async_depth":         nextAD,
		"is_async_downstream": isAsync,
		"downstream_timeout_ms": downstreamCall.Call.TimeoutMs,
		metaOverheadFromRetry: fromRetry,
		metaRetryAttempt:      retryAttempt,
		metaLogicalCallID:     logicalID,
		"reserved_cpu_start":  cpuStart,
		"reserved_cpu_end":    cpuEnd,
	}
	eng.ScheduleAt(engine.EventTypeDownstreamCallerOverheadStart, cpuStart, parent, downstreamCall.ServiceID, dataCommon)
	eng.ScheduleAt(engine.EventTypeDownstreamCallerOverheadEnd, cpuEnd, parent, downstreamCall.ServiceID, dataCommon)
	return cpuEnd
}

func execRetrySpawnImmediate(state *scenarioState, eng *engine.Engine, parent *models.Request, downstreamCall interaction.ResolvedCall, nextTD, nextAD int, isAsync bool, retryAttempt int, logicalID string) {
	data := map[string]interface{}{
		"endpoint_path":         downstreamCall.Path,
		"trace_depth":           nextTD,
		"async_depth":           nextAD,
		"is_async_downstream":   isAsync,
		"downstream_timeout_ms": downstreamCall.Call.TimeoutMs,
		metaRetryAttempt:        retryAttempt,
		metaLogicalCallID:       logicalID,
	}
	evt := &engine.Event{Request: parent, ServiceID: downstreamCall.ServiceID, Data: data}
	_ = execDownstreamSpawnFromEvent(state, eng, parent, evt)
}

func handleDownstreamCallerOverheadStart(state *scenarioState, eng *engine.Engine) engine.EventHandler {
	return func(eng *engine.Engine, evt *engine.Event) error {
		if evt.Request == nil {
			return fmt.Errorf("request is nil in downstream caller overhead start")
		}
		simTime := eng.GetSimTime()
		state.rm.NoteSimTime(simTime)
		instanceID, _ := evt.Data["instance_id"].(string)
		cpuMs := metadataFloat64(evt.Data, "cpu_ms")
		parent := evt.Request
		callerSvc := metadataString(evt.Data, "caller_service")
		callerEp := metadataString(evt.Data, "caller_endpoint")
		childPath := metadataString(evt.Data, "child_endpoint_path")
		dsCall, ok := resolveDownstreamCallSpec(state, parent, evt.ServiceID, childPath)
		if !ok {
			dsCall = config.DownstreamCall{}
		}
		resolved := interaction.ResolvedCall{ServiceID: evt.ServiceID, Path: childPath, Call: dsCall}
		if err := state.rm.AllocateCPU(instanceID, cpuMs, simTime); err != nil {
			if t0, ok0 := metadataTime(evt.Data, "reserved_cpu_start"); ok0 {
				if t1, ok1 := metadataTime(evt.Data, "reserved_cpu_end"); ok1 {
					state.rm.RollbackCPUTailReservation(instanceID, t0, t1)
				}
			}
			lbl := labelsForRequestMetricsWithRetry(parent, callerSvc, callerEp)
			el := metrics.EndpointErrorLabels(lbl, metrics.ReasonCPUCapacity)
			metrics.RecordErrorCount(state.collector, 1.0, simTime, el)
			rm := eng.GetRunManager()
			if maybeRetrySyncCallerOverheadFailure(state, eng, rm, parent, evt, simTime, metrics.ReasonCPUCapacity) {
				return nil
			}
			isAsync := metadataBool(evt.Data, "is_async_downstream")
			propagateSyncPendingFromCallerOverheadFailure(state, eng, parent, simTime, metrics.ReasonCPUCapacity, isAsync)
			return nil
		}
		lbl := labelsDownstreamCallerCPU(state, parent, callerSvc, callerEp, resolved, evt.Data)
		metrics.RecordDownstreamCallerCPU(state.collector, cpuMs, simTime, lbl)
		return nil
	}
}

func handleDownstreamCallerOverheadEnd(state *scenarioState, eng *engine.Engine) engine.EventHandler {
	return func(eng *engine.Engine, evt *engine.Event) error {
		if evt.Request == nil {
			return fmt.Errorf("request is nil in downstream caller overhead end")
		}
		simTime := eng.GetSimTime()
		state.rm.NoteSimTime(simTime)
		parent := evt.Request
		instanceID := metadataString(evt.Data, "instance_id")
		cpuMs := metadataFloat64(evt.Data, "cpu_ms")
		childPath := metadataString(evt.Data, "child_endpoint_path")
		dsCall, ok := resolveDownstreamCallSpec(state, parent, evt.ServiceID, childPath)
		if !ok {
			dsCall = config.DownstreamCall{}
		}
		resolved := interaction.ResolvedCall{ServiceID: evt.ServiceID, Path: childPath, Call: dsCall}
		state.rm.ReleaseCPU(instanceID, cpuMs, simTime)
		nextTD := metadataInt(evt.Data, "trace_depth")
		nextAD := metadataInt(evt.Data, "async_depth")
		isAsync := metadataBool(evt.Data, "is_async_downstream")
		fromRetry := metadataBool(evt.Data, metaOverheadFromRetry)
		retryAttempt := metadataInt(evt.Data, metaRetryAttempt)
		logicalID := metadataString(evt.Data, metaLogicalCallID)
		if usesQueueBroker(state, resolved) {
			fixed := -1.0
			if v, ok := evt.Data["queue_delivery_ms"].(float64); ok {
				fixed = v
			}
			scheduleQueuePublishFromOverhead(state, eng, parent, resolved, simTime, nextTD, nextAD, fromRetry, retryAttempt, logicalID, fixed)
			return nil
		}
		if fromRetry {
			execRetrySpawnImmediate(state, eng, parent, resolved, nextTD, nextAD, isAsync, retryAttempt, logicalID)
			return nil
		}
		scheduleDownstreamCallEvent(state, eng, parent, resolved, simTime, nextTD, nextAD, isAsync)
		return nil
	}
}
