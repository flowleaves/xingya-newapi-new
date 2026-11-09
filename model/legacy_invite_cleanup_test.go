package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSettleLegacyInviteRewardsForRemoval(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:legacy-invite-cleanup?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	oldDB, oldLogDB := DB, LOG_DB
	DB, LOG_DB = db, db
	t.Cleanup(func() {
		DB, LOG_DB = oldDB, oldLogDB
	})
	require.NoError(t, db.AutoMigrate(&User{}, &legacyInviteReward{}, &Log{}))

	inviter := User{Username: "cleanup-inviter", AffCode: "cleaninv", Quota: 1000, Status: common.UserStatusEnabled}
	invitee := User{Username: "cleanup-invitee", AffCode: "cleaninv2", Quota: 1000, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&inviter).Error)
	require.NoError(t, db.Create(&invitee).Error)

	rows := []legacyInviteReward{
		{InviteeId: invitee.Id, InviterId: inviter.Id, InviterQuota: 100, State: legacyInviteStateEligible},
		{InviteeId: invitee.Id, InviterId: inviter.Id, InviteeQuota: 50, InviterQuota: 100, State: legacyInviteStateEligible},
		{InviteeId: invitee.Id, InviterId: inviter.Id, InviterQuota: 100, State: legacyInviteStatePending},
	}
	require.NoError(t, db.Create(&rows).Error)

	summary, err := SettleLegacyInviteRewardsForRemoval(false)
	require.NoError(t, err)
	assert.Equal(t, int64(2), summary.EligibleRows)
	assert.Equal(t, int64(200), summary.EligibleInviterQuota)
	assert.Equal(t, int64(50), summary.EligibleInviteeQuota)
	assert.Equal(t, int64(1), summary.PendingRows)
	assert.False(t, summary.Applied)

	var unchanged User
	require.NoError(t, db.First(&unchanged, inviter.Id).Error)
	assert.Equal(t, 1000, unchanged.Quota)
	assert.Zero(t, unchanged.AffQuota)

	summary, err = SettleLegacyInviteRewardsForRemoval(true)
	require.NoError(t, err)
	assert.True(t, summary.Applied)
	assert.Equal(t, int64(2), summary.GrantedRows)
	assert.Equal(t, int64(1), summary.CancelledRows)

	var creditedInviter, creditedInvitee User
	require.NoError(t, db.First(&creditedInviter, inviter.Id).Error)
	require.NoError(t, db.First(&creditedInvitee, invitee.Id).Error)
	assert.Equal(t, 1100, creditedInviter.Quota)
	assert.Equal(t, 100, creditedInviter.AffQuota)
	assert.Equal(t, 200, creditedInviter.AffHistoryQuota)
	assert.Equal(t, 1050, creditedInvitee.Quota)

	var pending legacyInviteReward
	require.NoError(t, db.First(&pending, rows[2].Id).Error)
	assert.Equal(t, legacyInviteStateCancelled, pending.State)
	assert.Equal(t, legacyInviteRemoveNote, pending.CancelledNote)

	summary, err = SettleLegacyInviteRewardsForRemoval(true)
	require.NoError(t, err)
	assert.Zero(t, summary.EligibleRows)
	require.NoError(t, db.First(&creditedInviter, inviter.Id).Error)
	require.NoError(t, db.First(&creditedInvitee, invitee.Id).Error)
	assert.Equal(t, 1100, creditedInviter.Quota)
	assert.Equal(t, 1050, creditedInvitee.Quota)
}
