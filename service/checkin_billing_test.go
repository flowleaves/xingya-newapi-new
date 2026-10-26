package service

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestCheckinBillingPostgres(t *testing.T) {
	dsn, logDSN := os.Getenv("TEST_XINGYA_POSTGRES_DSN"), os.Getenv("TEST_XINGYA_POSTGRES_LOG_DSN")
	if dsn == "" || logDSN == "" {
		t.Skip("isolated PostgreSQL DSNs are required")
	}
	oldDB, oldLog, oldRedis, oldBatch := model.DB, model.LOG_DB, common.RedisEnabled, common.BatchUpdateEnabled
	mainType, logType := common.MainDatabaseType(), common.LogDatabaseType()
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLog
		common.SetDatabaseTypes(mainType, logType)
		common.RedisEnabled, common.BatchUpdateEnabled = oldRedis, oldBatch
	})
	var err error
	model.DB, err = gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	require.NoError(t, err)
	model.LOG_DB, err = gorm.Open(postgres.New(postgres.Config{DSN: logDSN, PreferSimpleProtocol: true}), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	require.NoError(t, err)
	for _, db := range []*gorm.DB{model.DB, model.LOG_DB} {
		pool, err := db.DB()
		require.NoError(t, err)
		pool.SetMaxOpenConns(1)
		t.Cleanup(func() { require.NoError(t, pool.Close()) })
	}
	require.NoError(t, model.DB.AutoMigrate(&model.Option{}, &model.User{}, &model.Token{}, &model.Channel{}))
	require.NoError(t, model.LOG_DB.AutoMigrate(&model.Log{}))
	require.NoError(t, model.InitXingyaTables(model.DB))
	credit := func(userID, amount int) {
		require.NoError(t, model.DB.Create(&model.XingyaCheckinWallet{UserID: userID, Available: amount, Granted: amount}).Error)
		require.NoError(t, model.DB.Create(&model.XingyaCheckinCreditLot{UserID: userID, Source: "opening", SourceKey: fmt.Sprintf("fixture:%d", userID), Granted: amount, Available: amount, CreatedAt: common.GetTimestamp(), ExpiresAt: time.Now().Add(model.CheckinCreditLifetime).Unix()}).Error)
	}
	for _, tc := range []struct {
		name                   string
		actual, budget, credit int
		fail, insufficient     bool
	}{
		{name: "ordinary balance zero and subscription-only preference still use checkin", actual: 100, budget: 300, credit: 1000},
		{name: "equal reservation is durably settled", actual: 300, budget: 300, credit: 1000},
		{name: "zero cost releases reservation", actual: 0, budget: 300, credit: 1000},
		{name: "insufficient checkin cannot fall back", budget: 50101, credit: 100, insufficient: true},
		{name: "failed request refunds once", budget: 300, credit: 1000, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user := model.User{Username: fmt.Sprintf("cb-%d", time.Now().UnixNano()), Password: "fixture", AffCode: fmt.Sprintf("cb-%d", time.Now().UnixNano()), Status: common.UserStatusEnabled, Quota: 0}
			require.NoError(t, model.DB.Create(&user).Error)
			token := model.Token{UserId: user.Id, Key: common.NewRequestId(), Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true, QuotaSource: model.CheckinQuotaSource, Group: "checkin-fixture"}
			require.NoError(t, model.DB.Create(&token).Error)
			credit(user.Id, tc.credit)
			info := &relaycommon.RelayInfo{UserId: user.Id, TokenId: token.Id, TokenKey: token.Key, TokenUnlimited: true, TokenQuotaSource: BillingSourceCheckin, TokenGroup: token.Group, UsingGroup: token.Group, RequestId: common.NewRequestId(), OriginModelName: "fixture", ChannelMeta: &relaycommon.ChannelMeta{}, UserSetting: dto.UserSetting{BillingPreference: "subscription_only"}, PriceData: hosttypes.PriceData{UsePrice: true, ModelPrice: float64(tc.budget) / common.QuotaPerUnit, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}}}
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			apiErr := PreConsumeBilling(ctx, tc.budget, info)
			if tc.insufficient {
				require.NotNil(t, apiErr)
				assert.Equal(t, types.ErrorCodeInsufficientUserQuota, apiErr.GetErrorCode())
				var saved model.User
				require.NoError(t, model.DB.First(&saved, user.Id).Error)
				assert.Zero(t, saved.Quota)
				return
			}
			require.Nil(t, apiErr)
			assert.Equal(t, BillingSourceCheckin, info.BillingSource)
			if tc.fail {
				completed := make(chan struct{}, 1)
				callback := "checkin_refund_observed"
				require.NoError(t, model.DB.Callback().Update().After("gorm:update").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Table == "xingya_checkin_spends" {
						select {
						case completed <- struct{}{}:
						default:
						}
					}
				}))
				t.Cleanup(func() { model.DB.Callback().Update().Remove(callback) })
				info.Billing.Refund(ctx)
				info.Billing.Refund(ctx)
				select {
				case <-completed:
				case <-time.After(5 * time.Second):
					t.Fatal("refund did not finalize")
				}
			} else {
				require.NoError(t, SettleBilling(ctx, info, tc.actual))
				require.NoError(t, SettleBilling(ctx, info, tc.actual))
			}
			wallet, err := model.GetXingyaCheckinWallet(user.Id)
			require.NoError(t, err)
			assert.Equal(t, tc.credit-tc.actual, wallet.Available)
			assert.Zero(t, wallet.Reserved)
			assert.Equal(t, tc.actual, wallet.Consumed)
			var row model.XingyaCheckinSpend
			require.NoError(t, model.DB.First(&row, "request_id = ?", info.RequestId).Error)
			assert.NotEqual(t, "reserved", row.State)
			var saved model.User
			require.NoError(t, model.DB.First(&saved, user.Id).Error)
			assert.Zero(t, saved.Quota)
		})
	}
	// The matched request tier is the minimum; a higher final tier becomes debt.
	info := &relaycommon.RelayInfo{TokenQuotaSource: BillingSourceCheckin, ChannelMeta: &relaycommon.ChannelMeta{}, TieredBillingSnapshot: &billingexpr.BillingSnapshot{ExprString: `len <= 250000 ? tier("short", fixed(0.01)) : tier("long", fixed(0.02))`, QuotaPerUnit: 500000, GroupRatio: 1, EstimatedQuotaAfterGroup: 5000}}
	minimum, err := CheckinRequestMinimum(info)
	require.NoError(t, err)
	assert.Equal(t, 5000, minimum)
	for _, tc := range []struct {
		name                string
		input, credit, want int
		wantAvailable       int
		wantBorrowed        int
		insufficient        bool
	}{
		{"short context settles at one sprout", 200000, 10000, 5000, 5000, 0, false},
		{"boundary stays in short tier", 250000, 10000, 5000, 5000, 0, false},
		{"long context repays the extra from free credit", 250001, 10000, 10000, 0, 0, false},
		{"long context leaves a negative balance when only the minimum was covered", 250001, 5000, 10000, -5000, 5000, false},
		{"a balance below the minimum is rejected", 200000, 4999, 0, 4999, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user := model.User{Username: fmt.Sprintf("ct-%d", time.Now().UnixNano()), AffCode: fmt.Sprintf("ct-%d", time.Now().UnixNano()), Status: common.UserStatusEnabled, Quota: 100000000}
			require.NoError(t, model.DB.Create(&user).Error)
			credit(user.Id, tc.credit)
			requestInfo := *info
			requestInfo.UserId, requestInfo.UserQuota = user.Id, user.Quota
			requestInfo.RequestId, requestInfo.IsPlayground, requestInfo.TokenUnlimited = common.NewRequestId(), true, true
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			// The caller's ordinary estimate is replaced by the check-in minimum.
			apiErr := PreConsumeBilling(ctx, 20000, &requestInfo)
			if tc.insufficient {
				require.NotNil(t, apiErr)
				assert.Equal(t, types.ErrorCodeInsufficientUserQuota, apiErr.GetErrorCode())
			} else {
				require.Nil(t, apiErr)
				wallet, err := model.GetXingyaCheckinWallet(user.Id)
				require.NoError(t, err)
				assert.Equal(t, minimum, wallet.Reserved)
				actual, err := billingexpr.ComputeTieredQuota(requestInfo.TieredBillingSnapshot, billingexpr.TokenParams{P: float64(tc.input), Len: float64(tc.input), C: 9000})
				require.NoError(t, err)
				assert.Equal(t, tc.want, actual.ActualQuotaAfterGroup)
				require.NoError(t, SettleBilling(ctx, &requestInfo, actual.ActualQuotaAfterGroup))
			}
			wallet, err := model.GetXingyaCheckinWallet(user.Id)
			require.NoError(t, err)
			assert.Equal(t, tc.wantAvailable, wallet.Available)
			assert.Equal(t, tc.wantBorrowed, wallet.Borrowed)
			assert.Zero(t, wallet.Reserved)
			assert.Equal(t, tc.want, wallet.Consumed)
			if !tc.insufficient {
				var spend model.XingyaCheckinSpend
				require.NoError(t, model.DB.Where("request_id = ?", requestInfo.RequestId).Take(&spend).Error)
				assert.Zero(t, spend.UnpaidQuota)
				assert.Equal(t, "settled", spend.State)
			}
			var saved model.User
			require.NoError(t, model.DB.First(&saved, user.Id).Error)
			assert.Equal(t, user.Quota, saved.Quota)
		})
	}
	t.Run("token and mixed pricing use native estimates", func(t *testing.T) {
		minimum, err := CheckinRequestMinimum(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, PriceData: hosttypes.PriceData{ModelRatio: 1, QuotaToPreConsume: 300}})
		require.NoError(t, err)
		assert.Equal(t, 300, minimum)
		mixed := *info
		mixed.TieredBillingSnapshot = &billingexpr.BillingSnapshot{ExprString: `len <= 250000 ? tier("short", fixed(0.01)) : tier("long", p*2+c*8)`, EstimatedQuotaAfterGroup: 725}
		minimum, err = CheckinRequestMinimum(&mixed)
		require.NoError(t, err)
		assert.Equal(t, 725, minimum)
		mixed.TieredBillingSnapshot.EstimatedQuotaAfterGroup = -1
		_, err = CheckinRequestMinimum(&mixed)
		require.Error(t, err)
	})
	t.Run("legacy request multipliers are included before reservation", func(t *testing.T) {
		info := &relaycommon.RelayInfo{TokenQuotaSource: BillingSourceCheckin, ChannelMeta: &relaycommon.ChannelMeta{}, PriceData: hosttypes.PriceData{UsePrice: true, ModelPrice: 0.0004, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}}}
		info.PriceData.AddOtherRatio("fixture_multiplier", 2)
		minimum, err := CheckinRequestMinimum(info)
		require.NoError(t, err)
		assert.Equal(t, 400, minimum)
	})
	t.Run("fractional fixed fees round exactly as settlement", func(t *testing.T) {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, PriceData: hosttypes.PriceData{UsePrice: true, ModelPrice: 0.000003, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}}}
		minimum, err := CheckinRequestMinimum(info)
		require.NoError(t, err)
		assert.Equal(t, 2, minimum, "1.5 internal quota rounds to 2 rather than reserving 1")
	})
	t.Run("synchronous context and tools are allowed but background work stays blocked", func(t *testing.T) {
		settings, err := model.GetXingyaCheckinSettings()
		require.NoError(t, err)
		settings.SpendEnabled, settings.Group = true, "checkin-fixture"
		_, err = model.SaveXingyaCheckinSettings(settings)
		require.NoError(t, err)
		for _, tc := range []struct {
			name, raw string
			request   dto.Request
			blocked   bool
		}{
			{"system prompt and tools", `{"tools":[{"type":"web_search"}],"enable_search":true}`, &dto.GeneralOpenAIRequest{}, false},
			{"remote response context", `{"previous_response_id":"fixture","input":[{"type":"input_file","file_id":"fixture"}]}`, &dto.OpenAIResponsesRequest{}, false},
			{"background response", `{"background":true}`, &dto.OpenAIResponsesRequest{}, true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
				ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(tc.raw))
				info := &relaycommon.RelayInfo{TokenQuotaSource: BillingSourceCheckin, TokenGroup: settings.Group, UsingGroup: settings.Group, Request: tc.request}
				err := PrepareCheckinRequest(ctx, info)
				common.CleanupBodyStorage(ctx)
				if tc.blocked {
					require.ErrorContains(t, err, "后台请求")
				} else {
					require.NoError(t, err)
				}
			})
		}
	})
	t.Run("fixed request fees do not depend on output limits or converted body size", func(t *testing.T) {
		user := model.User{Username: fmt.Sprintf("co-%d", time.Now().UnixNano()), AffCode: fmt.Sprintf("co-%d", time.Now().UnixNano()), Status: common.UserStatusEnabled}
		require.NoError(t, model.DB.Create(&user).Error)
		credit(user.Id, 2000)
		info := &relaycommon.RelayInfo{UserId: user.Id, RequestId: common.NewRequestId(), TokenQuotaSource: BillingSourceCheckin, TokenUnlimited: true, IsPlayground: true, ChannelMeta: &relaycommon.ChannelMeta{}, PriceData: hosttypes.PriceData{UsePrice: true, ModelPrice: 0.0004, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}}}
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
		require.Nil(t, PreConsumeBilling(ctx, 0, info))
		body := `{"messages":[{"role":"user","content":"text"}],"max_tokens":1280}`
		request := httptest.NewRequest("POST", "https://fixture.invalid", strings.NewReader(body))
		request.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(body)), nil }
		require.NoError(t, ReserveCheckinOutbound(info, request))
		body = `{"messages":[{"role":"user","content":"text"}],"max_tokens":10}`
		require.NoError(t, ReserveCheckinOutbound(info, request))
		wallet, err := model.GetXingyaCheckinWallet(user.Id)
		require.NoError(t, err)
		assert.Equal(t, 200, wallet.Reserved)
		body = `{"input":"text"}`
		require.NoError(t, ReserveCheckinOutbound(info, request), "fixed pricing does not need an output limit")
		require.NoError(t, SettleBilling(ctx, info, 200))
		wallet, err = model.GetXingyaCheckinWallet(user.Id)
		require.NoError(t, err)
		assert.Equal(t, 1800, wallet.Available)
	})
}
