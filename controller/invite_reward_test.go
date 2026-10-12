package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

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

func TestRegistrationAuditEndpointsRequireAdminAndReturnBoundedProjection(t *testing.T) {
	db := setupInviteRewardControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}))
	require.NoError(t, model.InitXingyaTables(db))
	oldRedis, oldSecret := common.RedisEnabled, common.SessionSecret
	common.RedisEnabled, common.SessionSecret = false, "registration-audit-test-secret"
	t.Cleanup(func() { common.RedisEnabled, common.SessionSecret = oldRedis, oldSecret })
	admin := &model.User{Username: "registration-audit-admin", Password: "private-password-hash", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, AuthVersion: 1, AffCode: "reg-admin"}
	user := &model.User{Username: "registered-user", Password: "private-password-hash", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1, AffCode: "reg-user"}
	require.NoError(t, db.Create(admin).Error)
	require.NoError(t, db.Create(user).Error)
	require.NoError(t, model.RecordRegistrationDevice(nil, user.Id, "203.0.113.42", "Test browser"))
	require.NoError(t, model.RecordRegistrationDevice(nil, admin.Id, "203.0.113.42", "Test browser"))
	adminSession, err := service.CreateLoginSession(admin.Id, "password", "127.0.0.1", "test")
	require.NoError(t, err)
	userSession, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "test")
	require.NoError(t, err)
	router := gin.New()
	router.GET("/api/user/registration_devices", middleware.AdminAuth(), AdminListRegistrationDevices)
	router.GET("/api/user/registration_devices/risk", middleware.AdminAuth(), AdminListRegistrationRisk)
	for _, path := range []string{"/api/user/registration_devices?p=1&page_size=1", "/api/user/registration_devices/risk?p=1&page_size=1"} {
		for _, test := range []struct {
			name, token string
			status      int
		}{
			{"anonymous", "", http.StatusUnauthorized},
			{"ordinary user", userSession.AccessToken, http.StatusForbidden},
			{"administrator", adminSession.AccessToken, http.StatusOK},
		} {
			t.Run(path+"/"+test.name, func(t *testing.T) {
				request := httptest.NewRequest(http.MethodGet, path, nil)
				if test.token != "" {
					request.Header.Set("Authorization", "Bearer "+test.token)
				}
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				assert.Equal(t, test.status, response.Code, response.Body.String())
				assert.NotContains(t, response.Body.String(), "private-password-hash")
				if test.status != http.StatusOK {
					assert.NotContains(t, response.Body.String(), "203.0.113.42")
					return
				}
				assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
				assert.Contains(t, response.Body.String(), `"client_ip":"203.0.113.42"`)
				var payload struct {
					Success bool `json:"success"`
					Data    struct {
						Items []any `json:"items"`
					} `json:"data"`
				}
				require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
				assert.True(t, payload.Success)
				assert.Len(t, payload.Data.Items, 1)
			})
		}
	}
	for _, query := range []string{"p=-1", "page_size=-1", "p=9223372036854775807&page_size=100", "from=-1", "from=bad", "from=200&to=100"} {
		for _, handler := range []gin.HandlerFunc{AdminListRegistrationDevices, AdminListRegistrationRisk} {
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			c.Request = httptest.NewRequest(http.MethodGet, "/?"+query, nil)
			handler(c)
			assert.Contains(t, response.Body.String(), `"success":false`, query)
		}
	}
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

	// The legacy column remains readable for old data, while current settlement uses
	// the separate reward ledger.
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
