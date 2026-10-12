package model

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"math"
	"os"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestXingyaCheckinPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_XINGYA_POSTGRES_DSN")
	logDSN := os.Getenv("TEST_XINGYA_POSTGRES_LOG_DSN")
	if dsn == "" || logDSN == "" {
		t.Skip("isolated PostgreSQL main/log DSNs are required")
	}
	oldDB, oldLog := DB, LOG_DB
	oldMainType, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	oldReady, oldRedis := xingyaTablesReady.Load(), common.RedisEnabled
	oldQuota := common.QuotaPerUnit
	t.Cleanup(func() {
		DB, LOG_DB = oldDB, oldLog
		common.SetDatabaseTypes(oldMainType, oldLogType)
		initCol()
		xingyaTablesReady.Store(oldReady)
		common.RedisEnabled = oldRedis
		common.QuotaPerUnit = oldQuota
	})
	common.RedisEnabled = false
	common.QuotaPerUnit = 500000
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	initCol()
	var err error
	DB, err = gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	require.NoError(t, err)
	LOG_DB, err = gorm.Open(postgres.New(postgres.Config{DSN: logDSN, PreferSimpleProtocol: true}), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	require.NoError(t, err)
	for _, db := range []*gorm.DB{DB, LOG_DB} {
		pool, err := db.DB()
		require.NoError(t, err)
		pool.SetMaxOpenConns(12)
		t.Cleanup(func() { require.NoError(t, pool.Close()) })
	}
	require.NoError(t, DB.AutoMigrate(&Option{}, &User{}, &Token{}, &Checkin{}))
	require.NoError(t, LOG_DB.AutoMigrate(&Log{}))
	require.NoError(t, InitXingyaTables(DB))
	require.NoError(t, InitXingyaTables(DB))
	var version string
	require.NoError(t, DB.Raw("SELECT version()").Scan(&version).Error)
	t.Log(version)
	sequence := 0
	newUser := func() User {
		sequence++
		user := User{Username: fmt.Sprintf("checkin-%d-%d", time.Now().UnixNano(), sequence), Password: "fixture", AffCode: fmt.Sprintf("cc-%d", time.Now().UnixNano()), Status: common.UserStatusEnabled, Quota: 123456}
		require.NoError(t, DB.Create(&user).Error)
		return user
	}
	setConfig := func(minimum, maximum float64) XingyaCheckinSettings {
		current, err := GetXingyaCheckinSettings()
		require.NoError(t, err)
		current.MinReward = minimum
		current.MaxReward = maximum
		current.Group = "checkin-fixture"
		current.SpendEnabled = true
		current.ClaimEnabled = true
		current, err = SaveXingyaCheckinSettings(current)
		require.NoError(t, err)
		return current
	}
	// Reset only this feature's state in the explicitly configured disposable DB.
	for _, table := range []any{&XingyaCheckinSpend{}, &XingyaCheckinGrant{}, &XingyaCheckinWallet{}} {
		require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(table).Error)
	}
	t.Run("configuration is atomic and validates bounds", func(t *testing.T) {
		s := setConfig(1.25, 2.5)
		for _, tc := range []struct{ min, max float64 }{{-1, 2}, {3, 2}, {1, math.NaN()}, {1, math.Inf(1)}, {1, 100001}, {1.001, 2}} {
			invalid := s
			invalid.MinReward = tc.min
			invalid.MaxReward = tc.max
			_, err := SaveXingyaCheckinSettings(invalid)
			require.Error(t, err)
			actual, err := GetXingyaCheckinSettings()
			require.NoError(t, err)
			assert.Equal(t, s, actual)
		}
		next, err := SaveXingyaCheckinSettings(s)
		require.NoError(t, err)
		assert.Equal(t, s.Revision+1, next.Revision)
		_, err = SaveXingyaCheckinSettings(s)
		require.ErrorIs(t, err, ErrCheckinConfigConflict)
	})
	t.Run("concurrent claim credits once without changing ordinary wallet", func(t *testing.T) {
		setConfig(1.25, 1.25)
		user := newUser()
		now := time.Date(2026, 10, 12, 0, 0, 0, 0, checkinLocation)
		var wg sync.WaitGroup
		errors := make(chan error, 20)
		awards := make(chan *XingyaCheckinGrant, 20)
		for range 20 {
			wg.Go(func() { grant, _, err := ClaimXingyaCheckin(user.Id, now); errors <- err; awards <- grant })
		}
		wg.Wait()
		close(errors)
		close(awards)
		for err := range errors {
			require.NoError(t, err)
		}
		var id int
		for grant := range awards {
			assert.Equal(t, 6250, grant.QuotaAwarded)
			if id == 0 {
				id = grant.ID
			}
			assert.Equal(t, id, grant.ID)
		}
		wallet, err := GetXingyaCheckinWallet(user.Id)
		require.NoError(t, err)
		assert.Equal(t, 6250, wallet.Available)
		assert.Equal(t, 6250, wallet.Granted)
		var reloaded User
		require.NoError(t, DB.First(&reloaded, user.Id).Error)
		assert.Equal(t, user.Quota, reloaded.Quota)
		grant, already, err := ClaimXingyaCheckin(user.Id, now.Add(-time.Second))
		require.NoError(t, err)
		assert.False(t, already)
		assert.Equal(t, "2026-10-11", grant.CheckinDate)
		_, err = SaveXingyaCheckinSettings(XingyaCheckinSettings{Group: "other", MinReward: 1, MaxReward: 1, Revision: mustCheckinConfig(t).Revision})
		require.Error(t, err)
	})
	t.Run("legacy award on launch date is not paid again", func(t *testing.T) {
		user := newUser()
		now := time.Now()
		day := CheckinBusinessTime(now).Format("2006-01-02")
		require.NoError(t, DB.Create(&Checkin{UserId: user.Id, CheckinDate: day, QuotaAwarded: 999, CreatedAt: now.Unix()}).Error)
		grant, already, err := ClaimXingyaCheckin(user.Id, now)
		require.NoError(t, err)
		assert.True(t, already)
		assert.Zero(t, grant.ID)
		wallet, err := GetXingyaCheckinWallet(user.Id)
		require.NoError(t, err)
		assert.Zero(t, wallet.Granted)
	})
	t.Run("random interval includes both endpoints and retry preserves the draw", func(t *testing.T) {
		setConfig(1, 1.02)
		previous := rand.Reader
		t.Cleanup(func() { rand.Reader = previous })
		for _, draw := range []byte{0, 2} {
			rand.Reader = bytes.NewReader([]byte{draw})
			user := newUser()
			grant, already, err := ClaimXingyaCheckin(user.Id, time.Now())
			require.NoError(t, err)
			assert.False(t, already)
			assert.Equal(t, 5000+int(draw)*50, grant.QuotaAwarded)
			rand.Reader = bytes.NewReader(nil)
			retry, already, err := ClaimXingyaCheckin(user.Id, time.Now())
			require.NoError(t, err)
			assert.True(t, already)
			assert.Equal(t, grant.ID, retry.ID)
		}
	})
	t.Run("reserve settle and release reconcile and are idempotent", func(t *testing.T) {
		user := newUser()
		require.NoError(t, DB.Create(&XingyaCheckinWallet{UserID: user.Id, Available: 1000, Granted: 1000}).Error)
		spend := XingyaCheckinSpend{UserID: user.Id, TokenID: 42, RequestID: common.NewRequestId(), Group: "checkin-fixture", Model: "fixture"}
		require.NoError(t, ReserveCheckinQuota(spend, 700))
		require.NoError(t, ReserveCheckinQuota(spend, 700))
		wrong := spend
		wrong.TokenID++
		require.Error(t, ReserveCheckinQuota(wrong, 700))
		require.ErrorIs(t, ReserveCheckinQuota(spend, 1001), ErrCheckinInsufficient)
		require.NoError(t, FinalizeCheckinSpend(user.Id, spend.RequestID, 500, false))
		require.NoError(t, FinalizeCheckinSpend(user.Id, spend.RequestID, 500, false))
		wallet, err := GetXingyaCheckinWallet(user.Id)
		require.NoError(t, err)
		assert.Equal(t, 500, wallet.Available)
		assert.Zero(t, wallet.Reserved)
		assert.Equal(t, 500, wallet.Consumed)
		require.Error(t, ReserveCheckinQuota(spend, 1))
		spend.RequestID = common.NewRequestId()
		require.NoError(t, ReserveCheckinQuota(spend, 500))
		require.NoError(t, FinalizeCheckinSpend(user.Id, spend.RequestID, 0, true))
		require.NoError(t, FinalizeCheckinSpend(user.Id, spend.RequestID, 0, true))
		wallet, err = GetXingyaCheckinWallet(user.Id)
		require.NoError(t, err)
		assert.Equal(t, wallet.Granted, wallet.Available+wallet.Reserved+wallet.Consumed)
		spend.RequestID = common.NewRequestId()
		require.NoError(t, ReserveCheckinQuota(spend, 0))
		require.NoError(t, FinalizeCheckinSpend(user.Id, spend.RequestID, 0, false))
		var row XingyaCheckinSpend
		require.NoError(t, DB.First(&row, "request_id = ?", spend.RequestID).Error)
		assert.Equal(t, "settled", row.State)
	})
	t.Run("concurrent requests cannot overreserve", func(t *testing.T) {
		user := newUser()
		require.NoError(t, DB.Create(&XingyaCheckinWallet{UserID: user.Id, Available: 100, Granted: 100}).Error)
		results := make(chan error, 12)
		var wg sync.WaitGroup
		for range 12 {
			wg.Go(func() {
				results <- ReserveCheckinQuota(XingyaCheckinSpend{UserID: user.Id, RequestID: common.NewRequestId()}, 30)
			})
		}
		wg.Wait()
		close(results)
		success := 0
		for err := range results {
			if err == nil {
				success++
			} else {
				require.ErrorIs(t, err, ErrCheckinInsufficient)
			}
		}
		assert.Equal(t, 3, success)
		wallet, err := GetXingyaCheckinWallet(user.Id)
		require.NoError(t, err)
		assert.Equal(t, 10, wallet.Available)
		assert.Equal(t, 90, wallet.Reserved)
	})
	t.Run("unexpected cost freezes new requests without charging other funds", func(t *testing.T) {
		user := newUser()
		require.NoError(t, DB.Create(&XingyaCheckinWallet{UserID: user.Id, Available: 1000, Granted: 1000}).Error)
		spend := XingyaCheckinSpend{UserID: user.Id, RequestID: common.NewRequestId(), Model: fmt.Sprintf("review-model-%d", user.Id)}
		require.NoError(t, ReserveCheckinQuota(spend, 100))
		require.NoError(t, FinalizeCheckinSpend(user.Id, spend.RequestID, 200, false))
		wallet, err := GetXingyaCheckinWallet(user.Id)
		require.NoError(t, err)
		assert.Equal(t, 900, wallet.Available)
		assert.Equal(t, 100, wallet.Consumed)
		var row XingyaCheckinSpend
		require.NoError(t, DB.First(&row, "request_id = ?", spend.RequestID).Error)
		assert.Equal(t, "review", row.State)
		assert.Equal(t, 100, row.UnpaidQuota)
		spend.RequestID = common.NewRequestId()
		require.ErrorIs(t, ReserveCheckinQuota(spend, 1), ErrCheckinReconciliation)
		other := newUser()
		require.NoError(t, DB.Create(&XingyaCheckinWallet{UserID: other.Id, Available: 1000, Granted: 1000}).Error)
		otherSpend := XingyaCheckinSpend{UserID: other.Id, RequestID: common.NewRequestId(), Model: spend.Model}
		require.ErrorIs(t, ReserveCheckinQuota(otherSpend, 1), ErrCheckinReconciliation, "an unbounded model must also pause for other checkin users")
		unaffected := otherSpend
		unaffected.RequestID, unaffected.Model = common.NewRequestId(), "unaffected-model"
		require.NoError(t, ReserveCheckinQuota(unaffected, 1))
		require.NoError(t, FinalizeCheckinSpend(other.Id, unaffected.RequestID, 0, true))
		var ordinary User
		require.NoError(t, DB.First(&ordinary, user.Id).Error)
		assert.Equal(t, user.Quota, ordinary.Quota)
		require.NoError(t, ResolveCheckinSpend(row.RequestID, row.UpdatedAt, "waive", 0))
		require.Error(t, ResolveCheckinSpend(row.RequestID, row.UpdatedAt, "waive", 0))
		require.NoError(t, DB.First(&row, "request_id = ?", row.RequestID).Error)
		assert.Equal(t, "waived", row.State)
		assert.Equal(t, 100, row.UnpaidQuota, "measured unpaid cost remains auditable")
		require.NoError(t, ReserveCheckinQuota(spend, 1))
		require.NoError(t, ReserveCheckinQuota(otherSpend, 1))
	})
	t.Run("stale reservation recovery is bounded and cannot repeat", func(t *testing.T) {
		for _, action := range []string{"release", "settle"} {
			user := newUser()
			require.NoError(t, DB.Create(&XingyaCheckinWallet{UserID: user.Id, Available: 1000, Granted: 1000}).Error)
			spend := XingyaCheckinSpend{UserID: user.Id, RequestID: common.NewRequestId()}
			require.NoError(t, ReserveCheckinQuota(spend, 700))
			var saved XingyaCheckinSpend
			require.NoError(t, DB.First(&saved, "request_id = ?", spend.RequestID).Error)
			require.Error(t, ResolveCheckinSpend(spend.RequestID, saved.UpdatedAt, action, 300), "active requests must not be released")
			staleAt := common.GetTimestamp() - 25*60*60
			require.NoError(t, DB.Model(&saved).Update("updated_at", staleAt).Error)
			require.Error(t, ResolveCheckinSpend(spend.RequestID, staleAt+1, action, 300))
			require.Error(t, ResolveCheckinSpend(spend.RequestID, staleAt, "settle", 701))
			require.NoError(t, ResolveCheckinSpend(spend.RequestID, staleAt, action, 300))
			require.Error(t, ResolveCheckinSpend(spend.RequestID, staleAt, action, 300))
			wallet, err := GetXingyaCheckinWallet(user.Id)
			require.NoError(t, err)
			paid := 300
			if action == "release" {
				paid = 0
			}
			assert.Equal(t, 1000-paid, wallet.Available)
			assert.Equal(t, paid, wallet.Consumed)
			assert.Zero(t, wallet.Reserved)
			assert.Equal(t, wallet.Granted, wallet.Available+wallet.Consumed)
		}
	})
	t.Run("dedicated key creation reuses exact token and keeps limits", func(t *testing.T) {
		user := newUser()
		require.NoError(t, DB.Model(&user).Update("username", fmt.Sprintf("用户名用户名用户名用户名用户名%d", user.Id)).Error)
		key, reused, err := CreateCheckinToken(user.Id, "checkin-fixture")
		require.NoError(t, err)
		assert.False(t, reused)
		assert.Equal(t, CheckinQuotaSource, key.QuotaSource)
		assert.LessOrEqual(t, len(key.Name), 50)
		assert.True(t, utf8.ValidString(key.Name))
		require.NoError(t, DB.Model(key).Updates(map[string]any{"model_limits_enabled": true, "model_limits": "only-model"}).Error)
		again, reused, err := CreateCheckinToken(user.Id, "checkin-fixture")
		require.NoError(t, err)
		assert.True(t, reused)
		assert.Equal(t, key.Id, again.Id)
		assert.Equal(t, "only-model", again.ModelLimits)
		require.NoError(t, DB.Model(key).Update("status", common.TokenStatusDisabled).Error)
		replacement, _, err := CreateCheckinToken(user.Id, "checkin-fixture")
		require.NoError(t, err)
		assert.NotEqual(t, key.Id, replacement.Id)
	})
	t.Run("refund write path rejects checkin and unknown sources", func(t *testing.T) {
		user := newUser()
		for _, source := range []string{CheckinQuotaSource, "unknown"} {
			row := Log{UserId: user.Id, Type: LogTypeConsume, RequestId: common.NewRequestId(), Quota: 123, Other: fmt.Sprintf(`{"billing_source":%q}`, source)}
			require.NoError(t, LOG_DB.Create(&row).Error)
			require.Error(t, DoSelfRefundWallet(user.Id, row.Id, 123, 123, "empty_response", row.RequestId, RefundLimits{}))
			require.Error(t, DoSelfRefundSubscription(user.Id, row.Id, 1, 123, 123, "empty_response", row.RequestId, RefundLimits{}))
		}
	})
	t.Run("invite migration runs once and startup preserves credits", func(t *testing.T) {
		invitee, inviter := newUser(), newUser()
		reward := XingyaInviteRewardPending{InviteeId: invitee.Id, InviterId: inviter.Id, State: InviteRewardStatePending, QualifyingCalls: InviteRewardRequiredCalls, QualifyingQuota: 450000, InviterQuota: 100}
		require.NoError(t, DB.Create(&reward).Error)
		assert.False(t, RecordSuccessfulBillableCall(invitee.Id, 100000, CheckinQuotaSource))
		require.NoError(t, DB.First(&reward, reward.Id).Error)
		assert.Equal(t, 450000, reward.QualifyingQuota)
		require.NoError(t, DB.Where("key = ?", "xingya_migration.invite_spend_080").Delete(&Option{}).Error)
		require.NoError(t, DB.Save(&Option{Key: "invite_reward_setting.required_consume_usd", Value: "1"}).Error)
		require.NoError(t, InitXingyaTables(DB))
		var gate Option
		require.NoError(t, DB.First(&gate, "key = ?", "invite_reward_setting.required_consume_usd").Error)
		assert.Equal(t, "0.8", gate.Value)
		require.NoError(t, DB.First(&reward, reward.Id).Error)
		assert.Equal(t, InviteRewardStateEligible, reward.State)
		assert.Greater(t, reward.AutoGrantAt, reward.EligibleAt)
		require.NoError(t, DB.Model(&gate).Update("value", "0.9").Error)
		require.NoError(t, InitXingyaTables(DB))
		require.NoError(t, DB.First(&gate, "key = ?", gate.Key).Error)
		assert.Equal(t, "0.9", gate.Value)
		settings := operation_setting.GetInviteRewardSetting()
		old := settings.RequiredConsumeUSD
		settings.RequiredConsumeUSD = 0.8
		t.Cleanup(func() { settings.RequiredConsumeUSD = old })
		assert.Equal(t, 400000, RequiredConsumeQuota())
	})
}

func mustCheckinConfig(t *testing.T) XingyaCheckinSettings {
	t.Helper()
	s, err := GetXingyaCheckinSettings()
	require.NoError(t, err)
	return s
}
