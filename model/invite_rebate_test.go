package model

import (
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupInviteRebateTestState(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "invite-rebate.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	oldDB := DB
	DB = db
	t.Cleanup(func() { DB = oldDB })
	require.NoError(t, db.AutoMigrate(&User{}, &XingyaInviteRebate{}))

	setting := operation_setting.GetInviteRebateSetting()
	oldSetting := *setting
	oldQuotaPerUnit := common.QuotaPerUnit
	*setting = operation_setting.InviteRebateSetting{
		Enabled:             true,
		RatioPercent:        5,
		WindowDays:          15,
		MaxRebatePerInvitee: 5000,
		IncludeTopUp:        true,
		IncludeRedemption:   true,
	}
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() {
		*setting = oldSetting
		common.QuotaPerUnit = oldQuotaPerUnit
	})
}

func createInviteRebateUser(t *testing.T, username string, inviterId int, createdAt int64) User {
	t.Helper()
	user := User{
		Username:  username,
		AffCode:   username + "-aff",
		InviterId: inviterId,
		CreatedAt: createdAt,
		Status:    common.UserStatusEnabled,
	}
	require.NoError(t, DB.Create(&user).Error)
	return user
}

func TestCreditInviteRebatePaysFivePercentInsideWindow(t *testing.T) {
	setupInviteRebateTestState(t)
	now := common.GetTimestamp()
	inviter := createInviteRebateUser(t, "inviter", 0, now-30*24*60*60)
	invitee := createInviteRebateUser(t, "invitee", inviter.Id, now-24*60*60)

	const base = 10_000_000
	require.NoError(t, creditInviteRebateTx(DB, invitee.Id, InviteRebateSource{
		Type:       InviteRebateSourceTopUp,
		Id:         1,
		OccurredAt: now,
	}, base))

	var updated User
	require.NoError(t, DB.First(&updated, inviter.Id).Error)
	assert.Equal(t, base*5/100, updated.AffQuota)
	assert.Equal(t, base*5/100, updated.AffHistoryQuota)

	var rows []XingyaInviteRebate
	require.NoError(t, DB.Find(&rows).Error)
	require.Len(t, rows, 1)
	assert.Equal(t, int64(base), rows[0].BaseQuota)
	assert.Equal(t, int64(base*5/100), rows[0].RebateQuota)
	assert.Equal(t, InviteRebateStatusGranted, rows[0].Status)
}

func TestCreditInviteRebateSkipsOutsideWindow(t *testing.T) {
	setupInviteRebateTestState(t)
	now := common.GetTimestamp()
	inviter := createInviteRebateUser(t, "inviter", 0, now-40*24*60*60)
	invitee := createInviteRebateUser(t, "invitee", inviter.Id, now-16*24*60*60)

	require.NoError(t, creditInviteRebateTx(DB, invitee.Id, InviteRebateSource{
		Type:       InviteRebateSourceTopUp,
		Id:         2,
		OccurredAt: now,
	}, 10_000_000))

	var count int64
	require.NoError(t, DB.Model(&XingyaInviteRebate{}).Count(&count).Error)
	assert.Zero(t, count)
	var updated User
	require.NoError(t, DB.First(&updated, inviter.Id).Error)
	assert.Zero(t, updated.AffQuota)
}

func TestCreditInviteRebateEnforcesPerInviteeCap(t *testing.T) {
	setupInviteRebateTestState(t)
	now := common.GetTimestamp()
	inviter := createInviteRebateUser(t, "inviter", 0, now-30*24*60*60)
	invitee := createInviteRebateUser(t, "invitee", inviter.Id, now-24*60*60)

	require.NoError(t, creditInviteRebateTx(DB, invitee.Id, InviteRebateSource{
		Type:       InviteRebateSourceTopUp,
		Id:         3,
		OccurredAt: now,
	}, 1_000_000_000))
	require.NoError(t, creditInviteRebateTx(DB, invitee.Id, InviteRebateSource{
		Type:       InviteRebateSourceTopUp,
		Id:         4,
		OccurredAt: now,
	}, 1_000_000_000))

	var updated User
	require.NoError(t, DB.First(&updated, inviter.Id).Error)
	capQuota := operation_setting.GetInviteRebateSetting().MaxRebateQuota()
	assert.Equal(t, capQuota, updated.AffQuota)

	var capped int64
	require.NoError(t, DB.Model(&XingyaInviteRebate{}).
		Where("status = ?", InviteRebateStatusCapped).Count(&capped).Error)
	assert.Equal(t, int64(1), capped)
}

func TestCreditInviteRebateIsIdempotent(t *testing.T) {
	setupInviteRebateTestState(t)
	now := common.GetTimestamp()
	inviter := createInviteRebateUser(t, "inviter", 0, now-30*24*60*60)
	invitee := createInviteRebateUser(t, "invitee", inviter.Id, now-24*60*60)
	source := InviteRebateSource{Type: InviteRebateSourceRedemption, Id: 5, OccurredAt: now}

	require.NoError(t, creditInviteRebateTx(DB, invitee.Id, source, 10_000_000))
	require.NoError(t, creditInviteRebateTx(DB, invitee.Id, source, 10_000_000))

	var rows int64
	require.NoError(t, DB.Model(&XingyaInviteRebate{}).Count(&rows).Error)
	assert.Equal(t, int64(1), rows)
	var updated User
	require.NoError(t, DB.First(&updated, inviter.Id).Error)
	assert.Equal(t, 500_000, updated.AffQuota)
}

func TestCreditInviteRebateSkipsSelfAndMissingInviter(t *testing.T) {
	setupInviteRebateTestState(t)
	now := common.GetTimestamp()
	self := createInviteRebateUser(t, "self", 0, now-24*60*60)
	self.InviterId = self.Id
	require.NoError(t, DB.Model(&self).Update("inviter_id", self.Id).Error)
	require.NoError(t, creditInviteRebateTx(DB, self.Id, InviteRebateSource{
		Type: InviteRebateSourceTopUp, Id: 6, OccurredAt: now,
	}, 10_000_000))

	invitee := createInviteRebateUser(t, "orphan", 999999, now-24*60*60)
	require.NoError(t, creditInviteRebateTx(DB, invitee.Id, InviteRebateSource{
		Type: InviteRebateSourceTopUp, Id: 7, OccurredAt: now,
	}, 10_000_000))

	var count int64
	require.NoError(t, DB.Model(&XingyaInviteRebate{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestCreditTopUpQuotaGrantsRebateAndSurvivesRebateFailure(t *testing.T) {
	setupInviteRebateTestState(t)
	now := common.GetTimestamp()
	inviter := createInviteRebateUser(t, "topup-inviter", 0, now-30*24*60*60)
	invitee := createInviteRebateUser(t, "topup-invitee", inviter.Id, now-24*60*60)

	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return creditTopUpQuota(tx, invitee.Id, 10_000_000, InviteRebateSource{
			Type: InviteRebateSourceTopUp, Id: 9, OccurredAt: now,
		}, nil)
	}))
	var updatedInviter, updatedInvitee User
	require.NoError(t, DB.First(&updatedInviter, inviter.Id).Error)
	require.NoError(t, DB.First(&updatedInvitee, invitee.Id).Error)
	assert.Equal(t, 10_000_000, updatedInvitee.Quota)
	assert.Equal(t, 500_000, updatedInviter.AffQuota)

	// A full affiliate balance must not reject the invitee's recharge. The rebate
	// transaction rolls back to its savepoint while the wallet credit survives.
	require.NoError(t, DB.Model(&User{}).Where("id = ?", inviter.Id).
		Update("aff_quota", common.MaxQuota).Error)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return creditTopUpQuota(tx, invitee.Id, 2_000_000, InviteRebateSource{
			Type: InviteRebateSourceTopUp, Id: 10, OccurredAt: now,
		}, nil)
	}))
	require.NoError(t, DB.First(&updatedInvitee, invitee.Id).Error)
	assert.Equal(t, 12_000_000, updatedInvitee.Quota)
	require.NoError(t, DB.First(&updatedInviter, inviter.Id).Error)
	assert.Equal(t, common.MaxQuota, updatedInviter.AffQuota)
}

func TestGetInviteRebateSummaryAndPage(t *testing.T) {
	setupInviteRebateTestState(t)
	now := common.GetTimestamp()
	inviter := createInviteRebateUser(t, "summary-inviter", 0, now-30*24*60*60)
	invitee := createInviteRebateUser(t, "summary-invitee", inviter.Id, now-24*60*60)
	require.NoError(t, creditInviteRebateTx(DB, invitee.Id, InviteRebateSource{
		Type: InviteRebateSourceTopUp, Id: 8, OccurredAt: now,
	}, 10_000_000))

	summary, err := GetInviteRebateSummary(inviter.Id)
	require.NoError(t, err)
	assert.Equal(t, int64(1), summary.RebateCount)
	assert.Equal(t, int64(500_000), summary.TotalRebateQuota)
	assert.Equal(t, int64(1), summary.InvitedCount)
	assert.Equal(t, int64(1), summary.ActiveWindowCount)
	assert.Equal(t, 500_000, summary.AffQuota)

	items, total, err := GetInviteRebatesForInviter(inviter.Id, 0, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, items, 1)
	assert.Equal(t, "summary-invitee", items[0].InviteeUsername)
}
