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
	require.NoError(t, DB.Where("key = ?", "xingya_checkin_eligibility").Delete(&Option{}).Error)
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
	credit := func(userID, amount int) {
		require.NoError(t, DB.Create(&XingyaCheckinWallet{UserID: userID, Available: amount, Granted: amount}).Error)
		require.NoError(t, DB.Create(&XingyaCheckinCreditLot{UserID: userID, Source: "opening", SourceKey: fmt.Sprintf("fixture:%d", userID), Granted: amount, Available: amount, CreatedAt: common.GetTimestamp(), ExpiresAt: time.Now().Add(CheckinCreditLifetime).Unix()}).Error)
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
	t.Run("fixed reward uses net cumulative spend and preserves one award per day", func(t *testing.T) {
		s := setConfig(2, 2)
		s.ConsumeThreshold, s.FixedReward = 5000, 30
		s, err := SaveXingyaCheckinSettings(s)
		require.NoError(t, err)
		threshold, err := CheckinRewardQuota(5000)
		require.NoError(t, err)
		fixed, err := CheckinRewardQuota(30)
		require.NoError(t, err)
		random, err := CheckinRewardQuota(2)
		require.NoError(t, err)
		for _, tc := range []struct {
			name   string
			used   int
			refund int
			want   int
		}{
			{"below threshold", threshold - 1, 0, random},
			{"exact threshold", threshold, 0, fixed},
			{"above threshold", threshold + 1, 0, fixed},
			{"refund drops below threshold", threshold, 1, random},
			{"net threshold after refund", threshold + 1, 1, fixed},
			{"refund exceeds persisted usage", 0, 1, random},
		} {
			t.Run(tc.name, func(t *testing.T) {
				user := newUser()
				require.NoError(t, DB.Model(&user).Update("used_quota", tc.used).Error)
				// A large free wallet must not qualify as ordinary consumption.
				credit(user.Id, threshold)
				if tc.refund > 0 {
					row := Log{UserId: user.Id, Type: LogTypeConsume, RequestId: common.NewRequestId(), Quota: tc.refund}
					require.NoError(t, LOG_DB.Create(&row).Error)
					require.NoError(t, DB.Create(&LogRefund{UserId: user.Id, LogId: row.Id, FundingSource: "wallet", BaseQuota: tc.refund, Quota: tc.refund, RequestId: row.RequestId}).Error)
				}
				minimum, maximum, err := XingyaCheckinRewardRange(DB, user.Id, s)
				require.NoError(t, err)
				assert.Equal(t, minimum, maximum)
				preview, err := CheckinRewardQuota(minimum)
				require.NoError(t, err)
				assert.Equal(t, tc.want, preview)
				grant, already, err := ClaimXingyaCheckin(user.Id, time.Time{})
				require.NoError(t, err)
				assert.False(t, already)
				assert.Equal(t, tc.want, grant.QuotaAwarded)
				saved := *grant
				// A later eligibility change must not redraw today's award.
				require.NoError(t, DB.Model(&user).Update("used_quota", threshold*2).Error)
				grant, already, err = ClaimXingyaCheckin(user.Id, time.Time{})
				require.NoError(t, err)
				assert.True(t, already)
				assert.Equal(t, saved, *grant)
				var lot XingyaCheckinCreditLot
				require.NoError(t, DB.Where("grant_id = ?", saved.ID).Take(&lot).Error)
				assert.Equal(t, saved.CreatedAt+int64(CheckinCreditLifetime/time.Second), lot.ExpiresAt)
			})
		}
		s.ConsumeThreshold = 0
		s, err = SaveXingyaCheckinSettings(s)
		require.NoError(t, err)
		user := newUser()
		require.NoError(t, DB.Model(&user).Update("used_quota", threshold).Error)
		grant, _, err := ClaimXingyaCheckin(user.Id, time.Time{})
		require.NoError(t, err)
		assert.Equal(t, random, grant.QuotaAwarded)
		// Restore the configured gate for the remaining accounting regressions.
		s.ConsumeThreshold, s.FixedReward = 5000, 30
		s, err = SaveXingyaCheckinSettings(s)
		require.NoError(t, err)
		persisted, err := GetXingyaCheckinSettings()
		require.NoError(t, err)
		assert.Equal(t, s, persisted)
	})
	t.Run("a user without awards can read an empty wallet without creating credit", func(t *testing.T) {
		user := newUser()
		for range 2 {
			balance, err := GetXingyaCheckinBalance(user.Id)
			require.NoError(t, err)
			assert.Equal(t, XingyaCheckinWallet{UserID: user.Id}, balance.Wallet)
			assert.Equal(t, XingyaCheckinExpiry{}, balance.Expiry)
		}
		var lots int64
		require.NoError(t, DB.Model(&XingyaCheckinCreditLot{}).Where("user_id = ?", user.Id).Count(&lots).Error)
		assert.Zero(t, lots)
	})
	t.Run("production receipt time sets the checkin date and thirty-day deadline", func(t *testing.T) {
		setConfig(2, 2)
		user := newUser()
		before := time.Now()
		grant, already, err := ClaimXingyaCheckin(user.Id, time.Time{})
		require.NoError(t, err)
		after := time.Now()
		assert.False(t, already)
		assert.GreaterOrEqual(t, grant.CreatedAt, before.Unix())
		assert.LessOrEqual(t, grant.CreatedAt, after.Unix())
		assert.Equal(t, CheckinBusinessTime(time.Unix(grant.CreatedAt, 0)).Format("2006-01-02"), grant.CheckinDate)
		var lot XingyaCheckinCreditLot
		require.NoError(t, DB.Where("grant_id = ?", grant.ID).Take(&lot).Error)
		assert.Equal(t, grant.CreatedAt+30*24*60*60, lot.ExpiresAt)
		assert.Equal(t, grant.QuotaAwarded, lot.Available)
	})
	t.Run("expiry keeps each award deadline and consumes earliest lots", func(t *testing.T) {
		setConfig(2, 2)
		user := newUser()
		now := time.Date(2030, 1, 1, 10, 0, 0, 0, checkinLocation)
		first, _, err := ClaimXingyaCheckin(user.Id, now)
		require.NoError(t, err)
		_, _, err = ClaimXingyaCheckin(user.Id, now.Add(24*time.Hour))
		require.NoError(t, err)
		expires := now.Add(CheckinCreditLifetime).Unix()
		balance, err := getXingyaCheckinBalanceAt(user.Id, expires-1)
		require.NoError(t, err)
		assert.Equal(t, 20000, balance.Wallet.Available)
		assert.Equal(t, expires, balance.Expiry.NextExpiresAt)
		assert.Equal(t, 20000, balance.Expiry.ExpiringSoon)
		spend := XingyaCheckinSpend{UserID: user.Id, RequestID: common.NewRequestId()}
		require.NoError(t, reserveCheckinQuotaAt(spend, 15000, expires-1))
		require.NoError(t, finalizeCheckinSpendAt(user.Id, spend.RequestID, 5000, false, expires-1))
		var lot XingyaCheckinCreditLot
		require.NoError(t, DB.Where("grant_id = ?", first.ID).Take(&lot).Error)
		assert.Equal(t, 5000, lot.Consumed)
		assert.Equal(t, 5000, lot.Available)
		balance, err = getXingyaCheckinBalanceAt(user.Id, expires)
		require.NoError(t, err)
		assert.Equal(t, 10000, balance.Wallet.Available)
		assert.Equal(t, 5000, balance.Wallet.Consumed)
		assert.Equal(t, 5000, balance.Wallet.Expired)
		again, err := getXingyaCheckinBalanceAt(user.Id, expires+1)
		require.NoError(t, err)
		assert.Equal(t, balance.Wallet, again.Wallet)
		assert.Equal(t, balance.Wallet.Granted, balance.Wallet.Available+balance.Wallet.Reserved+balance.Wallet.Consumed+balance.Wallet.Expired)
	})
	t.Run("cross-expiry charges and releases never revive credit", func(t *testing.T) {
		setConfig(2, 2)
		for _, tc := range []struct {
			name    string
			actual  int
			release bool
		}{{"tier difference", 5000, false}, {"failed request", 0, true}, {"full fee", 10000, false}} {
			t.Run(tc.name, func(t *testing.T) {
				user := newUser()
				now := time.Date(2030, 2, 1, 0, 0, 0, 0, checkinLocation)
				_, _, err := ClaimXingyaCheckin(user.Id, now)
				require.NoError(t, err)
				expires := now.Add(CheckinCreditLifetime).Unix()
				spend := XingyaCheckinSpend{UserID: user.Id, RequestID: common.NewRequestId()}
				require.NoError(t, reserveCheckinQuotaAt(spend, 10000, expires-1))
				before, err := getXingyaCheckinBalanceAt(user.Id, expires)
				require.NoError(t, err)
				assert.Equal(t, 10000, before.Wallet.Reserved)
				assert.Zero(t, before.Wallet.Expired)
				require.NoError(t, finalizeCheckinSpendAt(user.Id, spend.RequestID, tc.actual, tc.release, expires+1))
				require.NoError(t, finalizeCheckinSpendAt(user.Id, spend.RequestID, tc.actual, tc.release, expires+2))
				after, err := getXingyaCheckinBalanceAt(user.Id, expires+2)
				require.NoError(t, err)
				assert.Zero(t, after.Wallet.Available)
				assert.Zero(t, after.Wallet.Reserved)
				assert.Equal(t, tc.actual, after.Wallet.Consumed)
				assert.Equal(t, 10000-tc.actual, after.Wallet.Expired)
				var allocation XingyaCheckinAllocation
				require.NoError(t, DB.Where("request_id = ?", spend.RequestID).Take(&allocation).Error)
				assert.Equal(t, 10000-tc.actual, allocation.Expired)
			})
		}
	})
	t.Run("expiry commits when admission is rejected and no spend is created", func(t *testing.T) {
		setConfig(2, 2)
		user := newUser()
		now := time.Date(2030, 3, 1, 0, 0, 0, 0, checkinLocation)
		_, _, err := ClaimXingyaCheckin(user.Id, now)
		require.NoError(t, err)
		spend := XingyaCheckinSpend{UserID: user.Id, RequestID: common.NewRequestId()}
		require.ErrorIs(t, reserveCheckinQuotaAt(spend, 50001, now.Add(CheckinCreditLifetime).Unix()), ErrCheckinInsufficient)
		var wallet XingyaCheckinWallet
		require.NoError(t, DB.First(&wallet, "user_id = ?", user.Id).Error)
		assert.Zero(t, wallet.Available)
		assert.Equal(t, 10000, wallet.Expired)
		var count int64
		require.NoError(t, DB.Model(&XingyaCheckinSpend{}).Where("request_id = ?", spend.RequestID).Count(&count).Error)
		assert.Zero(t, count)
	})
	t.Run("reducing then increasing a reservation preserves earliest expiry and never revives expired credit", func(t *testing.T) {
		setConfig(2, 2)
		user := newUser()
		now := time.Date(2030, 4, 1, 0, 0, 0, 0, checkinLocation)
		_, _, err := ClaimXingyaCheckin(user.Id, now)
		require.NoError(t, err)
		_, _, err = ClaimXingyaCheckin(user.Id, now.Add(24*time.Hour))
		require.NoError(t, err)
		expires := now.Add(CheckinCreditLifetime).Unix()
		spend := XingyaCheckinSpend{UserID: user.Id, RequestID: common.NewRequestId()}
		require.NoError(t, reserveCheckinQuotaAt(spend, 15000, expires-1))
		require.NoError(t, reserveCheckinQuotaAt(spend, 5000, expires))
		balance, err := getXingyaCheckinBalanceAt(user.Id, expires)
		require.NoError(t, err)
		assert.Equal(t, 5000, balance.Wallet.Reserved)
		assert.Equal(t, 5000, balance.Wallet.Expired)
		assert.Equal(t, 10000, balance.Wallet.Available)
		for range 2 {
			require.NoError(t, reserveCheckinQuotaAt(spend, 10000, expires+1))
		}
		require.NoError(t, finalizeCheckinSpendAt(user.Id, spend.RequestID, 7500, false, expires+1))
		balance, err = getXingyaCheckinBalanceAt(user.Id, expires+1)
		require.NoError(t, err)
		assert.Equal(t, XingyaCheckinWallet{UserID: user.Id, Available: 7500, Granted: 20000, Consumed: 7500, Expired: 5000}, balance.Wallet)
		var lots []XingyaCheckinCreditLot
		require.NoError(t, DB.Where("user_id = ?", user.Id).Order("expires_at, id").Find(&lots).Error)
		require.Len(t, lots, 2)
		assert.Equal(t, 5000, lots[0].Consumed)
		assert.Equal(t, 5000, lots[0].Expired)
		assert.Equal(t, 2500, lots[1].Consumed)
		assert.Equal(t, 7500, lots[1].Available)
		balance, err = getXingyaCheckinBalanceAt(user.Id, expires+86400)
		require.NoError(t, err)
		assert.Zero(t, balance.Wallet.Available)
		assert.Equal(t, 12500, balance.Wallet.Expired)
	})
	t.Run("concurrent claim expiry and reservation preserve wallet totals", func(t *testing.T) {
		setConfig(2, 2)
		user := newUser()
		now := time.Date(2030, 6, 1, 0, 0, 0, 0, checkinLocation)
		for _, at := range []time.Time{now, now.Add(24 * time.Hour)} {
			_, _, err := ClaimXingyaCheckin(user.Id, at)
			require.NoError(t, err)
		}
		expires := now.Add(CheckinCreditLifetime)
		spend := XingyaCheckinSpend{UserID: user.Id, RequestID: common.NewRequestId()}
		operations := []func() error{
			func() error { _, err := getXingyaCheckinBalanceAt(user.Id, expires.Unix()); return err },
			func() error { return reserveCheckinQuotaAt(spend, 5000, expires.Unix()) },
			func() error { _, _, err := ClaimXingyaCheckin(user.Id, expires); return err },
		}
		var wg sync.WaitGroup
		start := make(chan struct{})
		results := make(chan error, len(operations))
		for _, operation := range operations {
			wg.Go(func() { <-start; results <- operation() })
		}
		close(start)
		wg.Wait()
		close(results)
		for err := range results {
			require.NoError(t, err)
		}
		balance, err := getXingyaCheckinBalanceAt(user.Id, expires.Unix())
		require.NoError(t, err)
		assert.Equal(t, XingyaCheckinWallet{UserID: user.Id, Available: 15000, Reserved: 5000, Granted: 30000, Expired: 10000}, balance.Wallet)
		var lots []XingyaCheckinCreditLot
		require.NoError(t, DB.Where("user_id = ?", user.Id).Order("expires_at, id").Find(&lots).Error)
		require.Len(t, lots, 3)
		assert.Equal(t, 10000, lots[0].Expired)
		assert.Equal(t, 5000, lots[1].Reserved)
		assert.Equal(t, 10000, lots[2].Available)
	})
	t.Run("releasing an expired reservation moves it into expiry", func(t *testing.T) {
		setConfig(2, 2)
		user := newUser()
		now := time.Date(2030, 5, 1, 0, 0, 0, 0, checkinLocation)
		_, _, err := ClaimXingyaCheckin(user.Id, now)
		require.NoError(t, err)
		at := now.Unix()
		spend := XingyaCheckinSpend{UserID: user.Id, RequestID: common.NewRequestId()}
		require.NoError(t, reserveCheckinQuotaAt(spend, 10000, at))
		require.NoError(t, finalizeCheckinSpendAt(user.Id, spend.RequestID, 0, true, now.Add(CheckinCreditLifetime).Unix()))
		balance, err := getXingyaCheckinBalanceAt(user.Id, now.Add(CheckinCreditLifetime).Unix())
		require.NoError(t, err)
		assert.Equal(t, 10000, balance.Wallet.Expired)
		assert.Zero(t, balance.Wallet.Available)
		assert.Zero(t, balance.Wallet.Reserved)
	})
	// Reset only this feature's state in the explicitly configured disposable DB.
	for _, table := range []any{&XingyaCheckinAllocation{}, &XingyaCheckinCreditLot{}, &XingyaCheckinSpend{}, &XingyaCheckinGrant{}, &XingyaCheckinWallet{}} {
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
		credit(user.Id, 1000)
		spend := XingyaCheckinSpend{UserID: user.Id, TokenID: 42, RequestID: common.NewRequestId(), Group: "checkin-fixture", Model: "fixture"}
		require.NoError(t, ReserveCheckinQuota(spend, 700))
		require.NoError(t, ReserveCheckinQuota(spend, 700))
		wrong := spend
		wrong.TokenID++
		require.Error(t, ReserveCheckinQuota(wrong, 700))
		require.ErrorIs(t, ReserveCheckinQuota(spend, 51001), ErrCheckinInsufficient)
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
	t.Run("concurrent requests cannot overreserve without borrowing", func(t *testing.T) {
		user := newUser()
		credit(user.Id, 45090)
		results := make(chan error, 12)
		var wg sync.WaitGroup
		for range 12 {
			wg.Go(func() {
				results <- ReserveCheckinQuota(XingyaCheckinSpend{UserID: user.Id, RequestID: common.NewRequestId()}, 15030)
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
		assert.Zero(t, wallet.Available)
		assert.Equal(t, 45090, wallet.Reserved)
		assert.Zero(t, wallet.Borrowed)
	})
	t.Run("an insufficient balance explains the required minimum", func(t *testing.T) {
		user := newUser()
		credit(user.Id, 100)
		err := ReserveCheckinQuota(XingyaCheckinSpend{UserID: user.Id, RequestID: common.NewRequestId()}, 500)
		require.ErrorIs(t, err, ErrCheckinInsufficient)
		assert.Contains(t, err.Error(), "本次请求至少需要")
		assert.Contains(t, err.Error(), "当前签到可用")
	})
	t.Run("a cost above the reservation becomes a negative balance and blocks new requests", func(t *testing.T) {
		setConfig(2, 2)
		user := newUser()
		credit(user.Id, 1000)
		spend := XingyaCheckinSpend{UserID: user.Id, RequestID: common.NewRequestId(), Model: fmt.Sprintf("negative-model-%d", user.Id)}
		require.NoError(t, ReserveCheckinQuota(spend, 100))
		require.NoError(t, FinalizeCheckinSpend(user.Id, spend.RequestID, 52000, false))
		wallet, err := GetXingyaCheckinWallet(user.Id)
		require.NoError(t, err)
		assert.Equal(t, -51000, wallet.Available)
		assert.Equal(t, 51000, wallet.Borrowed)
		assert.Equal(t, 52000, wallet.Consumed)
		var row XingyaCheckinSpend
		require.NoError(t, DB.First(&row, "request_id = ?", spend.RequestID).Error)
		assert.Equal(t, "settled", row.State)
		assert.Zero(t, row.UnpaidQuota)
		next := spend
		next.RequestID = common.NewRequestId()
		require.ErrorIs(t, ReserveCheckinQuota(next, 1), ErrCheckinInsufficient, "a negative balance blocks new requests")
		other := newUser()
		credit(other.Id, 1000)
		otherSpend := XingyaCheckinSpend{UserID: other.Id, RequestID: common.NewRequestId(), Model: spend.Model}
		require.NoError(t, ReserveCheckinQuota(otherSpend, 100), "a negative balance only pauses its owner")
		require.NoError(t, FinalizeCheckinSpend(other.Id, otherSpend.RequestID, 0, true))
		var ordinary User
		require.NoError(t, DB.First(&ordinary, user.Id).Error)
		assert.Equal(t, user.Quota, ordinary.Quota)
		grant, already, err := ClaimXingyaCheckin(user.Id, time.Time{})
		require.NoError(t, err)
		assert.False(t, already)
		assert.Equal(t, 10000, grant.QuotaAwarded)
		wallet, err = GetXingyaCheckinWallet(user.Id)
		require.NoError(t, err)
		assert.Equal(t, -41000, wallet.Available)
		assert.Equal(t, 41000, wallet.Borrowed)
		require.ErrorIs(t, ReserveCheckinQuota(next, 1), ErrCheckinInsufficient, "a partial reward keeps the block until the debt is repaid")
	})
	t.Run("stale reservations are released automatically and active ones stay", func(t *testing.T) {
		user := newUser()
		credit(user.Id, 1000)
		stale := XingyaCheckinSpend{UserID: user.Id, RequestID: common.NewRequestId()}
		active := XingyaCheckinSpend{UserID: user.Id, RequestID: common.NewRequestId()}
		require.NoError(t, ReserveCheckinQuota(stale, 300))
		require.NoError(t, ReserveCheckinQuota(active, 300))
		staleAt := common.GetTimestamp() - 25*60*60
		require.NoError(t, DB.Model(&XingyaCheckinSpend{}).Where("request_id = ?", stale.RequestID).Update("updated_at", staleAt).Error)
		fresh := XingyaCheckinSpend{UserID: user.Id, RequestID: common.NewRequestId()}
		require.NoError(t, ReserveCheckinQuota(fresh, 400), "the stale reservation returns before the new one is checked")
		var staleRow, activeRow XingyaCheckinSpend
		require.NoError(t, DB.First(&staleRow, "request_id = ?", stale.RequestID).Error)
		require.NoError(t, DB.First(&activeRow, "request_id = ?", active.RequestID).Error)
		assert.Equal(t, "released", staleRow.State)
		assert.Equal(t, "reserved", activeRow.State)
		wallet, err := GetXingyaCheckinWallet(user.Id)
		require.NoError(t, err)
		assert.Equal(t, 300, wallet.Available)
		assert.Equal(t, 700, wallet.Reserved)
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
	assertLedger := func(t *testing.T, userID int) XingyaCheckinWallet {
		t.Helper()
		wallet, err := GetXingyaCheckinWallet(userID)
		require.NoError(t, err)
		assert.True(t, validCheckinWallet(wallet))
		var lots []XingyaCheckinCreditLot
		require.NoError(t, DB.Where("user_id = ?", userID).Find(&lots).Error)
		var spends []XingyaCheckinSpend
		require.NoError(t, DB.Where("user_id = ?", userID).Find(&spends).Error)
		var available, reserved, consumed, expired, granted, borrowed int
		for _, lot := range lots {
			assert.True(t, validCheckinLot(lot))
			available += lot.Available
			reserved += lot.Reserved
			consumed += lot.Consumed
			expired += lot.Expired
			granted += lot.Granted
		}
		for _, spend := range spends {
			borrowed += spend.BorrowedReserved + spend.BorrowedConsumed
			reserved += spend.BorrowedReserved
			consumed += spend.BorrowedConsumed
			var allocations []XingyaCheckinAllocation
			require.NoError(t, DB.Where("request_id = ?", spend.RequestID).Find(&allocations).Error)
			for _, allocation := range allocations {
				assert.True(t, validCheckinAllocation(allocation))
			}
		}
		assert.Equal(t, wallet.Available+wallet.Borrowed, available)
		assert.Equal(t, wallet.Reserved, reserved)
		assert.Equal(t, wallet.Consumed, consumed)
		assert.Equal(t, wallet.Expired, expired)
		assert.Equal(t, wallet.Granted, granted)
		assert.Equal(t, wallet.Borrowed, borrowed)
		return wallet
	}
	t.Run("a reservation released after its credit lot expires is recorded as expired", func(t *testing.T) {
		user := newUser()
		credit(user.Id, 10000)
		var lot XingyaCheckinCreditLot
		require.NoError(t, DB.Where("user_id = ?", user.Id).Take(&lot).Error)
		spend := XingyaCheckinSpend{UserID: user.Id, RequestID: common.NewRequestId()}
		require.NoError(t, reserveCheckinQuotaAt(spend, 5000, lot.ExpiresAt-1))
		require.NoError(t, finalizeCheckinSpendAt(user.Id, spend.RequestID, 0, true, lot.ExpiresAt))
		wallet := assertLedger(t, user.Id)
		assert.Zero(t, wallet.Available)
		assert.Zero(t, wallet.Borrowed)
		assert.Zero(t, wallet.Reserved)
		assert.Equal(t, 10000, wallet.Expired)
	})
	t.Run("the debt ceiling absorbs the excess instead of blocking the settlement", func(t *testing.T) {
		setConfig(2, 2)
		user := newUser()
		credit(user.Id, 5000)
		spend := XingyaCheckinSpend{UserID: user.Id, RequestID: common.NewRequestId(), Model: "token-priced"}
		require.NoError(t, ReserveCheckinQuota(spend, 1000))
		require.NoError(t, FinalizeCheckinSpend(user.Id, spend.RequestID, 600000, false))
		require.NoError(t, FinalizeCheckinSpend(user.Id, spend.RequestID, 600000, false), "finalization stays idempotent")
		wallet := assertLedger(t, user.Id)
		limit, err := CheckinDebtLimit()
		require.NoError(t, err)
		assert.Equal(t, -496000, wallet.Available)
		assert.Equal(t, 496000, wallet.Borrowed)
		assert.LessOrEqual(t, wallet.Borrowed, limit)
		var saved XingyaCheckinSpend
		require.NoError(t, DB.First(&saved, "request_id = ?", spend.RequestID).Error)
		assert.Equal(t, "settled", saved.State)
		assert.Equal(t, 99000, saved.UnpaidQuota, "the part above the ceiling is absorbed and audited")
		require.NotNil(t, saved.BalanceAfter)
		assert.Equal(t, -496000, *saved.BalanceAfter)
		next := spend
		next.RequestID = common.NewRequestId()
		require.ErrorIs(t, ReserveCheckinQuota(next, 1), ErrCheckinInsufficient)
		grant, already, err := ClaimXingyaCheckin(user.Id, time.Time{})
		require.NoError(t, err)
		assert.False(t, already)
		wallet = assertLedger(t, user.Id)
		assert.Equal(t, -486000, wallet.Available)
		assert.Equal(t, 486000, wallet.Borrowed)
		var lot XingyaCheckinCreditLot
		require.NoError(t, DB.First(&lot, "grant_id = ?", grant.ID).Error)
		assert.Zero(t, lot.Available)
		assert.Equal(t, 10000, lot.Consumed)
	})
	t.Run("a reward repays debt while another request is in flight", func(t *testing.T) {
		setConfig(2, 2)
		user := newUser()
		credit(user.Id, 10000)
		spend := XingyaCheckinSpend{UserID: user.Id, RequestID: common.NewRequestId()}
		require.NoError(t, ReserveCheckinQuota(spend, 5000))
		require.NoError(t, FinalizeCheckinSpend(user.Id, spend.RequestID, 12500, false))
		wallet := assertLedger(t, user.Id)
		assert.Equal(t, -2500, wallet.Available)
		assert.Equal(t, 2500, wallet.Borrowed)
		assert.Equal(t, 12500, wallet.Consumed)
		_, _, err := ClaimXingyaCheckin(user.Id, time.Time{})
		require.NoError(t, err)
		wallet = assertLedger(t, user.Id)
		assert.Equal(t, 7500, wallet.Available)
		assert.Zero(t, wallet.Borrowed)
		next := XingyaCheckinSpend{UserID: user.Id, RequestID: common.NewRequestId()}
		require.NoError(t, ReserveCheckinQuota(next, 7000))
		require.NoError(t, FinalizeCheckinSpend(user.Id, next.RequestID, 0, true))
		assert.Equal(t, wallet, assertLedger(t, user.Id))
	})
	t.Run("group switching moves only dedicated keys while old in-flight funds can finish", func(t *testing.T) {
		useUserCacheMiniRedis(t)
		settings := setConfig(2, 2)
		user := newUser()
		grant, _, err := ClaimXingyaCheckin(user.Id, time.Time{})
		require.NoError(t, err)
		dedicated, _, err := CreateCheckinToken(user.Id, settings.Group)
		require.NoError(t, err)
		regular := Token{UserId: user.Id, Key: common.NewRequestId(), Group: settings.Group, Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
		require.NoError(t, DB.Create(&regular).Error)
		_, err = cacheInitToken(*dedicated)
		require.NoError(t, err)
		before, err := GetXingyaCheckinBalance(user.Id)
		require.NoError(t, err)
		var lotBefore XingyaCheckinCreditLot
		require.NoError(t, DB.First(&lotBefore, "grant_id = ?", grant.ID).Error)
		spend := XingyaCheckinSpend{UserID: user.Id, TokenID: dedicated.Id, RequestID: common.NewRequestId(), Group: settings.Group, Model: "old-model"}
		require.NoError(t, ReserveCheckinQuota(spend, 5000))
		settings.Group = "new-token-priced-group"
		settings, err = SaveXingyaCheckinSettings(settings)
		require.NoError(t, err)
		var moved, unchanged Token
		require.NoError(t, DB.First(&moved, dedicated.Id).Error)
		require.NoError(t, DB.First(&unchanged, regular.Id).Error)
		assert.Equal(t, settings.Group, moved.Group)
		cached, err := GetTokenByKey(dedicated.Key, false)
		require.NoError(t, err)
		assert.Equal(t, settings.Group, cached.Group, "the old group cannot survive in the token cache")
		assert.Equal(t, dedicated.Key, moved.Key)
		assert.Equal(t, regular, unchanged)
		reused, exists, err := CreateCheckinToken(user.Id, settings.Group)
		require.NoError(t, err)
		assert.True(t, exists)
		assert.Equal(t, dedicated.Id, reused.Id)
		require.NoError(t, ReserveCheckinQuota(spend, 5000))
		require.NoError(t, FinalizeCheckinSpend(user.Id, spend.RequestID, 0, true))
		after, err := GetXingyaCheckinBalance(user.Id)
		require.NoError(t, err)
		assert.Equal(t, before, after)
		var savedGrant XingyaCheckinGrant
		var savedLot XingyaCheckinCreditLot
		require.NoError(t, DB.First(&savedGrant, grant.ID).Error)
		require.NoError(t, DB.First(&savedLot, lotBefore.ID).Error)
		assert.Equal(t, *grant, savedGrant)
		assert.Equal(t, lotBefore, savedLot)
		settings.Group = dedicated.Group
		_, err = SaveXingyaCheckinSettings(settings)
		require.NoError(t, err)
		assertLedger(t, user.Id)
	})
	t.Run("refresh opens one additional daily round without reclaiming any rewards", func(t *testing.T) {
		setConfig(2, 2)
		user := newUser()
		at := time.Date(2031, 3, 1, 10, 0, 0, 0, checkinLocation)
		original, _, err := ClaimXingyaCheckin(user.Id, at)
		require.NoError(t, err)
		expected := XingyaCheckinEligibility{Day: "2031-03-01"}
		refreshed, err := RefreshXingyaCheckinEligibility(expected, at)
		require.NoError(t, err)
		assert.Equal(t, int64(1), refreshed.Round)
		_, err = RefreshXingyaCheckinEligibility(expected, at)
		require.ErrorIs(t, err, ErrCheckinConfigConflict, "replaying a refresh must not grant another round")
		var wg sync.WaitGroup
		results := make(chan error, 2)
		awards := make(chan *XingyaCheckinGrant, 2)
		for range 2 {
			wg.Go(func() { grant, _, err := ClaimXingyaCheckin(user.Id, at); results <- err; awards <- grant })
		}
		wg.Wait()
		close(results)
		close(awards)
		for err := range results {
			require.NoError(t, err)
		}
		var extraID int
		for grant := range awards {
			assert.Equal(t, int64(1), grant.ClaimRound)
			if extraID == 0 {
				extraID = grant.ID
			}
			assert.Equal(t, extraID, grant.ID)
		}
		var saved XingyaCheckinGrant
		require.NoError(t, DB.First(&saved, original.ID).Error)
		assert.Equal(t, *original, saved)
		var wallet XingyaCheckinWallet
		require.NoError(t, DB.First(&wallet, "user_id = ?", user.Id).Error)
		assert.Equal(t, 20000, wallet.Available)
		other := newUser()
		// A legacy claim blocks round zero, but not an explicitly refreshed round.
		require.NoError(t, DB.Create(&Checkin{UserId: other.Id, CheckinDate: expected.Day, QuotaAwarded: 777}).Error)
		extra, already, err := ClaimXingyaCheckin(other.Id, at)
		require.NoError(t, err)
		assert.False(t, already)
		assert.Equal(t, int64(1), extra.ClaimRound)
		tomorrow, already, err := ClaimXingyaCheckin(user.Id, at.Add(24*time.Hour))
		require.NoError(t, err)
		assert.False(t, already)
		assert.Zero(t, tomorrow.ClaimRound)
		_, already, err = ClaimXingyaCheckin(user.Id, at.Add(24*time.Hour))
		require.NoError(t, err)
		assert.True(t, already)
	})

}

func mustCheckinConfig(t *testing.T) XingyaCheckinSettings {
	t.Helper()
	s, err := GetXingyaCheckinSettings()
	require.NoError(t, err)
	return s
}

func TestXingyaCheckinFixedRewardValidation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		threshold float64
		reward    float64
		valid     bool
	}{
		{"configured gate", 5000, 30, true},
		{"disabled gate", 0, 0, true},
		{"enabled gate requires positive reward", 5000, 0, false},
		{"negative threshold", -1, 30, false},
		{"fractional precision", 5000.001, 30, false},
		{"nonfinite threshold", math.Inf(1), 30, false},
		{"nonfinite reward", 5000, math.NaN(), false},
		{"oversized reward", 5000, operation_setting.MaxCheckinRewardTier + 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateXingyaCheckinSettings(XingyaCheckinSettings{ConsumeThreshold: tc.threshold, FixedReward: tc.reward})
			if tc.valid {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestXingyaCheckinOptionsRequireDedicatedInterfaces(t *testing.T) {
	for _, key := range []string{checkinEligibilityKey, checkinOptionPrefix + "revision", checkinOptionPrefix + "group", "xingya_migration.checkin_credit_expiry", checkinReviewClearMigration} {
		t.Run(key, func(t *testing.T) {
			require.Error(t, validateOptionValue(key, "0"))
		})
	}
}
