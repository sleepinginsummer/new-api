package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	// AsyncChatTaskAction 标识异步 chat completion 任务类型。
	AsyncChatTaskAction = "async_chat_completion"
	// AsyncChatRedisKeyPrefix 是 Redis 中异步 chat 任务的 key 前缀。
	AsyncChatRedisKeyPrefix = "async_chat:"
	// AsyncChatTaskTTL 定义异步 chat 任务结果与元数据的统一保留时间。
	AsyncChatTaskTTL = 2 * time.Hour
	// AsyncChatObject 定义异步 chat 接口返回的 object 值。
	AsyncChatObject = "chat.completion.async"
	// AsyncChatStatusProcessing 表示异步任务仍在处理中。
	AsyncChatStatusProcessing = "processing"
	// AsyncChatStatusCompleted 表示异步任务已完成。
	AsyncChatStatusCompleted = "completed"
	// AsyncChatStatusError 表示异步任务执行失败。
	AsyncChatStatusError = "error"
)

// AsyncChatStore 定义异步 chat 在 Redis 中存储的完整状态。
type AsyncChatStore struct {
	ID           string          `json:"id"`
	Object       string          `json:"object"`
	Status       string          `json:"status"`
	CreatedAt    int64           `json:"created_at"`
	UpdatedAt    int64           `json:"updated_at"`
	RequestPath  string          `json:"request_path"`
	Mode         string          `json:"mode"`
	UserID       int             `json:"user_id"`
	Model        string          `json:"model"`
	Group        string          `json:"group"`
	ChannelID    int             `json:"channel_id,omitempty"`
	ChannelName  string          `json:"channel_name,omitempty"`
	ErrorMessage string          `json:"error_message,omitempty"`
	ResponseBody json.RawMessage `json:"response_body,omitempty"`
}

// AsyncChatProcessingResponse 定义异步 chat 处理中返回体。
type AsyncChatProcessingResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Status  string `json:"status"`
	Created int64  `json:"created"`
	Updated int64  `json:"updated,omitempty"`
}

// AsyncChatErrorPayload 定义异步 chat 错误详情。
type AsyncChatErrorPayload struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

// AsyncChatErrorResponse 定义异步 chat 失败返回体。
type AsyncChatErrorResponse struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"`
	Status  string                 `json:"status"`
	Created int64                  `json:"created,omitempty"`
	Updated int64                  `json:"updated,omitempty"`
	Error   *AsyncChatErrorPayload `json:"error"`
}

// AsyncChatExecutionHooks 允许异步 chat 任务在后台复用真实 relay 执行逻辑。
type AsyncChatExecutionHooks struct {
	// ExecuteRelay 必须同步执行一次真实 chat relay，并将结果写入 recorder。
	ExecuteRelay func(c *gin.Context)
}

var asyncChatHooks = &AsyncChatExecutionHooks{}

// SetAsyncChatExecutionHooks 注入异步 chat 后台执行钩子。
func SetAsyncChatExecutionHooks(hooks *AsyncChatExecutionHooks) {
	if hooks == nil {
		asyncChatHooks = &AsyncChatExecutionHooks{}
		return
	}
	asyncChatHooks = hooks
}

// NewAsyncChatTaskID 生成异步 chat 使用的 UUID。
func NewAsyncChatTaskID() string {
	return uuid.NewString()
}

// ValidateAsyncChatEnabled 校验异步 chat 功能运行所需的 Redis 前提。
func ValidateAsyncChatEnabled() error {
	if !common.RedisEnabled || common.RDB == nil {
		return errors.New("async chat requires redis to be enabled")
	}
	return nil
}

// NewAsyncChatStore 构造异步 chat 初始存储对象。
func NewAsyncChatStore(id string, userID int, requestPath string, model string, group string) *AsyncChatStore {
	now := time.Now().Unix()
	return &AsyncChatStore{
		ID:          id,
		Object:      AsyncChatObject,
		Status:      AsyncChatStatusProcessing,
		CreatedAt:   now,
		UpdatedAt:   now,
		RequestPath: requestPath,
		Mode:        DispatchTaskModeAsync,
		UserID:      userID,
		Model:       model,
		Group:       group,
	}
}

// RedisKey 返回异步 chat 任务在 Redis 中的完整 key。
func (s *AsyncChatStore) RedisKey() string {
	return BuildAsyncChatRedisKey(s.ID)
}

// Save 持久化异步 chat 状态到 Redis，并刷新 TTL。
func (s *AsyncChatStore) Save() error {
	payload, err := common.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshal async chat store failed: %w", err)
	}
	return common.RedisSet(s.RedisKey(), string(payload), AsyncChatTaskTTL)
}

// ToProcessingResponse 转换为处理中返回体。
func (s *AsyncChatStore) ToProcessingResponse() *AsyncChatProcessingResponse {
	return &AsyncChatProcessingResponse{
		ID:      s.ID,
		Object:  AsyncChatObject,
		Status:  AsyncChatStatusProcessing,
		Created: s.CreatedAt,
		Updated: s.UpdatedAt,
	}
}

// ToErrorResponse 转换为错误返回体。
func (s *AsyncChatStore) ToErrorResponse(errorType string) *AsyncChatErrorResponse {
	return &AsyncChatErrorResponse{
		ID:      s.ID,
		Object:  AsyncChatObject,
		Status:  AsyncChatStatusError,
		Created: s.CreatedAt,
		Updated: s.UpdatedAt,
		Error: &AsyncChatErrorPayload{
			Message: s.ErrorMessage,
			Type:    errorType,
		},
	}
}

// BuildAsyncChatRedisKey 构造异步 chat Redis key。
func BuildAsyncChatRedisKey(id string) string {
	return AsyncChatRedisKeyPrefix + id
}

// GetAsyncChatStore 读取异步 chat 任务状态。
func GetAsyncChatStore(id string) (*AsyncChatStore, error) {
	value, err := common.RedisGet(BuildAsyncChatRedisKey(id))
	if err != nil {
		return nil, err
	}
	store := &AsyncChatStore{}
	if err := common.UnmarshalJsonStr(value, store); err != nil {
		return nil, fmt.Errorf("unmarshal async chat store failed: %w", err)
	}
	return store, nil
}

// CreateAsyncChatTaskLog 创建异步 chat 对应的任务日志记录。
func CreateAsyncChatTaskLog(relayInfo *commonRelayInfo, taskID string) (*model.Task, error) {
	task := model.InitTask(constant.TaskPlatform(fmt.Sprintf("%d", constant.ChannelTypeOpenAI)), relayInfo.toRelayInfo())
	task.TaskID = taskID
	task.Action = AsyncChatTaskAction
	task.Status = model.TaskStatusSubmitted
	task.Progress = "0%"
	task.ChannelId = 0
	task.Quota = 0
	task.SetData(map[string]any{
		"mode":   DispatchTaskModeAsync,
		"model":  relayInfo.Model,
		"status": AsyncChatStatusProcessing,
	})
	return task, task.Insert()
}

// commonRelayInfo 是异步 chat 创建任务日志时需要的最小上下文。
type commonRelayInfo struct {
	UserID     int
	UsingGroup string
	Model      string
}

// toRelayInfo 将轻量上下文转换为 model.InitTask 所需结构。
func (c *commonRelayInfo) toRelayInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		UserId:          c.UserID,
		UsingGroup:      c.UsingGroup,
		OriginModelName: c.Model,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: c.Model,
		},
	}
}

// BuildAsyncChatTaskLogContext 从 gin.Context 提取创建异步任务日志所需的最小上下文。
func BuildAsyncChatTaskLogContext(c *gin.Context, modelName string) *commonRelayInfo {
	return &commonRelayInfo{
		UserID:     c.GetInt("id"),
		UsingGroup: common.GetContextKeyString(c, constant.ContextKeyUsingGroup),
		Model:      modelName,
	}
}

// UpdateAsyncChatTaskLogStatus 更新异步 chat 任务日志的状态与摘要信息。
func UpdateAsyncChatTaskLogStatus(task *model.Task, status model.TaskStatus, progress string, failReason string, channelID int, data map[string]any) error {
	if task == nil {
		return nil
	}
	now := time.Now().Unix()
	task.Status = status
	task.Progress = progress
	task.FailReason = failReason
	if channelID > 0 {
		task.ChannelId = channelID
	}
	if task.StartTime == 0 && (status == model.TaskStatusQueued || status == model.TaskStatusInProgress) {
		task.StartTime = now
	}
	if status == model.TaskStatusSuccess || status == model.TaskStatusFailure {
		task.FinishTime = now
	}
	if len(data) > 0 {
		task.SetData(data)
	}
	return task.Update()
}

// ExecuteAsyncChatTask 在后台复用现有 relay 主流程执行一次真实 chat completion 请求。
func ExecuteAsyncChatTask(origin *gin.Context, taskID string, requestBody []byte, requestModel string, taskLog *model.Task) {
	if asyncChatHooks == nil || asyncChatHooks.ExecuteRelay == nil {
		return
	}

	store, err := GetAsyncChatStore(taskID)
	if err != nil {
		return
	}

	ctx, recorder, cleanup, err := cloneContextForAsyncChat(origin, taskID, requestBody)
	if err != nil {
		store.Status = AsyncChatStatusError
		store.UpdatedAt = time.Now().Unix()
		store.ErrorMessage = err.Error()
		_ = store.Save()
		_ = UpdateAsyncChatTaskLogStatus(taskLog, model.TaskStatusFailure, "100%", err.Error(), 0, map[string]any{
			"mode":   DispatchTaskModeAsync,
			"model":  requestModel,
			"status": AsyncChatStatusError,
			"error":  err.Error(),
		})
		return
	}
	defer cleanup()

	// 进入真实 relay 前先将任务日志置为排队中。
	_ = UpdateAsyncChatTaskLogStatus(taskLog, model.TaskStatusQueued, "10%", "", 0, map[string]any{
		"mode":   DispatchTaskModeAsync,
		"model":  requestModel,
		"status": AsyncChatStatusProcessing,
	})

	asyncChatHooks.ExecuteRelay(ctx)

	resp := recorder.Result()
	defer resp.Body.Close()
	bodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		bodyBytes = nil
	}

	store.UpdatedAt = time.Now().Unix()
	store.ChannelID = common.GetContextKeyInt(ctx, constant.ContextKeyChannelId)
	store.ChannelName = common.GetContextKeyString(ctx, constant.ContextKeyChannelName)

	if resp.StatusCode >= 400 {
		store.Status = AsyncChatStatusError
		store.ErrorMessage = extractAsyncChatErrorMessage(bodyBytes)
		_ = store.Save()
		_ = UpdateAsyncChatTaskLogStatus(taskLog, model.TaskStatusFailure, "100%", store.ErrorMessage, store.ChannelID, map[string]any{
			"mode":       DispatchTaskModeAsync,
			"model":      requestModel,
			"status":     AsyncChatStatusError,
			"error":      store.ErrorMessage,
			"channel_id": store.ChannelID,
		})
		return
	}

	store.Status = AsyncChatStatusCompleted
	store.ErrorMessage = ""
	store.ResponseBody = append(store.ResponseBody[:0], bodyBytes...)
	_ = store.Save()
	_ = UpdateAsyncChatTaskLogStatus(taskLog, model.TaskStatusSuccess, "100%", "", store.ChannelID, map[string]any{
		"mode":       DispatchTaskModeAsync,
		"model":      requestModel,
		"status":     AsyncChatStatusCompleted,
		"channel_id": store.ChannelID,
		"has_result": len(bodyBytes) > 0,
	})
}

// cloneContextForAsyncChat 复制最小请求上下文，供后台 goroutine 安全执行真实 relay。
func cloneContextForAsyncChat(origin *gin.Context, taskID string, requestBody []byte) (*gin.Context, *httptest.ResponseRecorder, func(), error) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)

	req := origin.Request.Clone(context.Background())
	req.Method = origin.Request.Method
	req.URL.Path = origin.Request.URL.Path
	req.RequestURI = origin.Request.RequestURI
	req.Header = origin.Request.Header.Clone()
	req.Body = io.NopCloser(bytes.NewReader(requestBody))
	req.ContentLength = int64(len(requestBody))
	ctx.Request = req
	ctx.Params = append(ctx.Params[:0], origin.Params...)

	// 复制 middleware 预先写入的上下文键，保证用户、分组与限流信息在后台仍可复用。
	ctx.Keys = make(map[string]any, len(origin.Keys)+4)
	for key, value := range origin.Keys {
		ctx.Keys[key] = value
	}
	ctx.Set(common.RequestIdKey, taskID)
	ctx.Set("dispatch_task_id", taskID)
	ctx.Set("dispatch_mode", DispatchTaskModeAsync)

	storage, err := common.CreateBodyStorage(requestBody)
	if err != nil {
		return nil, nil, nil, err
	}
	ctx.Set(common.KeyBodyStorage, storage)

	cleanup := func() {
		common.CleanupBodyStorage(ctx)
	}
	return ctx, recorder, cleanup, nil
}

// extractAsyncChatErrorMessage 从 relay 返回体中提取适合写入 Redis 和任务日志的错误信息。
func extractAsyncChatErrorMessage(body []byte) string {
	if len(body) == 0 {
		return "async task failed"
	}

	var payload map[string]any
	if err := common.Unmarshal(body, &payload); err == nil {
		if errObj, ok := payload["error"].(map[string]any); ok {
			if message, ok := errObj["message"].(string); ok && message != "" {
				return message
			}
		}
	}
	return string(body)
}

// DeleteExpiredAsyncChatTaskLogs 清理已过期的异步 chat 任务日志。
func DeleteExpiredAsyncChatTaskLogs(expireBefore int64) error {
	return model.DeleteExpiredAsyncChatTasks(AsyncChatTaskAction, expireBefore)
}

// StartAsyncChatCleanupTask 启动异步 chat 任务日志清理协程。
func StartAsyncChatCleanupTask() {
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			expireBefore := time.Now().Add(-AsyncChatTaskTTL).Unix()
			if err := DeleteExpiredAsyncChatTaskLogs(expireBefore); err != nil {
				common.SysError("cleanup async chat task logs failed: " + err.Error())
			}
		}
	}()
}
