package model

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"

	"gorm.io/gorm"
)

// LegacyInviteCleanupSummary reports what a dry run would do, or what an applied run did.
type LegacyInviteCleanupSummary struct {
	Applied              bool  `json:"applied"`
	EligibleRows         int64 `json:"eligible_rows"`
	EligibleInviterQuota int64 `json:"eligible_inviter_quota"`
	EligibleInviteeQuota int64 `json:"eligible_invitee_quota"`
	PendingRows          int64 `json:"pending_rows"`
	GrantedRows          int64 `json:"granted_rows"`
	CancelledRows        int64 `json:"cancelled_rows"`
}

// legacyInviteReward is the read/write projection of the retired deferred-invite
// ledger. The table is intentionally no longer part of InitXingyaTables: this
// maintenance path only reads and closes the rows that already exist.
type legacyInviteReward struct {
	Id            int    `json:"id" gorm:"primaryKey;autoIncrement"`
	InviteeId     int    `json:"invitee_id" gorm:"not null"`
	InviterId     int    `json:"inviter_id" gorm:"not null"`
	InviteeQuota  int    `json:"invitee_quota" gorm:"not null;default:0"`
	InviterQuota  int    `json:"inviter_quota" gorm:"not null;default:0"`
	State         string `json:"state" gorm:"type:varchar(16);not null;default:'pending'"`
	GrantMethod   string `json:"grant_method" gorm:"type:varchar(16)"`
	GrantedAt     int64  `json:"granted_at" gorm:"bigint;not null;default:0"`
	SettledAt     int64  `json:"settled_at" gorm:"bigint;not null;default:0"`
	CancelledNote string `json:"cancelled_note" gorm:"type:varchar(255)"`
}

func (legacyInviteReward) TableName() string {
	return "xingya_invite_reward_pending"
}

const (
	legacyInviteStatePending   = "pending"
	legacyInviteStateEligible  = "eligible"
	legacyInviteStateGranted   = "granted"
	legacyInviteStateCancelled = "cancelled"
	legacyInviteRemoveNote     = "invite reward mechanism removed"
)

// SettleLegacyInviteRewardsForRemoval closes the retired deferred-invite ledger.
//
// Eligible rows are paid exactly once through the same wallet/affiliate helpers the
// live settlement used, and pending rows are cancelled with an audit note. The
// function is idempotent: granted and cancelled rows are never touched again, so a
// retry after a partial failure cannot double-credit. A dry run only reports counts.
func SettleLegacyInviteRewardsForRemoval(apply bool) (LegacyInviteCleanupSummary, error) {
	summary := LegacyInviteCleanupSummary{Applied: apply}
	if DB == nil {
		return summary, errors.New("数据库未初始化")
	}
	if !DB.Migrator().HasTable(&legacyInviteReward{}) {
		return summary, nil
	}
	if err := DB.Model(&legacyInviteReward{}).
		Where("state = ?", legacyInviteStateEligible).
		Select("COUNT(*) AS eligible_rows, COALESCE(SUM(inviter_quota), 0) AS eligible_inviter_quota, COALESCE(SUM(invitee_quota), 0) AS eligible_invitee_quota").
		Scan(&summary).Error; err != nil {
		return summary, err
	}
	summary.Applied = apply
	if err := DB.Model(&legacyInviteReward{}).
		Where("state = ?", legacyInviteStatePending).
		Count(&summary.PendingRows).Error; err != nil {
		return summary, err
	}
	if err := DB.Model(&legacyInviteReward{}).
		Where("state = ?", legacyInviteStateGranted).
		Count(&summary.GrantedRows).Error; err != nil {
		return summary, err
	}
	if err := DB.Model(&legacyInviteReward{}).
		Where("state = ?", legacyInviteStateCancelled).
		Count(&summary.CancelledRows).Error; err != nil {
		return summary, err
	}
	if !apply {
		return summary, nil
	}

	var eligible []legacyInviteReward
	if err := DB.Where("state = ?", legacyInviteStateEligible).Order("id asc").Find(&eligible).Error; err != nil {
		return summary, err
	}
	for _, candidate := range eligible {
		if err := settleLegacyInviteReward(candidate.Id); err != nil {
			return summary, fmt.Errorf("settle legacy invite reward %d: %w", candidate.Id, err)
		}
	}

	result := DB.Model(&legacyInviteReward{}).
		Where("state = ?", legacyInviteStatePending).
		Updates(map[string]any{
			"state":          legacyInviteStateCancelled,
			"settled_at":     common.GetTimestamp(),
			"cancelled_note": legacyInviteRemoveNote,
		})
	if result.Error != nil {
		return summary, result.Error
	}
	summary.PendingRows = result.RowsAffected
	summary.GrantedRows = 0
	summary.CancelledRows = 0
	if err := DB.Model(&legacyInviteReward{}).Where("state = ?", legacyInviteStateGranted).Count(&summary.GrantedRows).Error; err != nil {
		return summary, err
	}
	if err := DB.Model(&legacyInviteReward{}).Where("state = ?", legacyInviteStateCancelled).Count(&summary.CancelledRows).Error; err != nil {
		return summary, err
	}
	return summary, nil
}

func settleLegacyInviteReward(rewardId int) error {
	var creditedInviterId, creditedInviterQuota int
	var creditedInviteeId, creditedInviteeQuota int
	err := DB.Transaction(func(tx *gorm.DB) error {
		var reward legacyInviteReward
		if err := lockForUpdate(tx).Where("id = ?", rewardId).First(&reward).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if reward.State != legacyInviteStateEligible {
			return nil
		}

		var inviter User
		inviterErr := tx.Select("id").Where("id = ?", reward.InviterId).First(&inviter).Error
		if inviterErr != nil && !errors.Is(inviterErr, gorm.ErrRecordNotFound) {
			return inviterErr
		}
		if errors.Is(inviterErr, gorm.ErrRecordNotFound) {
			return closeLegacyInviteRewardTx(tx, reward.Id, "inviter deleted")
		}

		if reward.InviteeQuota > 0 {
			var invitee User
			inviteeErr := tx.Select("id").Where("id = ?", reward.InviteeId).First(&invitee).Error
			if errors.Is(inviteeErr, gorm.ErrRecordNotFound) {
				return closeLegacyInviteRewardTx(tx, reward.Id, "invitee deleted")
			}
			if inviteeErr != nil {
				return inviteeErr
			}
			if err := increaseUserQuotaTx(tx, reward.InviteeId, reward.InviteeQuota); err != nil {
				return err
			}
			creditedInviteeId = reward.InviteeId
			creditedInviteeQuota = reward.InviteeQuota
		}

		if reward.InviterQuota > 0 {
			if reward.InviteeQuota > 0 {
				if err := inviteUserRewardTx(tx, reward.InviterId, reward.InviterQuota, false); err != nil {
					return err
				}
			} else {
				if err := inviteUserRewardTx(tx, reward.InviterId, reward.InviterQuota, true); err != nil {
					return err
				}
				creditedInviterId = reward.InviterId
				creditedInviterQuota = reward.InviterQuota
			}
		}

		now := common.GetTimestamp()
		result := tx.Model(&legacyInviteReward{}).
			Where("id = ? AND state = ?", reward.Id, legacyInviteStateEligible).
			Updates(map[string]any{
				"state":        legacyInviteStateGranted,
				"grant_method": "cleanup",
				"granted_at":   now,
				"settled_at":   now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errors.New("legacy invite reward state changed during settlement")
		}
		return nil
	})
	if err != nil {
		return err
	}
	if creditedInviteeQuota > 0 {
		syncCreditUserQuotaCache(creditedInviteeId, creditedInviteeQuota, "legacy invite cleanup")
	}
	if creditedInviterQuota > 0 {
		syncCreditUserQuotaCache(creditedInviterId, creditedInviterQuota, "legacy invite cleanup")
	}
	if creditedInviterId > 0 || creditedInviteeId > 0 {
		var reward legacyInviteReward
		if err := DB.Where("id = ?", rewardId).First(&reward).Error; err != nil {
			common.SysError("failed to load settled legacy invite reward for logging: " + err.Error())
			return nil
		}
		if reward.InviteeQuota > 0 {
			RecordLog(reward.InviteeId, LogTypeSystem, common.NewMessage("Invitation code bonus {{quota}}", map[string]any{"quota": logger.FormatQuota(reward.InviteeQuota)}))
			if reward.InviterQuota > 0 {
				if err := invalidateUserCache(reward.InviterId); err != nil {
					common.SysLog("failed to invalidate inviter cache after legacy invite cleanup: " + err.Error())
				}
			}
		}
		if reward.InviterQuota > 0 {
			RecordLog(reward.InviterId, LogTypeSystem, common.NewMessage("Bonus for inviting a user {{quota}}", map[string]any{"quota": logger.FormatQuota(reward.InviterQuota)}))
		}
	}
	return nil
}

func closeLegacyInviteRewardTx(tx *gorm.DB, rewardId int, note string) error {
	return tx.Model(&legacyInviteReward{}).
		Where("id = ? AND state = ?", rewardId, legacyInviteStateEligible).
		Updates(map[string]any{
			"state":          legacyInviteStateCancelled,
			"settled_at":     common.GetTimestamp(),
			"cancelled_note": note,
		}).Error
}
