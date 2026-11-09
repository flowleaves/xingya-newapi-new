package controller

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGetInviteRebatesReturnsMaskedInviteeAndSummary(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "invite-rebate-api.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	oldDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = oldDB })
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.XingyaInviteRebate{}))

	now := common.GetTimestamp()
	inviter := model.User{Username: "api-inviter", AffCode: "apiinv", Quota: 0, Status: common.UserStatusEnabled}
	invitee := model.User{Username: "invitee-name", AffCode: "apiinv2", InviterId: inviter.Id, CreatedAt: now - 3600, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&inviter).Error)
	require.NoError(t, db.Create(&invitee).Error)
	require.NoError(t, db.Create(&model.XingyaInviteRebate{
		InviterId:     inviter.Id,
		InviteeId:     invitee.Id,
		SourceType:    model.InviteRebateSourceTopUp,
		SourceId:      1,
		BaseQuota:     10_000_000,
		RebateQuota:   500_000,
		RatioBp:       500,
		WindowStartAt: invitee.CreatedAt,
		WindowEndAt:   invitee.CreatedAt + 15*24*60*60,
		OccurredAt:    now,
		Status:        model.InviteRebateStatusGranted,
		CreatedAt:     now,
	}).Error)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/user/invite_rebates?p=1&page_size=20", nil)
	c.Set("id", inviter.Id)
	GetInviteRebates(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var body struct {
		Success bool `json:"success"`
		Data    struct {
			Summary model.InviteRebateSummary `json:"summary"`
			Items   []model.InviteRebateItem  `json:"items"`
			Total   int64                     `json:"total"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &body))
	assert.True(t, body.Success)
	assert.Equal(t, int64(1), body.Data.Total)
	assert.Equal(t, int64(500_000), body.Data.Summary.TotalRebateQuota)
	require.Len(t, body.Data.Items, 1)
	assert.Equal(t, "i***e", body.Data.Items[0].InviteeUsername)
}
