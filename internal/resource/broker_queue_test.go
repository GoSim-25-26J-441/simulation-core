package resource

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestBrokerQueueEnqueueDropPolicies(t *testing.T) {
	now := time.Now()

	t.Run("reject", func(t *testing.T) {
		s := newBrokerQueues().GetOrCreateShard("mq", "/a", &config.QueueBehavior{
			Capacity: 1, DropPolicy: "reject", ConsumerTarget: "svc:/p", ConsumerConcurrency: 1,
		})
		_ = s.Enqueue(&QueuedMessage{ID: "m1", EnqueueTime: now})
		res := s.Enqueue(&QueuedMessage{ID: "m2", EnqueueTime: now})
		if res.Accepted || res.DropReason != "reject" {
			t.Fatalf("expected reject drop, got %+v", res)
		}
		if snap := s.Snapshot(now); snap.DropCount != 1 || snap.Depth != 1 {
			t.Fatalf("unexpected reject snapshot: %+v", snap)
		}
	})

	t.Run("drop_newest", func(t *testing.T) {
		s := newBrokerQueues().GetOrCreateShard("mq", "/b", &config.QueueBehavior{
			Capacity: 1, DropPolicy: "drop_newest", ConsumerTarget: "svc:/p", ConsumerConcurrency: 1,
		})
		_ = s.Enqueue(&QueuedMessage{ID: "m1", EnqueueTime: now})
		res := s.Enqueue(&QueuedMessage{ID: "m2", EnqueueTime: now})
		if res.Accepted || res.DropReason != "drop_newest" {
			t.Fatalf("expected drop_newest reject, got %+v", res)
		}
		if snap := s.Snapshot(now); snap.DropCount != 1 || snap.Depth != 1 {
			t.Fatalf("unexpected drop_newest snapshot: %+v", snap)
		}
	})

	t.Run("drop_oldest", func(t *testing.T) {
		s := newBrokerQueues().GetOrCreateShard("mq", "/c", &config.QueueBehavior{
			Capacity: 1, DropPolicy: "drop_oldest", ConsumerTarget: "svc:/p", ConsumerConcurrency: 1,
		})
		_ = s.Enqueue(&QueuedMessage{ID: "m1", EnqueueTime: now.Add(-time.Second)})
		res := s.Enqueue(&QueuedMessage{ID: "m2", EnqueueTime: now})
		if !res.Accepted || !res.DroppedOldest || res.DropReason != "drop_oldest" {
			t.Fatalf("expected accepted drop_oldest, got %+v", res)
		}
		msg := s.TryPopForDispatch()
		if msg == nil || msg.ID != "m2" {
			t.Fatalf("expected newest retained, got %+v", msg)
		}
		if snap := s.Snapshot(now); snap.DropCount != 1 {
			t.Fatalf("expected dropCount=1 for drop_oldest, got %+v", snap)
		}
	})

	t.Run("block_full", func(t *testing.T) {
		s := newBrokerQueues().GetOrCreateShard("mq", "/d", &config.QueueBehavior{
			Capacity: 1, DropPolicy: "block", ConsumerTarget: "svc:/p", ConsumerConcurrency: 1,
		})
		_ = s.Enqueue(&QueuedMessage{ID: "m1", EnqueueTime: now})
		res := s.Enqueue(&QueuedMessage{ID: "m2", EnqueueTime: now})
		if res.Accepted || res.DropReason != "block_full" {
			t.Fatalf("expected block_full drop, got %+v", res)
		}
		if snap := s.Snapshot(now); snap.DropCount != 1 || snap.Depth != 1 {
			t.Fatalf("unexpected block_full snapshot: %+v", snap)
		}
	})
}

func TestTopicSubscriberPartitionShardsAreIndependent(t *testing.T) {
	bq := newBrokerQueues()
	eff := &config.TopicBehavior{Capacity: 10}
	sub := &config.TopicSubscriber{Name: "sub", ConsumerGroup: "g1", ConsumerConcurrency: 1, ConsumerTarget: "worker:/handle"}
	s0 := bq.GetOrCreateTopicSubscriberPartitionShard("evt", "/orders", 0, "g1", eff, sub)
	s1 := bq.GetOrCreateTopicSubscriberPartitionShard("evt", "/orders", 1, "g1", eff, sub)
	if s0 == s1 {
		t.Fatalf("expected distinct shards per partition")
	}
	_ = s0.Enqueue(&QueuedMessage{ID: "p0", EnqueueTime: time.Now()})
	_ = s1.Enqueue(&QueuedMessage{ID: "p1", EnqueueTime: time.Now()})
	if s0.Depth() != 1 || s1.Depth() != 1 {
		t.Fatalf("unexpected depths p0=%d p1=%d", s0.Depth(), s1.Depth())
	}
}

func TestBrokerQueueShardExpireMessagesByRetention(t *testing.T) {
	now := time.Now()
	s := newBrokerQueues().GetOrCreateTopicSubscriberPartitionShard("evt", "/orders", 2, "g1", &config.TopicBehavior{Capacity: 10}, &config.TopicSubscriber{ConsumerTarget: "w:/h", ConsumerConcurrency: 1})
	_ = s.Enqueue(&QueuedMessage{ID: "old", EnqueueTime: now.Add(-70 * time.Second)})
	_ = s.Enqueue(&QueuedMessage{ID: "new", EnqueueTime: now.Add(-10 * time.Second)})
	expired := s.ExpireQueuedByRetention(now, 60000)
	if len(expired) != 1 {
		t.Fatalf("expected 1 expired, got %d", len(expired))
	}
	if s.Depth() != 1 {
		t.Fatalf("expected depth 1 after expiry, got %d", s.Depth())
	}
	msg := s.TryPopForDispatch()
	if msg == nil || msg.ID != "new" {
		t.Fatalf("expected newest message to remain, got %+v", msg)
	}
}

func TestTopicShardRetentionScheduleDedup(t *testing.T) {
	s := newBrokerQueues().GetOrCreateTopicSubscriberPartitionShard("evt", "/orders", 0, "g1", &config.TopicBehavior{Capacity: 10},
		&config.TopicSubscriber{ConsumerTarget: "w:/h", ConsumerConcurrency: 1})
	t0 := time.Unix(1_700_000_000, 0)
	_ = s.Enqueue(&QueuedMessage{ID: "a", EnqueueTime: t0, TopicOffset: 0})
	deadline := t0.Add(100 * time.Millisecond)
	if !s.ShouldScheduleRetentionAt(deadline) {
		t.Fatal("first schedule should pass")
	}
	s.NoteRetentionScheduled(deadline)
	for i := 0; i < 500; i++ {
		if s.ShouldScheduleRetentionAt(deadline) {
			t.Fatalf("iteration %d: duplicate schedule at same fire time should be skipped", i)
		}
	}
	earlier := deadline.Add(-20 * time.Millisecond)
	if !s.ShouldScheduleRetentionAt(earlier) {
		t.Fatal("strictly earlier retention fire time should preempt")
	}
	s.NoteRetentionScheduled(earlier)
	s.ClearRetentionSchedule()
	if !s.ShouldScheduleRetentionAt(deadline) {
		t.Fatal("after clear, same deadline should schedule again")
	}
}

func TestTopicHealthSnapshotsConcurrentNextOffsetNoDeadlock(t *testing.T) {
	bq := newBrokerQueues()
	_ = bq.GetOrCreateTopicSubscriberPartitionShard("evt", "/t", 0, "g", &config.TopicBehavior{Capacity: 10},
		&config.TopicSubscriber{ConsumerTarget: "w:/h", ConsumerConcurrency: 1})
	now := time.Now()
	var stop int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for atomic.LoadInt32(&stop) == 0 {
				_ = bq.TopicHealthSnapshots(now)
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for atomic.LoadInt32(&stop) == 0 {
				_ = bq.NextTopicPartitionOffset("evt", "/t", 0)
			}
		}()
	}
	time.Sleep(100 * time.Millisecond)
	atomic.StoreInt32(&stop, 1)
	wg.Wait()
}
