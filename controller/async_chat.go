package controller

import (
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

func init() {
	service.SetAsyncChatExecutionHooks(&service.AsyncChatExecutionHooks{
		ExecuteRelay: func(c *gin.Context) {
			Relay(c, types.RelayFormatOpenAI)
		},
	})
}

// RelayAsyncChatSubmit 提交一个异步 chat completion 任务并立即返回 UUID。
func RelayAsyncChatSubmit(c *gin.Context) {
	if err := service.ValidateAsyncChatEnabled(); err != nil {
		writeAsyncChatSubmitError(c, http.StatusServiceUnavailable, err.Error())
		return
	}

	request, err := helper.GetAndValidateRequest(c, types.RelayFormatOpenAI)
	if err != nil {
		writeAsyncChatSubmitError(c, http.StatusBadRequest, err.Error())
		return
	}
	if request.IsStream(c) {
		writeAsyncChatSubmitError(c, http.StatusBadRequest, "stream is not supported for async chat completions")
		return
	}

	openAIRequest, ok := request.(*dto.GeneralOpenAIRequest)
	if !ok {
		writeAsyncChatSubmitError(c, http.StatusBadRequest, "invalid async chat request type")
		return
	}

	bodyStorage, err := common.GetBodyStorage(c)
	if err != nil {
		writeAsyncChatSubmitError(c, http.StatusBadRequest, err.Error())
		return
	}
	requestBody, err := bodyStorage.Bytes()
	if err != nil {
		writeAsyncChatSubmitError(c, http.StatusBadRequest, err.Error())
		return
	}

	taskID := service.NewAsyncChatTaskID()
	store := service.NewAsyncChatStore(
		taskID,
		c.GetInt("id"),
		c.Request.URL.Path,
		openAIRequest.Model,
		common.GetContextKeyString(c, constant.ContextKeyUsingGroup),
	)
	if err := store.Save(); err != nil {
		writeAsyncChatSubmitError(c, http.StatusInternalServerError, fmt.Sprintf("save async chat task failed: %v", err))
		return
	}

	service.RegisterDispatchTask(taskID, c.Request.URL.Path, store.Group, store.Model, service.DispatchTaskModeAsync)

	taskLog, err := service.CreateAsyncChatTaskLog(service.BuildAsyncChatTaskLogContext(c, openAIRequest.Model), taskID)
	if err != nil {
		writeAsyncChatSubmitError(c, http.StatusInternalServerError, fmt.Sprintf("create async chat task log failed: %v", err))
		return
	}

	asyncContext := c.Copy()
	bodyCopy := append([]byte(nil), requestBody...)
	gopool.Go(func() {
		service.ExecuteAsyncChatTask(asyncContext, taskID, bodyCopy, openAIRequest.Model, taskLog)
	})

	c.JSON(http.StatusOK, gin.H{
		"id":      taskID,
		"object":  service.AsyncChatObject,
		"status":  service.AsyncChatStatusProcessing,
		"created": store.CreatedAt,
	})
}

// RelayAsyncChatPoll 轮询异步 chat completion 任务结果。
func RelayAsyncChatPoll(c *gin.Context) {
	if err := service.ValidateAsyncChatEnabled(); err != nil {
		c.JSON(http.StatusOK, &service.AsyncChatErrorResponse{
			ID:     c.Param("uuid"),
			Object: service.AsyncChatObject,
			Status: service.AsyncChatStatusError,
			Error: &service.AsyncChatErrorPayload{
				Message: err.Error(),
				Type:    "async_task_error",
			},
		})
		return
	}

	taskID := c.Param("uuid")
	store, err := service.GetAsyncChatStore(taskID)
	if err != nil || store == nil || store.UserID != c.GetInt("id") {
		c.JSON(http.StatusOK, &service.AsyncChatErrorResponse{
			ID:     taskID,
			Object: service.AsyncChatObject,
			Status: service.AsyncChatStatusError,
			Error: &service.AsyncChatErrorPayload{
				Message: "task expired or not found",
				Type:    "async_task_expired",
			},
		})
		return
	}

	switch store.Status {
	case service.AsyncChatStatusCompleted:
		c.Header("X-Async-Task-Id", store.ID)
		c.Header("X-Async-Task-Status", service.AsyncChatStatusCompleted)
		c.Data(http.StatusOK, "application/json", store.ResponseBody)
	case service.AsyncChatStatusError:
		c.JSON(http.StatusOK, store.ToErrorResponse("async_task_error"))
	default:
		c.JSON(http.StatusOK, store.ToProcessingResponse())
	}
}

// writeAsyncChatSubmitError 返回异步 chat 提交阶段的 OpenAI 风格错误响应。
func writeAsyncChatSubmitError(c *gin.Context, statusCode int, message string) {
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"message": message,
			"type":    "async_chat_error",
		},
	})
}
