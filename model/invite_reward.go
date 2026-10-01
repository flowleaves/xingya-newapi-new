package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"

	"gorm.io/gorm"
)

// Invite reward states. A row starts pending and moves exactly once, either to
// granted or to cancelled, so the terminal states double as the idempotency record.
const (
	InviteRewardStatePending   = "pending"
	InviteRewardStateGranted   = "granted"
	InviteRewardStateCancelled = "cancelled"
)

// inviteRewardWindowSeconds is the qualifying period an invitee must complete
// before the reward is paid.
//
// The requirement is "24 hours after registration AND 5 successful calls". The
// window is applied to the calls, not to the waiting: only successful calls made at
// least 24 hours after registration advance SuccessCallsAfterInviteWindow. A burst of
// calls inside the first day therefore cannot qualify an invitee on its own, which is
// the behaviour a farming account would otherwise exploit by registering, spending a
// few calls and cashing out immediately.
const inviteRewardWindowSeconds int64 = 24 * 60 * 60

// InviteRewardRequiredCalls is how many qualifying successful calls the invitee must
// complete before either quota share is paid.
const InviteRewardRequiredCalls = 5

// inviteRewardBatchLimit bounds how many pending rows one scheduled pass settles, so
// a backlog cannot turn a single run into an unbounded chain of transactions.
const inviteRewardBatchLimit = 200

// XingyaInviteRewardPending is the deferred invite reward that replaced granting both
// quota shares at registration time.
//
// InviteeId carries a unique index because an invitee can have exactly one reward: it
// is the row's natural identity and the reason a duplicate insert can never double
// promise a payout. The two quota columns are a snapshot taken at registration, so
// changing QuotaForInviter / QuotaForInvitee later cannot silently reprice a reward
// that was already promised.
type XingyaInviteRewardPending struct {
	Id            int    `json:"id" gorm:"primaryKey;autoIncrement"`
	InviteeId     int    `json:"invitee_id" gorm:"not null;uniqueIndex:idx_xingya_invite_reward_invitee"`
	InviterId     int    `json:"inviter_id" gorm:"not null;index"`
	InviteeQuota  int    `json:"invitee_quota" gorm:"not null;default:0"`
	InviterQuota  int    `json:"inviter_quota" gorm:"not null;default:0"`
	State         string `json:"state" gorm:"type:varchar(16);not null;default:'pending';index:idx_xingya_invite_reward_state_time,priority:1"`
	CreatedAt     int64  `json:"created_at" gorm:"bigint;not null;index:idx_xingya_invite_reward_state_time,priority:2"`
	GrantedAt     int64  `json:"granted_at" gorm:"bigint;not null;default:0"`
	SettledAt     int64  `json:"settled_at" gorm:"bigint;not null;default:0"`
	CancelledNote string `json:"cancelled_note" gorm:"type:varchar(255)"`
}

func (XingyaInviteRewardPending) TableName() string {
	return "xingya_invite_reward_pending"
}

// CreateInviteRewardPending records a deferred reward for one invited registration.
//
// It is a no-op when no share is configured or when the inviter is unknown, matching
// the enablement conditions the previous immediate-grant path applied. A duplicate
// invitee is also a no-op rather than an error: the caller is a registration that has
// already succeeded, and failing it over a bookkeeping insert would reject a valid
// account.
func CreateInviteRewardPending(inviteeId int, inviterId int) error {
	if inviteeId <= 0 || inviterId <= 0 {
		return nil
	}
	if common.QuotaForInvitee <= 0 && common.QuotaForInviter <= 0 {
		return nil
	}

	var existing int64
	if err := DB.Model(&XingyaInviteRewardPending{}).
		Where("invitee_id = ?", inviteeId).
		Count(&existing).Error; err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}

	reward := &XingyaInviteRewardPending{
		InviteeId:    inviteeId,
		InviterId:    inviterId,
		InviteeQuota: common.QuotaForInvitee,
		InviterQuota: common.QuotaForInviter,
		State:        InviteRewardStatePending,
		CreatedAt:    common.GetTimestamp(),
	}
	// The unique index is the real guard; the count above only avoids a pointless
	// insert in the common case. TranslateError is not enabled in this project, so a
	// lost race surfaces as a driver-level unique violation and is reported to the
	// caller rather than being matched by error value.
	return DB.Create(reward).Error
}

// HasPendingInviteReward reports whether any reward has reached its window and still
// needs settling. The scheduled handler uses it as its enablement check so an idle
// system schedules no task row at all; the query is an index range scan on
// (state, created_at) and stops at the first match.
func HasPendingInviteReward() bool {
	if common.QuotaForInvitee <= 0 && common.QuotaForInviter <= 0 {
		return false
	}
	var pending int64
	err := DB.Model(&XingyaInviteRewardPending{}).
		Where("state = ? AND created_at <= ?", InviteRewardStatePending, common.GetTimestamp()-inviteRewardWindowSeconds).
		Limit(1).
		Count(&pending).Error
	if err != nil {
		common.SysError("failed to check pending invite rewards: " + err.Error())
		return false
	}
	return pending > 0
}

// SettleInviteReward passes over the pending rewards whose window has elapsed and
// pays those whose invitee has completed the required qualifying calls.
//
// It returns how many rewards were granted and how many were cancelled. A reward that
// is not yet eligible is left pending and retried on a later pass; nothing is written
// for it, so an invitee who never reaches the call count simply never gets paid.
func SettleInviteReward(now int64) (granted int, cancelled int, err error) {
	rewards := make([]*XingyaInviteRewardPending, 0)
	if err = DB.Where("state = ? AND created_at <= ?", InviteRewardStatePending, now-inviteRewardWindowSeconds).
		Order("id asc").
		Limit(inviteRewardBatchLimit).
		Find(&rewards).Error; err != nil {
		return 0, 0, err
	}

	for _, reward := range rewards {
		outcome, settleErr := settleOneInviteRewardTx(reward.Id)
		if settleErr != nil {
			// One broken reward must not stop the rest of the batch. The row stays
			// pending, so the next pass retries it.
			common.SysError("failed to settle invite reward " + common.GetJsonString(reward.Id) + ": " + settleErr.Error())
			continue
		}
		switch outcome {
		case InviteRewardStateGranted:
			granted++
			// The log entries are written after the settlement transaction has
			// committed: under SQLite LOG_DB and DB are the same database, where
			// recording a log from inside the transaction deadlocks.
			RecordInviteRewardLogs(reward.Id)
		case InviteRewardStateCancelled:
			cancelled++
		}
	}
	return granted, cancelled, nil
}

// settleOneInviteRewardTx settles a single reward inside its own transaction.
//
// The state and both credits commit together: either the invitee and the inviter are
// paid and the row is closed, or nothing changed and the row stays pending. The row is
// re-read under a row lock and its state re-checked, which is what makes a concurrent
// pass or a retry after a crash unable to pay the same reward twice.
func settleOneInviteRewardTx(rewardId int) (outcome string, err error) {
	err = DB.Transaction(func(tx *gorm.DB) error {
		var reward XingyaInviteRewardPending
		if err := lockForUpdate(tx).Where("id = ?", rewardId).First(&reward).Error; err != nil {
			return err
		}
		if reward.State != InviteRewardStatePending {
			return nil
		}

		var invitee User
		if err := tx.Select("id", "success_calls_after_invite_window").
			Where("id = ?", reward.InviteeId).First(&invitee).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				outcome = InviteRewardStateCancelled
				return closeInviteRewardTx(tx, reward.Id, InviteRewardStateCancelled, "invitee deleted")
			}
			return err
		}
		if invitee.SuccessCallsAfterInviteWindow < InviteRewardRequiredCalls {
			return nil
		}

		now := common.GetTimestamp()
		if reward.InviteeQuota > 0 {
			if err := increaseUserQuotaTx(tx, reward.InviteeId, reward.InviteeQuota); err != nil {
				return err
			}
		}

		note := ""
		if reward.InviterQuota > 0 {
			var inviterExists int64
			if err := tx.Model(&User{}).Where("id = ?", reward.InviterId).Count(&inviterExists).Error; err != nil {
				return err
			}
			if inviterExists == 0 {
				// The invitee's share is still owed, so the reward is paid and
				// closed rather than left pending forever. The unpaid inviter share
				// is recorded on the row so the gap stays auditable.
				note = "inviter deleted; inviter share not paid"
			} else if err := inviteUserTx(tx, reward.InviterId); err != nil {
				return err
			}
		}

		result := tx.Model(&XingyaInviteRewardPending{}).
			Where("id = ? AND state = ?", reward.Id, InviteRewardStatePending).
			Updates(map[string]any{
				"state":          InviteRewardStateGranted,
				"granted_at":     now,
				"settled_at":     now,
				"cancelled_note": note,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		outcome = InviteRewardStateGranted
		return nil
	})
	return outcome, err
}

// closeInviteRewardTx moves a reward to a terminal state that pays nothing.
func closeInviteRewardTx(tx *gorm.DB, rewardId int, state string, note string) error {
	return tx.Model(&XingyaInviteRewardPending{}).
		Where("id = ? AND state = ?", rewardId, InviteRewardStatePending).
		Updates(map[string]any{
			"state":          state,
			"settled_at":     common.GetTimestamp(),
			"cancelled_note": note,
		}).Error
}

// GetInviteRewards returns the deferred rewards for the admin view, newest first, with
// the caller's page window applied.
func GetInviteRewards(state string, startIdx int, num int) (rewards []*XingyaInviteRewardPending, total int64, err error) {
	query := DB.Model(&XingyaInviteRewardPending{})
	if state != "" {
		query = query.Where("state = ?", state)
	}
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rewards = make([]*XingyaInviteRewardPending, 0)
	err = query.Order("id desc").Limit(num).Offset(startIdx).Find(&rewards).Error
	return rewards, total, err
}

// RecordInviteRewardLogs writes the user-visible log entries for a settled reward.
// It runs outside the settlement transaction because under SQLite LOG_DB and DB are
// the same database, where writing a log inside the transaction deadlocks.
func RecordInviteRewardLogs(rewardId int) {
	var reward XingyaInviteRewardPending
	if err := DB.Where("id = ?", rewardId).First(&reward).Error; err != nil {
		common.SysLog("failed to load settled invite reward for logging: " + err.Error())
		return
	}
	if reward.State != InviteRewardStateGranted {
		return
	}
	if reward.InviteeQuota > 0 {
		RecordLog(reward.InviteeId, LogTypeSystem,
			"使用邀请码赠送 "+logger.LogQuota(reward.InviteeQuota))
	}
	if reward.InviterQuota > 0 && reward.CancelledNote == "" {
		RecordLog(reward.InviterId, LogTypeSystem,
			"邀请用户赠送 "+logger.LogQuota(reward.InviterQuota))
	}
}
