package simd

import (
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/engine"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

// Metadata keys for DES lifecycle / sync-timeout coordination.
const (
	metaDESFinalized       = "des_finalized"
	metaCallerSyncResolved = "caller_sync_resolved"
	metaSubtreeFailed      = "sync_subtree_failed"
	metaFailureReason      = "failure_reason"
	metaAsyncOpTimedOut    = "async_operation_timed_out"
	metaSyncWaitTimedOut   = "sync_wait_timed_out"
	metaDownstreamAsync    = "downstream_call_async"
)

func finalizeRequestCompletion(state *scenarioState, eng *engine.Engine, rm *engine.RunManager, request *models.Request, simTime time.Time, labels map[string]string) {
	if metadataBool(request.Metadata, metaDESFinalized) {
		return
	}
	// Async attempt superseded by retry scheduling: release path in handleRequestComplete already ran;
	// skip success latency / circuit success for this abandoned attempt.
	if metadataBool(request.Metadata, metaAsyncAttemptAbandoned) {
		request.Metadata[metaDESFinalized] = true
		request.Status = models.RequestStatusCompleted
		request.CompletionTime = simTime
		request.Duration = simTime.Sub(request.ArrivalTime)
		return
	}
	request.Metadata[metaDESFinalized] = true
	request.Status = models.RequestStatusCompleted
	request.CompletionTime = simTime
	request.Duration = simTime.Sub(request.ArrivalTime)

	// Async downstream op already timed out: local work finished without success latency / root series.
	if metadataBool(request.Metadata, metaAsyncOpTimedOut) && metadataBool(request.Metadata, metaDownstreamAsync) {
		if state.policies != nil {
			if cb := state.policies.GetCircuitBreaker(); cb != nil {
				cb.RecordFailure(request.ServiceName, request.Endpoint, simTime)
			}
		}
		return
	}

	totalLatencyMs := float64(request.Duration.Milliseconds())
	// Caller already timed out waiting for this sync child: do not emit success latency / CB success.
	if !metadataBool(request.Metadata, metaSyncWaitTimedOut) {
		metrics.RecordLatency(state.collector, totalLatencyMs, simTime, labels)
		if request.ParentID == "" {
			metrics.RecordRootRequestLatency(state.collector, totalLatencyMs, simTime, labels)
		}
		rm.RecordLatency(totalLatencyMs)
		if state.policies != nil {
			if cb := state.policies.GetCircuitBreaker(); cb != nil {
				cb.RecordSuccess(request.ServiceName, request.Endpoint, simTime)
			}
		}
	} else if state.policies != nil && !metadataBool(request.Metadata, metaCallerSyncResolved) {
		// Timeout path already recorded CB failure and/or marked caller resolved; avoid double-counting.
		if cb := state.policies.GetCircuitBreaker(); cb != nil {
			cb.RecordFailure(request.ServiceName, request.Endpoint, simTime)
		}
	}

	if request.ParentID != "" && !metadataBool(request.Metadata, metaDownstreamAsync) {
		notifyParentSyncChildResolved(state, eng, rm, request, request.ParentID, simTime, false, "")
	}
}

// finalizeRequestFailure marks a request failed and propagates to sync parents when applicable.
func finalizeRequestFailure(state *scenarioState, eng *engine.Engine, rm *engine.RunManager, request *models.Request, simTime time.Time, labels map[string]string, reason string) {
	if metadataBool(request.Metadata, metaDESFinalized) {
		return
	}
	request.Metadata[metaDESFinalized] = true
	request.Status = models.RequestStatusFailed
	if reason != "" {
		request.Error = reason
		request.Metadata[metaFailureReason] = reason
	}
	request.CompletionTime = simTime
	request.Duration = simTime.Sub(request.ArrivalTime)

	totalLatencyMs := float64(request.Duration.Milliseconds())
	metrics.RecordLatency(state.collector, totalLatencyMs, simTime, labels)
	if request.ParentID == "" {
		metrics.RecordRootRequestLatency(state.collector, totalLatencyMs, simTime, labels)
	}

	errLabels := metrics.EndpointErrorLabels(labels, reason)
	metrics.RecordErrorCount(state.collector, 1.0, simTime, errLabels)
	if request.ParentID == "" {
		metrics.RecordIngressLogicalFailure(state.collector, 1.0, simTime, errLabels)
	}

	if state.policies != nil {
		if cb := state.policies.GetCircuitBreaker(); cb != nil {
			cb.RecordFailure(request.ServiceName, request.Endpoint, simTime)
		}
	}

	if request.ParentID != "" && !metadataBool(request.Metadata, metaDownstreamAsync) {
		notifyParentSyncChildResolved(state, eng, rm, request, request.ParentID, simTime, true, reason)
	}
}

func notifyParentSyncChildResolved(state *scenarioState, eng *engine.Engine, rm *engine.RunManager, child *models.Request, parentID string, simTime time.Time, childFailed bool, failureReason string) {
	if parentID == "" {
		return
	}
	if metadataBool(child.Metadata, metaDownstreamAsync) {
		return
	}
	if metadataBool(child.Metadata, metaCallerSyncResolved) {
		return
	}
	child.Metadata[metaCallerSyncResolved] = true

	state.pendingSyncMu.Lock()
	n, ok := state.pendingSync[parentID]
	if !ok || n <= 0 {
		state.pendingSyncMu.Unlock()
		return
	}
	n--
	if childFailed {
		if p, ok := rm.GetRequest(parentID); ok && p.Metadata != nil {
			p.Metadata[metaSubtreeFailed] = true
			if failureReason != "" {
				p.Metadata[metaFailureReason] = failureReason
			}
		}
	}
	if n > 0 {
		state.pendingSync[parentID] = n
		state.pendingSyncMu.Unlock()
		return
	}
	delete(state.pendingSync, parentID)
	state.pendingSyncMu.Unlock()

	parent, ok := rm.GetRequest(parentID)
	if !ok {
		return
	}
	plabels := labelsForRequestMetrics(parent, parent.ServiceName, parent.Endpoint)
	if metadataBool(parent.Metadata, metaSubtreeFailed) {
		finalizeRequestFailure(state, eng, rm, parent, simTime, plabels, metrics.ReasonDownstreamFailure)
		return
	}
	finalizeRequestCompletion(state, eng, rm, parent, simTime, plabels)
}

// propagateSyncChildFailureFromStartFailure is used when a downstream child fails before local completion (e.g. CPU allocation).
func propagateSyncChildFailureFromStartFailure(state *scenarioState, eng *engine.Engine, request *models.Request, simTime time.Time, reason string) {
	if request.ParentID == "" {
		return
	}
	if metadataBool(request.Metadata, metaDownstreamAsync) {
		return
	}
	if metadataBool(request.Metadata, metaCallerSyncResolved) {
		return
	}
	request.Metadata[metaCallerSyncResolved] = true

	rm := eng.GetRunManager()
	parentID := request.ParentID
	state.pendingSyncMu.Lock()
	n, ok := state.pendingSync[parentID]
	if !ok || n <= 0 {
		state.pendingSyncMu.Unlock()
		return
	}
	n--
	if p, ok := rm.GetRequest(parentID); ok && p.Metadata != nil {
		p.Metadata[metaSubtreeFailed] = true
		p.Metadata[metaFailureReason] = reason
	}
	if n > 0 {
		state.pendingSync[parentID] = n
		state.pendingSyncMu.Unlock()
		return
	}
	delete(state.pendingSync, parentID)
	state.pendingSyncMu.Unlock()

	parent, ok := rm.GetRequest(parentID)
	if !ok {
		return
	}
	plabels := labelsForRequestMetrics(parent, parent.ServiceName, parent.Endpoint)
	finalizeRequestFailure(state, eng, rm, parent, simTime, plabels, metrics.ReasonDownstreamFailure)
}
