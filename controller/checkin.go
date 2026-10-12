package controller

import (
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

// GetCheckinStatus 获取用户签到状态和历史记录
func GetCheckinStatus(c *gin.Context) {
	setting := operation_setting.GetCheckinSetting()
	userId := c.GetInt("id")
	// 获取月份参数，默认为当前月份
	month := c.DefaultQuery("month", time.Now().Format("2006-01"))
	if parsed, err := time.Parse("2006-01", month); err != nil || parsed.Format("2006-01") != month {
		common.ApiErrorMsg(c, "月份格式必须为 YYYY-MM")
		return
	}

	stats, err := model.GetUserCheckinStats(userId, month)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	// 昨日使用量（前端渲染"昨日调用次数/昨日消耗额度" + 命中档位）
	start, end := model.YesterdayRange()
	yesterdayCount, yesterdayQuota, _ := model.GetYesterdayUsage(userId, start, end, setting.IncludeSubscription)

	// 历史累计消耗（复用钱包 used_quota，含订阅，供规则 C 判定与前端展示）
	totalUsedQuota, err := model.GetUserUsedQuota(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"enabled":              false,
			"count_enabled":        setting.CountEnabled,
			"quota_enabled":        setting.QuotaEnabled,
			"include_subscription": setting.IncludeSubscription,
			"count_tiers":          setting.CountTiers,
			"quota_tiers":          setting.QuotaTiers,
			"fallback_reward":      setting.FallbackReward,
			"c_enabled":            setting.CEnabled,
			"c_base_threshold":     setting.CBaseThreshold,
			"c_base_reward":        setting.CBaseReward,
			"c_step_quota":         setting.CStepQuota,
			"c_step_reward":        setting.CStepReward,
			"c_max_reward":         setting.CMaxReward,
			"total_used_quota":     totalUsedQuota,
			"yesterday": gin.H{
				"count": yesterdayCount,
				"quota": yesterdayQuota,
			},
			"stats": stats,
		},
	})
}

// DoCheckin 执行用户签到
func DoCheckin(c *gin.Context) {
	c.JSON(http.StatusGone, gin.H{"success": false, "code": "checkin_endpoint_retired", "message": "原签到接口已停止，请刷新网页后使用每日签到"})
}
