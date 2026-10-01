package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupInviteRewardTestState prepares an isolated invite-reward fixture and restores
// the global quota settings the settlement reads.
func setupInviteRewardTestState(t *testing.T, inviteeQuota int, inviterQuota int) {
	t.Helper()
	truncateTables(t)
	require.NoError(t, ensureXingyaTables(DB))
	require.NoError(t, DB.Exec("DELETE FROM xingya_invite_reward_pending").Error)

	oldInviteeQuota := common.QuotaForInvitee
	oldInviterQuota := common.QuotaForInviter
	common.QuotaForInvitee = inviteeQuota
	common.QuotaForInviter = inviterQuota
	t.Cleanup(func() {
		common.QuotaForInvitee = oldInviteeQuota
		common.QuotaForInviter = oldInviterQuota
		DB.Exec("DELETE FROM xingya_invite_reward_pending")
	})
}

// registerInviteRewardFixture records a reward exactly as an invited registration
// would, then backdates it so the 24-hour window has already elapsed. Backdating
// states the elapsed window explicitly instead of sleeping for it.
func registerInviteRewardFixture(t *testing.T, inviterId int, inviteeId int, registeredAgo int64) *XingyaInviteRewardPending {
	t.Helper()
	require.NoError(t, CreateInviteRewardPending(inviteeId, inviterId))
	var reward XingyaInviteRewardPending
	require.NoError(t, DB.Where("invitee_id = ?", inviteeId).First(&reward).Error)
	reward.CreatedAt = common.GetTimestamp() - registeredAgo
	require.NoError(t, DB.Model(&XingyaInviteRewardPending{}).Where("id = ?", reward.Id).
		Update("created_at", reward.CreatedAt).Error)
	return &reward
}

func getInviteRewardFromDB(t *testing.T, id int) XingyaInviteRewardPending {
	t.Helper()
	var reward XingyaInviteRewardPending
	require.NoError(t, DB.Where("id = ?", id).First(&reward).Error)
	return reward
}

func setQualifyingInviteCalls(t *testing.T, userId int, calls int) {
	t.Helper()
	require.NoError(t, DB.Model(&User{}).Where("id = ?", userId).
		Update("success_calls_after_invite_window", calls).Error)
}

func TestCreateInviteRewardPendingPromisesWithoutPaying(t *testing.T) {
	setupInviteRewardTestState(t, 5000, 8000)

	inviter := createReserveTestUser(t, 100000)
	invitee := createReserveTestUser(t, 100000)

	require.NoError(t, CreateInviteRewardPending(invitee.Id, inviter.Id))

	var reward XingyaInviteRewardPending
	require.NoError(t, DB.Where("invitee_id = ?", invitee.Id).First(&reward).Error)
	assert.Equal(t, InviteRewardStatePending, reward.State)
	assert.Equal(t, inviter.Id, reward.InviterId)
	assert.Equal(t, 5000, reward.InviteeQuota)
	assert.Equal(t, 8000, reward.InviterQuota)
	assert.Zero(t, reward.GrantedAt)

	// The whole point of the change: registering must not move any money yet. The
	// inviter's affiliate counters stay untouched as well.
	assert.Equal(t, 100000, getUserQuotaFromDB(t, invitee.Id))
	var storedInviter User
	require.NoError(t, DB.First(&storedInviter, inviter.Id).Error)
	assert.Zero(t, storedInviter.AffCount)
	assert.Zero(t, storedInviter.AffQuota)
	assert.Zero(t, storedInviter.AffHistoryQuota)
}

func TestCreateInviteRewardPendingIsIdempotentPerInvitee(t *testing.T) {
	setupInviteRewardTestState(t, 5000, 8000)

	inviter := createReserveTestUser(t, 100000)
	invitee := createReserveTestUser(t, 100000)

	require.NoError(t, CreateInviteRewardPending(invitee.Id, inviter.Id))
	require.NoError(t, CreateInviteRewardPending(invitee.Id, inviter.Id))

	var count int64
	require.NoError(t, DB.Model(&XingyaInviteRewardPending{}).
		Where("invitee_id = ?", invitee.Id).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestCreateInviteRewardPendingSkipsDisabledAndUnknownInviter(t *testing.T) {
	setupInviteRewardTestState(t, 0, 0)

	inviter := createReserveTestUser(t, 100000)
	invitee := createReserveTestUser(t, 100000)

	// No share configured: nothing to promise.
	require.NoError(t, CreateInviteRewardPending(invitee.Id, inviter.Id))
	// No inviter: nothing to promise.
	require.NoError(t, CreateInviteRewardPending(invitee.Id, 0))

	var count int64
	require.NoError(t, DB.Model(&XingyaInviteRewardPending{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestHasPendingInviteRewardWaitsForTheWindow(t *testing.T) {
	setupInviteRewardTestState(t, 5000, 8000)

	inviter := createReserveTestUser(t, 100000)
	invitee := createReserveTestUser(t, 100000)
	registerInviteRewardFixture(t, inviter.Id, invitee.Id, 60)

	assert.False(t, HasPendingInviteReward(), "a reward inside its window is not settleable")

	require.NoError(t, DB.Model(&XingyaInviteRewardPending{}).
		Where("invitee_id = ?", invitee.Id).
		Update("created_at", common.GetTimestamp()-inviteRewardWindowSeconds).Error)
	assert.True(t, HasPendingInviteReward())
}

func TestSettleInviteRewardRequiresTheQualifyingCallCount(t *testing.T) {
	setupInviteRewardTestState(t, 5000, 8000)

	inviter := createReserveTestUser(t, 100000)
	invitee := createReserveTestUser(t, 100000)
	reward := registerInviteRewardFixture(t, inviter.Id, invitee.Id, inviteRewardWindowSeconds+60)
	setQualifyingInviteCalls(t, invitee.Id, InviteRewardRequiredCalls-1)

	granted, cancelled, err := SettleInviteReward(common.GetTimestamp())
	require.NoError(t, err)
	assert.Zero(t, granted)
	assert.Zero(t, cancelled)

	stored := getInviteRewardFromDB(t, reward.Id)
	assert.Equal(t, InviteRewardStatePending, stored.State, "an ineligible reward stays pending for a later pass")
	assert.Equal(t, 100000, getUserQuotaFromDB(t, invitee.Id))
}

func TestSettleInviteRewardPaysBothSharesOnce(t *testing.T) {
	setupInviteRewardTestState(t, 5000, 8000)

	inviter := createReserveTestUser(t, 100000)
	invitee := createReserveTestUser(t, 100000)
	reward := registerInviteRewardFixture(t, inviter.Id, invitee.Id, inviteRewardWindowSeconds+60)
	setQualifyingInviteCalls(t, invitee.Id, InviteRewardRequiredCalls)

	granted, cancelled, err := SettleInviteReward(common.GetTimestamp())
	require.NoError(t, err)
	assert.Equal(t, 1, granted)
	assert.Zero(t, cancelled)

	assert.Equal(t, 105000, getUserQuotaFromDB(t, invitee.Id))
	stored := getInviteRewardFromDB(t, reward.Id)
	assert.Equal(t, InviteRewardStateGranted, stored.State)
	assert.NotZero(t, stored.GrantedAt)
	assert.Empty(t, stored.CancelledNote)

	var storedInviter User
	require.NoError(t, DB.First(&storedInviter, inviter.Id).Error)
	assert.Equal(t, 1, storedInviter.AffCount)
	assert.Equal(t, 8000, storedInviter.AffQuota)
	assert.Equal(t, 8000, storedInviter.AffHistoryQuota)

	// A second pass must not pay the same reward again.
	granted, cancelled, err = SettleInviteReward(common.GetTimestamp())
	require.NoError(t, err)
	assert.Zero(t, granted)
	assert.Zero(t, cancelled)
	assert.Equal(t, 105000, getUserQuotaFromDB(t, invitee.Id))
}

func TestSettleInviteRewardPaysInviteeWhenInviterIsGone(t *testing.T) {
	setupInviteRewardTestState(t, 5000, 8000)

	inviter := createReserveTestUser(t, 100000)
	invitee := createReserveTestUser(t, 100000)
	reward := registerInviteRewardFixture(t, inviter.Id, invitee.Id, inviteRewardWindowSeconds+60)
	setQualifyingInviteCalls(t, invitee.Id, InviteRewardRequiredCalls)

	require.NoError(t, DB.Exec("DELETE FROM users WHERE id = ?", inviter.Id).Error)

	granted, cancelled, err := SettleInviteReward(common.GetTimestamp())
	require.NoError(t, err)
	assert.Equal(t, 1, granted)
	assert.Zero(t, cancelled)

	assert.Equal(t, 105000, getUserQuotaFromDB(t, invitee.Id))
	stored := getInviteRewardFromDB(t, reward.Id)
	assert.Equal(t, InviteRewardStateGranted, stored.State)
	assert.Equal(t, "inviter deleted; inviter share not paid", stored.CancelledNote)
}

func TestSettleInviteRewardCancelsWhenInviteeIsGone(t *testing.T) {
	setupInviteRewardTestState(t, 5000, 8000)

	inviter := createReserveTestUser(t, 100000)
	invitee := createReserveTestUser(t, 100000)
	reward := registerInviteRewardFixture(t, inviter.Id, invitee.Id, inviteRewardWindowSeconds+60)
	setQualifyingInviteCalls(t, invitee.Id, InviteRewardRequiredCalls)

	require.NoError(t, DB.Exec("DELETE FROM users WHERE id = ?", invitee.Id).Error)

	granted, cancelled, err := SettleInviteReward(common.GetTimestamp())
	require.NoError(t, err)
	assert.Zero(t, granted)
	assert.Equal(t, 1, cancelled)

	stored := getInviteRewardFromDB(t, reward.Id)
	assert.Equal(t, InviteRewardStateCancelled, stored.State)
	assert.Equal(t, "invitee deleted", stored.CancelledNote)
}

// TestSuccessCallsAfterInviteWindowOnlyCountsCallsPastTheWindow is the regression test
// for the farming case the deferred reward exists to stop: a burst of calls made in the
// first day must not qualify an invitee.
func TestSuccessCallsAfterInviteWindowOnlyCountsCallsPastTheWindow(t *testing.T) {
	truncateTables(t)
	require.NoError(t, ensureXingyaTables(DB))
	require.NoError(t, DB.Exec("DELETE FROM xingya_invite_reward_pending").Error)

	fresh := createReserveTestUser(t, 100000)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", fresh.Id).
		Update("created_at", common.GetTimestamp()).Error)
	settled := createReserveTestUser(t, 100000)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", settled.Id).
		Update("created_at", common.GetTimestamp()-inviteRewardWindowSeconds-60).Error)

	for range InviteRewardRequiredCalls {
		UpdateUserUsedQuotaAndRequestCount(fresh.Id, 100)
		UpdateUserUsedQuotaAndRequestCount(settled.Id, 100)
	}

	var freshStored, settledStored User
	require.NoError(t, DB.First(&freshStored, fresh.Id).Error)
	require.NoError(t, DB.First(&settledStored, settled.Id).Error)
	assert.Zero(t, freshStored.SuccessCallsAfterInviteWindow,
		"calls made inside the window must not qualify an invitee")
	assert.Equal(t, InviteRewardRequiredCalls, settledStored.SuccessCallsAfterInviteWindow)
	// The ordinary counters keep counting regardless of the window.
	assert.Equal(t, InviteRewardRequiredCalls, freshStored.RequestCount)
	assert.Equal(t, InviteRewardRequiredCalls, settledStored.RequestCount)
}
