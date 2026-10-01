package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupInviteRewardControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() {
		model.DB = previousDB
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.AutoMigrate(&model.XingyaInviteRewardPending{}))
	return db
}

// TestAdminListInviteRewardsProjectsSettlementState pins the shape support staff need:
// which side was paid and, for a closed reward, why it was not.
func TestAdminListInviteRewardsProjectsSettlementState(t *testing.T) {
	db := setupInviteRewardControllerTestDB(t)
	require.NoError(t, db.Create(&[]model.XingyaInviteRewardPending{
		{InviteeId: 1, InviterId: 10, InviteeQuota: 5000, InviterQuota: 8000, State: model.InviteRewardStatePending, CreatedAt: 100},
		{InviteeId: 2, InviterId: 11, InviteeQuota: 5000, InviterQuota: 8000, State: model.InviteRewardStateGranted, CreatedAt: 200, GrantedAt: 300, SettledAt: 300},
		{InviteeId: 3, InviterId: 12, InviteeQuota: 5000, InviterQuota: 0, State: model.InviteRewardStateCancelled, CreatedAt: 400, SettledAt: 500, CancelledNote: "invitee deleted"},
	}).Error)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/user/invite_rewards/?state=granted", nil)
	AdminListInviteRewards(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Total int                               `json:"total"`
			Items []model.XingyaInviteRewardPending `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.True(t, response.Success)
	assert.Equal(t, 1, response.Data.Total)
	require.Len(t, response.Data.Items, 1)
	assert.Equal(t, model.InviteRewardStateGranted, response.Data.Items[0].State)
	assert.Equal(t, 300, int(response.Data.Items[0].GrantedAt))
}

func TestAdminListInviteRewardsRejectsUnknownState(t *testing.T) {
	setupInviteRewardControllerTestDB(t)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/user/invite_rewards/?state=refunded", nil)
	AdminListInviteRewards(c)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":false`)
}

// TestAdminUserListProjectionHidesInviteQualifyingCalls is a regression test for the
// column the deferred reward counts into.
//
// The admin list loads users with a SELECT * plus a denylist of omitted columns, so a
// new user column is exposed to every administrator by default. The internal counter
// must stay invisible: it is bookkeeping for the invite reward, not part of a user's
// public or administrative profile, and it should not become another field the
// frontend has to keep in sync.
func TestAdminUserListProjectionHidesInviteQualifyingCalls(t *testing.T) {
	db := setupInviteRewardControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}))
	owner := model.User{
		Username:    "projection-owner",
		Password:    "expected-to-be-omitted",
		AffCode:     "projection-owner-aff",
		Group:       "default",
		Status:      common.UserStatusEnabled,
		Role:        common.RoleCommonUser,
		AuthVersion: 1,
	}
	require.NoError(t, db.Create(&owner).Error)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", owner.Id).
		Update("success_calls_after_invite_window", 7).Error)

	// The counter is readable where settlement needs it.
	var settlementView model.User
	require.NoError(t, db.First(&settlementView, owner.Id).Error)
	assert.Equal(t, 7, settlementView.SuccessCallsAfterInviteWindow)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/user/?p=1&page_size=10", nil)
	GetAllUsers(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.String()
	assert.NotContains(t, body, "success_calls_after_invite_window")
	assert.NotContains(t, body, "expected-to-be-omitted", "the admin list must not carry the credential hash")
}
