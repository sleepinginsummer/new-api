package controller

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// GetDispatchQueueTasks 返回统一调度器当前任务快照。
func GetDispatchQueueTasks(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	channelID, _ := strconv.Atoi(c.Query("channel_id"))
	items, total := service.ListDispatchTaskSnapshots(
		c.Query("status"),
		c.Query("model"),
		channelID,
		pageInfo.GetPage(),
		pageInfo.GetPageSize(),
	)
	pageInfo.SetTotal(total)
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}
