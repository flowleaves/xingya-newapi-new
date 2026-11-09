package model

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	InviteRebateStatusGranted = "granted"
	InviteRebateStatusCapped  = "capped"

	InviteRebateSourceTopUp      = operation_setting.InviteRebateSourceTopUp
	InviteRebateSourceRedemption = operation_setting.InviteRebateSourceRedemption
)

var ErrInviteAffQuotaLimitExceeded = errors.New("affiliate quota limit exceeded")

// InviteRebateSource identifies the recharge event that may produce a rebate.
// Type is topup or redemption; Id is the source row id and OccurredAt is the
// successful completion time used for the 15-day window.
type InviteRebateSource struct {
	Type       string
	Id         int
	OccurredAt int64
}

// XingyaInviteRebate is the audit ledger for the new invite rebate mechanism.
// The money result lives in users.aff_quota / aff_history; this table keeps the
// per-source breakdown and the unique idempotency key.
type XingyaInviteRebate struct {
	Id            int    `json:"id" gorm:"primaryKey;autoIncrement"`
	InviterId     int    `json:"inviter_id" gorm:"not null;index:idx_xingya_invite_rebate_inviter"`
	InviteeId     int    `json:"invitee_id" gorm:"not null;index:idx_xingya_invite_rebate_invitee"`
	SourceType    string `json:"source_type" gorm:"type:varchar(16);not null;uniqueIndex:idx_xingya_invite_rebate_source,priority:1"`
	SourceId      int    `json:"source_id" gorm:"not null;uniqueIndex:idx_xingya_invite_rebate_source,priority:2"`
	BaseQuota     int64  `json:"base_quota" gorm:"bigint;not null"`
	RebateQuota   int64  `json:"rebate_quota" gorm:"bigint;not null"`
	RatioBp       int    `json:"ratio_bp" gorm:"not null"`
	WindowStartAt int64  `json:"window_start_at" gorm:"bigint;not null"`
	WindowEndAt   int64  `json:"window_end_at" gorm:"bigint;not null"`
	OccurredAt    int64  `json:"occurred_at" gorm:"bigint;not null"`
	Status        string `json:"status" gorm:"type:varchar(16);not null;default:'granted'"`
	Note          string `json:"note" gorm:"type:varchar(255)"`
	CreatedAt     int64  `json:"created_at" gorm:"bigint;not null"`
}

func (XingyaInviteRebate) TableName() string {
	return "xingya_invite_rebates"
}

// InviteRebateItem is the user-facing projection. The controller masks the
// invitee name before it leaves the backend.
type InviteRebateItem struct {
	XingyaInviteRebate
	InviteeUsername  string `json:"invitee_username" gorm:"column:invitee_username"`
	InviteeCreatedAt int64  `json:"invitee_created_at" gorm:"column:invitee_created_at"`
}

type InviteRebateSummary struct {
	AffCode             string  `json:"aff_code"`
	AffQuota            int     `json:"aff_quota"`
	AffHistoryQuota     int     `json:"aff_history_quota"`
	TotalRebateQuota    int64   `json:"total_rebate_quota"`
	RebateCount         int64   `json:"rebate_count"`
	InvitedCount        int64   `json:"invited_count"`
	ActiveWindowCount   int64   `json:"active_window_count"`
	RatioPercent        float64 `json:"ratio_percent"`
	WindowDays          int     `json:"window_days"`
	MaxRebatePerInvitee float64 `json:"max_rebate_per_invitee"`
	MaxRebateQuota      int     `json:"max_rebate_quota"`
}

// creditInviteRebateTx records and pays one rebate inside the caller's
// transaction. The caller wraps this in a savepoint, so a rebate failure never
// blocks the invitee's recharge.
func creditInviteRebateTx(tx *gorm.DB, inviteeId int, source InviteRebateSource, baseQuota int) error {
	setting := operation_setting.GetInviteRebateSetting().Sanitized()
	if !setting.ActiveFor(source.Type) || source.Id <= 0 || baseQuota <= 0 {
		return nil
	}
	if source.OccurredAt <= 0 {
		source.OccurredAt = common.GetTimestamp()
	}

	var invitee User
	if err := lockForUpdate(tx).
		Select("id", "inviter_id", "created_at").
		Where("id = ?", inviteeId).
		First(&invitee).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if invitee.InviterId <= 0 || invitee.InviterId == inviteeId || invitee.CreatedAt <= 0 {
		return nil
	}

	windowStart := invitee.CreatedAt
	windowEnd := windowStart + int64(setting.WindowDays)*24*60*60
	if source.OccurredAt < windowStart || source.OccurredAt > windowEnd {
		return nil
	}

	var inviter User
	if err := tx.Select("id").Where("id = ?", invitee.InviterId).First(&inviter).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}

	ratioBp := setting.RatioBasisPoints()
	maxRebateQuota := setting.MaxRebateQuota()
	if ratioBp <= 0 || maxRebateQuota <= 0 {
		return nil
	}

	var usedQuota int64
	if err := tx.Model(&XingyaInviteRebate{}).
		Where("invitee_id = ? AND status = ?", inviteeId, InviteRebateStatusGranted).
		Select("COALESCE(SUM(rebate_quota), 0)").
		Scan(&usedQuota).Error; err != nil {
		return err
	}
	remaining := int64(maxRebateQuota) - usedQuota
	if remaining <= 0 {
		_, err := enqueueInviteRebate(tx, invitee, source, baseQuota, ratioBp, windowStart, windowEnd, 0, InviteRebateStatusCapped, "per-invitee cap reached")
		return err
	}

	rebateQuota := common.QuotaFromDecimal(
		decimal.NewFromInt(int64(baseQuota)).
			Mul(decimal.NewFromInt(int64(ratioBp))).
			Div(decimal.NewFromInt(10000)),
	)
	if rebateQuota <= 0 {
		return nil
	}
	note := ""
	if int64(rebateQuota) > remaining {
		rebateQuota = int(remaining)
		note = "per-invitee cap reached"
	}

	inserted, err := enqueueInviteRebate(tx, invitee, source, baseQuota, ratioBp, windowStart, windowEnd, rebateQuota, InviteRebateStatusGranted, note)
	if err != nil {
		return err
	}
	if !inserted {
		return nil
	}

	result := tx.Model(&User{}).
		Where("id = ? AND aff_quota <= ?", invitee.InviterId, common.MaxQuota-rebateQuota).
		Updates(map[string]any{
			"aff_quota":   gorm.Expr("aff_quota + ?", rebateQuota),
			"aff_history": gorm.Expr("aff_history + ?", rebateQuota),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		var count int64
		if err := tx.Model(&User{}).Where("id = ?", invitee.InviterId).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return gorm.ErrRecordNotFound
		}
		return ErrInviteAffQuotaLimitExceeded
	}
	return nil
}

func enqueueInviteRebate(
	tx *gorm.DB,
	invitee User,
	source InviteRebateSource,
	baseQuota int,
	ratioBp int,
	windowStart int64,
	windowEnd int64,
	rebateQuota int,
	status string,
	note string,
) (bool, error) {
	row := &XingyaInviteRebate{
		InviterId:     invitee.InviterId,
		InviteeId:     invitee.Id,
		SourceType:    source.Type,
		SourceId:      source.Id,
		BaseQuota:     int64(baseQuota),
		RebateQuota:   int64(rebateQuota),
		RatioBp:       ratioBp,
		WindowStartAt: windowStart,
		WindowEndAt:   windowEnd,
		OccurredAt:    source.OccurredAt,
		Status:        status,
		Note:          note,
		CreatedAt:     common.GetTimestamp(),
	}
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(row)
	return result.RowsAffected > 0, result.Error
}

// GetInviteRebateSummary returns the current totals for one inviter.
func GetInviteRebateSummary(inviterId int) (InviteRebateSummary, error) {
	setting := operation_setting.GetInviteRebateSetting().Sanitized()
	summary := InviteRebateSummary{
		RatioPercent:        setting.RatioPercent,
		WindowDays:          setting.WindowDays,
		MaxRebatePerInvitee: setting.MaxRebatePerInvitee,
		MaxRebateQuota:      setting.MaxRebateQuota(),
	}
	var user User
	if err := DB.Select("aff_code", "aff_quota", "aff_history").
		Where("id = ?", inviterId).First(&user).Error; err != nil {
		return summary, err
	}
	summary.AffCode = user.AffCode
	summary.AffQuota = user.AffQuota
	summary.AffHistoryQuota = user.AffHistoryQuota

	var rebateTotals struct {
		RebateCount      int64 `gorm:"column:rebate_count"`
		TotalRebateQuota int64 `gorm:"column:total_rebate_quota"`
	}
	if err := DB.Model(&XingyaInviteRebate{}).
		Where("inviter_id = ? AND status = ?", inviterId, InviteRebateStatusGranted).
		Select("COUNT(*) AS rebate_count, COALESCE(SUM(rebate_quota), 0) AS total_rebate_quota").
		Scan(&rebateTotals).Error; err != nil {
		return summary, err
	}
	summary.RebateCount = rebateTotals.RebateCount
	summary.TotalRebateQuota = rebateTotals.TotalRebateQuota
	if err := DB.Model(&User{}).Where("inviter_id = ?", inviterId).Count(&summary.InvitedCount).Error; err != nil {
		return summary, err
	}
	if summary.WindowDays > 0 {
		cutoff := common.GetTimestamp() - int64(summary.WindowDays)*24*60*60
		if err := DB.Model(&User{}).
			Where("inviter_id = ? AND created_at > ?", inviterId, cutoff).
			Count(&summary.ActiveWindowCount).Error; err != nil {
			return summary, err
		}
	}
	return summary, nil
}

// GetInviteRebatesForInviter returns the inviter-owned ledger page.
func GetInviteRebatesForInviter(inviterId int, offset int, limit int) ([]*InviteRebateItem, int64, error) {
	if inviterId <= 0 {
		return nil, 0, nil
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	query := DB.Table("xingya_invite_rebates AS r").
		Joins("LEFT JOIN users AS u ON u.id = r.invitee_id").
		Where("r.inviter_id = ?", inviterId)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	items := make([]*InviteRebateItem, 0)
	if err := query.
		Select("r.*, u.username AS invitee_username, u.created_at AS invitee_created_at").
		Order("r.id DESC").
		Limit(limit).
		Offset(offset).
		Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// creditInviteRebateWithSavepoint keeps a rebate failure from rolling back the
// invitee's successful recharge. A failed rebate is logged and skipped.
func creditInviteRebateWithSavepoint(tx *gorm.DB, userId int, source InviteRebateSource, baseQuota int) {
	if err := tx.SavePoint("invite_rebate").Error; err != nil {
		common.SysError("failed to create invite rebate savepoint: " + err.Error())
		return
	}
	if err := creditInviteRebateTx(tx, userId, source, baseQuota); err != nil {
		if rbErr := tx.RollbackTo("invite_rebate").Error; rbErr != nil {
			common.SysError("failed to roll back invite rebate savepoint: " + rbErr.Error())
		}
		RecordInviteRebateFailure(source, userId, err)
	}
}

// RecordInviteRebateFailure is kept for callers that want a correlated log line
// without exposing financial details in the response path.
func RecordInviteRebateFailure(source InviteRebateSource, inviteeId int, err error) {
	common.SysError(fmt.Sprintf("invite rebate failed source=%s/%d invitee=%d: %v", source.Type, source.Id, inviteeId, err))
}
