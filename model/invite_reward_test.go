package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupInviteRewardTestState(t *testing.T, inviterQuota int) {
	t.Helper()
	truncateTables(t)
	require.NoError(t, ensureXingyaTables(DB))
	require.NoError(t, DB.Exec("DELETE FROM xingya_invite_reward_pending").Error)
	oldInviteeQuota := common.QuotaForInvitee
	oldInviterQuota := common.QuotaForInviter
	common.QuotaForInvitee = 9999
	common.QuotaForInviter = inviterQuota
	t.Cleanup(func() {
		common.QuotaForInvitee = oldInviteeQuota
		common.QuotaForInviter = oldInviterQuota
		DB.Exec("DELETE FROM xingya_invite_reward_pending")
	})
}

func registerInviteRewardFixture(t *testing.T, inviterId int, inviteeId int) *XingyaInviteRewardPending {
	t.Helper()
	require.NoError(t, CreateInviteRewardPending(inviteeId, inviterId))
	var reward XingyaInviteRewardPending
	require.NoError(t, DB.Where("invitee_id = ?", inviteeId).First(&reward).Error)
	return &reward
}

func getInviteRewardFromDB(t *testing.T, id int) XingyaInviteRewardPending {
	t.Helper()
	var reward XingyaInviteRewardPending
	require.NoError(t, DB.Where("id = ?", id).First(&reward).Error)
	return reward
}

// recordSuccessfulCall records one successful billable call carrying the given spend.
func recordSuccessfulCall(t *testing.T, userId int, quota int) {
	t.Helper()
	RecordSuccessfulBillableCall(userId, quota)
}

// qualifyInviteReward satisfies the call count only, leaving the spend gate to the
// caller so a test can isolate which condition it is exercising.
func qualifyInviteReward(t *testing.T, userId int) {
	t.Helper()
	for range InviteRewardRequiredCalls {
		recordSuccessfulCall(t, userId, 0)
	}
}

// satisfyInviteReward completes both published conditions the way production does: the
// spend accrues across the calls rather than being written afterwards. A single call
// carries a value that is a multiple of the gate, so a 1:1 mapping is exact and reading
// the gate once keeps the fixture consistent even if the gate changes mid-test.
func satisfyInviteReward(t *testing.T, userId int) {
	t.Helper()
	gate := RequiredConsumeQuota()
	require.Positive(t, gate, "fixture expects a configured spend gate")
	for range InviteRewardRequiredCalls {
		recordSuccessfulCall(t, userId, gate)
	}
}

func TestCreateInviteRewardPendingOnlyStoresInviterReward(t *testing.T) {
	setupInviteRewardTestState(t, 8000)
	inviter := createReserveTestUser(t, 100000)
	invitee := createReserveTestUser(t, 100000)
	reward := registerInviteRewardFixture(t, inviter.Id, invitee.Id)

	assert.Equal(t, InviteRewardStatePending, reward.State)
	assert.Zero(t, reward.InviteeQuota)
	assert.Equal(t, 8000, reward.InviterQuota)
	assert.Zero(t, reward.QualifyingCalls)
	assert.Equal(t, 100000, getUserQuotaFromDB(t, inviter.Id))
	assert.Equal(t, 100000, getUserQuotaFromDB(t, invitee.Id))
}

func TestSuccessfulBillableCallsPromoteAtTen(t *testing.T) {
	setupInviteRewardTestState(t, 8000)
	inviter := createReserveTestUser(t, 100000)
	invitee := createReserveTestUser(t, 100000)
	reward := registerInviteRewardFixture(t, inviter.Id, invitee.Id)

	gate := RequiredConsumeQuota()
	require.Positive(t, gate, "fixture expects a configured spend gate")
	perCall := gate / InviteRewardRequiredCalls
	for range InviteRewardRequiredCalls - 1 {
		recordSuccessfulCall(t, invitee.Id, perCall)
	}
	stored := getInviteRewardFromDB(t, reward.Id)
	assert.Equal(t, InviteRewardStatePending, stored.State)
	assert.Equal(t, InviteRewardRequiredCalls-1, stored.QualifyingCalls)

	// The final call completes the count and the spend at the same time.
	recordSuccessfulCall(t, invitee.Id, gate-perCall*(InviteRewardRequiredCalls-1))
	stored = getInviteRewardFromDB(t, reward.Id)
	assert.Equal(t, InviteRewardStateEligible, stored.State)
	assert.Equal(t, InviteRewardRequiredCalls, stored.QualifyingCalls)
	assert.Equal(t, gate, stored.QualifyingQuota)
	assert.NotZero(t, stored.EligibleAt)
	assert.Greater(t, stored.AutoGrantAt, stored.EligibleAt)
}

// TestSpendGateBlocksPromotionUntilTheThresholdIsReached covers the second published
// condition: completing the call count alone must not make a reward claimable.
func TestSpendGateBlocksPromotionUntilTheThresholdIsReached(t *testing.T) {
	setupInviteRewardTestState(t, 8000)
	inviter := createReserveTestUser(t, 100000)
	invitee := createReserveTestUser(t, 100000)
	reward := registerInviteRewardFixture(t, inviter.Id, invitee.Id)
	gate := RequiredConsumeQuota()
	require.Positive(t, gate, "fixture expects a configured spend gate")

	// Enough calls, but each carries too little spend to accumulate the gate.
	require.NoError(t, DB.Model(&XingyaInviteRewardPending{}).Where("id = ?", reward.Id).
		Update("qualifying_quota", gate-1).Error)
	qualifyInviteReward(t, invitee.Id)

	stored := getInviteRewardFromDB(t, reward.Id)
	assert.Equal(t, InviteRewardStatePending, stored.State,
		"the call count alone must not promote a reward")
	assert.Equal(t, InviteRewardRequiredCalls, stored.QualifyingCalls)
	assert.Equal(t, gate-1, stored.QualifyingQuota)

	// The final unit of spend promotes it.
	recordSuccessfulCall(t, invitee.Id, 1)
	stored = getInviteRewardFromDB(t, reward.Id)
	assert.Equal(t, InviteRewardStateEligible, stored.State)
	assert.Equal(t, gate, stored.QualifyingQuota)
	assert.NotZero(t, stored.EligibleAt)
}

// TestSettleRefusesWhenRecordedSpendFallsShortOfTheGate guards a row that was promoted
// before the spend gate existed, or under a lower gate an administrator has since raised.
func TestSettleRefusesWhenRecordedSpendFallsShortOfTheGate(t *testing.T) {
	setupInviteRewardTestState(t, 8000)
	inviter := createReserveTestUser(t, 100000)
	invitee := createReserveTestUser(t, 100000)
	reward := &XingyaInviteRewardPending{
		InviteeId: invitee.Id, InviterId: inviter.Id, InviterQuota: 8000,
		QualifyingCalls: InviteRewardRequiredCalls, QualifyingQuota: 0,
		EligibleAt:  common.GetTimestamp() - 60,
		AutoGrantAt: common.GetTimestamp() - 1, State: InviteRewardStateEligible,
		CreatedAt: common.GetTimestamp() - 3600,
	}
	require.NoError(t, DB.Create(reward).Error)

	granted, cancelled, err := SettleInviteReward(common.GetTimestamp())
	require.NoError(t, err)
	assert.Zero(t, granted)
	assert.Zero(t, cancelled)
	assert.Equal(t, InviteRewardStateEligible, getInviteRewardFromDB(t, reward.Id).State,
		"an under-spent reward stays eligible for a later pass")
	assert.Equal(t, 100000, getUserQuotaFromDB(t, inviter.Id))
}

func TestManualClaimCreditsInviterWalletAndIsIdempotent(t *testing.T) {
	setupInviteRewardTestState(t, 8000)
	inviter := createReserveTestUser(t, 100000)
	invitee := createReserveTestUser(t, 100000)
	reward := registerInviteRewardFixture(t, inviter.Id, invitee.Id)
	satisfyInviteReward(t, invitee.Id)

	granted, alreadyGranted, err := ClaimInviteReward(inviter.Id, reward.Id)
	require.NoError(t, err)
	assert.True(t, granted)
	assert.False(t, alreadyGranted)
	assert.Equal(t, 108000, getUserQuotaFromDB(t, inviter.Id))
	assert.Equal(t, 100000, getUserQuotaFromDB(t, invitee.Id), "new rewards do not credit invitees")

	var storedInviter User
	require.NoError(t, DB.First(&storedInviter, inviter.Id).Error)
	assert.Equal(t, 1, storedInviter.AffCount)
	assert.Zero(t, storedInviter.AffQuota)
	assert.Equal(t, 8000, storedInviter.AffHistoryQuota)
	assert.Equal(t, InviteRewardGrantManual, getInviteRewardFromDB(t, reward.Id).GrantMethod)

	granted, alreadyGranted, err = ClaimInviteReward(inviter.Id, reward.Id)
	require.NoError(t, err)
	assert.True(t, granted)
	assert.True(t, alreadyGranted)
	assert.Equal(t, 108000, getUserQuotaFromDB(t, inviter.Id))
}

func TestAutomaticGrantRunsAtStoredMidnight(t *testing.T) {
	setupInviteRewardTestState(t, 8000)
	inviter := createReserveTestUser(t, 100000)
	invitee := createReserveTestUser(t, 100000)
	reward := registerInviteRewardFixture(t, inviter.Id, invitee.Id)
	satisfyInviteReward(t, invitee.Id)
	require.NoError(t, DB.Model(&XingyaInviteRewardPending{}).Where("id = ?", reward.Id).
		Updates(map[string]any{"auto_grant_at": common.GetTimestamp() - 1}).Error)

	granted, cancelled, err := SettleInviteReward(common.GetTimestamp())
	require.NoError(t, err)
	assert.Equal(t, 1, granted)
	assert.Zero(t, cancelled)
	assert.Equal(t, InviteRewardGrantAuto, getInviteRewardFromDB(t, reward.Id).GrantMethod)
	assert.Equal(t, 108000, getUserQuotaFromDB(t, inviter.Id))
}

func TestNewRewardCancelsWhenInviterIsDeleted(t *testing.T) {
	setupInviteRewardTestState(t, 8000)
	inviter := createReserveTestUser(t, 100000)
	invitee := createReserveTestUser(t, 100000)
	reward := registerInviteRewardFixture(t, inviter.Id, invitee.Id)
	satisfyInviteReward(t, invitee.Id)
	_, err := inviter.Delete()
	require.NoError(t, err)

	// New-format rewards are cancelled by the same transaction that deletes the
	// inviter, so the midnight worker has no financial work left to discover.
	assert.Equal(t, InviteRewardStateCancelled, getInviteRewardFromDB(t, reward.Id).State)
}

func TestHistoricalRewardAlsoCancelsWhenInviterIsDeleted(t *testing.T) {
	setupInviteRewardTestState(t, 8000)
	inviter := createReserveTestUser(t, 100000)
	invitee := createReserveTestUser(t, 100000)
	reward := &XingyaInviteRewardPending{
		InviteeId: invitee.Id, InviterId: inviter.Id, InviteeQuota: 5000, InviterQuota: 8000,
		QualifyingCalls: InviteRewardRequiredCalls, QualifyingQuota: RequiredConsumeQuota(),
		EligibleAt:  common.GetTimestamp() - 60,
		AutoGrantAt: common.GetTimestamp() + 3600, State: InviteRewardStateEligible, CreatedAt: common.GetTimestamp() - 3600,
	}
	require.NoError(t, DB.Create(reward).Error)

	_, err := inviter.Delete()
	require.NoError(t, err)
	assert.Equal(t, InviteRewardStateCancelled, getInviteRewardFromDB(t, reward.Id).State)
}

func TestHistoricalRewardKeepsInviteeCompatibilityPath(t *testing.T) {
	setupInviteRewardTestState(t, 8000)
	inviter := createReserveTestUser(t, 100000)
	invitee := createReserveTestUser(t, 100000)
	reward := &XingyaInviteRewardPending{
		InviteeId: invitee.Id, InviterId: inviter.Id, InviteeQuota: 5000, InviterQuota: 8000,
		QualifyingCalls: InviteRewardRequiredCalls, QualifyingQuota: RequiredConsumeQuota(),
		EligibleAt:  common.GetTimestamp() - 60,
		AutoGrantAt: common.GetTimestamp() - 1, State: InviteRewardStateEligible, CreatedAt: common.GetTimestamp() - 3600,
	}
	require.NoError(t, DB.Create(reward).Error)

	granted, cancelled, err := SettleInviteReward(common.GetTimestamp())
	require.NoError(t, err)
	assert.Equal(t, 1, granted)
	assert.Zero(t, cancelled)
	assert.Equal(t, 105000, getUserQuotaFromDB(t, invitee.Id))
	assert.Equal(t, 100000, getUserQuotaFromDB(t, inviter.Id))
	var storedInviter User
	require.NoError(t, DB.First(&storedInviter, inviter.Id).Error)
	assert.Equal(t, 8000, storedInviter.AffQuota)
	assert.Equal(t, 8000, storedInviter.AffHistoryQuota)
}

func TestUsageCounterDoesNotWriteLegacyInviteField(t *testing.T) {
	setupInviteRewardTestState(t, 8000)
	user := createReserveTestUser(t, 100000)
	UpdateUserUsedQuotaAndRequestCount(user.Id, 100)
	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	assert.Equal(t, 1, stored.RequestCount)
	assert.Zero(t, stored.SuccessCallsAfterInviteWindow)
}
