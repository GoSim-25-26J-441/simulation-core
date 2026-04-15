package resource

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

// QueuedMessage is a broker backlog entry before a consumer request is spawned.
type QueuedMessage struct {
	ID              string
	EnqueueTime     time.Time
	Redeliveries    int
	ParentRequestID string
	TraceID         string
	Metadata        map[string]interface{}
	// TopicOffset is assigned for kind:topic per-partition log (0,1,…). Unused for point-to-point queue shards.
	TopicOffset int64
}

// BrokerQueueShard models one topic (endpoint path) on a queue service.
type BrokerQueueShard struct {
	mu sync.Mutex

	BrokerID  string
	Topic     string
	Partition int

	Capacity        int // 0 = unlimited
	MaxConcurrency  int
	MaxRedeliveries int
	DropPolicy      string
	DLQTarget       string
	ConsumerTarget  string // "svc:path"

	// Topic-only (pub/sub subscriber group); empty for point-to-point queue shards.
	AckTimeoutMs   float64
	ConsumerGroup  string
	SubscriberName string

	inFlight        int
	messages        []*QueuedMessage
	dropCount       int64
	redeliveryCount int64
	dlqCount        int64

	// Topic subscriber shards only: contiguous commit frontier for partition offsets (Kafka-like ordering).
	nextCommitExclusive int64
	pendingCommit       map[int64]struct{}

	// retentionNextFireScheduled is the simulation time of the next DES topic_retention_expire we scheduled
	// for this shard (dedup: avoid O(publishes) duplicate events for the same earliest-expiry deadline).
	retentionNextFireScheduled time.Time
}

// BrokerShardRuntimeSnapshot captures current queue/topic shard runtime state.
type BrokerShardRuntimeSnapshot struct {
	Depth              int
	InFlight           int
	MaxConcurrency     int
	ConsumerTarget     string
	OldestMessageAgeMs float64
	DropCount          int64
	RedeliveryCount    int64
	DlqCount           int64
}

// BrokerQueueKey builds a stable map key for broker + topic.
func BrokerQueueKey(brokerID, topic string) string {
	return brokerID + ":" + topic
}

// EnqueueResult describes enqueue outcome.
type EnqueueResult struct {
	Accepted      bool
	DropReason    string
	DroppedOldest bool
	// EvictedTopicOffset is set for topic shards when drop_oldest evicted the prior head (commit/skip that offset).
	EvictedTopicOffset int64
}

// Enqueue adds a message to the shard according to drop policy.
// For drop_policy "block", a full queue rejects the publish (producer may retry at a higher layer).
func (s *BrokerQueueShard) Enqueue(m *QueuedMessage) EnqueueResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	dp := strings.ToLower(strings.TrimSpace(s.DropPolicy))
	if dp == "" {
		dp = "block"
	}

	atCap := s.capacityReachedLocked()

	if !atCap {
		s.messages = append(s.messages, m)
		return EnqueueResult{Accepted: true}
	}

	switch dp {
	case "reject":
		s.dropCount++
		return EnqueueResult{Accepted: false, DropReason: "reject"}
	case "drop_newest":
		s.dropCount++
		return EnqueueResult{Accepted: false, DropReason: "drop_newest"}
	case "drop_oldest":
		if len(s.messages) == 0 {
			s.messages = append(s.messages, m)
			return EnqueueResult{Accepted: true}
		}
		s.dropCount++
		evictedOff := s.messages[0].TopicOffset
		s.messages = s.messages[1:]
		s.messages = append(s.messages, m)
		return EnqueueResult{Accepted: true, DropReason: "drop_oldest", DroppedOldest: true, EvictedTopicOffset: evictedOff}
	case "block":
		s.dropCount++
		return EnqueueResult{Accepted: false, DropReason: "block_full"}
	default:
		s.dropCount++
		return EnqueueResult{Accepted: false, DropReason: "reject"}
	}
}

func (s *BrokerQueueShard) capacityReachedLocked() bool {
	if s.Capacity <= 0 {
		return false
	}
	return len(s.messages) >= s.Capacity
}

// Depth returns current backlog length.
func (s *BrokerQueueShard) Depth() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.messages)
}

// TryPopForDispatch returns the next message if a consumer slot is available.
func (s *BrokerQueueShard) TryPopForDispatch() *QueuedMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.MaxConcurrency > 0 && s.inFlight >= s.MaxConcurrency {
		return nil
	}
	if len(s.messages) == 0 {
		return nil
	}
	m := s.messages[0]
	s.messages = s.messages[1:]
	s.inFlight++
	return m
}

// ConsumerFinished decrements in-flight consumers after a consumer request completes.
func (s *BrokerQueueShard) ConsumerFinished() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inFlight > 0 {
		s.inFlight--
	}
}

// RequeueFront puts a message back at the front (redelivery). Adjusts inFlight if the caller had already popped.
func (s *BrokerQueueShard) RequeueFront(m *QueuedMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inFlight > 0 {
		s.inFlight--
	}
	s.messages = append([]*QueuedMessage{m}, s.messages...)
}

// ExpireQueuedByRetention removes queued messages whose age is >= retentionMs at now (inclusive of boundary),
// from the front of the FIFO (oldest first). Returns offsets removed, in removal order.
// retentionMs <= 0 disables expiry.
func (s *BrokerQueueShard) ExpireQueuedByRetention(now time.Time, retentionMs int) []int64 {
	if retentionMs <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.messages) == 0 {
		return nil
	}
	threshold := now.Add(-time.Duration(retentionMs) * time.Millisecond)
	var removed []int64
	for len(s.messages) > 0 {
		m := s.messages[0]
		if m == nil || m.EnqueueTime.After(threshold) {
			break
		}
		removed = append(removed, m.TopicOffset)
		s.messages = s.messages[1:]
		s.dropCount++
	}
	return removed
}

// RecordTopicOffsetProcessed marks offset o as committed (success, DLQ, or retention skip). Advances the contiguous frontier.
func (s *BrokerQueueShard) RecordTopicOffsetProcessed(o int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pendingCommit == nil {
		s.pendingCommit = make(map[int64]struct{})
	}
	if o < s.nextCommitExclusive {
		return
	}
	s.pendingCommit[o] = struct{}{}
	for {
		if _, ok := s.pendingCommit[s.nextCommitExclusive]; !ok {
			break
		}
		delete(s.pendingCommit, s.nextCommitExclusive)
		s.nextCommitExclusive++
	}
}

// CommittedOffsetExclusive returns the next offset not yet committed for this subscriber group (exclusive frontier).
func (s *BrokerQueueShard) CommittedOffsetExclusive() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nextCommitExclusive
}

// ShouldScheduleRetentionAt returns true if we should schedule topic_retention_expire for candidate (fire time).
func (s *BrokerQueueShard) ShouldScheduleRetentionAt(candidate time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.retentionNextFireScheduled.IsZero() {
		return true
	}
	return candidate.Before(s.retentionNextFireScheduled)
}

// NoteRetentionScheduled records that a DES event was scheduled to fire at sim time at.
func (s *BrokerQueueShard) NoteRetentionScheduled(at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.retentionNextFireScheduled = at
}

// ClearRetentionSchedule clears the dedup state when a retention event fires or becomes irrelevant.
func (s *BrokerQueueShard) ClearRetentionSchedule() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.retentionNextFireScheduled = time.Time{}
}

// OldestQueuedEnqueueTime returns enqueue time of the head message, if any.
func (s *BrokerQueueShard) OldestQueuedEnqueueTime() (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.messages) == 0 {
		return time.Time{}, false
	}
	return s.messages[0].EnqueueTime, true
}

// TopicConsumerLagMessages returns max(0, highWatermarkExclusive-committedExclusive).
func TopicConsumerLagMessages(highWatermarkExclusive, committedExclusive int64) float64 {
	lag := highWatermarkExclusive - committedExclusive
	if lag < 0 {
		return 0
	}
	return float64(lag)
}

// NoteDLQ increments dead-letter count for this shard.
func (s *BrokerQueueShard) NoteDLQ() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dlqCount++
}

// NoteRedelivery increments redelivery count for this shard.
func (s *BrokerQueueShard) NoteRedelivery() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.redeliveryCount++
}

// Snapshot returns current shard runtime state (depth, in-flight, oldest age, counters).
func (s *BrokerQueueShard) Snapshot(now time.Time) BrokerShardRuntimeSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	oldest := 0.0
	if len(s.messages) > 0 {
		age := now.Sub(s.messages[0].EnqueueTime).Milliseconds()
		if age > 0 {
			oldest = float64(age)
		}
	}
	return BrokerShardRuntimeSnapshot{
		Depth:              len(s.messages),
		InFlight:           s.inFlight,
		MaxConcurrency:     s.MaxConcurrency,
		ConsumerTarget:     s.ConsumerTarget,
		OldestMessageAgeMs: oldest,
		DropCount:          s.dropCount,
		RedeliveryCount:    s.redeliveryCount,
		DlqCount:           s.dlqCount,
	}
}

type QueueBrokerHealthSnapshot struct {
	BrokerID           string
	Topic              string
	Depth              int
	InFlight           int
	MaxConcurrency     int
	ConsumerTarget     string
	OldestMessageAgeMs float64
	DropCount          int64
	RedeliveryCount    int64
	DlqCount           int64
}

type TopicBrokerHealthSnapshot struct {
	BrokerID                 string
	Topic                    string
	Partition                int
	Subscriber               string
	ConsumerGroup            string
	HighWatermarkExclusive   int64   // next offset to assign on this partition; log end (exclusive)
	CommittedOffsetExclusive int64   // contiguous commit frontier for this consumer group (exclusive)
	ConsumerLag              float64 // max(0, high_watermark_exclusive - committed_offset_exclusive)
	Depth                    int
	InFlight                 int
	MaxConcurrency           int
	ConsumerTarget           string
	OldestMessageAgeMs       float64
	DropCount                int64
	RedeliveryCount          int64
	DlqCount                 int64
}

// TopicSubscriberShardKey uniquely identifies a subscriber group backlog on a topic service endpoint.
func TopicSubscriberShardKey(brokerID, topicPath, consumerGroup string) string {
	return TopicSubscriberPartitionShardKey(brokerID, topicPath, 0, consumerGroup)
}

// TopicSubscriberPartitionShardKey uniquely identifies a subscriber group backlog on one partition.
func TopicSubscriberPartitionShardKey(brokerID, topicPath string, partition int, consumerGroup string) string {
	return brokerID + "\x1e" + topicPath + "\x1e" + strconv.Itoa(partition) + "\x1e" + consumerGroup
}

// BrokerQueues holds point-to-point queue shards (broker+topic) and pub/sub topic shards (broker+topic+consumerGroup).
type BrokerQueues struct {
	mu          sync.RWMutex
	queueShards map[string]*BrokerQueueShard
	topicShards map[string]*BrokerQueueShard
	// topicPartitionHW maps TopicPartitionLogKey → next offset to assign (exclusive high watermark).
	topicPartitionHW map[string]int64
}

// TopicPartitionLogKey identifies one topic partition on a broker for offset assignment.
func TopicPartitionLogKey(brokerID, topicPath string, partition int) string {
	return brokerID + "\x1f" + topicPath + "\x1f" + strconv.Itoa(partition)
}

func newBrokerQueues() *BrokerQueues {
	return &BrokerQueues{
		queueShards:      make(map[string]*BrokerQueueShard),
		topicShards:      make(map[string]*BrokerQueueShard),
		topicPartitionHW: make(map[string]int64),
	}
}

// NextTopicPartitionOffset assigns a monotonic offset within (broker, topic, partition) and advances the HW exclusive.
func (bq *BrokerQueues) NextTopicPartitionOffset(brokerID, topicPath string, partition int) int64 {
	k := TopicPartitionLogKey(brokerID, topicPath, partition)
	bq.mu.Lock()
	defer bq.mu.Unlock()
	o := bq.topicPartitionHW[k]
	bq.topicPartitionHW[k] = o + 1
	return o
}

// HighWatermarkExclusive returns the next offset that would be assigned (end offset exclusive for the log).
func (bq *BrokerQueues) HighWatermarkExclusive(brokerID, topicPath string, partition int) int64 {
	bq.mu.RLock()
	defer bq.mu.RUnlock()
	return bq.topicPartitionHW[TopicPartitionLogKey(brokerID, topicPath, partition)]
}

// GetOrCreateShard returns the broker shard for brokerID + topic (kind: queue).
func (bq *BrokerQueues) GetOrCreateShard(brokerID, topic string, eff *config.QueueBehavior) *BrokerQueueShard {
	if eff == nil {
		eff = config.DefaultQueueBehavior()
	}
	key := BrokerQueueKey(brokerID, topic)
	bq.mu.Lock()
	defer bq.mu.Unlock()
	if s, ok := bq.queueShards[key]; ok {
		return s
	}
	maxC := eff.ConsumerConcurrency
	if maxC <= 0 {
		maxC = 1
	}
	cap := eff.Capacity
	if cap < 0 {
		cap = 0
	}
	s := &BrokerQueueShard{
		BrokerID:        brokerID,
		Topic:           topic,
		Capacity:        cap,
		MaxConcurrency:  maxC,
		MaxRedeliveries: eff.MaxRedeliveries,
		DropPolicy:      eff.DropPolicy,
		DLQTarget:       eff.DLQTarget,
		ConsumerTarget:  eff.ConsumerTarget,
	}
	bq.queueShards[key] = s
	return s
}

// GetOrCreateTopicSubscriberShard returns backlog + consumer state for one topic subscriber group (kind: topic).
func (bq *BrokerQueues) GetOrCreateTopicSubscriberShard(brokerID, topicPath, consumerGroup string, topicEff *config.TopicBehavior, sub *config.TopicSubscriber) *BrokerQueueShard {
	return bq.GetOrCreateTopicSubscriberPartitionShard(brokerID, topicPath, 0, consumerGroup, topicEff, sub)
}

// GetOrCreateTopicSubscriberPartitionShard returns backlog + consumer state for one topic partition+subscriber group.
func (bq *BrokerQueues) GetOrCreateTopicSubscriberPartitionShard(brokerID, topicPath string, partition int, consumerGroup string, topicEff *config.TopicBehavior, sub *config.TopicSubscriber) *BrokerQueueShard {
	if topicEff == nil {
		topicEff = config.DefaultTopicBehavior()
	}
	if sub == nil {
		sub = &config.TopicSubscriber{}
	}
	key := TopicSubscriberPartitionShardKey(brokerID, topicPath, partition, consumerGroup)
	bq.mu.Lock()
	defer bq.mu.Unlock()
	if s, ok := bq.topicShards[key]; ok {
		return s
	}
	maxC := sub.ConsumerConcurrency
	if maxC <= 0 {
		maxC = 1
	}
	cap := topicEff.Capacity
	if cap < 0 {
		cap = 0
	}
	dp := strings.TrimSpace(sub.DropPolicy)
	if dp == "" {
		dp = "block"
	}
	s := &BrokerQueueShard{
		BrokerID:        brokerID,
		Topic:           topicPath,
		Partition:       partition,
		Capacity:        cap,
		MaxConcurrency:  maxC,
		MaxRedeliveries: sub.MaxRedeliveries,
		DropPolicy:      dp,
		DLQTarget:       strings.TrimSpace(sub.DLQ),
		ConsumerTarget:  strings.TrimSpace(sub.ConsumerTarget),
		AckTimeoutMs:    sub.AckTimeoutMs,
		ConsumerGroup:   strings.TrimSpace(consumerGroup),
		SubscriberName:  strings.TrimSpace(sub.Name),
	}
	bq.topicShards[key] = s
	return s
}

// GetShard returns an existing point-to-point queue shard.
func (bq *BrokerQueues) GetShard(brokerID, topic string) (*BrokerQueueShard, bool) {
	bq.mu.RLock()
	defer bq.mu.RUnlock()
	s, ok := bq.queueShards[BrokerQueueKey(brokerID, topic)]
	return s, ok
}

// GetTopicSubscriberShard returns an existing topic subscriber group shard.
func (bq *BrokerQueues) GetTopicSubscriberShard(brokerID, topicPath, consumerGroup string) (*BrokerQueueShard, bool) {
	return bq.GetTopicSubscriberPartitionShard(brokerID, topicPath, 0, consumerGroup)
}

// GetTopicSubscriberPartitionShard returns an existing topic partition+subscriber group shard.
func (bq *BrokerQueues) GetTopicSubscriberPartitionShard(brokerID, topicPath string, partition int, consumerGroup string) (*BrokerQueueShard, bool) {
	bq.mu.RLock()
	defer bq.mu.RUnlock()
	s, ok := bq.topicShards[TopicSubscriberPartitionShardKey(brokerID, topicPath, partition, consumerGroup)]
	return s, ok
}

// GetTopicShardByKey returns a topic shard by TopicSubscriberShardKey (stored on consumer requests).
func (bq *BrokerQueues) GetTopicShardByKey(key string) (*BrokerQueueShard, bool) {
	bq.mu.RLock()
	defer bq.mu.RUnlock()
	s, ok := bq.topicShards[key]
	return s, ok
}

// AllShards returns all queue and topic shards for metrics gauges.
func (bq *BrokerQueues) AllShards() []*BrokerQueueShard {
	bq.mu.RLock()
	defer bq.mu.RUnlock()
	out := make([]*BrokerQueueShard, 0, len(bq.queueShards)+len(bq.topicShards))
	for _, s := range bq.queueShards {
		out = append(out, s)
	}
	for _, s := range bq.topicShards {
		out = append(out, s)
	}
	return out
}

// QueueHealthSnapshots returns per-(broker,topic) queue shard runtime state.
func (bq *BrokerQueues) QueueHealthSnapshots(now time.Time) []QueueBrokerHealthSnapshot {
	bq.mu.RLock()
	defer bq.mu.RUnlock()
	out := make([]QueueBrokerHealthSnapshot, 0, len(bq.queueShards))
	for _, s := range bq.queueShards {
		snap := s.Snapshot(now)
		out = append(out, QueueBrokerHealthSnapshot{
			BrokerID:           s.BrokerID,
			Topic:              s.Topic,
			Depth:              snap.Depth,
			InFlight:           snap.InFlight,
			MaxConcurrency:     snap.MaxConcurrency,
			ConsumerTarget:     snap.ConsumerTarget,
			OldestMessageAgeMs: snap.OldestMessageAgeMs,
			DropCount:          snap.DropCount,
			RedeliveryCount:    snap.RedeliveryCount,
			DlqCount:           snap.DlqCount,
		})
	}
	return out
}

// TopicHealthSnapshots returns per-(broker,topic,consumer_group) topic shard runtime state.
func (bq *BrokerQueues) TopicHealthSnapshots(now time.Time) []TopicBrokerHealthSnapshot {
	bq.mu.RLock()
	defer bq.mu.RUnlock()
	out := make([]TopicBrokerHealthSnapshot, 0, len(bq.topicShards))
	for _, s := range bq.topicShards {
		snap := s.Snapshot(now)
		// Read HW under the same bq read lock — do not call HighWatermarkExclusive (nested RWMutex on bq deadlocks).
		hw := bq.topicPartitionHW[TopicPartitionLogKey(s.BrokerID, s.Topic, s.Partition)]
		committed := s.CommittedOffsetExclusive()
		lag := TopicConsumerLagMessages(hw, committed)
		out = append(out, TopicBrokerHealthSnapshot{
			BrokerID:                 s.BrokerID,
			Topic:                    s.Topic,
			Partition:                s.Partition,
			Subscriber:               s.SubscriberName,
			ConsumerGroup:            s.ConsumerGroup,
			HighWatermarkExclusive:   hw,
			CommittedOffsetExclusive: committed,
			ConsumerLag:              lag,
			Depth:                    snap.Depth,
			InFlight:                 snap.InFlight,
			MaxConcurrency:           snap.MaxConcurrency,
			ConsumerTarget:           snap.ConsumerTarget,
			OldestMessageAgeMs:       snap.OldestMessageAgeMs,
			DropCount:                snap.DropCount,
			RedeliveryCount:          snap.RedeliveryCount,
			DlqCount:                 snap.DlqCount,
		})
	}
	return out
}
