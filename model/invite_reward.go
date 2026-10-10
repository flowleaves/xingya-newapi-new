package model

import (
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	InviteRewardStatePending   = "pending"
	InviteRewardStateEligible  = "eligible"
	InviteRewardStateGranted   = "granted"
	InviteRewardStateCancelled = "cancelled"
	InviteRewardGrantAuto      = "auto"
	InviteRewardGrantManual    = "manual"
)

// InviteRewardRequiredCalls is the number of successful, billable calls the invited
// account must complete before the inviter may claim a new reward. It is re-exported
// from the operation settings so the ledger and the admin configuration cannot drift.
const InviteRewardRequiredCalls = operation_setting.InviteRewardRequiredCalls

const inviteRewardBatchLimit = 200

var (
	ErrInviteRewardNotFound    = errors.New("invite reward not found")
	ErrInviteRewardNotEligible = errors.New("invite reward is not eligible")
	ErrInviteRewardCancelled   = errors.New("invite reward is cancelled")
)

// XingyaInviteRewardPending is the durable reward ledger. New rows only store the
// inviter's reward; InviteeQuota remains for already-created legacy rows.
type XingyaInviteRewardPending struct {
	Id              int    `json:"id" gorm:"primaryKey;autoIncrement;index:idx_xingya_invite_reward_inviter,priority:2"`
	InviteeId       int    `json:"invitee_id" gorm:"not null;uniqueIndex:idx_xingya_invite_reward_invitee"`
	InviterId       int    `json:"inviter_id" gorm:"not null;index:idx_xingya_invite_reward_inviter,priority:1"`
	InviteeQuota    int    `json:"invitee_quota" gorm:"not null;default:0"`
	InviterQuota    int    `json:"inviter_quota" gorm:"not null;default:0"`
	QualifyingCalls int    `json:"qualifying_calls" gorm:"not null;default:0"`
	QualifyingQuota int    `json:"qualifying_quota" gorm:"not null;default:0"`
	EligibleAt      int64  `json:"eligible_at" gorm:"bigint;not null;default:0"`
	AutoGrantAt     int64  `json:"auto_grant_at" gorm:"bigint;not null;default:0;index:idx_xingya_invite_reward_state_auto_grant,priority:2"`
	GrantMethod     string `json:"grant_method" gorm:"type:varchar(16)"`
	State           string `json:"state" gorm:"type:varchar(16);not null;default:'pending';index:idx_xingya_invite_reward_state_auto_grant,priority:1"`
	CreatedAt       int64  `json:"created_at" gorm:"bigint;not null"`
	GrantedAt       int64  `json:"granted_at" gorm:"bigint;not null;default:0"`
	SettledAt       int64  `json:"settled_at" gorm:"bigint;not null;default:0"`
	CancelledNote   string `json:"cancelled_note" gorm:"type:varchar(255)"`
}

func (XingyaInviteRewardPending) TableName() string {
	return "xingya_invite_reward_pending"
}

// InviteRewardSelfItem is the user-facing projection. The controller masks the
// invitee name before it leaves the backend.
type InviteRewardSelfItem struct {
	XingyaInviteRewardPending
	InviteeUsername    string `json:"invitee_username" gorm:"column:invitee_username"`
	InviteeDisplayName string `json:"invitee_display_name" gorm:"column:invitee_display_name"`
}

// CreateInviteRewardPending records a new reward promise. New rewards only pay the
// inviter, while InviteeQuota is deliberately retained as zero for old-data safety.
func CreateInviteRewardPending(inviteeId int, inviterId int) error {
	if inviteeId <= 0 || inviterId <= 0 || common.QuotaForInviter <= 0 {
		return nil
	}

	reward := &XingyaInviteRewardPending{
		InviteeId:    inviteeId,
		InviterId:    inviterId,
		InviteeQuota: 0,
		InviterQuota: common.QuotaForInviter,
		State:        InviteRewardStatePending,
		CreatedAt:    common.GetTimestamp(),
	}
	// The unique invitee index is the actual idempotency guard. A concurrent duplicate
	// insert is harmless after a successful registration and is therefore ignored by the
	// database in one statement, while unrelated database errors still surface.
	return DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "invitee_id"}},
		DoNothing: true,
	}).Create(reward).Error
}

// inviteRewardAutoGrantAt returns the next local midnight. The scheduler may run a
// few minutes after it, but the ledger keeps the exact promised timestamp for the UI.
func inviteRewardAutoGrantAt(now int64) int64 {
	local := time.Unix(now, 0).In(time.Local)
	return time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, local.Location()).Unix()
}

// inviteRewardSpendGate returns the cumulative spend the invited account must reach, in
// internal quota units. Zero means no spend requirement.
func inviteRewardSpendGate() int {
	return RequiredConsumeQuota()
}

// RequiredConsumeQuota exposes the configured spend gate in internal quota units so the
// API layer can render the same rule the ledger enforces.
func RequiredConsumeQuota() int {
	return operation_setting.GetInviteRewardSetting().RequiredConsumeQuota(common.QuotaPerUnit)
}

// RecordSuccessfulBillableCall advances only an existing pending reward, accumulating
// both the qualifying call count and the qualifying spend.
//
// This is one conditional UPDATE. PostgreSQL serializes concurrent writes to the same
// row, and the assignments are ordered so MySQL also evaluates the promotion threshold
// before the counters are incremented. Ordinary users therefore incur no user-row write,
// and an account that was never invited costs nothing.
//
// Both published conditions must hold at once: the invited account has to complete the
// required number of successful billable calls *and* spend the configured amount. The
// spend gate is read once per call so an administrator changing it takes effect without
// a restart; already-promoted rows keep their eligibility because the state check below
// only matches pending rows.
func RecordSuccessfulBillableCall(userId int, quotaConsumed int) {
	if userId <= 0 || DB == nil || !xingyaTablesReady.Load() {
		return
	}
	quotaDelta := quotaConsumed
	if quotaDelta < 0 {
		quotaDelta = 0
	}
	now := common.GetTimestamp()
	autoGrantAt := inviteRewardAutoGrantAt(now)
	spendGate := inviteRewardSpendGate()
	err := DB.Exec(`
UPDATE xingya_invite_reward_pending
SET state = CASE WHEN qualifying_calls + 1 >= ? AND qualifying_quota + ? >= ? THEN ? ELSE state END,
    eligible_at = CASE WHEN qualifying_calls + 1 >= ? AND qualifying_quota + ? >= ? AND eligible_at = 0 THEN ? ELSE eligible_at END,
    auto_grant_at = CASE WHEN qualifying_calls + 1 >= ? AND qualifying_quota + ? >= ? AND auto_grant_at = 0 THEN ? ELSE auto_grant_at END,
    qualifying_calls = qualifying_calls + 1,
    qualifying_quota = qualifying_quota + ?
WHERE invitee_id = ? AND state = ?
`, InviteRewardRequiredCalls, quotaDelta, spendGate, InviteRewardStateEligible,
		InviteRewardRequiredCalls, quotaDelta, spendGate, now,
		InviteRewardRequiredCalls, quotaDelta, spendGate, autoGrantAt,
		quotaDelta,
		userId, InviteRewardStatePending).Error
	if err != nil {
		common.SysError(fmt.Sprintf("failed to record successful invite call for user %d: %v", userId, err))
	}
}

// HasPendingInviteReward reports whether the scheduled midnight pass has work.
func HasPendingInviteReward() bool {
	if DB == nil || !xingyaTablesReady.Load() {
		return false
	}
	var count int64
	err := DB.Model(&XingyaInviteRewardPending{}).
		Where("state = ? AND auto_grant_at > 0 AND auto_grant_at <= ?", InviteRewardStateEligible, common.GetTimestamp()).
		Limit(1).Count(&count).Error
	if err != nil {
		common.SysError("failed to check pending invite rewards: " + err.Error())
		return false
	}
	return count > 0
}

// SettleInviteReward automatically grants eligible rows whose promised midnight has
// arrived. ClaimInviteReward uses the same transaction with a different grant method.
func SettleInviteReward(now int64) (granted int, cancelled int, err error) {
	rewards := make([]*XingyaInviteRewardPending, 0)
	err = DB.Where("state = ? AND auto_grant_at > 0 AND auto_grant_at <= ?", InviteRewardStateEligible, now).
		Order("id asc").Limit(inviteRewardBatchLimit).Find(&rewards).Error
	if err != nil {
		return 0, 0, err
	}
	for _, reward := range rewards {
		outcome, _, alreadyGranted, settleErr := settleInviteRewardTx(reward.Id, InviteRewardGrantAuto, 0)
		if settleErr != nil {
			common.SysError("failed to settle invite reward " + common.GetJsonString(reward.Id) + ": " + settleErr.Error())
			continue
		}
		if alreadyGranted {
			continue
		}
		switch outcome {
		case InviteRewardStateGranted:
			granted++
			RecordInviteRewardLogs(reward.Id)
		case InviteRewardStateCancelled:
			cancelled++
		}
	}
	return granted, cancelled, nil
}

// ClaimInviteReward lets the inviter claim an eligible row before midnight. A granted
// row returns a successful idempotent result, allowing a client retry to refresh safely.
func ClaimInviteReward(inviterId int, rewardId int) (granted bool, alreadyGranted bool, err error) {
	if inviterId <= 0 || rewardId <= 0 {
		return false, false, ErrInviteRewardNotFound
	}
	outcome, _, alreadyGranted, err := settleInviteRewardTx(rewardId, InviteRewardGrantManual, inviterId)
	if err != nil {
		return false, false, err
	}
	return outcome == InviteRewardStateGranted, alreadyGranted, nil
}

// settleInviteRewardTx is the one accounting boundary for automatic and manual grants.
// It locks the reward row, checks ownership/state, credits the appropriate historical
// path, and changes state in the same transaction.
func settleInviteRewardTx(rewardId int, method string, ownerId int) (outcome string, inviterCredit int, alreadyGranted bool, err error) {
	var creditedInviterId int
	err = DB.Transaction(func(tx *gorm.DB) error {
		var reward XingyaInviteRewardPending
		if err := lockForUpdate(tx).Where("id = ?", rewardId).First(&reward).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrInviteRewardNotFound
			}
			return err
		}
		if ownerId > 0 && reward.InviterId != ownerId {
			return ErrInviteRewardNotFound
		}
		if reward.State == InviteRewardStateGranted {
			alreadyGranted = true
			outcome = InviteRewardStateGranted
			return nil
		}
		if reward.State == InviteRewardStateCancelled {
			return ErrInviteRewardCancelled
		}
		if reward.State != InviteRewardStateEligible || reward.QualifyingCalls < InviteRewardRequiredCalls {
			return ErrInviteRewardNotEligible
		}
		// Both published conditions are re-checked under the row lock. The promotion that
		// set the eligible state already required the spend gate, so this guards against a
		// row promoted before the gate existed, or one promoted under a lower gate that an
		// administrator has since raised.
		if spendGate := inviteRewardSpendGate(); spendGate > 0 && reward.QualifyingQuota < spendGate {
			return ErrInviteRewardNotEligible
		}

		var inviter User
		inviterErr := tx.Select("id").Where("id = ?", reward.InviterId).First(&inviter).Error
		if inviterErr != nil {
			if errors.Is(inviterErr, gorm.ErrRecordNotFound) {
				outcome = InviteRewardStateCancelled
				return closeInviteRewardTx(tx, reward.Id, InviteRewardStateCancelled, "inviter deleted")
			}
			if !errors.Is(inviterErr, gorm.ErrRecordNotFound) {
				return inviterErr
			}
		}

		var invitee User
		inviteeErr := tx.Select("id").Where("id = ?", reward.InviteeId).First(&invitee).Error
		if inviteeErr != nil && reward.InviteeQuota > 0 {
			// A historical invitee share is still paid through its original path only
			// while the invited account exists. There is no account to credit otherwise.
			outcome = InviteRewardStateCancelled
			return closeInviteRewardTx(tx, reward.Id, InviteRewardStateCancelled, "invitee deleted")
		}
		if inviteeErr != nil && !errors.Is(inviteeErr, gorm.ErrRecordNotFound) {
			return inviteeErr
		}

		if reward.InviteeQuota > 0 {
			if err := increaseUserQuotaTx(tx, reward.InviteeId, reward.InviteeQuota); err != nil {
				return err
			}
		}
		if reward.InviterQuota > 0 && inviterErr == nil {
			if reward.InviteeQuota > 0 {
				if err := inviteUserRewardTx(tx, reward.InviterId, reward.InviterQuota, false); err != nil {
					return err
				}
			} else {
				if err := inviteUserRewardTx(tx, reward.InviterId, reward.InviterQuota, true); err != nil {
					return err
				}
				inviterCredit = reward.InviterQuota
				creditedInviterId = reward.InviterId
			}
		}

		now := common.GetTimestamp()
		result := tx.Model(&XingyaInviteRewardPending{}).
			Where("id = ? AND state = ?", reward.Id, InviteRewardStateEligible).
			Updates(map[string]any{
				"state":        InviteRewardStateGranted,
				"grant_method": method,
				"granted_at":   now,
				"settled_at":   now,
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
	if err == nil && inviterCredit > 0 && creditedInviterId > 0 {
		syncCreditUserQuotaCache(creditedInviterId, inviterCredit, "invite reward")
	}
	return outcome, inviterCredit, alreadyGranted, err
}

func closeInviteRewardTx(tx *gorm.DB, rewardId int, state string, note string) error {
	return tx.Model(&XingyaInviteRewardPending{}).
		Where("id = ? AND state IN ?", rewardId, []string{InviteRewardStatePending, InviteRewardStateEligible}).
		Updates(map[string]any{
			"state":          state,
			"settled_at":     common.GetTimestamp(),
			"cancelled_note": note,
		}).Error
}

// CancelInviteRewardsForDeletedInviter closes every unsettled reward as part of the
// inviter deletion transaction. Historical rows keep their original quota fields for
// compatibility, but an inviter deletion must not leave any pending payout eligible.
func CancelInviteRewardsForDeletedInviter(tx *gorm.DB, inviterId int) error {
	if inviterId <= 0 {
		return nil
	}
	return tx.Model(&XingyaInviteRewardPending{}).
		Where("inviter_id = ? AND state IN ?", inviterId,
			[]string{InviteRewardStatePending, InviteRewardStateEligible}).
		Updates(map[string]any{
			"state":          InviteRewardStateCancelled,
			"settled_at":     common.GetTimestamp(),
			"cancelled_note": "inviter deleted",
		}).Error
}

// GetInviteRewards returns the admin ledger projection.
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

// GetInviteRewardsForInviter returns rows owned by one inviter for the self page.
func GetInviteRewardsForInviter(inviterId int, startIdx int, num int) (rewards []*InviteRewardSelfItem, total int64, err error) {
	query := DB.Table("xingya_invite_reward_pending AS r").
		Select("r.*, u.username AS invitee_username, u.display_name AS invitee_display_name").
		Joins("LEFT JOIN users AS u ON u.id = r.invitee_id").
		Where("r.inviter_id = ?", inviterId)
	if err = query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rewards = make([]*InviteRewardSelfItem, 0)
	err = query.Order("r.id desc").Limit(num).Offset(startIdx).Find(&rewards).Error
	return rewards, total, err
}

// GetInviteRewardForInviter is used by the claim endpoint and keeps ownership in the
// model layer so a guessed reward id cannot be used to affect another wallet.
func GetInviteRewardForInviter(inviterId int, rewardId int) (*XingyaInviteRewardPending, error) {
	var reward XingyaInviteRewardPending
	err := DB.Where("id = ? AND inviter_id = ?", rewardId, inviterId).First(&reward).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrInviteRewardNotFound
	}
	return &reward, err
}

// RecordInviteRewardLogs writes logs after the accounting transaction commits.
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
		RecordLog(reward.InviteeId, LogTypeSystem, "使用邀请码赠送 "+logger.LogQuota(reward.InviteeQuota))
	}
	if reward.InviterQuota > 0 {
		RecordLog(reward.InviterId, LogTypeSystem, "邀请奖励发放 "+logger.LogQuota(reward.InviterQuota))
	}
}
