package service

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

func resetDispatcherForTest() {
	dispatcherInstance = &dispatcher{
		roundRobin: make(map[string]int),
		cooldowns:  make(map[int]time.Time),
		waiting:    make(map[string][]*waitingDispatchRequest),
		tasks:      make(map[string]*dispatchTaskRecord),
	}
}

func testChannel(id int, priority int64, concurrency int64) *model.Channel {
	return &model.Channel{
		Id:                 id,
		Name:               "channel",
		Priority:           &priority,
		ChannelConcurrency: &concurrency,
	}
}

func TestAcquireDispatchLeaseRoundRobin(t *testing.T) {
	resetDispatcherForTest()
	common.DeleteChannelConcurrencyState(3001)
	common.DeleteChannelConcurrencyState(3002)

	one := testChannel(3001, 10, 1)
	two := testChannel(3002, 10, 1)
	RegisterDispatchTask("rr-1", "/v1/chat/completions", "default", "gpt", DispatchTaskModeSync)
	RegisterDispatchTask("rr-2", "/v1/chat/completions", "default", "gpt", DispatchTaskModeSync)

	first, err := AcquireDispatchLease(context.Background(), DispatchRequest{
		TaskID:     "rr-1",
		Group:      "default",
		Model:      "gpt",
		Candidates: []*model.Channel{one, two},
	})
	if err != nil {
		t.Fatalf("expected first lease success, got %v", err)
	}
	defer first.Release()

	second, err := AcquireDispatchLease(context.Background(), DispatchRequest{
		TaskID:     "rr-2",
		Group:      "default",
		Model:      "gpt",
		Candidates: []*model.Channel{one, two},
	})
	if err != nil {
		t.Fatalf("expected second lease success, got %v", err)
	}
	defer second.Release()

	if first.Channel.Id == second.Channel.Id {
		t.Fatalf("expected round robin to choose different channels, got %d then %d", first.Channel.Id, second.Channel.Id)
	}
}

func TestAcquireDispatchLeaseWaitsForRelease(t *testing.T) {
	resetDispatcherForTest()
	common.DeleteChannelConcurrencyState(3003)
	channel := testChannel(3003, 10, 1)
	RegisterDispatchTask("queue-1", "/v1/chat/completions", "default", "gpt", DispatchTaskModeSync)
	RegisterDispatchTask("queue-2", "/v1/chat/completions", "default", "gpt", DispatchTaskModeSync)

	first, err := AcquireDispatchLease(context.Background(), DispatchRequest{
		TaskID:     "queue-1",
		Group:      "default",
		Model:      "gpt",
		Candidates: []*model.Channel{channel},
	})
	if err != nil {
		t.Fatalf("expected first lease success, got %v", err)
	}

	done := make(chan *DispatchLease, 1)
	go func() {
		second, secondErr := AcquireDispatchLease(context.Background(), DispatchRequest{
			TaskID:     "queue-2",
			Group:      "default",
			Model:      "gpt",
			Candidates: []*model.Channel{channel},
		})
		if secondErr != nil {
			t.Errorf("expected queued acquire success, got %v", secondErr)
			return
		}
		done <- second
	}()

	time.Sleep(50 * time.Millisecond)
	first.Release()

	select {
	case second := <-done:
		second.Release()
	case <-time.After(time.Second):
		t.Fatal("expected waiter to be woken after release")
	}
}

func TestAcquireDispatchLeaseReturnsAllChannelsFailedWhenCoolingDown(t *testing.T) {
	resetDispatcherForTest()
	channel := testChannel(3004, 10, 1)
	MarkChannelCooldown(channel.Id, time.Minute)

	_, err := AcquireDispatchLease(context.Background(), DispatchRequest{
		TaskID:     "cooldown-1",
		Group:      "default",
		Model:      "gpt",
		Candidates: []*model.Channel{channel},
	})
	if err == nil {
		t.Fatal("expected all channels failed error")
	}
	if err != ErrAllChannelsFailed {
		t.Fatalf("expected ErrAllChannelsFailed, got %v", err)
	}
}

func TestAcquireDispatchLeaseReturnsNoAlternativeChannelWhenOnlyExcludedCandidateRemains(t *testing.T) {
	resetDispatcherForTest()
	channel := testChannel(3005, 10, 1)

	_, err := AcquireDispatchLease(context.Background(), DispatchRequest{
		TaskID:           "exclude-1",
		Group:            "default",
		Model:            "gpt",
		Candidates:       []*model.Channel{channel},
		ExcludeChannelID: channel.Id,
	})
	if err == nil {
		t.Fatal("expected no alternative channel error")
	}
	if err != ErrNoAlternativeChannel {
		t.Fatalf("expected ErrNoAlternativeChannel, got %v", err)
	}
}

func TestAcquireDispatchLeaseDoesNotQueueWhenNoAlternativeChannelExists(t *testing.T) {
	resetDispatcherForTest()
	channel := testChannel(3006, 10, 1)
	RegisterDispatchTask("exclude-queue", "/v1/chat/completions", "default", "gpt", DispatchTaskModeSync)

	_, err := AcquireDispatchLease(context.Background(), DispatchRequest{
		TaskID:           "exclude-queue",
		Group:            "default",
		Model:            "gpt",
		Candidates:       []*model.Channel{channel},
		ExcludeChannelID: channel.Id,
	})
	if err != ErrNoAlternativeChannel {
		t.Fatalf("expected ErrNoAlternativeChannel, got %v", err)
	}

	dispatcherInstance.mu.Lock()
	defer dispatcherInstance.mu.Unlock()
	if len(dispatcherInstance.waiting[dispatcherInstance.shardKey("default", "gpt")]) != 0 {
		t.Fatal("expected no queued waiters when no alternative channel exists")
	}
}

func TestDispatchTaskAsyncHistoryTTL(t *testing.T) {
	resetDispatcherForTest()

	RegisterDispatchTask("async-1", "/v1/chat/completions/async", "default", "gpt", DispatchTaskModeAsync)
	MarkDispatchTaskCompleted("async-1")

	dispatcherInstance.mu.Lock()
	record := dispatcherInstance.tasks["async-1"]
	dispatcherInstance.mu.Unlock()

	if record == nil {
		t.Fatal("expected async dispatch task record to exist")
	}

	got := record.HistoryUntil.Sub(record.CompletedAt)
	if got < dispatchAsyncHistoryTTL-time.Second || got > dispatchAsyncHistoryTTL+time.Second {
		t.Fatalf("expected async history ttl around %v, got %v", dispatchAsyncHistoryTTL, got)
	}
}
