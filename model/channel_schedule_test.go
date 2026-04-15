package model

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
)

func limitedChannel(id int, weight uint, limit int64) *Channel {
	return &Channel{
		Id:                 id,
		Weight:             &weight,
		ChannelConcurrency: &limit,
	}
}

func TestSelectChannelBySchedulingPolicyPrefersIdleLimited(t *testing.T) {
	common.DeleteChannelConcurrencyState(2001)
	common.DeleteChannelConcurrencyState(2002)

	unlimited := limitedChannel(2001, 100, 0)
	limited := limitedChannel(2002, 1, 2)

	selected := selectChannelBySchedulingPolicy([]*Channel{unlimited, limited})
	if selected == nil || selected.Id != limited.Id {
		t.Fatalf("expected limited idle channel to be selected first, got %+v", selected)
	}
}

func TestSelectChannelBySchedulingPolicyPrefersHigherIdle(t *testing.T) {
	common.DeleteChannelConcurrencyState(2003)
	common.DeleteChannelConcurrencyState(2004)

	one := limitedChannel(2003, 1, 2)
	two := limitedChannel(2004, 1, 3)

	lease, err := common.AcquireChannelConcurrency(context.Background(), one.Id, one.GetChannelConcurrency())
	if err != nil {
		t.Fatalf("expected acquire success, got %v", err)
	}
	defer lease.Release()

	selected := selectChannelBySchedulingPolicy([]*Channel{one, two})
	if selected == nil || selected.Id != two.Id {
		t.Fatalf("expected higher idle channel, got %+v", selected)
	}
}

func TestSelectChannelBySchedulingPolicyPrefersLowerQueueWhenFull(t *testing.T) {
	common.DeleteChannelConcurrencyState(2005)
	common.DeleteChannelConcurrencyState(2006)

	one := limitedChannel(2005, 1, 1)
	two := limitedChannel(2006, 1, 1)

	firstLease, _ := common.AcquireChannelConcurrency(context.Background(), one.Id, one.GetChannelConcurrency())
	defer firstLease.Release()
	secondLease, _ := common.AcquireChannelConcurrency(context.Background(), two.Id, two.GetChannelConcurrency())
	defer secondLease.Release()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_, _ = common.AcquireChannelConcurrency(ctx, one.Id, one.GetChannelConcurrency())
	}()
	time.Sleep(50 * time.Millisecond)

	selected := selectChannelBySchedulingPolicy([]*Channel{one, two})
	if selected == nil || selected.Id != two.Id {
		t.Fatalf("expected lower queue channel, got %+v", selected)
	}
}

func TestSelectChannelBySchedulingPolicyWeightBias(t *testing.T) {
	common.DeleteChannelConcurrencyState(2007)
	common.DeleteChannelConcurrencyState(2008)

	heavy := limitedChannel(2007, 100, 1)
	light := limitedChannel(2008, 1, 1)

	heavyCount := 0
	for i := 0; i < 1000; i++ {
		selected := selectChannelBySchedulingPolicy([]*Channel{heavy, light})
		if selected != nil && selected.Id == heavy.Id {
			heavyCount++
		}
	}
	if heavyCount <= 700 {
		t.Fatalf("expected heavy weight channel to dominate, heavyCount=%d", heavyCount)
	}
}
