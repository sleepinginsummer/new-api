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
	RegisterDispatchTask("rr-1", "/v1/chat/completions", "default", "gpt")
	RegisterDispatchTask("rr-2", "/v1/chat/completions", "default", "gpt")

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
	RegisterDispatchTask("queue-1", "/v1/chat/completions", "default", "gpt")
	RegisterDispatchTask("queue-2", "/v1/chat/completions", "default", "gpt")

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
