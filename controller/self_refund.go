package controller

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// GetSelfRefundable 获取用户可补回的日志列表
//
// 两个选择器：request_id（用户日志列表携带的标识符）与 log_id（真实主键，
// 独立补回页使用）。列表的 `id` 是页内展示序号，不能当主键回传。
func GetSelfRefundable(c *gin.Context) {
	userId := c.GetInt("id")
	pageInfo := common.GetPageQuery(c)

	logId, _ := strconv.Atoi(c.Query("log_id"))
	logs, total, err := service.GetRefundableLogs(userId, service.RefundableLogQuery{
		StartIdx:  pageInfo.GetStartIdx(),
		Num:       pageInfo.GetPageSize(),
		RequestId: c.Query("request_id"),
		LogId:     logId,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}

	// 获取补回设置（含累计已用额度）
	setting, err := service.GetRefundableSetting(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	// The candidate count is returned explicitly. The previous version populated
	// a page-info struct and then discarded it, so the client had no way to know
	// how many rows the server matched.
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"setting": setting,
			"logs":    logs,
			"total":   total,
		},
	})
}

// DoSelfRefund 执行自助补回
//
// 两个选择器：request_id（用户日志列表携带的标识符；列表的 `id` 是页内展示
// 序号，不能当主键回传）与 log_id（真实主键，独立补回页使用）。两者同时给出
// 时必须指向同一条日志，否则拒绝。
func DoSelfRefund(c *gin.Context) {
	userId := c.GetInt("id")

	var req struct {
		LogId     int    `json:"log_id"`
		RequestId string `json:"request_id"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		common.ApiErrorMsg(c, "无效的请求参数")
		return
	}
	if req.LogId <= 0 && req.RequestId == "" {
		common.ApiErrorMsg(c, "无效的日志ID")
		return
	}

	info, err := service.DoSelfRefund(userId, req.LogId, req.RequestId)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "补回成功",
		"data":    info,
	})
}
