package simd

import (
	"github.com/GoSim-25-26J-441/simulation-core/internal/engine"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

func handleDownstreamTimeout(state *scenarioState, eng *engine.Engine) engine.EventHandler {
	return func(eng *engine.Engine, evt *engine.Event) error {
		simTime := eng.GetSimTime()
		state.rm.NoteSimTime(simTime)

		childID, _ := evt.Data["child_request_id"].(string)
		parentID, _ := evt.Data["parent_request_id"].(string)
		isAsync, _ := evt.Data["is_async_downstream"].(bool)

		rm := eng.GetRunManager()
		child, ok := rm.GetRequest(childID)
		if !ok {
			return nil
		}

		if metadataBool(child.Metadata, metaCallerSyncResolved) {
			return nil
		}

		if isAsync {
			if child.Status == models.RequestStatusCompleted || child.Status == models.RequestStatusFailed {
				return nil
			}
			if metadataBool(child.Metadata, metaAsyncOpTimedOut) {
				return nil
			}
			lbl := labelsForRequestMetrics(child, child.ServiceName, child.Endpoint)
			errLbl := metrics.EndpointErrorLabels(lbl, metrics.ReasonTimeout)
			metrics.RecordErrorCount(state.collector, 1.0, simTime, errLbl)
			if state.policies != nil {
				if cb := state.policies.GetCircuitBreaker(); cb != nil {
					cb.RecordFailure(child.ServiceName, child.Endpoint, simTime)
				}
			}
			if maybeRetryAsyncTimeout(state, eng, rm, child, parentID, simTime) {
				return nil
			}
			child.Metadata[metaAsyncOpTimedOut] = true
			child.Metadata[metaFailureReason] = metrics.ReasonTimeout
			return nil
		}

		// Sync wait: completion at same sim time runs first (priority 0) before timeout (priority 1).
		if child.Status == models.RequestStatusCompleted {
			return nil
		}

		child.Metadata[metaSyncWaitTimedOut] = true
		child.Metadata[metaFailureReason] = metrics.ReasonTimeout
		lbl := labelsForRequestMetrics(child, child.ServiceName, child.Endpoint)
		errLbl := metrics.EndpointErrorLabels(lbl, metrics.ReasonTimeout)
		metrics.RecordErrorCount(state.collector, 1.0, simTime, errLbl)
		if state.policies != nil {
			if cb := state.policies.GetCircuitBreaker(); cb != nil {
				cb.RecordFailure(child.ServiceName, child.Endpoint, simTime)
			}
		}

		if maybeRetrySyncTimeout(state, eng, rm, child, parentID, simTime) {
			return nil
		}

		notifyParentSyncChildResolved(state, eng, rm, child, parentID, simTime, true, metrics.ReasonTimeout)
		return nil
	}
}
