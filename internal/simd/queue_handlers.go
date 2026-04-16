package simd

import (
	"fmt"
	"strings"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/engine"
	"github.com/GoSim-25-26J-441/simulation-core/internal/interaction"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
	"github.com/GoSim-25-26J-441/simulation-core/internal/resource"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/utils"
)

const (
	metaQueueEdge             = "queue_edge"
	metaBrokerService         = "broker_service"
	metaBrokerTopic           = "broker_topic"
	metaQueueConsumer         = "queue_consumer"
	metaQueueMsgID            = "queue_msg_id"
	metaQueueShardKey         = "queue_shard_key"
	metaQueueProducerService  = "queue_producer_service"
	metaQueueProducerEp       = "queue_producer_endpoint"
	metaDeferredQueueFinalize = "deferred_queue_finalize"
	metaQueueAckDeadline      = "queue_ack_deadline"
	metaDeferredCallerExtraMs = "deferred_caller_extra_ms"
)

// usesQueueBroker reports whether this edge should use broker semantics (not direct HTTP spawn).
func usesQueueBroker(state *scenarioState, rc interaction.ResolvedCall) bool {
	k := strings.ToLower(strings.TrimSpace(rc.Call.Kind))
	if k == "queue" {
		return true
	}
	tgt := state.services[rc.ServiceID]
	if tgt == nil {
		return false
	}
	return strings.ToLower(strings.TrimSpace(tgt.Kind)) == "queue"
}

func effectiveQueueForBroker(state *scenarioState, brokerID string) *config.QueueBehavior {
	svc := state.services[brokerID]
	if svc == nil || svc.Behavior == nil || svc.Behavior.Queue == nil {
		return config.EffectiveQueueBehavior(nil)
	}
	return config.EffectiveQueueBehavior(svc.Behavior.Queue)
}

func parseConsumerTarget(target string) (svcID, path string, err error) {
	return parseWorkloadTarget(strings.TrimSpace(target))
}

func queueBrokerLabels(state *scenarioState, brokerID, topic, producerSvc, producerEp string) map[string]string {
	lbl := metrics.EndpointLabelsWithOrigin(producerSvc, producerEp, metrics.OriginDownstream)
	if producerSvc == "" {
		lbl = metrics.EndpointLabelsWithOrigin(brokerID, topic, metrics.OriginDownstream)
	}
	lbl["broker_service"] = brokerID
	lbl["topic"] = topic
	lbl["queue"] = topic
	return lbl
}

func queueStateLabels(brokerID, topic string) map[string]string {
	return map[string]string{
		"broker_service": brokerID,
		"topic":          topic,
		"queue":          topic,
	}
}

func sampleQueueDeliveryMs(state *scenarioState, brokerID string) float64 {
	eff := effectiveQueueForBroker(state, brokerID)
	delivery := 0.0
	if eff.DeliveryLatencyMs.Mean > 0 {
		sig := eff.DeliveryLatencyMs.Sigma
		if sig < 0 {
			sig = 0
		}
		delivery = state.rng.NormFloat64(eff.DeliveryLatencyMs.Mean, sig)
		if delivery < 0 {
			delivery = 0
		}
	}
	return delivery
}

// callerTopologyFromRequest returns the producer/caller instance id, zone, and host id.
// When the caller instance no longer exists in the resource manager, zone/host may come from parent metadata if present.
func callerTopologyFromRequest(state *scenarioState, parent *models.Request) (callerInstanceID, callerHostZone, callerHostID string) {
	if parent == nil || parent.Metadata == nil {
		return "", "", ""
	}
	callerInstanceID, _ = parent.Metadata["instance_id"].(string)
	if callerInstanceID != "" && state != nil && state.rm != nil {
		if inst, ok := state.rm.GetServiceInstance(callerInstanceID); ok && inst != nil {
			callerHostID = strings.TrimSpace(inst.HostID())
			if host, ok := state.rm.GetHost(callerHostID); ok && host != nil {
				return callerInstanceID, strings.TrimSpace(host.Zone()), callerHostID
			}
			return callerInstanceID, "", callerHostID
		}
	}
	if parent.Metadata != nil {
		if v, ok := parent.Metadata["caller_host_zone"].(string); ok {
			callerHostZone = strings.TrimSpace(v)
		}
		if v, ok := parent.Metadata["caller_host_id"].(string); ok {
			callerHostID = strings.TrimSpace(v)
		}
	}
	return callerInstanceID, callerHostZone, callerHostID
}

// scheduleQueuePublishFromOverhead schedules delivery latency then queue_enqueue (caller CPU already finished).
// fixedDeliveryMs < 0 samples delivery; otherwise uses the precomputed value (paired with caller CPU overhead scheduling).
// Returns simulation time when the enqueue event fires (publish ack).
func scheduleQueuePublishFromOverhead(state *scenarioState, eng *engine.Engine, parent *models.Request, downstreamCall interaction.ResolvedCall, simTime time.Time, nextTD, nextAD int, fromRetry bool, retryAttempt int, logicalID string, fixedDeliveryMs float64, callerTopology downstreamCallerTopology) time.Time {
	brokerID := downstreamCall.ServiceID
	topic := downstreamCall.Path
	delivery := fixedDeliveryMs
	if delivery < 0 {
		delivery = sampleQueueDeliveryMs(state, brokerID)
	}
	ackTime := simTime.Add(time.Duration(delivery * float64(time.Millisecond)))
	callerInstanceID, callerHostZone, callerHostID := resolveCallerTopologyForSpawn(state, parent, callerTopology)
	data := map[string]interface{}{
		"endpoint_path":         topic,
		"trace_depth":           nextTD,
		"async_depth":           nextAD,
		"is_async_downstream":   true,
		"downstream_timeout_ms": downstreamCall.Call.TimeoutMs,
		metaRetryAttempt:        retryAttempt,
		metaLogicalCallID:       logicalID,
		metaOverheadFromRetry:   fromRetry,
		metaBrokerService:       brokerID,
		metaBrokerTopic:         topic,
		metaQueueEdge:           true,
		"delivery_ms":           delivery,
		"caller_instance_id":    callerInstanceID,
		"caller_host_zone":      callerHostZone,
		"caller_host_id":        callerHostID,
	}
	eng.ScheduleAt(engine.EventTypeQueueEnqueue, ackTime, parent, brokerID, data)
	return ackTime
}

func handleQueueEnqueue(state *scenarioState, eng *engine.Engine) engine.EventHandler {
	return func(eng *engine.Engine, evt *engine.Event) error {
		if evt.Request == nil {
			return fmt.Errorf("request is nil in queue enqueue")
		}
		simTime := eng.GetSimTime()
		state.rm.NoteSimTime(simTime)
		parent := evt.Request
		brokerID := evt.ServiceID
		topic := metadataString(evt.Data, "endpoint_path")
		if topic == "" {
			topic = "/"
		}
		eff := effectiveQueueForBroker(state, brokerID)
		shard := state.rm.GetBrokerQueue(brokerID, topic, eff)

		msgID := utils.GenerateRequestID()
		meta := map[string]interface{}{
			"trace_depth":            metadataInt(evt.Data, "trace_depth"),
			"async_depth":            metadataInt(evt.Data, "async_depth"),
			metaLogicalCallID:        metadataString(evt.Data, metaLogicalCallID),
			metaRetryAttempt:         metadataInt(evt.Data, metaRetryAttempt),
			metaBrokerService:        brokerID,
			metaBrokerTopic:          topic,
			"workload_from":          parent.Metadata["workload_from"],
			"workload_source_kind":   parent.Metadata["workload_source_kind"],
			"workload_traffic_class": parent.Metadata["workload_traffic_class"],
		}
		callerInstanceID := metadataString(evt.Data, "caller_instance_id")
		callerHostZone := metadataString(evt.Data, "caller_host_zone")
		callerHostID := metadataString(evt.Data, "caller_host_id")
		fallbackInstanceID, fallbackHostZone, fallbackHostID := callerTopologyFromRequest(state, parent)
		if callerInstanceID == "" {
			callerInstanceID = fallbackInstanceID
		}
		if callerHostZone == "" {
			callerHostZone = fallbackHostZone
		}
		if callerHostID == "" {
			callerHostID = fallbackHostID
		}
		if callerInstanceID != "" {
			meta["instance_id"] = callerInstanceID
		}
		if callerHostZone != "" {
			meta["caller_host_zone"] = callerHostZone
		}
		if callerHostID != "" {
			meta["caller_host_id"] = callerHostID
		}
		msg := &resource.QueuedMessage{
			ID:              msgID,
			EnqueueTime:     simTime,
			ParentRequestID: parent.ID,
			TraceID:         parent.TraceID,
			Metadata:        meta,
		}
		res := shard.Enqueue(msg)
		lbl := queueBrokerLabels(state, brokerID, topic, parent.ServiceName, parent.Endpoint)
		stateLbl := queueStateLabels(brokerID, topic)
		metrics.RecordQueuePublishAttemptCount(state.collector, 1.0, simTime, lbl)
		if !res.Accepted {
			metrics.RecordQueueDropCount(state.collector, 1.0, simTime, withReason(lbl, res.DropReason))
			metrics.RecordQueueDepth(state.collector, float64(shard.Depth()), simTime, stateLbl)
			return nil
		}
		metrics.RecordQueueEnqueueCount(state.collector, 1.0, simTime, lbl)
		if res.DroppedOldest {
			metrics.RecordQueueDropCount(state.collector, 1.0, simTime, withReason(lbl, "drop_oldest"))
		}
		deliveryMs := metadataFloat64(evt.Data, "delivery_ms")
		metrics.RecordQueuePublishLatencyMs(state.collector, deliveryMs, simTime, lbl)
		metrics.RecordQueueDepth(state.collector, float64(shard.Depth()), simTime, stateLbl)
		metrics.RecordMessageAgeMs(state.collector, 0, simTime, lbl)

		eng.ScheduleAt(engine.EventTypeQueueDequeue, simTime, nil, brokerID, map[string]interface{}{
			metaBrokerService: brokerID,
			metaBrokerTopic:   topic,
		})
		return nil
	}
}

func withReason(lbl map[string]string, reason string) map[string]string {
	out := make(map[string]string, len(lbl)+1)
	for k, v := range lbl {
		out[k] = v
	}
	out["reason"] = reason
	return out
}

func handleQueueDequeue(state *scenarioState, eng *engine.Engine) engine.EventHandler {
	return func(eng *engine.Engine, evt *engine.Event) error {
		simTime := eng.GetSimTime()
		state.rm.NoteSimTime(simTime)
		brokerID := metadataString(evt.Data, metaBrokerService)
		if brokerID == "" {
			brokerID = evt.ServiceID
		}
		topic := metadataString(evt.Data, metaBrokerTopic)
		shard, ok := state.rm.BrokerQueues().GetShard(brokerID, topic)
		if !ok {
			return nil
		}
		eff := effectiveQueueForBroker(state, brokerID)
		msg := shard.TryPopForDispatch()
		if msg == nil {
			return nil
		}
		lbl := queueBrokerLabels(state, brokerID, topic, "", "")
		metrics.RecordQueueDequeueCount(state.collector, 1.0, simTime, lbl)
		metrics.RecordMessageAgeMs(state.collector, float64(simTime.Sub(msg.EnqueueTime).Milliseconds()), simTime, lbl)

		consumerSvc, consumerPath, err := parseConsumerTarget(shard.ConsumerTarget)
		if err != nil {
			return err
		}
		rm := eng.GetRunManager()
		parent, ok := rm.GetRequest(msg.ParentRequestID)
		if !ok {
			shard.ConsumerFinished()
			return nil
		}
		rc := interaction.ResolvedCall{ServiceID: consumerSvc, Path: consumerPath, Call: config.DownstreamCall{}}
		child, err := state.interact.CreateDownstreamRequest(parent, rc)
		if err != nil {
			shard.ConsumerFinished()
			return err
		}
		child.ArrivalTime = simTime
		child.Metadata["trace_depth"] = metadataInt(msg.Metadata, "trace_depth")
		child.Metadata["async_depth"] = metadataInt(msg.Metadata, "async_depth")
		if logical := metadataString(msg.Metadata, metaLogicalCallID); logical != "" {
			child.Metadata[metaLogicalCallID] = logical
		}
		child.Metadata[metaQueueConsumer] = true
		child.Metadata[metaQueueMsgID] = msg.ID
		child.Metadata[metaBrokerService] = brokerID
		child.Metadata[metaBrokerTopic] = topic
		child.Metadata[metaQueueShardKey] = resource.BrokerQueueKey(brokerID, topic)
		if v, ok := msg.Metadata["workload_from"]; ok {
			child.Metadata["workload_from"] = v
		}
		if v, ok := msg.Metadata["workload_source_kind"]; ok {
			child.Metadata["workload_source_kind"] = v
		}
		if v, ok := msg.Metadata["workload_traffic_class"]; ok {
			child.Metadata["workload_traffic_class"] = v
		}
		if v, ok := msg.Metadata[metaRetryAttempt]; ok {
			child.Metadata[metaRetryAttempt] = v
		}
		if v, ok := msg.Metadata["instance_id"].(string); ok && v != "" {
			child.Metadata["caller_instance_id"] = v
		}
		if v, ok := msg.Metadata["caller_host_zone"].(string); ok && v != "" {
			child.Metadata["caller_host_zone"] = v
		}
		if v, ok := msg.Metadata["caller_host_id"].(string); ok && v != "" {
			child.Metadata["caller_host_id"] = v
		}

		inst, _, err := selectInstanceForRequest(state, child, simTime)
		if err != nil {
			shard.RequeueFront(msg)
			return nil
		}
		child.Metadata["instance_id"] = inst.ID()
		rm.AddRequest(child)
		metrics.RecordRequestCount(state.collector, 1.0, simTime, labelsForRequestMetrics(child, consumerSvc, consumerPath))

		eng.ScheduleAt(engine.EventTypeRequestStart, simTime, child, consumerSvc, map[string]interface{}{
			"endpoint_path": consumerPath,
			"instance_id":   inst.ID(),
		})

		if eff.AckTimeoutMs > 0 {
			deadline := simTime.Add(time.Duration(eff.AckTimeoutMs) * time.Millisecond)
			ackData := map[string]interface{}{
				"child_request_id":      child.ID,
				metaBrokerService:       brokerID,
				metaBrokerTopic:         topic,
				metaQueueMsgID:          msg.ID,
				"msg_parent_request_id": msg.ParentRequestID,
				"msg_trace_id":          msg.TraceID,
				"msg_enqueue_time":      msg.EnqueueTime,
				"msg_redeliveries":      msg.Redeliveries,
			}
			if msg.Metadata != nil {
				ackData["msg_metadata"] = msg.Metadata
			}
			eng.ScheduleAtPriority(engine.EventTypeQueueAckTimeout, deadline, 1, nil, "", ackData)
		}
		return nil
	}
}

func handleQueueAckTimeout(state *scenarioState, eng *engine.Engine) engine.EventHandler {
	return func(eng *engine.Engine, evt *engine.Event) error {
		simTime := eng.GetSimTime()
		state.rm.NoteSimTime(simTime)
		childID := metadataString(evt.Data, "child_request_id")
		brokerID := metadataString(evt.Data, metaBrokerService)
		topic := metadataString(evt.Data, metaBrokerTopic)
		msgID := metadataString(evt.Data, metaQueueMsgID)
		rm := eng.GetRunManager()
		child, ok := rm.GetRequest(childID)
		if !ok || child.Status != models.RequestStatusProcessing {
			return nil
		}
		shard, ok := state.rm.BrokerQueues().GetShard(brokerID, topic)
		if !ok {
			return nil
		}
		eff := effectiveQueueForBroker(state, brokerID)

		child.Status = models.RequestStatusFailed
		if child.Metadata == nil {
			child.Metadata = make(map[string]interface{})
		}
		child.Metadata[metaBrokerAckTimedOut] = true
		lbl := labelsForRequestMetrics(child, child.ServiceName, child.Endpoint)
		el := metrics.EndpointErrorLabels(lbl, metrics.ReasonTimeout)
		metrics.RecordErrorCount(state.collector, 1.0, simTime, el)

		prevRed := metadataInt(evt.Data, "msg_redeliveries")
		nextRed := prevRed + 1

		enqueueTime := simTime
		if t, ok := evt.Data["msg_enqueue_time"].(time.Time); ok && !t.IsZero() {
			enqueueTime = t
		}
		msg := &resource.QueuedMessage{
			ID:              msgID,
			EnqueueTime:     enqueueTime,
			Redeliveries:    nextRed,
			ParentRequestID: metadataString(evt.Data, "msg_parent_request_id"),
			TraceID:         metadataString(evt.Data, "msg_trace_id"),
			Metadata:        nil,
		}
		if m, ok := evt.Data["msg_metadata"].(map[string]interface{}); ok {
			msg.Metadata = m
		}

		qLbl := queueBrokerLabels(state, brokerID, topic, "", "")

		if nextRed > eff.MaxRedeliveries {
			shard.ConsumerFinished()
			eng.ScheduleAt(engine.EventTypeQueueDLQ, simTime, nil, brokerID, map[string]interface{}{
				metaBrokerTopic: topic,
			})
		} else {
			shard.RequeueFront(msg)
			shard.NoteRedelivery()
			metrics.RecordQueueRedeliveryCount(state.collector, 1.0, simTime, qLbl)
		}

		eng.ScheduleAt(engine.EventTypeQueueDequeue, simTime, nil, brokerID, map[string]interface{}{
			metaBrokerService: brokerID,
			metaBrokerTopic:   topic,
		})
		return nil
	}
}

func handleAsyncParentFinalize(state *scenarioState, eng *engine.Engine) engine.EventHandler {
	return func(eng *engine.Engine, evt *engine.Event) error {
		if evt.Request == nil {
			return fmt.Errorf("request is nil in async parent finalize")
		}
		simTime := eng.GetSimTime()
		state.rm.NoteSimTime(simTime)
		parent := evt.Request
		rm := eng.GetRunManager()

		state.pendingSyncMu.Lock()
		n, syncPending := state.pendingSync[parent.ID]
		state.pendingSyncMu.Unlock()
		if syncPending && n > 0 {
			return nil
		}

		if parent.Metadata != nil {
			delete(parent.Metadata, metaDeferredQueueFinalize)
			delete(parent.Metadata, metaQueueAckDeadline)
		}
		extra := 0.0
		if parent.Metadata != nil {
			extra = metadataFloat64(parent.Metadata, metaDeferredCallerExtraMs)
			delete(parent.Metadata, metaDeferredCallerExtraMs)
		}

		labels := labelsForRequestMetrics(parent, parent.ServiceName, parent.Endpoint)
		hopMs := localServiceHopLatencyMs(parent, simTime) + extra
		procMs := localServiceProcessingLatencyMs(parent, simTime) + extra
		metrics.RecordServiceRequestLatency(state.collector, hopMs, simTime, labels)
		metrics.RecordServiceProcessingLatency(state.collector, procMs, simTime, labels)
		finalizeRequestCompletion(state, eng, rm, parent, simTime, labels)
		if inst, ok := parent.Metadata["instance_id"].(string); ok {
			_ = dequeueNextRequestForInstance(state, eng, rm, inst, parent.ServiceName, parent.Endpoint, simTime)
		}
		return nil
	}
}

// handleQueueDLQ is reserved for explicit DLQ emission events.
func handleQueueDLQ(state *scenarioState, eng *engine.Engine) engine.EventHandler {
	return func(eng *engine.Engine, evt *engine.Event) error {
		simTime := eng.GetSimTime()
		state.rm.NoteSimTime(simTime)
		if topic := metadataString(evt.Data, metaBrokerTopic); topic != "" {
			if shard, ok := state.rm.BrokerQueues().GetShard(evt.ServiceID, topic); ok {
				shard.NoteDLQ()
			}
		}
		lbl := queueBrokerLabels(state, evt.ServiceID, metadataString(evt.Data, metaBrokerTopic), "", "")
		metrics.RecordQueueDlqCount(state.collector, 1.0, simTime, lbl)
		return nil
	}
}

// handleQueueRedelivery is reserved for explicit redelivery bookkeeping.
func handleQueueRedelivery(state *scenarioState, eng *engine.Engine) engine.EventHandler {
	return func(eng *engine.Engine, evt *engine.Event) error {
		simTime := eng.GetSimTime()
		state.rm.NoteSimTime(simTime)
		lbl := queueBrokerLabels(state, evt.ServiceID, metadataString(evt.Data, metaBrokerTopic), "", "")
		metrics.RecordQueueRedeliveryCount(state.collector, 1.0, simTime, lbl)
		return nil
	}
}

func metaQueueConsumerDone(state *scenarioState, eng *engine.Engine, request *models.Request, simTime time.Time) {
	if !metadataBool(request.Metadata, metaQueueConsumer) {
		return
	}
	key := metadataString(request.Metadata, metaQueueShardKey)
	if key == "" {
		return
	}
	parts := strings.SplitN(key, ":", 2)
	if len(parts) != 2 {
		return
	}
	shard, ok := state.rm.BrokerQueues().GetShard(parts[0], parts[1])
	if !ok {
		return
	}
	shard.ConsumerFinished()
	lbl := queueStateLabels(parts[0], parts[1])
	metrics.RecordQueueDepth(state.collector, float64(shard.Depth()), simTime, lbl)
	eng.ScheduleAt(engine.EventTypeQueueDequeue, simTime, nil, parts[0], map[string]interface{}{
		metaBrokerService: parts[0],
		metaBrokerTopic:   parts[1],
	})
}
