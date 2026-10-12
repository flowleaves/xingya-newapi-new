package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestCheckinBrowserSessionPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_XINGYA_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL DSN required")
	}
	oldDB, oldLog, oldRedis, oldSecret, oldSecure := model.DB, model.LOG_DB, common.RedisEnabled, common.SessionSecret, common.SessionCookieSecure
	oldMainType, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	t.Cleanup(func() {
		model.DB, model.LOG_DB, common.RedisEnabled, common.SessionSecret, common.SessionCookieSecure = oldDB, oldLog, oldRedis, oldSecret, oldSecure
		common.SetDatabaseTypes(oldMainType, oldLogType)
	})
	common.RedisEnabled, common.SessionCookieSecure = false, true
	common.SessionSecret = "isolated-checkin-auth-fixture"
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	model.DB, model.LOG_DB = db, db
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.AuditLog{}, &model.UserAccessToken{}, &model.Option{}))
	require.NoError(t, db.Save(&model.Option{Key: "LegacyAccessTokenRetireAt", Value: fmt.Sprint(time.Now().Add(24 * time.Hour).Unix())}).Error)
	require.NoError(t, model.EnsureLegacyAccessTokenRetireAt(time.Now().Unix()))
	legacy, err := common.GenerateRandomCharsKey(32)
	require.NoError(t, err)
	user := model.User{Username: fmt.Sprintf("cs-%d", time.Now().UnixNano()), AffCode: fmt.Sprintf("cs-%d", time.Now().UnixNano()), Password: "fixture", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1, AccessToken: &legacy, Group: "default"}
	require.NoError(t, db.Create(&user).Error)
	now := time.Now().Unix()
	session := model.UserSession{SID: common.NewRequestId(), UserID: user.Id, Version: 1, UserAuthVersion: user.AuthVersion, Status: model.UserSessionStatusActive, RefreshHash: "fixture", LoginMethod: "password", LastActiveAt: now, ExpiresAt: now + 3600}
	require.NoError(t, model.CreateUserSession(&session))
	identity := service.AuthIdentity{UserID: user.Id, SessionID: session.SID, UserAuthVersion: 1, SessionVersion: 1}
	jwt, _, err := service.IssueAccessToken(identity)
	require.NoError(t, err)
	scoped, _ := createMiddlewareScopedToken(t, user.Id, 0, "wallet:write")
	router := gin.New()
	for _, path := range []string{"/api/user/xingya-daily-claim", "/api/user/xingya-checkin/key"} {
		router.POST(path, UserAuth(), CheckinBrowserSession(), SessionCookieOriginGuard(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	}
	request := func(path, credential, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "https://xingya.site"+path, nil)
		if credential != "" {
			req.Header.Set("Authorization", "Bearer "+credential)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	for _, path := range []string{"/api/user/xingya-daily-claim", "/api/user/xingya-checkin/key"} {
		assert.Equal(t, http.StatusNoContent, request(path, jwt, "https://xingya.site").Code)
		for _, credential := range []string{"", legacy, scoped, "sk-fixture", issueExpiredDashboardAccessToken(t, identity), tamperDashboardToken(jwt)} {
			assert.NotEqual(t, http.StatusNoContent, request(path, credential, "https://xingya.site").Code)
		}
		assert.Equal(t, http.StatusForbidden, request(path, jwt, "https://untrusted.invalid").Code)
		assert.Equal(t, http.StatusForbidden, request(path, jwt, "").Code)
	}
	for _, mutation := range []struct {
		name         string
		target       any
		field        string
		value, reset any
	}{
		{"revoked session", &session, "status", model.UserSessionStatusRevoked, model.UserSessionStatusActive},
		{"expired session", &session, "expires_at", now - 1, now + 3600},
		{"rotated session", &session, "version", int64(2), int64(1)},
		{"invalidated user credentials", &user, "auth_version", int64(2), int64(1)},
		{"disabled user", &user, "status", common.UserStatusDisabled, common.UserStatusEnabled},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			require.NoError(t, db.Model(mutation.target).Update(mutation.field, mutation.value).Error)
			assert.Equal(t, http.StatusUnauthorized, request("/api/user/xingya-daily-claim", jwt, "https://xingya.site").Code)
			require.NoError(t, db.Model(mutation.target).Update(mutation.field, mutation.reset).Error)
		})
	}
}
