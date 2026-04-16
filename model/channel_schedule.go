package model

import (
	"math/rand"

	"github.com/QuantumNous/new-api/common"
)

type scheduledChannelCandidate struct {
	channel *Channel
	stats   common.ChannelConcurrencyStats
}

// selectChannelBySchedulingPolicy 在同一优先级内按空闲优先、排队数、权重综合选择渠道。
func selectChannelBySchedulingPolicy(channels []*Channel) *Channel {
	if len(channels) == 0 {
		return nil
	}
	if len(channels) == 1 {
		return channels[0]
	}

	limitedIdle := make([]scheduledChannelCandidate, 0)
	unlimited := make([]scheduledChannelCandidate, 0)
	limitedQueued := make([]scheduledChannelCandidate, 0)

	for _, channel := range channels {
		if channel == nil {
			continue
		}
		stats := common.GetChannelConcurrencyStats(channel.Id, channel.GetChannelConcurrency())
		candidate := scheduledChannelCandidate{
			channel: channel,
			stats:   stats,
		}
		if channel.GetChannelConcurrency() <= 0 {
			unlimited = append(unlimited, candidate)
			continue
		}
		if stats.Idle() > 0 {
			limitedIdle = append(limitedIdle, candidate)
			continue
		}
		limitedQueued = append(limitedQueued, candidate)
	}

	if selected := selectFromIdleCandidates(limitedIdle); selected != nil {
		return selected
	}
	if selected := selectWeightedChannel(unlimited); selected != nil {
		return selected
	}
	return selectFromQueuedCandidates(limitedQueued)
}

// SelectChannelBySchedulingPolicy 导出给 service 层复用当前调度策略。
func SelectChannelBySchedulingPolicy(channels []*Channel) *Channel {
	return selectChannelBySchedulingPolicy(channels)
}

// selectFromIdleCandidates 先按空闲槽位降序，再按排队数升序，最后按权重打散。
func selectFromIdleCandidates(candidates []scheduledChannelCandidate) *Channel {
	if len(candidates) == 0 {
		return nil
	}
	maxIdle := int64(-1)
	for _, candidate := range candidates {
		if idle := candidate.stats.Idle(); idle > maxIdle {
			maxIdle = idle
		}
	}
	idleBest := make([]scheduledChannelCandidate, 0)
	for _, candidate := range candidates {
		if candidate.stats.Idle() == maxIdle {
			idleBest = append(idleBest, candidate)
		}
	}
	minQueue := -1
	for _, candidate := range idleBest {
		if minQueue == -1 || candidate.stats.Queued < minQueue {
			minQueue = candidate.stats.Queued
		}
	}
	queueBest := make([]scheduledChannelCandidate, 0)
	for _, candidate := range idleBest {
		if candidate.stats.Queued == minQueue {
			queueBest = append(queueBest, candidate)
		}
	}
	return selectWeightedChannel(queueBest)
}

// selectFromQueuedCandidates 在全部满载时按排队数最少优先，再按权重打散。
func selectFromQueuedCandidates(candidates []scheduledChannelCandidate) *Channel {
	if len(candidates) == 0 {
		return nil
	}
	minQueue := -1
	for _, candidate := range candidates {
		if minQueue == -1 || candidate.stats.Queued < minQueue {
			minQueue = candidate.stats.Queued
		}
	}
	queueBest := make([]scheduledChannelCandidate, 0)
	for _, candidate := range candidates {
		if candidate.stats.Queued == minQueue {
			queueBest = append(queueBest, candidate)
		}
	}
	return selectWeightedChannel(queueBest)
}

// selectWeightedChannel 在同一候选层内按现有权重进行随机打散。
func selectWeightedChannel(candidates []scheduledChannelCandidate) *Channel {
	if len(candidates) == 0 {
		return nil
	}
	if len(candidates) == 1 {
		return candidates[0].channel
	}

	sumWeight := 0
	for _, candidate := range candidates {
		sumWeight += candidate.channel.GetWeight()
	}

	smoothingFactor := 1
	smoothingAdjustment := 0
	if sumWeight == 0 {
		sumWeight = len(candidates) * 100
		smoothingAdjustment = 100
	} else if sumWeight/len(candidates) < 10 {
		smoothingFactor = 100
	}

	totalWeight := sumWeight * smoothingFactor
	randomWeight := rand.Intn(totalWeight)
	for _, candidate := range candidates {
		randomWeight -= candidate.channel.GetWeight()*smoothingFactor + smoothingAdjustment
		if randomWeight < 0 {
			return candidate.channel
		}
	}
	return candidates[len(candidates)-1].channel
}
