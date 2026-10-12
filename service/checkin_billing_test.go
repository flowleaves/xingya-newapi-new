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
	"github.com/QuantumNous/new-api/setting/operation_setting"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
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
	for _, tc := range []struct {
		name                   string
		actual, budget, credit int
		fail, insufficient     bool
	}{
		{name: "ordinary balance zero and subscription-only preference still use checkin", actual: 100, budget: 300, credit: 1000},
		{name: "equal reservation is durably settled", actual: 300, budget: 300, credit: 1000},
		{name: "zero cost releases reservation", actual: 0, budget: 300, credit: 1000},
		{name: "insufficient checkin cannot fall back", budget: 300, credit: 100, insufficient: true},
		{name: "failed request refunds once", budget: 300, credit: 1000, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user := model.User{Username: fmt.Sprintf("cb-%d", time.Now().UnixNano()), Password: "fixture", AffCode: fmt.Sprintf("cb-%d", time.Now().UnixNano()), Status: common.UserStatusEnabled, Quota: 0}
			require.NoError(t, model.DB.Create(&user).Error)
			token := model.Token{UserId: user.Id, Key: common.NewRequestId(), Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true, QuotaSource: model.CheckinQuotaSource, Group: "checkin-fixture"}
			require.NoError(t, model.DB.Create(&token).Error)
			require.NoError(t, model.DB.Create(&model.XingyaCheckinWallet{UserID: user.Id, Available: tc.credit, Granted: tc.credit}).Error)
			info := &relaycommon.RelayInfo{UserId: user.Id, TokenId: token.Id, TokenKey: token.Key, TokenUnlimited: true, TokenQuotaSource: BillingSourceCheckin, TokenGroup: token.Group, UsingGroup: token.Group, RequestId: common.NewRequestId(), OriginModelName: "fixture", ChannelMeta: &relaycommon.ChannelMeta{}, CheckinMaxInputTokens: 100, CheckinMaxOutputTokens: 100, UserSetting: dto.UserSetting{BillingPreference: "subscription_only"}, PriceData: hosttypes.PriceData{UsePrice: true, ModelPrice: float64(tc.budget) / common.QuotaPerUnit, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}}}
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
	// This evaluates a maximum across token and request-priced branches, not
	// just the sample used for ordinary input-only estimation.
	info := &relaycommon.RelayInfo{TokenQuotaSource: BillingSourceCheckin, ChannelMeta: &relaycommon.ChannelMeta{}, CheckinMaxInputTokens: 100, CheckinMaxOutputTokens: 100, TieredBillingSnapshot: &billingexpr.BillingSnapshot{ExprString: `len < 1000 ? tier("short", fixed(0.01)) : tier("long", p*2+c*8)`, QuotaPerUnit: 500000, GroupRatio: 1}}
	budget, err := CheckinRequestBudget(info)
	require.NoError(t, err)
	assert.Equal(t, 5000, budget)
	t.Run("legacy request multipliers are included before reservation", func(t *testing.T) {
		info := &relaycommon.RelayInfo{TokenQuotaSource: BillingSourceCheckin, ChannelMeta: &relaycommon.ChannelMeta{}, CheckinMaxInputTokens: 100, CheckinMaxOutputTokens: 100, PriceData: hosttypes.PriceData{ModelRatio: 1, CompletionRatio: 1, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}}}
		info.PriceData.AddOtherRatio("fixture_multiplier", 2)
		budget, err := CheckinRequestBudget(info)
		require.NoError(t, err)
		assert.Equal(t, 400, budget)
	})
	t.Run("priced function tools cannot bypass the request budget", func(t *testing.T) {
		settings, err := model.GetXingyaCheckinSettings()
		require.NoError(t, err)
		original := settings
		settings.SpendEnabled = true
		if settings.Group == "" {
			settings.Group = "checkin-fixture"
		}
		settings, err = model.SaveXingyaCheckinSettings(settings)
		require.NoError(t, err)
		t.Cleanup(func() {
			original.Revision = settings.Revision
			_, err := model.SaveXingyaCheckinSettings(original)
			require.NoError(t, err)
		})
		operation_setting.SetToolPriceForTest("checkin_paid_function", 4)
		t.Cleanup(func() { operation_setting.DeleteToolPriceForTest("checkin_paid_function") })
		operation_setting.SetToolPriceForTest("checkin_local_function:billing-target*", 4)
		t.Cleanup(func() { operation_setting.DeleteToolPriceForTest("checkin_local_function:billing-target*") })
		for _, tc := range []struct {
			name string
			paid bool
		}{{"checkin_paid_function", true}, {"checkin_local_function", false}} {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			raw := fmt.Sprintf(`{"model":"fixture","messages":[{"role":"user","content":"hi"}],"max_tokens":10,"tools":[{"type":"function","function":{"name":%q,"parameters":{"type":"object"}}}]}`, tc.name)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(raw))
			maxOutput := uint(10)
			info := &relaycommon.RelayInfo{TokenQuotaSource: BillingSourceCheckin, TokenGroup: settings.Group, UsingGroup: settings.Group, OriginModelName: "fixture", Request: &dto.GeneralOpenAIRequest{MaxTokens: &maxOutput}}
			err := PrepareCheckinRequest(ctx, info)
			common.CleanupBodyStorage(ctx)
			if tc.paid {
				require.ErrorContains(t, err, "额外调用费用")
			} else {
				require.NoError(t, err)
				info.BillingModelName = "billing-target"
				_, err = CheckinRequestBudget(info)
				require.ErrorContains(t, err, "额外调用费用", "pricing aliases must not bypass the surcharge check")
			}
		}
		for _, extra := range []string{
			`"enable_search":true`,
			`"search_parameters":{"mode":"on"}`,
			`"functions":[{"name":"checkin_paid_function"}]`,
			`"messages":[{"role":"system","tools":[{"type":"function","function":{"name":"checkin_paid_function"}}]}]`,
		} {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{`+extra+`}`))
			info := &relaycommon.RelayInfo{TokenQuotaSource: BillingSourceCheckin, TokenGroup: settings.Group, UsingGroup: settings.Group, Request: &dto.GeneralOpenAIRequest{}}
			require.ErrorContains(t, PrepareCheckinRequest(ctx, info), "费用", extra)
			common.CleanupBodyStorage(ctx)
		}
	})
	t.Run("final converted payload is reserved before sending", func(t *testing.T) {
		user := model.User{Username: fmt.Sprintf("co-%d", time.Now().UnixNano()), AffCode: fmt.Sprintf("co-%d", time.Now().UnixNano()), Status: common.UserStatusEnabled}
		require.NoError(t, model.DB.Create(&user).Error)
		require.NoError(t, model.DB.Create(&model.XingyaCheckinWallet{UserID: user.Id, Available: 2000, Granted: 2000}).Error)
		info := &relaycommon.RelayInfo{UserId: user.Id, RequestId: common.NewRequestId(), TokenQuotaSource: BillingSourceCheckin, TokenUnlimited: true, IsPlayground: true, ChannelMeta: &relaycommon.ChannelMeta{}, CheckinMaxInputTokens: 10, CheckinMaxOutputTokens: 1, PriceData: hosttypes.PriceData{ModelRatio: 1, CompletionRatio: 1, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}}}
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
		require.Nil(t, PreConsumeBilling(ctx, 0, info))
		body := `{"messages":[{"role":"user","content":"text"}],"max_tokens":1280}`
		request := httptest.NewRequest("POST", "https://fixture.invalid", strings.NewReader(body))
		request.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(body)), nil }
		require.ErrorIs(t, ReserveCheckinOutbound(info, request), model.ErrCheckinInsufficient)
		body = `{"messages":[{"role":"user","content":"text"}],"max_tokens":10}`
		require.NoError(t, ReserveCheckinOutbound(info, request))
		wallet, err := model.GetXingyaCheckinWallet(user.Id)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, wallet.Reserved, len(body)+1024+10)
		body = `{"input":"text"}`
		require.Error(t, ReserveCheckinOutbound(info, request), "Codex-style dropped output limits cannot proceed")
		require.NoError(t, SettleBilling(ctx, info, 200))
		wallet, err = model.GetXingyaCheckinWallet(user.Id)
		require.NoError(t, err)
		assert.Equal(t, 1800, wallet.Available)
	})
}

func TestCheckinContentRejectsOpaqueCosts(t *testing.T) {
	for _, raw := range []string{`[{"type":"input_file","file_url":"https://fixture.invalid/file"}]`, `[{"role":"user","content":[{"type":"document","source":{"type":"url","url":"https://fixture.invalid/file"}}]}]`, `[{"type":"item_reference","id":"previous"}]`, `[{"type":"reasoning","encrypted_content":"hidden"}]`, `[{"role":"user","content":[{"type":"tool_result","content":[{"type":"image","source":{"type":"url"}}]}]}]`} {
		require.Error(t, validateCheckinContent(gjson.Parse(raw), 0))
	}
	for _, raw := range []string{
		`[{"type":"function_call_output","call_id":"fixture","output":[{"type":"input_image","image_url":"https://fixture.invalid/image"}]}]`,
		`[{"type":"function_call_output","call_id":"fixture","output":[{"type":"input_file","file_id":"fixture-file"}]}]`,
	} {
		require.Error(t, validateCheckinContent(gjson.Parse(raw), 0), "tool output blocks must obey the same media restrictions")
	}
	require.NoError(t, validateCheckinContent(gjson.Parse(`[{"type":"function_call_output","call_id":"fixture","output":"plain text"}]`), 0))
	require.NoError(t, validateCheckinContent(gjson.Parse(`[{"role":"user","content":[{"type":"text","text":"hi"}]},{"role":"assistant","content":[{"type":"tool_use","input":{"type":"custom-data"}}]}]`), 0))
}
