package resource

import (
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
}

// BrokerQueueShard models one topic (endpoint path) on a queue service.
type BrokerQueueShard struct {
	mu sync.Mutex

	BrokerID string
	Topic    string

	Capacity        int // 0 = unlimited
	MaxConcurrency  int
	MaxRedeliveries int
	DropPolicy      string
	DLQTarget       string
	ConsumerTarget  string // "svc:path"

	inFlight int
	messages []*QueuedMessage
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
		return EnqueueResult{Accepted: false, DropReason: "reject"}
	case "drop_newest":
		return EnqueueResult{Accepted: false, DropReason: "drop_newest"}
	case "drop_oldest":
		if len(s.messages) == 0 {
			s.messages = append(s.messages, m)
			return EnqueueResult{Accepted: true}
		}
		s.messages = s.messages[1:]
		s.messages = append(s.messages, m)
		return EnqueueResult{Accepted: true, DropReason: "drop_oldest", DroppedOldest: true}
	case "block":
		return EnqueueResult{Accepted: false, DropReason: "block_full"}
	default:
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

// BrokerQueues holds per-topic broker state.
type BrokerQueues struct {
	mu     sync.RWMutex
	shards map[string]*BrokerQueueShard
}

func newBrokerQueues() *BrokerQueues {
	return &BrokerQueues{shards: make(map[string]*BrokerQueueShard)}
}

// GetOrCreateShard returns the broker shard for brokerID + topic.
func (bq *BrokerQueues) GetOrCreateShard(brokerID, topic string, eff *config.QueueBehavior) *BrokerQueueShard {
	if eff == nil {
		eff = config.DefaultQueueBehavior()
	}
	key := BrokerQueueKey(brokerID, topic)
	bq.mu.Lock()
	defer bq.mu.Unlock()
	if s, ok := bq.shards[key]; ok {
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
	bq.shards[key] = s
	return s
}

// GetShard returns an existing shard.
func (bq *BrokerQueues) GetShard(brokerID, topic string) (*BrokerQueueShard, bool) {
	bq.mu.RLock()
	defer bq.mu.RUnlock()
	s, ok := bq.shards[BrokerQueueKey(brokerID, topic)]
	return s, ok
}

// AllShards returns shards for metrics gauges.
func (bq *BrokerQueues) AllShards() []*BrokerQueueShard {
	bq.mu.RLock()
	defer bq.mu.RUnlock()
	out := make([]*BrokerQueueShard, 0, len(bq.shards))
	for _, s := range bq.shards {
		out = append(out, s)
	}
	return out
}
