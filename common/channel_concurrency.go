package common

import (
	"context"
	"fmt"
	"sync"
)

// ChannelConcurrencyStats 描述单进程内某个渠道的实时并发状态。
type ChannelConcurrencyStats struct {
	Limit    int64
	InFlight int
	Queued   int
}

// Idle 返回当前可立即处理的空闲槽位数量。
func (s ChannelConcurrencyStats) Idle() int64 {
	if s.Limit <= 0 {
		return 0
	}
	idle := s.Limit - int64(s.InFlight)
	if idle < 0 {
		return 0
	}
	return idle
}

// ChannelConcurrencyLease 表示一次渠道并发占位，必须在请求结束时释放。
type ChannelConcurrencyLease struct {
	channelID int
	limit     int64
	acquired  bool
	released  bool
}

// Release 释放当前请求占用的渠道并发槽位。
func (l *ChannelConcurrencyLease) Release() {
	if l == nil || !l.acquired || l.released || l.limit <= 0 {
		return
	}
	l.released = true
	releaseChannelConcurrency(l.channelID)
}

type channelConcurrencyWaiter struct {
	ch      chan struct{}
	granted bool
}

type channelConcurrencyState struct {
	limit    int64
	inFlight int
	queued   int
	waiters  []*channelConcurrencyWaiter
	mu       sync.Mutex
}

var channelConcurrencyStates sync.Map

func getChannelConcurrencyState(channelID int) *channelConcurrencyState {
	if state, ok := channelConcurrencyStates.Load(channelID); ok {
		return state.(*channelConcurrencyState)
	}
	state := &channelConcurrencyState{}
	actual, _ := channelConcurrencyStates.LoadOrStore(channelID, state)
	return actual.(*channelConcurrencyState)
}

// TryAcquireChannelConcurrency 尝试立即申请渠道并发槽位，不会进入等待队列。
func TryAcquireChannelConcurrency(channelID int, limit int64) (*ChannelConcurrencyLease, bool) {
	lease := &ChannelConcurrencyLease{
		channelID: channelID,
		limit:     limit,
	}
	if limit <= 0 {
		lease.acquired = true
		return lease, true
	}

	state := getChannelConcurrencyState(channelID)
	state.mu.Lock()
	defer state.mu.Unlock()

	state.limit = limit
	if state.inFlight >= int(limit) {
		return nil, false
	}

	state.inFlight++
	lease.acquired = true
	return lease, true
}

// AcquireChannelConcurrency 在真正发起上游请求前申请渠道并发槽位。
func AcquireChannelConcurrency(ctx context.Context, channelID int, limit int64) (*ChannelConcurrencyLease, error) {
	lease := &ChannelConcurrencyLease{
		channelID: channelID,
		limit:     limit,
		acquired:  true,
	}
	if limit <= 0 {
		return lease, nil
	}

	state := getChannelConcurrencyState(channelID)
	state.mu.Lock()
	state.limit = limit
	if state.inFlight < int(limit) && len(state.waiters) == 0 {
		state.inFlight++
		state.mu.Unlock()
		return lease, nil
	}

	waiter := &channelConcurrencyWaiter{ch: make(chan struct{})}
	state.waiters = append(state.waiters, waiter)
	state.queued++
	state.mu.Unlock()

	select {
	case <-waiter.ch:
		return lease, nil
	case <-ctx.Done():
		state.mu.Lock()
		if waiter.granted {
			state.mu.Unlock()
			return lease, nil
		}
		for i, current := range state.waiters {
			if current == waiter {
				state.waiters = append(state.waiters[:i], state.waiters[i+1:]...)
				if state.queued > 0 {
					state.queued--
				}
				break
			}
		}
		state.mu.Unlock()
		return nil, ctx.Err()
	}
}

func releaseChannelConcurrency(channelID int) {
	state := getChannelConcurrencyState(channelID)
	state.mu.Lock()
	defer state.mu.Unlock()

	if state.inFlight > 0 {
		state.inFlight--
	}
	for len(state.waiters) > 0 {
		if state.limit > 0 && state.inFlight >= int(state.limit) {
			return
		}
		waiter := state.waiters[0]
		state.waiters = state.waiters[1:]
		if state.queued > 0 {
			state.queued--
		}
		if waiter == nil || waiter.granted {
			continue
		}
		waiter.granted = true
		state.inFlight++
		close(waiter.ch)
		return
	}
}

// GetChannelConcurrencyStats 返回指定渠道的实时并发状态快照。
func GetChannelConcurrencyStats(channelID int, limit int64) ChannelConcurrencyStats {
	stats := ChannelConcurrencyStats{Limit: limit}
	if limit <= 0 {
		return stats
	}
	state := getChannelConcurrencyState(channelID)
	state.mu.Lock()
	defer state.mu.Unlock()
	state.limit = limit
	stats.InFlight = state.inFlight
	stats.Queued = state.queued
	return stats
}

// FormatChannelConcurrencyStatus 将并发状态格式化为“排队数/处理中数/并发限制”。
func FormatChannelConcurrencyStatus(stats ChannelConcurrencyStats) string {
	return fmt.Sprintf("%d/%d/%d", stats.Queued, stats.InFlight, stats.Limit)
}

// DeleteChannelConcurrencyState 删除渠道运行态缓存。
func DeleteChannelConcurrencyState(channelID int) {
	channelConcurrencyStates.Delete(channelID)
}
