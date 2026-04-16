package simd

import (
	"fmt"
	"strconv"
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
	metaTopicConsumer    = "topic_consumer"
	metaTopicShardKey    = "topic_shard_key"
	metaTopicSubscriber  = "topic_subscriber_name"
	metaTopicConsumerGrp = "topic_consumer_group"
	metaTopicPartition   = "topic_partition"
	metaTopicOffset      = "topic_offset"
)

// usesTopicBroker reports pub/sub broker semantics (kind: topic downstream or target service).
func usesTopicBroker(state *scenarioState, rc interaction.ResolvedCall) bool {
	k := strings.ToLower(strings.TrimSpace(rc.Call.Kind))
	if k == "topic" {
		return true
	}
	tgt := state.services[rc.ServiceID]
	if tgt == nil {
		return false
	}
	return strings.ToLower(strings.TrimSpace(tgt.Kind)) == "topic"
}

// usesAsyncBroker is true for queue or topic broker edges (async producer, separate consumer work).
func usesAsyncBroker(state *scenarioState, rc interaction.ResolvedCall) bool {
	return usesQueueBroker(state, rc) || usesTopicBroker(state, rc)
}

func effectiveTopicForBroker(state *scenarioState, brokerID string) *config.TopicBehavior {
	svc := state.services[brokerID]
	if svc == nil || svc.Behavior == nil || svc.Behavior.Topic == nil {
		return config.EffectiveTopicBehavior(nil)
	}
	return config.EffectiveTopicBehavior(svc.Behavior.Topic)
}

func sampleTopicDeliveryMs(state *scenarioState, brokerID string) float64 {
	eff := effectiveTopicForBroker(state, brokerID)
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

func topicBrokerLabels(state *scenarioState, brokerID, topicPath, producerSvc, producerEp, subName, consumerGroup, consumerSvc, consumerEp string) map[string]string {
	lbl := metrics.EndpointLabelsWithOrigin(producerSvc, producerEp, metrics.OriginDownstream)
	if producerSvc == "" {
		lbl = metrics.EndpointLabelsWithOrigin(brokerID, topicPath, metrics.OriginDownstream)
	} else {
		lbl["producer_service"] = producerSvc
		lbl["producer_endpoint"] = producerEp
	}
	lbl["topic_service"] = brokerID
	lbl["broker_service"] = brokerID
	lbl["topic"] = topicPath
	if consumerGroup != "" {
		lbl["consumer_group"] = consumerGroup
	}
	if subName != "" {
		lbl["subscriber"] = subName
	}
	if consumerSvc != "" {
		lbl["consumer_service"] = consumerSvc
		lbl["consumer_endpoint"] = consumerEp
	}
	return lbl
}

func withTopicPartitionLabel(lbl map[string]string, partition int) map[string]string {
	if lbl == nil {
		lbl = map[string]string{}
	}
	lbl["partition"] = strconv.Itoa(partition)
	return lbl
}

func topicStateLabels(brokerID, topicPath, subName, consumerGroup string, partition int) map[string]string {
	lbl := map[string]string{
		"topic_service":  brokerID,
		"broker_service": brokerID,
		"topic":          topicPath,
		"partition":      strconv.Itoa(partition),
	}
	if consumerGroup != "" {
		lbl["consumer_group"] = consumerGroup
	}
	if subName != "" {
		lbl["subscriber"] = subName
	}
	return lbl
}

func topicPartitionForPublish(state *scenarioState, brokerID, topicPath string, partitions int, msgMeta map[string]interface{}) int {
	if partitions <= 1 {
		return 0
	}
	if k := metadataString(msgMeta, "partition_key"); k != "" {
		h := 0
		for i := 0; i < len(k); i++ {
			h = (h*31 + int(k[i])) % partitions
		}
		if h < 0 {
			h += partitions
		}
		return h
	}
	key := brokerID + "|" + topicPath
	n := state.topicPartitionCursor[key] % partitions
	state.topicPartitionCursor[key] = (state.topicPartitionCursor[key] + 1) % partitions
	return n
}

func topicAckTimeoutMs(shard *resource.BrokerQueueShard) float64 {
	if shard != nil && shard.AckTimeoutMs > 0 {
		return shard.AckTimeoutMs
	}
	return 0
}

func applyTopicPartitionKeyToPublishData(parent *models.Request, d config.DownstreamCall, data map[string]interface{}) {
	if strings.TrimSpace(d.PartitionKey) != "" {
		data["partition_key"] = strings.TrimSpace(d.PartitionKey)
		return
	}
	from := strings.TrimSpace(d.PartitionKeyFrom)
	if from == "" || parent == nil || parent.Metadata == nil {
		return
	}
	if v, ok := parent.Metadata[from]; ok {
		data["partition_key"] = fmt.Sprint(v)
	}
}

// scheduleTopicShardRetention schedules the next DES retention check at oldest_enqueue + retention_ms (min now).
func scheduleTopicShardRetention(state *scenarioState, eng *engine.Engine, brokerID, topic string, partition int, g string, eff *config.TopicBehavior) {
	ret := int(eff.RetentionMs)
	if ret <= 0 {
		return
	}
	shard, ok := state.rm.BrokerQueues().GetTopicSubscriberPartitionShard(brokerID, topic, partition, g)
	if !ok {
		return
	}
	t0, ok := shard.OldestQueuedEnqueueTime()
	if !ok {
		return
	}
	simTime := eng.GetSimTime()
	deadline := t0.Add(time.Duration(ret) * time.Millisecond)
	if deadline.Before(simTime) {
		deadline = simTime
	}
	if !shard.ShouldScheduleRetentionAt(deadline) {
		return
	}
	eng.ScheduleAt(engine.EventTypeTopicRetentionExpire, deadline, nil, brokerID, map[string]interface{}{
		metaBrokerService:    brokerID,
		metaBrokerTopic:      topic,
		metaTopicConsumerGrp: g,
		metaTopicPartition:   partition,
	})
	shard.NoteRetentionScheduled(deadline)
}

func handleTopicRetentionExpire(state *scenarioState, eng *engine.Engine) engine.EventHandler {
	return func(eng *engine.Engine, evt *engine.Event) error {
		simTime := eng.GetSimTime()
		state.rm.NoteSimTime(simTime)
		brokerID := metadataString(evt.Data, metaBrokerService)
		if brokerID == "" {
			brokerID = evt.ServiceID
		}
		topic := metadataString(evt.Data, metaBrokerTopic)
		g := metadataString(evt.Data, metaTopicConsumerGrp)
		partition := metadataInt(evt.Data, metaTopicPartition)
		eff := effectiveTopicForBroker(state, brokerID)
		ret := int(eff.RetentionMs)
		if ret <= 0 {
			return nil
		}
		shard, ok := state.rm.BrokerQueues().GetTopicSubscriberPartitionShard(brokerID, topic, partition, g)
		if !ok {
			return nil
		}
		shard.ClearRetentionSchedule()
		removed := shard.ExpireQueuedByRetention(simTime, ret)
		if len(removed) == 0 {
			if shard.Depth() > 0 {
				scheduleTopicShardRetention(state, eng, brokerID, topic, partition, g, eff)
			}
			return nil
		}
		for _, off := range removed {
			shard.RecordTopicOffsetProcessed(off)
		}
		lbl := withTopicPartitionLabel(topicBrokerLabels(state, brokerID, topic, "", "", shard.SubscriberName, g, "", ""), partition)
		metrics.RecordTopicDropCount(state.collector, float64(len(removed)), simTime, withReason(lbl, "retention_expired"))
		stateLbl := topicStateLabels(brokerID, topic, shard.SubscriberName, g, partition)
		hw := state.rm.BrokerQueues().HighWatermarkExclusive(brokerID, topic, partition)
		lag := resource.TopicConsumerLagMessages(hw, shard.CommittedOffsetExclusive())
		metrics.RecordTopicBacklogDepth(state.collector, float64(shard.Depth()), simTime, stateLbl)
		metrics.RecordTopicConsumerLag(state.collector, lag, simTime, stateLbl)

		if shard.Depth() > 0 {
			scheduleTopicShardRetention(state, eng, brokerID, topic, partition, g, eff)
		}
		eng.ScheduleAt(engine.EventTypeTopicDequeue, simTime, nil, brokerID, map[string]interface{}{
			metaBrokerService:    brokerID,
			metaBrokerTopic:      topic,
			metaTopicConsumerGrp: g,
			metaTopicSubscriber:  shard.SubscriberName,
			metaTopicPartition:   partition,
		})
		return nil
	}
}

// scheduleTopicPublishFromOverhead schedules delivery latency then topic_publish (caller CPU already finished).
func scheduleTopicPublishFromOverhead(state *scenarioState, eng *engine.Engine, parent *models.Request, downstreamCall interaction.ResolvedCall, simTime time.Time, nextTD, nextAD int, fromRetry bool, retryAttempt int, logicalID string, fixedDeliveryMs float64, callerTopology downstreamCallerTopology) time.Time {
	brokerID := downstreamCall.ServiceID
	topic := downstreamCall.Path
	delivery := fixedDeliveryMs
	if delivery < 0 {
		delivery = sampleTopicDeliveryMs(state, brokerID)
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
		"meta_topic_edge":       true,
		"caller_instance_id":    callerInstanceID,
		"caller_host_zone":      callerHostZone,
		"caller_host_id":        callerHostID,
	}
	applyTopicPartitionKeyToPublishData(parent, downstreamCall.Call, data)
	eng.ScheduleAt(engine.EventTypeTopicPublish, ackTime, parent, brokerID, data)
	return ackTime
}

func handleTopicPublish(state *scenarioState, eng *engine.Engine) engine.EventHandler {
	return func(eng *engine.Engine, evt *engine.Event) error {
		if evt.Request == nil {
			return fmt.Errorf("request is nil in topic publish")
		}
		simTime := eng.GetSimTime()
		state.rm.NoteSimTime(simTime)
		parent := evt.Request
		brokerID := evt.ServiceID
		topic := metadataString(evt.Data, "endpoint_path")
		if topic == "" {
			topic = "/"
		}
		var rawTopic *config.TopicBehavior
		if state.services[brokerID] != nil && state.services[brokerID].Behavior != nil {
			rawTopic = state.services[brokerID].Behavior.Topic
		}
		subs := config.CanonicalTopicSubscribersForHash(rawTopic)
		eff := effectiveTopicForBroker(state, brokerID)
		if len(subs) == 0 {
			subs = eff.Subscribers
		}
		partitions := eff.Partitions
		if partitions <= 0 {
			partitions = 1
		}
		deliveryMs := metadataFloat64(evt.Data, "delivery_ms")
		pubLbl := topicBrokerLabels(state, brokerID, topic, parent.ServiceName, parent.Endpoint, "", "", "", "")
		metrics.RecordTopicPublishCount(state.collector, 1.0, simTime, pubLbl)
		metrics.RecordTopicPublishLatencyMs(state.collector, deliveryMs, simTime, pubLbl)
		partition := topicPartitionForPublish(state, brokerID, topic, partitions, evt.Data)
		partOffset := state.rm.BrokerQueues().NextTopicPartitionOffset(brokerID, topic, partition)

		for i := range subs {
			sub := &subs[i]
			msgID := utils.GenerateRequestID()
			g := strings.TrimSpace(sub.ConsumerGroup)
			shard := state.rm.GetBrokerTopicSubscriberPartitionShard(brokerID, topic, partition, g, eff, sub)
			meta := map[string]interface{}{
				"trace_depth":            metadataInt(evt.Data, "trace_depth"),
				"async_depth":            metadataInt(evt.Data, "async_depth"),
				metaLogicalCallID:        metadataString(evt.Data, metaLogicalCallID),
				metaRetryAttempt:         metadataInt(evt.Data, metaRetryAttempt),
				metaBrokerService:        brokerID,
				metaBrokerTopic:          topic,
				metaTopicSubscriber:      strings.TrimSpace(sub.Name),
				metaTopicConsumerGrp:     g,
				metaTopicPartition:       partition,
				metaTopicOffset:          partOffset,
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
				TopicOffset:     partOffset,
				ParentRequestID: parent.ID,
				TraceID:         parent.TraceID,
				Metadata:        meta,
			}
			res := shard.Enqueue(msg)
			lbl := withTopicPartitionLabel(topicBrokerLabels(state, brokerID, topic, parent.ServiceName, parent.Endpoint, strings.TrimSpace(sub.Name), g, "", ""), partition)
			stateLbl := topicStateLabels(brokerID, topic, strings.TrimSpace(sub.Name), g, partition)

			if !res.Accepted {
				metrics.RecordTopicDropCount(state.collector, 1.0, simTime, withReason(lbl, res.DropReason))
				// Partition log already advanced HW; this group did not store the message — skip/commit offset so lag stays coherent.
				shard.RecordTopicOffsetProcessed(partOffset)
				metrics.RecordTopicBacklogDepth(state.collector, float64(shard.Depth()), simTime, stateLbl)
				hw := state.rm.BrokerQueues().HighWatermarkExclusive(brokerID, topic, partition)
				lag := resource.TopicConsumerLagMessages(hw, shard.CommittedOffsetExclusive())
				metrics.RecordTopicConsumerLag(state.collector, lag, simTime, stateLbl)
				continue
			}
			if res.DroppedOldest {
				metrics.RecordTopicDropCount(state.collector, 1.0, simTime, withReason(lbl, "drop_oldest"))
				shard.RecordTopicOffsetProcessed(res.EvictedTopicOffset)
			}
			metrics.RecordTopicBacklogDepth(state.collector, float64(shard.Depth()), simTime, stateLbl)
			hw := state.rm.BrokerQueues().HighWatermarkExclusive(brokerID, topic, partition)
			lag := resource.TopicConsumerLagMessages(hw, shard.CommittedOffsetExclusive())
			metrics.RecordTopicConsumerLag(state.collector, lag, simTime, stateLbl)
			metrics.RecordTopicMessageAgeMs(state.collector, 0, simTime, lbl)

			scheduleTopicShardRetention(state, eng, brokerID, topic, partition, g, eff)
			eng.ScheduleAt(engine.EventTypeTopicDequeue, simTime, nil, brokerID, map[string]interface{}{
				metaBrokerService:    brokerID,
				metaBrokerTopic:      topic,
				metaTopicConsumerGrp: g,
				metaTopicSubscriber:  strings.TrimSpace(sub.Name),
				metaTopicPartition:   partition,
			})
		}
		return nil
	}
}

func handleTopicDequeue(state *scenarioState, eng *engine.Engine) engine.EventHandler {
	return func(eng *engine.Engine, evt *engine.Event) error {
		simTime := eng.GetSimTime()
		state.rm.NoteSimTime(simTime)
		brokerID := metadataString(evt.Data, metaBrokerService)
		if brokerID == "" {
			brokerID = evt.ServiceID
		}
		topic := metadataString(evt.Data, metaBrokerTopic)
		g := metadataString(evt.Data, metaTopicConsumerGrp)
		partition := metadataInt(evt.Data, metaTopicPartition)
		shard, ok := state.rm.BrokerQueues().GetTopicSubscriberPartitionShard(brokerID, topic, partition, g)
		if !ok {
			return nil
		}
		msg := shard.TryPopForDispatch()
		if msg == nil {
			return nil
		}
		partition = metadataInt(msg.Metadata, metaTopicPartition)
		subName := metadataString(msg.Metadata, metaTopicSubscriber)
		cs, cp, err := parseConsumerTarget(shard.ConsumerTarget)
		if err != nil {
			return err
		}
		lbl := withTopicPartitionLabel(topicBrokerLabels(state, brokerID, topic, "", "", subName, g, cs, cp), partition)
		metrics.RecordTopicDeliverCount(state.collector, 1.0, simTime, lbl)
		metrics.RecordTopicMessageAgeMs(state.collector, float64(simTime.Sub(msg.EnqueueTime).Milliseconds()), simTime, lbl)

		rm := eng.GetRunManager()
		parent, ok := rm.GetRequest(msg.ParentRequestID)
		if !ok {
			shard.ConsumerFinished()
			return nil
		}
		rc := interaction.ResolvedCall{ServiceID: cs, Path: cp, Call: config.DownstreamCall{}}
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
		child.Metadata[metaTopicConsumer] = true
		child.Metadata[metaQueueMsgID] = msg.ID
		child.Metadata[metaBrokerService] = brokerID
		child.Metadata[metaBrokerTopic] = topic
		child.Metadata[metaTopicShardKey] = resource.TopicSubscriberPartitionShardKey(brokerID, topic, partition, g)
		child.Metadata[metaTopicConsumerGrp] = g
		child.Metadata[metaTopicSubscriber] = subName
		child.Metadata[metaTopicPartition] = partition
		child.Metadata[metaTopicOffset] = msg.TopicOffset
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
			scheduleTopicShardRetention(state, eng, brokerID, topic, partition, g, effectiveTopicForBroker(state, brokerID))
			return nil
		}
		child.Metadata["instance_id"] = inst.ID()
		rm.AddRequest(child)
		metrics.RecordRequestCount(state.collector, 1.0, simTime, labelsForRequestMetrics(child, cs, cp))

		eng.ScheduleAt(engine.EventTypeRequestStart, simTime, child, cs, map[string]interface{}{
			"endpoint_path": cp,
			"instance_id":   inst.ID(),
		})

		ackMs := topicAckTimeoutMs(shard)
		if ackMs > 0 {
			deadline := simTime.Add(time.Duration(ackMs) * time.Millisecond)
			ackData := map[string]interface{}{
				"child_request_id":      child.ID,
				metaBrokerService:       brokerID,
				metaBrokerTopic:         topic,
				metaQueueMsgID:          msg.ID,
				metaTopicConsumerGrp:    g,
				metaTopicPartition:      partition,
				"msg_topic_offset":      msg.TopicOffset,
				"msg_parent_request_id": msg.ParentRequestID,
				"msg_trace_id":          msg.TraceID,
				"msg_enqueue_time":      msg.EnqueueTime,
				"msg_redeliveries":      msg.Redeliveries,
			}
			if msg.Metadata != nil {
				ackData["msg_metadata"] = msg.Metadata
			}
			eng.ScheduleAtPriority(engine.EventTypeTopicAckTimeout, deadline, 1, nil, "", ackData)
		}
		return nil
	}
}

func handleTopicAckTimeout(state *scenarioState, eng *engine.Engine) engine.EventHandler {
	return func(eng *engine.Engine, evt *engine.Event) error {
		simTime := eng.GetSimTime()
		state.rm.NoteSimTime(simTime)
		childID := metadataString(evt.Data, "child_request_id")
		brokerID := metadataString(evt.Data, metaBrokerService)
		topic := metadataString(evt.Data, metaBrokerTopic)
		msgID := metadataString(evt.Data, metaQueueMsgID)
		g := metadataString(evt.Data, metaTopicConsumerGrp)
		partition := metadataInt(evt.Data, metaTopicPartition)
		rm := eng.GetRunManager()
		child, ok := rm.GetRequest(childID)
		if !ok || child.Status != models.RequestStatusProcessing {
			return nil
		}
		shard, ok := state.rm.BrokerQueues().GetTopicSubscriberPartitionShard(brokerID, topic, partition, g)
		if !ok {
			return nil
		}

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
			TopicOffset:     metadataInt64(evt.Data, "msg_topic_offset"),
			Redeliveries:    nextRed,
			ParentRequestID: metadataString(evt.Data, "msg_parent_request_id"),
			TraceID:         metadataString(evt.Data, "msg_trace_id"),
			Metadata:        nil,
		}
		if m, ok := evt.Data["msg_metadata"].(map[string]interface{}); ok {
			msg.Metadata = m
		}

		tLbl := withTopicPartitionLabel(topicBrokerLabels(state, brokerID, topic, "", "", shard.SubscriberName, g, "", ""), partition)

		if nextRed > shard.MaxRedeliveries {
			shard.ConsumerFinished()
			eng.ScheduleAt(engine.EventTypeTopicDLQ, simTime, nil, brokerID, map[string]interface{}{
				metaBrokerTopic:      topic,
				metaTopicConsumerGrp: g,
				metaTopicPartition:   partition,
				"msg_topic_offset":   metadataInt64(evt.Data, "msg_topic_offset"),
			})
		} else {
			msg.Redeliveries = nextRed
			shard.RequeueFront(msg)
			shard.NoteRedelivery()
			metrics.RecordTopicRedeliveryCount(state.collector, 1.0, simTime, tLbl)
			scheduleTopicShardRetention(state, eng, brokerID, topic, partition, g, effectiveTopicForBroker(state, brokerID))
		}

		eng.ScheduleAt(engine.EventTypeTopicDequeue, simTime, nil, brokerID, map[string]interface{}{
			metaBrokerService:    brokerID,
			metaBrokerTopic:      topic,
			metaTopicConsumerGrp: g,
			metaTopicSubscriber:  shard.SubscriberName,
			metaTopicPartition:   partition,
		})
		return nil
	}
}

func handleTopicDLQ(state *scenarioState, eng *engine.Engine) engine.EventHandler {
	return func(eng *engine.Engine, evt *engine.Event) error {
		simTime := eng.GetSimTime()
		state.rm.NoteSimTime(simTime)
		g := metadataString(evt.Data, metaTopicConsumerGrp)
		topic := metadataString(evt.Data, metaBrokerTopic)
		partition := metadataInt(evt.Data, metaTopicPartition)
		off := metadataInt64(evt.Data, "msg_topic_offset")
		if shard, ok := state.rm.BrokerQueues().GetTopicSubscriberPartitionShard(evt.ServiceID, topic, partition, g); ok {
			shard.NoteDLQ()
			// DLQ policy: treat as committed/skip at this offset so consumer lag cannot grow unbounded (documented).
			shard.RecordTopicOffsetProcessed(off)
		}
		lbl := withTopicPartitionLabel(topicBrokerLabels(state, evt.ServiceID, topic, "", "", "", g, "", ""), partition)
		metrics.RecordTopicDlqCount(state.collector, 1.0, simTime, lbl)
		return nil
	}
}

func metaTopicConsumerDone(state *scenarioState, eng *engine.Engine, request *models.Request, simTime time.Time) {
	if !metadataBool(request.Metadata, metaTopicConsumer) {
		return
	}
	key := metadataString(request.Metadata, metaTopicShardKey)
	if key == "" {
		return
	}
	shard, ok := state.rm.BrokerQueues().GetTopicShardByKey(key)
	if !ok {
		return
	}
	shard.ConsumerFinished()
	off := metadataInt64(request.Metadata, metaTopicOffset)
	shard.RecordTopicOffsetProcessed(off)
	parts := strings.Split(key, "\x1e")
	brokerID := ""
	topic := ""
	g := ""
	partition := 0
	if len(parts) >= 4 {
		brokerID, topic = parts[0], parts[1]
		if p, err := strconv.Atoi(parts[2]); err == nil && p >= 0 {
			partition = p
		}
		g = parts[3]
	} else if len(parts) >= 3 {
		brokerID, topic, g = parts[0], parts[1], parts[2]
	}
	lbl := topicStateLabels(brokerID, topic, shard.SubscriberName, g, partition)
	hw := state.rm.BrokerQueues().HighWatermarkExclusive(brokerID, topic, partition)
	lag := resource.TopicConsumerLagMessages(hw, shard.CommittedOffsetExclusive())
	metrics.RecordTopicBacklogDepth(state.collector, float64(shard.Depth()), simTime, lbl)
	metrics.RecordTopicConsumerLag(state.collector, lag, simTime, lbl)
	eng.ScheduleAt(engine.EventTypeTopicDequeue, simTime, nil, brokerID, map[string]interface{}{
		metaBrokerService:    brokerID,
		metaBrokerTopic:      topic,
		metaTopicConsumerGrp: g,
		metaTopicSubscriber:  shard.SubscriberName,
		metaTopicPartition:   partition,
	})
}
