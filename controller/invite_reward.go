package controller

import (
	"context"
	"errors"
	"fmt"
	"strconv"

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
	case "", model.InviteRewardStatePending, model.InviteRewardStateGranted, model.InviteRewardStateCancelled:
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
