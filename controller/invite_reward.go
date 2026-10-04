package controller

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// AdminListInviteRewards serves the deferred-reward ledger to support staff.
//
// It is read-only. There is deliberately no way to pay a reward by hand from here:
// settlement is driven by the invitee's own activity, and an operator-triggered payout
// would need its own audit trail rather than a re-used list endpoint.
func AdminListInviteRewards(c *gin.Context) {
	state := c.Query("state")
	switch state {
	case "", model.InviteRewardStatePending, model.InviteRewardStateEligible, model.InviteRewardStateGranted, model.InviteRewardStateCancelled:
	default:
		common.ApiErrorMsg(c, "无效的邀请奖励状态")
		return
	}

	pageInfo := common.GetPageQuery(c)
	rewards, total, err := model.GetInviteRewards(state, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(rewards)
	common.ApiSuccess(c, pageInfo)
}

// ListInviteRewardsSelf returns only rewards owned by the authenticated inviter.
func ListInviteRewardsSelf(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	rewards, total, err := model.GetInviteRewardsForInviter(c.GetInt("id"), pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	for _, reward := range rewards {
		reward.InviteeUsername = maskInviteeIdentity(reward.InviteeUsername)
		reward.InviteeDisplayName = maskInviteeIdentity(reward.InviteeDisplayName)
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(rewards)
	common.ApiSuccess(c, gin.H{
		"items":          pageInfo.Items,
		"total":          pageInfo.Total,
		"page":           pageInfo.Page,
		"page_size":      pageInfo.PageSize,
		"required_calls": model.InviteRewardRequiredCalls,
	})
}

// ClaimInviteRewardSelf performs an inviter-owned manual claim. The model layer
// enforces the ownership check again inside the row-lock transaction.
func ClaimInviteRewardSelf(c *gin.Context) {
	rewardId, err := strconv.Atoi(c.Param("id"))
	if err != nil || rewardId <= 0 {
		common.ApiErrorMsg(c, "无效的邀请奖励")
		return
	}
	granted, alreadyGranted, err := model.ClaimInviteReward(c.GetInt("id"), rewardId)
	if err != nil {
		switch {
		case errors.Is(err, model.ErrInviteRewardNotFound):
			common.ApiErrorMsg(c, "邀请奖励不存在")
		case errors.Is(err, model.ErrInviteRewardNotEligible):
			common.ApiErrorMsg(c, "邀请奖励尚未达标")
		case errors.Is(err, model.ErrInviteRewardCancelled):
			common.ApiErrorMsg(c, "邀请奖励已取消")
		default:
			common.ApiError(c, err)
		}
		return
	}
	if granted && !alreadyGranted {
		model.RecordInviteRewardLogs(rewardId)
	}
	common.ApiSuccess(c, gin.H{"granted": granted, "already_granted": alreadyGranted})
}

func maskInviteeIdentity(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) == 1 {
		return string(runes[0]) + "*"
	}
	if len(runes) == 2 {
		return string(runes[0]) + "*"
	}
	return string(runes[0]) + "***" + string(runes[len(runes)-1])
}

// AdminListRegistrationRisk returns recent duplicate fingerprint groups. The default
// range is the last 90 days; raw addresses never cross this API boundary.
func AdminListRegistrationRisk(c *gin.Context) {
	now := common.GetTimestamp()
	from := now - 90*24*60*60
	to := now
	if value := c.Query("from"); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			common.ApiErrorMsg(c, "无效的开始时间")
			return
		}
		from = parsed
	}
	if value := c.Query("to"); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			common.ApiErrorMsg(c, "无效的结束时间")
			return
		}
		to = parsed
	}
	if from > to {
		common.ApiErrorMsg(c, "时间范围无效")
		return
	}
	minRepeats := 2
	if value := c.Query("min_repeats"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 2 {
			common.ApiErrorMsg(c, "重复次数筛选无效")
			return
		}
		minRepeats = parsed
	}
	pageInfo := common.GetPageQuery(c)
	groups, total, err := model.GetRegistrationRiskGroups(from, to, minRepeats, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(groups)
	common.ApiSuccess(c, gin.H{
		"items":     groups,
		"total":     pageInfo.Total,
		"page":      pageInfo.Page,
		"page_size": pageInfo.PageSize,
		"from":      from,
		"to":        to,
	})
}

// AdminGetRegistrationDevice serves the device signals recorded for one registration.
//
// It is what makes the same-device limit diagnosable. The response carries the masked
// network and the keyed digests that were actually stored — never the literal client
// address — so an operator can confirm whether a rejected signup matched an existing
// record without the endpoint becoming a way to read user addresses out of the database.
func AdminGetRegistrationDevice(c *gin.Context) {
	userId, err := strconv.Atoi(c.Param("id"))
	if err != nil || userId <= 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}

	device, err := model.GetRegistrationDevice(userId)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiSuccess(c, nil)
			return
		}
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"user_id":    device.UserId,
		"ip_prefix":  device.IpPrefix,
		"ip_hash":    device.IpHash,
		"user_agent": device.UserAgent,
		"reg_time":   device.RegTime,
	})
}

// runInviteRewardTask settles every deferred invite reward that has come due and
// returns a summary for the system task history.
//
// It reports a plain success even when nothing was payable: a pass where every invitee
// is still short of the required calls is the normal steady state, not a failure.
func runInviteRewardTask(ctx context.Context) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	granted, cancelled, err := model.SettleInviteReward(common.GetTimestamp())
	if err != nil {
		return nil, err
	}
	summary := map[string]any{
		"granted":   granted,
		"cancelled": cancelled,
	}
	if granted+cancelled > 0 {
		common.SysLog(fmt.Sprintf("invite reward settlement: granted=%d cancelled=%d", granted, cancelled))
	}
	return summary, nil
}
