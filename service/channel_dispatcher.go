package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

var (
	// ErrAllChannelsFailed 表示当前请求的全部候选渠道都处于不可用状态。
	ErrAllChannelsFailed = errors.New("all channels failed")
	// ErrNoAlternativeChannel 表示当前层存在候选渠道，但在排除刚失败渠道后已无可尝试的替代渠道。
	ErrNoAlternativeChannel = errors.New("no alternative channel")
	// ErrDispatchTimeout 表示调度等待超时或被取消。
	ErrDispatchTimeout = errors.New("dispatch timeout")
)

const (
	DispatchTaskStatusQueued     = "queued"
	DispatchTaskStatusInProgress = "in_progress"
	DispatchTaskStatusCompleted  = "completed"
	DispatchTaskStatusError      = "error"
	DispatchTaskModeSync         = "sync"
	DispatchTaskModeAsync        = "async"
	dispatchHistoryTTL           = 20 * time.Minute
	dispatchAsyncHistoryTTL      = 2 * time.Hour
	dispatchCooldownDuration     = time.Minute
)

// DispatchRequest 描述一次统一调度请求。
type DispatchRequest struct {
	TaskID           string
	RequestPath      string
	Group            string
	Model            string
	Candidates       []*model.Channel
	QueueFront       bool
	ExcludeChannelID int
}

// DispatchLease 表示一次统一调度占位结果。
type DispatchLease struct {
	Channel *model.Channel
	Lease   *common.ChannelConcurrencyLease
}

// Release 释放当前占位并唤醒等待任务。
func (l *DispatchLease) Release() {
	if l == nil {
		return
	}
	if l.Lease != nil {
		l.Lease.Release()
	}
	if l.Channel != nil {
		dispatcherInstance.release(l.Channel.Id)
	}
}

// DispatchTaskSnapshot 提供队列页面使用的任务快照。
type DispatchTaskSnapshot struct {
	TaskID             string `json:"task_id"`
	RequestPath        string `json:"request_path"`
	Mode               string `json:"mode"`
	Model              string `json:"model"`
	Group              string `json:"group"`
	Status             string `json:"status"`
	ChannelID          int    `json:"channel_id"`
	ChannelName        string `json:"channel_name"`
	EnqueuedAt         int64  `json:"enqueued_at"`
	StartedAt          int64  `json:"started_at"`
	CompletedAt        int64  `json:"completed_at"`
	CreatedAt          int64  `json:"created_at"`
	UpdatedAt          int64  `json:"updated_at"`
	QueueDurationMs    int64  `json:"queue_duration_ms"`
	ProcessingDuration int64  `json:"processing_duration_ms"`
	RetryCount         int    `json:"retry_count"`
	LastError          string `json:"last_error"`
}

type dispatchTaskRecord struct {
	TaskID       string
	RequestPath  string
	Mode         string
	Model        string
	Group        string
	Status       string
	ChannelID    int
	ChannelName  string
	EnqueuedAt   time.Time
	StartedAt    time.Time
	CompletedAt  time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
	RetryCount   int
	LastError    string
	HistoryUntil time.Time
}

type waitingDispatchRequest struct {
	taskID           string
	group            string
	model            string
	candidates       []*model.Channel
	excludeChannelID int
	notify           chan *DispatchLease
	notifyErr        chan error
}

type dispatcher struct {
	mu sync.Mutex

	roundRobin map[string]int
	cooldowns  map[int]time.Time
	waiting    map[string][]*waitingDispatchRequest
	tasks      map[string]*dispatchTaskRecord
}

var dispatcherInstance = &dispatcher{
	roundRobin: make(map[string]int),
	cooldowns:  make(map[int]time.Time),
	waiting:    make(map[string][]*waitingDispatchRequest),
	tasks:      make(map[string]*dispatchTaskRecord),
}

// RegisterDispatchTask 初始化一个调度任务快照。
func RegisterDispatchTask(taskID string, requestPath string, group string, model string, mode string) {
	dispatcherInstance.registerTask(taskID, requestPath, group, model, mode)
}

// MarkDispatchTaskCompleted 标记任务成功完成。
func MarkDispatchTaskCompleted(taskID string) {
	dispatcherInstance.completeTask(taskID)
}

// MarkDispatchTaskError 标记任务最终失败。
func MarkDispatchTaskError(taskID string, err error) {
	dispatcherInstance.failTask(taskID, err)
}

// IncrementDispatchTaskRetry 增加任务重试次数。
func IncrementDispatchTaskRetry(taskID string, err error) {
	dispatcherInstance.incrementRetry(taskID, err)
}

// AcquireDispatchLease 获取统一调度占位结果。
func AcquireDispatchLease(ctx context.Context, req DispatchRequest) (*DispatchLease, error) {
	return dispatcherInstance.acquire(ctx, req)
}

// MarkChannelCooldown 记录渠道 429 冷却。
func MarkChannelCooldown(channelID int, duration time.Duration) {
	dispatcherInstance.markCooldown(channelID, duration)
}

// ListDispatchTaskSnapshots 返回队列页快照。
func ListDispatchTaskSnapshots(status string, modelName string, channelID int, page int, pageSize int) ([]DispatchTaskSnapshot, int) {
	return dispatcherInstance.listSnapshots(status, modelName, channelID, page, pageSize)
}

// registerTask 初始化或刷新调度快照。
func (d *dispatcher) registerTask(taskID string, requestPath string, group string, model string, mode string) {
	if taskID == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	record, ok := d.tasks[taskID]
	if !ok {
		record = &dispatchTaskRecord{
			TaskID:      taskID,
			RequestPath: requestPath,
			Mode:        normalizeDispatchTaskMode(mode),
			Group:       group,
			Model:       model,
			Status:      DispatchTaskStatusInProgress,
			CreatedAt:   now,
		}
		d.tasks[taskID] = record
	}
	record.RequestPath = requestPath
	record.Mode = normalizeDispatchTaskMode(mode)
	record.Group = group
	record.Model = model
	record.UpdatedAt = now
	d.cleanupLocked(now)
}

func (d *dispatcher) incrementRetry(taskID string, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	record := d.ensureTaskLocked(taskID)
	record.RetryCount++
	record.LastError = errorString(err)
	record.UpdatedAt = time.Now()
}

func (d *dispatcher) completeTask(taskID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	record := d.ensureTaskLocked(taskID)
	now := time.Now()
	record.Status = DispatchTaskStatusCompleted
	record.CompletedAt = now
	record.UpdatedAt = now
	record.HistoryUntil = now.Add(dispatchHistoryTTLForMode(record.Mode))
}

func (d *dispatcher) failTask(taskID string, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	record := d.ensureTaskLocked(taskID)
	now := time.Now()
	record.Status = DispatchTaskStatusError
	record.LastError = errorString(err)
	record.CompletedAt = now
	record.UpdatedAt = now
	record.HistoryUntil = now.Add(dispatchHistoryTTLForMode(record.Mode))
}

func (d *dispatcher) ensureTaskLocked(taskID string) *dispatchTaskRecord {
	record, ok := d.tasks[taskID]
	if !ok {
		record = &dispatchTaskRecord{
			TaskID:    taskID,
			CreatedAt: time.Now(),
		}
		d.tasks[taskID] = record
	}
	return record
}

func (d *dispatcher) acquire(ctx context.Context, req DispatchRequest) (*DispatchLease, error) {
	if len(req.Candidates) == 0 {
		return nil, ErrAllChannelsFailed
	}
	waiter := &waitingDispatchRequest{
		taskID:           req.TaskID,
		group:            req.Group,
		model:            req.Model,
		candidates:       req.Candidates,
		excludeChannelID: req.ExcludeChannelID,
		notify:           make(chan *DispatchLease, 1),
		notifyErr:        make(chan error, 1),
	}

	d.mu.Lock()
	d.cleanupLocked(time.Now())
	lease, err := d.tryAcquireLocked(waiter)
	if err == nil && lease != nil {
		d.markInProgressLocked(req.TaskID, lease.Channel)
		d.mu.Unlock()
		return lease, nil
	}
	if errors.Is(err, ErrAllChannelsFailed) {
		d.markErrorLocked(req.TaskID, err)
		d.mu.Unlock()
		return nil, err
	}
	if errors.Is(err, ErrNoAlternativeChannel) {
		d.mu.Unlock()
		return nil, err
	}

	d.markQueuedLocked(req.TaskID)
	shardKey := d.shardKey(req.Group, req.Model)
	if req.QueueFront {
		d.waiting[shardKey] = append([]*waitingDispatchRequest{waiter}, d.waiting[shardKey]...)
	} else {
		d.waiting[shardKey] = append(d.waiting[shardKey], waiter)
	}
	d.mu.Unlock()

	select {
	case lease := <-waiter.notify:
		if lease == nil {
			return nil, ErrDispatchTimeout
		}
		return lease, nil
	case err := <-waiter.notifyErr:
		if err == nil {
			return nil, ErrDispatchTimeout
		}
		return nil, err
	case <-ctx.Done():
		d.mu.Lock()
		d.removeWaiterLocked(shardKey, waiter)
		d.mu.Unlock()
		return nil, ErrDispatchTimeout
	}
}

func (d *dispatcher) tryAcquireLocked(waiter *waitingDispatchRequest) (*DispatchLease, error) {
	baseAvailable := d.filterAvailableCandidatesLocked(waiter.candidates, 0)
	if len(baseAvailable) == 0 {
		return nil, ErrAllChannelsFailed
	}
	available := d.filterAvailableCandidatesLocked(waiter.candidates, waiter.excludeChannelID)
	if len(available) == 0 {
		return nil, ErrNoAlternativeChannel
	}
	channel := d.pickChannelLocked(waiter.group, waiter.model, available)
	if channel == nil {
		return nil, nil
	}
	lease, ok := common.TryAcquireChannelConcurrency(channel.Id, channel.GetChannelConcurrency())
	if !ok {
		return nil, nil
	}
	return &DispatchLease{Channel: channel, Lease: lease}, nil
}

func (d *dispatcher) pickChannelLocked(group string, modelName string, candidates []*model.Channel) *model.Channel {
	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].GetPriority() == candidates[j].GetPriority() {
			return candidates[i].Id < candidates[j].Id
		}
		return candidates[i].GetPriority() > candidates[j].GetPriority()
	})

	bestPriority := candidates[0].GetPriority()
	currentPriority := make([]*model.Channel, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.GetPriority() != bestPriority {
			break
		}
		currentPriority = append(currentPriority, candidate)
	}

	idleChannels := make([]*model.Channel, 0, len(currentPriority))
	unlimitedChannels := make([]*model.Channel, 0, len(currentPriority))
	for _, candidate := range currentPriority {
		stats := common.GetChannelConcurrencyStats(candidate.Id, candidate.GetChannelConcurrency())
		if candidate.GetChannelConcurrency() <= 0 {
			unlimitedChannels = append(unlimitedChannels, candidate)
			continue
		}
		if stats.Idle() > 0 {
			idleChannels = append(idleChannels, candidate)
		}
	}

	target := idleChannels
	if len(target) == 0 {
		target = unlimitedChannels
	}
	if len(target) == 0 {
		return nil
	}

	rrKey := fmt.Sprintf("%s|%s|%d", group, modelName, bestPriority)
	start := d.roundRobin[rrKey]
	if start < 0 {
		start = 0
	}
	selected := target[start%len(target)]
	d.roundRobin[rrKey] = (start + 1) % len(target)
	return selected
}

func (d *dispatcher) filterAvailableCandidatesLocked(candidates []*model.Channel, excludeChannelID int) []*model.Channel {
	now := time.Now()
	result := make([]*model.Channel, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate == nil {
			continue
		}
		if excludeChannelID > 0 && candidate.Id == excludeChannelID {
			continue
		}
		if until, ok := d.cooldowns[candidate.Id]; ok {
			if now.Before(until) {
				continue
			}
			delete(d.cooldowns, candidate.Id)
		}
		result = append(result, candidate)
	}
	return result
}

func (d *dispatcher) removeWaiterLocked(shardKey string, target *waitingDispatchRequest) {
	queue := d.waiting[shardKey]
	for idx, item := range queue {
		if item == target {
			d.waiting[shardKey] = append(queue[:idx], queue[idx+1:]...)
			break
		}
	}
}

func (d *dispatcher) release(channelID int) {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	d.cleanupLocked(now)
	for shardKey, queue := range d.waiting {
		for idx := 0; idx < len(queue); idx++ {
			waiter := queue[idx]
			if waiter == nil {
				continue
			}
			if !containsChannel(waiter.candidates, channelID) {
				continue
			}
			lease, err := d.tryAcquireLocked(waiter)
			if err != nil {
				if errors.Is(err, ErrAllChannelsFailed) || errors.Is(err, ErrNoAlternativeChannel) {
					d.markErrorLocked(waiter.taskID, err)
					select {
					case waiter.notifyErr <- err:
					default:
					}
					d.waiting[shardKey] = append(queue[:idx], queue[idx+1:]...)
				}
				continue
			}
			if lease == nil {
				continue
			}
			d.markInProgressLocked(waiter.taskID, lease.Channel)
			select {
			case waiter.notify <- lease:
			default:
			}
			d.waiting[shardKey] = append(queue[:idx], queue[idx+1:]...)
			return
		}
	}
}

func (d *dispatcher) markCooldown(channelID int, duration time.Duration) {
	if channelID <= 0 {
		return
	}
	if duration <= 0 {
		duration = dispatchCooldownDuration
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cooldowns[channelID] = time.Now().Add(duration)
}

func (d *dispatcher) markQueuedLocked(taskID string) {
	record := d.ensureTaskLocked(taskID)
	now := time.Now()
	if record.EnqueuedAt.IsZero() {
		record.EnqueuedAt = now
	}
	record.Status = DispatchTaskStatusQueued
	record.ChannelID = 0
	record.ChannelName = ""
	record.UpdatedAt = now
}

func (d *dispatcher) markInProgressLocked(taskID string, channel *model.Channel) {
	record := d.ensureTaskLocked(taskID)
	now := time.Now()
	record.Status = DispatchTaskStatusInProgress
	if record.EnqueuedAt.IsZero() {
		record.EnqueuedAt = now
	}
	if record.StartedAt.IsZero() {
		record.StartedAt = now
	}
	record.ChannelID = 0
	record.ChannelName = ""
	if channel != nil {
		record.ChannelID = channel.Id
		record.ChannelName = channel.Name
	}
	record.UpdatedAt = now
}

func (d *dispatcher) markErrorLocked(taskID string, err error) {
	record := d.ensureTaskLocked(taskID)
	now := time.Now()
	record.Status = DispatchTaskStatusError
	record.LastError = errorString(err)
	record.CompletedAt = now
	record.UpdatedAt = now
	record.HistoryUntil = now.Add(dispatchHistoryTTLForMode(record.Mode))
}

func (d *dispatcher) listSnapshots(status string, modelName string, channelID int, page int, pageSize int) ([]DispatchTaskSnapshot, int) {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	d.cleanupLocked(now)

	items := make([]DispatchTaskSnapshot, 0, len(d.tasks))
	for _, task := range d.tasks {
		if status != "" && task.Status != status {
			continue
		}
		if modelName != "" && !strings.Contains(strings.ToLower(task.Model), strings.ToLower(modelName)) {
			continue
		}
		if channelID > 0 && task.ChannelID != channelID {
			continue
		}
		items = append(items, snapshotFromRecord(task, now))
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt > items[j].UpdatedAt
	})

	total := len(items)
	if pageSize <= 0 {
		pageSize = 20
	}
	if page <= 0 {
		page = 1
	}
	start := (page - 1) * pageSize
	if start >= total {
		return []DispatchTaskSnapshot{}, total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	return items[start:end], total
}

func (d *dispatcher) cleanupLocked(now time.Time) {
	for channelID, until := range d.cooldowns {
		if now.After(until) {
			delete(d.cooldowns, channelID)
		}
	}
	for taskID, task := range d.tasks {
		if (task.Status == DispatchTaskStatusCompleted || task.Status == DispatchTaskStatusError) &&
			!task.HistoryUntil.IsZero() && now.After(task.HistoryUntil) {
			delete(d.tasks, taskID)
		}
	}
}

func (d *dispatcher) shardKey(group string, modelName string) string {
	return fmt.Sprintf("%s|%s", group, modelName)
}

func snapshotFromRecord(task *dispatchTaskRecord, now time.Time) DispatchTaskSnapshot {
	snapshot := DispatchTaskSnapshot{
		TaskID:      task.TaskID,
		RequestPath: task.RequestPath,
		Mode:        normalizeDispatchTaskMode(task.Mode),
		Model:       task.Model,
		Group:       task.Group,
		Status:      task.Status,
		ChannelID:   task.ChannelID,
		ChannelName: task.ChannelName,
		CreatedAt:   task.CreatedAt.Unix(),
		UpdatedAt:   task.UpdatedAt.Unix(),
		RetryCount:  task.RetryCount,
		LastError:   task.LastError,
	}
	if !task.EnqueuedAt.IsZero() {
		snapshot.EnqueuedAt = task.EnqueuedAt.Unix()
	}
	if !task.StartedAt.IsZero() {
		snapshot.StartedAt = task.StartedAt.Unix()
	}
	if !task.CompletedAt.IsZero() {
		snapshot.CompletedAt = task.CompletedAt.Unix()
	}
	if !task.EnqueuedAt.IsZero() {
		queueEnd := now
		if !task.StartedAt.IsZero() {
			queueEnd = task.StartedAt
		}
		snapshot.QueueDurationMs = queueEnd.Sub(task.EnqueuedAt).Milliseconds()
	}
	if !task.StartedAt.IsZero() {
		processEnd := now
		if !task.CompletedAt.IsZero() {
			processEnd = task.CompletedAt
		}
		snapshot.ProcessingDuration = processEnd.Sub(task.StartedAt).Milliseconds()
	}
	return snapshot
}

// normalizeDispatchTaskMode 统一调度快照模式值，避免出现空值或未知值。
func normalizeDispatchTaskMode(mode string) string {
	if mode == DispatchTaskModeAsync {
		return DispatchTaskModeAsync
	}
	return DispatchTaskModeSync
}

// dispatchHistoryTTLForMode 根据同步/异步模式返回对应历史保留时间。
func dispatchHistoryTTLForMode(mode string) time.Duration {
	if normalizeDispatchTaskMode(mode) == DispatchTaskModeAsync {
		return dispatchAsyncHistoryTTL
	}
	return dispatchHistoryTTL
}

func containsChannel(channels []*model.Channel, channelID int) bool {
	for _, channel := range channels {
		if channel != nil && channel.Id == channelID {
			return true
		}
	}
	return false
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
