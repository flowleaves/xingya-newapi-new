package controller

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting"
	settingconfig "github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func TestXingyaCheckinAPIPostgres(t *testing.T) {
	dsn, logDSN := os.Getenv("TEST_XINGYA_POSTGRES_DSN"), os.Getenv("TEST_XINGYA_POSTGRES_LOG_DSN")
	if dsn == "" || logDSN == "" {
		t.Skip("isolated PostgreSQL main/log DSNs are required")
	}
	oldDB, oldLog, oldRedis, oldQuota := model.DB, model.LOG_DB, common.RedisEnabled, common.QuotaPerUnit
	oldMainType, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	oldGroups, oldRatios := setting.UserUsableGroups2JSONString(), ratio_setting.GroupRatio2JSONString()
	t.Cleanup(func() {
		model.DB, model.LOG_DB, common.RedisEnabled, common.QuotaPerUnit = oldDB, oldLog, oldRedis, oldQuota
		common.SetDatabaseTypes(oldMainType, oldLogType)
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(oldGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldRatios))
	})
	gin.SetMode(gin.TestMode)
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	common.RedisEnabled, common.QuotaPerUnit = false, 500000
	var err error
	model.DB, err = gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	require.NoError(t, err)
	// Initialize model's dialect-dependent column names through normal startup.
	// No migration runs on this temporary non-master initialization.
	t.Setenv("LOG_SQL_DSN", "")
	master := common.IsMasterNode
	common.IsMasterNode = false
	require.NoError(t, model.InitLogDB())
	common.IsMasterNode = master
	model.LOG_DB, err = gorm.Open(postgres.New(postgres.Config{DSN: logDSN, PreferSimpleProtocol: true}), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	require.NoError(t, err)
	for _, db := range []*gorm.DB{model.DB, model.LOG_DB} {
		pool, err := db.DB()
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, pool.Close()) })
	}
	require.NoError(t, model.DB.AutoMigrate(&model.Option{}, &model.User{}, &model.Token{}, &model.Checkin{}))
	require.NoError(t, model.LOG_DB.AutoMigrate(&model.Log{}, &model.AuditLog{}))
	require.NoError(t, model.InitXingyaTables(model.DB))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","checkin-fixture":"Check-in"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"checkin-fixture":1}`))
	user := model.User{Username: fmt.Sprintf("ca-%d", time.Now().UnixNano()), AffCode: fmt.Sprintf("ca-%d", time.Now().UnixNano()), Group: "default", Quota: 90000, Status: common.UserStatusEnabled}
	require.NoError(t, model.DB.Create(&user).Error)
	config, err := model.GetXingyaCheckinSettings()
	require.NoError(t, err)
	config.ClaimEnabled, config.SpendEnabled, config.MinReward, config.MaxReward, config.Group = true, true, 2, 2, "checkin-fixture"
	config, err = model.SaveXingyaCheckinSettings(config)
	require.NoError(t, err)

	t.Run("first status read returns an empty wallet and no expiry for a user without awards", func(t *testing.T) {
		ctx, recorder := newAuthenticatedContext(t, http.MethodGet, "/api/user/xingya-checkin", nil, user.Id)
		GetXingyaCheckinStatus(ctx)
		body := recorder.Body.String()
		require.True(t, gjson.Get(body, "success").Bool(), body)
		assert.False(t, gjson.Get(body, "data.today_record.checked_in").Bool())
		assert.Empty(t, gjson.Get(body, "data.records").Array())
		assert.Zero(t, gjson.Get(body, "data.wallet.available").Int())
		assert.Zero(t, gjson.Get(body, "data.wallet.expired").Int())
		assert.Zero(t, gjson.Get(body, "data.expiry.next_expires_at").Int())
		assert.False(t, gjson.Get(body, "data.expiry.opening").Exists())
	})
	t.Run("browser and key balance APIs exclude expired credit while preserving legacy rewards and history", func(t *testing.T) {
		expiredUser := model.User{Username: fmt.Sprintf("expired-%d", time.Now().UnixNano()), AffCode: fmt.Sprintf("expired-%d", time.Now().UnixNano()), Group: "default", Quota: 90000, Status: common.UserStatusEnabled}
		require.NoError(t, model.DB.Create(&expiredUser).Error)
		awardedAt := time.Now().Add(-31 * 24 * time.Hour)
		grant, _, err := model.ClaimXingyaCheckin(expiredUser.Id, awardedAt)
		require.NoError(t, err)
		legacy := model.Checkin{UserId: expiredUser.Id, CheckinDate: model.CheckinBusinessTime(awardedAt.Add(time.Hour)).Format("2006-01-02"), QuotaAwarded: 777}
		require.NoError(t, model.DB.Create(&legacy).Error)
		token, _, err := model.CreateCheckinToken(expiredUser.Id, config.Group)
		require.NoError(t, err)
		ctx, recorder := newAuthenticatedContext(t, http.MethodGet, "/api/user/xingya-checkin?month="+grant.CheckinDate[:7], nil, expiredUser.Id)
		GetXingyaCheckinStatus(ctx)
		body := recorder.Body.String()
		require.True(t, gjson.Get(body, "success").Bool(), body)
		assert.Zero(t, gjson.Get(body, "data.wallet.available").Int())
		assert.Equal(t, int64(10000), gjson.Get(body, "data.wallet.expired").Int())
		assert.Zero(t, gjson.Get(body, "data.expiry.next_expires_at").Int())
		records := gjson.Get(body, "data.records").Array()
		require.Len(t, records, 2)
		assert.Equal(t, "checkin", records[0].Get("source").String())
		assert.Equal(t, awardedAt.Add(model.CheckinCreditLifetime).Unix(), records[0].Get("credit.expires_at").Int())
		assert.Equal(t, int64(10000), records[0].Get("credit.expired").Int())
		assert.Equal(t, "legacy", records[1].Get("source").String())
		assert.False(t, records[1].Get("credit").Exists())
		ctx, recorder = newAuthenticatedContext(t, http.MethodGet, "/api/usage/token", nil, expiredUser.Id)
		ctx.Request.Header.Set("Authorization", "Bearer "+token.GetFullKey())
		GetTokenUsage(ctx)
		assert.Zero(t, gjson.Get(recorder.Body.String(), "data.total_available").Int())
		assert.Equal(t, int64(10000), gjson.Get(recorder.Body.String(), "data.total_expired").Int())
		ctx, recorder = newAuthenticatedContext(t, http.MethodGet, "/v1/dashboard/billing/credit_grants", nil, expiredUser.Id)
		ctx.Set("token_id", token.Id)
		GetTokenStatus(ctx)
		assert.Zero(t, gjson.Get(recorder.Body.String(), "total_available").Int())
		assert.Equal(t, int64(10000), gjson.Get(recorder.Body.String(), "total_expired").Int())
		request := model.XingyaCheckinSpend{RequestID: common.NewRequestId(), UserID: expiredUser.Id, TokenID: token.Id, Group: token.Group}
		require.ErrorIs(t, model.ReserveCheckinQuota(request, 1000), model.ErrCheckinInsufficient)
		var saved model.User
		require.NoError(t, model.DB.First(&saved, expiredUser.Id).Error)
		assert.Equal(t, 90000, saved.Quota)
	})
	t.Run("claims credit only the dedicated wallet and key response identifies a masked reusable token", func(t *testing.T) {
		ctx, recorder := newAuthenticatedContext(t, http.MethodPost, "/api/user/xingya-daily-claim", nil, user.Id)
		ClaimXingyaDailyCredit(ctx)
		assert.True(t, gjson.Get(recorder.Body.String(), "success").Bool(), recorder.Body.String())
		assert.Equal(t, int64(10000), gjson.Get(recorder.Body.String(), "data.quota_awarded").Int())
		ctx, recorder = newAuthenticatedContext(t, http.MethodPost, "/api/user/xingya-checkin/key", nil, user.Id)
		CreateXingyaCheckinKey(ctx)
		require.True(t, gjson.Get(recorder.Body.String(), "success").Bool(), recorder.Body.String())
		id := gjson.Get(recorder.Body.String(), "data.token.id").Int()
		var token model.Token
		require.NoError(t, model.DB.First(&token, id).Error)
		assert.Equal(t, "签到-"+user.Username, token.Name)
		assert.Equal(t, model.CheckinQuotaSource, token.QuotaSource)
		assert.NotContains(t, recorder.Body.String(), token.GetFullKey())
		ctx, recorder = newAuthenticatedContext(t, http.MethodPost, "/api/user/xingya-checkin/key", nil, user.Id)
		CreateXingyaCheckinKey(ctx)
		assert.Equal(t, id, gjson.Get(recorder.Body.String(), "data.token.id").Int())
		assert.True(t, gjson.Get(recorder.Body.String(), "data.reused").Bool())
		var saved model.User
		require.NoError(t, model.DB.First(&saved, user.Id).Error)
		assert.Equal(t, 90000, saved.Quota)
	})
	t.Run("a selected past month does not hide today's award and paused history remains readable", func(t *testing.T) {
		ctx, recorder := newAuthenticatedContext(t, http.MethodGet, "/api/user/xingya-checkin?month=2025-01", nil, user.Id)
		GetXingyaCheckinStatus(ctx)
		assert.True(t, gjson.Get(recorder.Body.String(), "data.today_record.checked_in").Bool())
		assert.Equal(t, "2025-01", gjson.Get(recorder.Body.String(), "data.month").String())
		assert.Equal(t, int64(10000), gjson.Get(recorder.Body.String(), "data.wallet.available").Int())
		config.ClaimEnabled = false
		config, err = model.SaveXingyaCheckinSettings(config)
		require.NoError(t, err)
		ctx, recorder = newAuthenticatedContext(t, http.MethodGet, "/api/user/xingya-checkin", nil, user.Id)
		GetXingyaCheckinStatus(ctx)
		assert.True(t, gjson.Get(recorder.Body.String(), "success").Bool())
		assert.False(t, gjson.Get(recorder.Body.String(), "data.claim_enabled").Bool())
		assert.Equal(t, int64(10000), gjson.Get(recorder.Body.String(), "data.wallet.available").Int())
	})
	t.Run("retired endpoint returns gone and stale settings cannot overwrite a newer revision", func(t *testing.T) {
		ctx, recorder := newAuthenticatedContext(t, http.MethodPost, "/api/user/checkin", nil, user.Id)
		DoCheckin(ctx)
		assert.Equal(t, http.StatusGone, recorder.Code)
		stale := config
		stale.Revision--
		ctx, recorder = newAuthenticatedContext(t, http.MethodPut, "/api/xingya/checkin/settings", stale, user.Id)
		UpdateXingyaCheckinSettings(ctx)
		assert.Equal(t, http.StatusConflict, recorder.Code)
	})
	t.Run("ordinary token edits cannot change dedicated funding or its fixed group", func(t *testing.T) {
		var token model.Token
		require.NoError(t, model.DB.Where("user_id = ? AND quota_source = ?", user.Id, model.CheckinQuotaSource).Take(&token).Error)
		for _, change := range []struct{ source, group string }{{"wallet", token.Group}, {model.CheckinQuotaSource, "default"}} {
			ctx, recorder := newAuthenticatedContext(t, http.MethodPut, "/api/token/", map[string]any{"id": token.Id, "name": token.Name, "quota_source": change.source, "group": change.group, "expired_time": -1, "unlimited_quota": true}, user.Id)
			common.SetContextKey(ctx, constant.ContextKeyUserGroup, user.Group)
			UpdateToken(ctx)
			assert.False(t, gjson.Get(recorder.Body.String(), "success").Bool())
		}
	})
	t.Run("dedicated token usage shows finite wallet funds and respects the separate key limit", func(t *testing.T) {
		var token model.Token
		require.NoError(t, model.DB.Where("user_id = ? AND quota_source = ?", user.Id, model.CheckinQuotaSource).Take(&token).Error)
		request := model.XingyaCheckinSpend{RequestID: common.NewRequestId(), UserID: user.Id, TokenID: token.Id, Group: token.Group}
		require.NoError(t, model.ReserveCheckinQuota(request, 3000))
		require.NoError(t, model.FinalizeCheckinSpend(user.Id, request.RequestID, 1200, false))
		for _, limited := range []bool{false, true} {
			require.NoError(t, model.DB.Model(&token).Updates(map[string]any{"unlimited_quota": !limited, "remain_quota": 700}).Error)
			ctx, recorder := newAuthenticatedContext(t, http.MethodGet, "/api/usage/token", nil, user.Id)
			ctx.Request.Header.Set("Authorization", "Bearer "+token.GetFullKey())
			GetTokenUsage(ctx)
			body := recorder.Body.String()
			assert.Equal(t, int64(10000), gjson.Get(body, "data.total_granted").Int())
			assert.Equal(t, int64(1200), gjson.Get(body, "data.total_used").Int())
			assert.False(t, gjson.Get(body, "data.unlimited_quota").Bool())
			want := int64(8800)
			if limited {
				want = 700
			}
			assert.Equal(t, want, gjson.Get(body, "data.total_available").Int())
			ctx, recorder = newAuthenticatedContext(t, http.MethodGet, "/v1/dashboard/billing/credit_grants", nil, user.Id)
			ctx.Set("token_id", token.Id)
			GetTokenStatus(ctx)
			assert.Equal(t, want, gjson.Get(recorder.Body.String(), "total_available").Int())
		}
	})
	t.Run("supported protocols and failed retries reconcile without invite progress", func(t *testing.T) {
		require.NoError(t, i18n.Init())
		require.NoError(t, model.DB.AutoMigrate(&model.Channel{}, &model.Ability{}))
		oldMemory, oldBatch, oldLogEnabled, oldRetry := common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.RetryTimes
		oldTimeout := constant.StreamingTimeout
		constant.StreamingTimeout = 30
		oldPrices, oldCompletions := ratio_setting.ModelRatio2JSONString(), ratio_setting.CompletionRatio2JSONString()
		oldFixedPrices := ratio_setting.ModelPrice2JSONString()
		common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.RetryTimes = false, false, true, 1
		t.Cleanup(func() {
			constant.StreamingTimeout = oldTimeout
			common.MemoryCacheEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.RetryTimes = oldMemory, oldBatch, oldLogEnabled, oldRetry
			require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(oldPrices))
			require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(oldFixedPrices))
			require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(oldCompletions))
		})
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"checkin-fixture-model":1}`))
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"checkin-fixture-model":0.00022}`))
		require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(`{"checkin-fixture-model":1}`))
		var attempts atomic.Int32
		var tierInput atomic.Int32
		var observedReserve atomic.Int64
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attempts.Add(1)
			raw, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			if input := tierInput.Load(); input > 0 {
				wallet, err := model.GetXingyaCheckinWallet(user.Id)
				require.NoError(t, err)
				observedReserve.Store(int64(wallet.Reserved))
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"id":"tier_fixture","model":"checkin-fixture-model","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":%d,"completion_tokens":10,"total_tokens":%d}}`, input, input+10)
				return
			}
			if gjson.GetBytes(raw, "model").String() == "checkin-fixture-failure" {
				w.WriteHeader(http.StatusServiceUnavailable)
				io.WriteString(w, `{"error":{"message":"fixture unavailable","type":"server_error"}}`)
				return
			}
			stream := gjson.GetBytes(raw, "stream").Bool()
			if stream {
				w.Header().Set("Content-Type", "text/event-stream")
			} else {
				w.Header().Set("Content-Type", "application/json")
			}
			if name := gjson.GetBytes(raw, "model").String(); name == "checkin-fixture-truncated" || name == "checkin-fixture-cancel" {
				// An observed usage snapshot remains billable even without the
				// terminal marker or after the downstream client disconnects.
				io.WriteString(w, "data: {\"id\":\"partial_fixture\",\"model\":\"checkin-fixture-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":10,\"total_tokens\":110}}\n\n")
				if name == "checkin-fixture-cancel" {
					io.WriteString(w, "data: {\"id\":\"partial_fixture\",\"choices\":[]}\n\n")
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				}
				return
			}
			if r.URL.Path == "/v1/embeddings" {
				io.WriteString(w, `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2]}],"model":"checkin-fixture-model","usage":{"prompt_tokens":100,"total_tokens":100}}`)
			} else if r.URL.Path == "/v1/responses" {
				if stream {
					io.WriteString(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_fixture\",\"model\":\"checkin-fixture-model\"}}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":100,\"output_tokens\":10,\"total_tokens\":110}}}\n\n")
				} else {
					io.WriteString(w, `{"id":"resp_fixture","model":"checkin-fixture-model","status":"completed","output":[],"usage":{"input_tokens":100,"output_tokens":10,"total_tokens":110}}`)
				}
			} else if stream {
				io.WriteString(w, "data: {\"id\":\"chat_fixture\",\"model\":\"checkin-fixture-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\ndata: {\"id\":\"chat_fixture\",\"model\":\"checkin-fixture-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":10,\"total_tokens\":110}}\n\ndata: [DONE]\n\n")
			} else {
				io.WriteString(w, `{"id":"chat_fixture","model":"checkin-fixture-model","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110}}`)
			}
		}))
		t.Cleanup(upstream.Close)
		channel := model.Channel{Type: constant.ChannelTypeOpenAI, Name: "checkin-fixture", Key: "fixture-upstream-credential", BaseURL: common.GetPointer(upstream.URL), Status: common.ChannelStatusEnabled, Models: "checkin-fixture-model", Group: config.Group}
		require.NoError(t, model.DB.Create(&channel).Error)
		require.NoError(t, model.DB.Create(&model.Ability{Group: config.Group, Model: "checkin-fixture-model", ChannelId: channel.Id, Enabled: true}).Error)
		t.Cleanup(func() {
			model.DB.Where("channel_id = ?", channel.Id).Delete(&model.Ability{})
			model.DB.Delete(&channel)
		})
		var token model.Token
		require.NoError(t, model.DB.Where("user_id = ? AND quota_source = ?", user.Id, model.CheckinQuotaSource).Take(&token).Error)
		require.NoError(t, model.DB.Model(&token).Update("unlimited_quota", true).Error)
		reward := model.XingyaInviteRewardPending{InviteeId: user.Id, InviterId: user.Id, State: model.InviteRewardStatePending}
		require.NoError(t, model.DB.Create(&reward).Error)
		router := gin.New()
		router.Use(func(c *gin.Context) { c.Set(common.RequestIdKey, common.NewRequestId()); c.Next() }, middleware.BodyStorageCleanup(), middleware.TokenAuth(), middleware.Distribute())
		for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages", "/v1/embeddings"} {
			router.POST(path, func(c *gin.Context) {
				format := types.RelayFormatOpenAI
				if c.Request.URL.Path == "/v1/responses" {
					format = types.RelayFormatOpenAIResponses
				} else if c.Request.URL.Path == "/v1/messages" {
					format = types.RelayFormatClaude
				} else if c.Request.URL.Path == "/v1/embeddings" {
					format = types.RelayFormatEmbedding
				}
				Relay(c, format)
			})
		}
		for _, tc := range []struct {
			name, path, body, keySuffix string
			status                      int
		}{
			{"remote tool output", "/v1/responses", `{"model":"checkin-fixture-model","input":[{"type":"function_call_output","call_id":"fixture","output":[{"type":"input_image","image_url":"https://fixture.invalid/image"}]}],"max_output_tokens":32}`, "", http.StatusBadRequest},
			{"hosted search", "/v1/chat/completions", `{"model":"checkin-fixture-model","messages":[{"role":"user","content":"hi"}],"max_tokens":32,"web_search_options":{}}`, "", http.StatusBadRequest},
			{"channel override", "/v1/chat/completions", `{"model":"checkin-fixture-model","messages":[{"role":"user","content":"hi"}],"max_tokens":32}`, "-1", http.StatusForbidden},
		} {
			t.Run(tc.name+" is rejected before upstream without moving funds", func(t *testing.T) {
				before, err := model.GetXingyaCheckinWallet(user.Id)
				require.NoError(t, err)
				request := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Authorization", "Bearer "+token.GetFullKey()+tc.keySuffix)
				recorder := httptest.NewRecorder()
				router.ServeHTTP(recorder, request)
				assert.Equal(t, tc.status, recorder.Code, recorder.Body.String())
				assert.Zero(t, attempts.Load())
				after, err := model.GetXingyaCheckinWallet(user.Id)
				require.NoError(t, err)
				assert.Equal(t, before, after)
			})
		}
		for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages", "/v1/embeddings"} {
			for _, stream := range []bool{false, true} {
				if path == "/v1/embeddings" && stream {
					continue
				}
				body := map[string]any{"model": "checkin-fixture-model", "stream": stream}
				charge := 110
				if path == "/v1/responses" {
					body["input"] = "hi"
				} else if path == "/v1/embeddings" {
					body["input"] = "hi"
					charge = 110
				} else {
					body["messages"] = []map[string]string{{"role": "user", "content": "hi"}}
				}
				raw, err := common.Marshal(body)
				require.NoError(t, err)
				before, err := model.GetXingyaCheckinWallet(user.Id)
				require.NoError(t, err)
				request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(raw)))
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Authorization", "Bearer "+token.GetFullKey())
				recorder := httptest.NewRecorder()
				router.ServeHTTP(recorder, request)
				require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
				after, err := model.GetXingyaCheckinWallet(user.Id)
				require.NoError(t, err)
				assert.Equal(t, charge, before.Available-after.Available, path)
				assert.Zero(t, after.Reserved)
				var log model.Log
				require.NoError(t, model.LOG_DB.Where("user_id = ? AND type = ?", user.Id, model.LogTypeConsume).Order("id DESC").Take(&log).Error)
				assert.Equal(t, "checkin", gjson.Get(log.Other, "billing_source").String())
				assert.False(t, gjson.Get(log.Other, "invite_qualifying").Bool())
			}
		}
		assert.Equal(t, int32(7), attempts.Load())
		for _, mode := range []string{"truncated", "cancel"} {
			t.Run(mode+" stream preserves measured checkin consumption", func(t *testing.T) {
				require.NoError(t, model.DB.Model(&channel).Update("model_mapping", fmt.Sprintf(`{"checkin-fixture-model":"checkin-fixture-%s"}`, mode)).Error)
				before, err := model.GetXingyaCheckinWallet(user.Id)
				require.NoError(t, err)
				request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"checkin-fixture-model","messages":[{"role":"user","content":"hi"}],"max_tokens":32,"stream":true}`))
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Authorization", "Bearer "+token.GetFullKey())
				recorder := httptest.NewRecorder()
				if mode == "cancel" {
					ctx, cancel := context.WithCancel(request.Context())
					defer cancel()
					router.ServeHTTP(&checkinCancelRecorder{ResponseRecorder: recorder, cancel: cancel}, request.WithContext(ctx))
					require.ErrorIs(t, ctx.Err(), context.Canceled)
				} else {
					router.ServeHTTP(recorder, request)
				}
				require.Contains(t, recorder.Body.String(), "hello")
				after, err := model.GetXingyaCheckinWallet(user.Id)
				require.NoError(t, err)
				assert.Equal(t, 110, before.Available-after.Available)
				assert.Zero(t, after.Reserved)
				var log model.Log
				require.NoError(t, model.LOG_DB.Where("user_id = ? AND type = ?", user.Id, model.LogTypeConsume).Order("id DESC").Take(&log).Error)
				assert.False(t, gjson.Get(log.Other, "invite_qualifying").Bool())
				assert.Equal(t, "checkin", gjson.Get(log.Other, "billing_source").String())
			})
		}
		assert.Equal(t, int32(9), attempts.Load())
		// A final upstream failure releases the one shared reservation after all
		// attempts. No retry can switch the token's funding source or fixed group.
		require.NoError(t, model.DB.Model(&channel).Update("model_mapping", `{"checkin-fixture-model":"checkin-fixture-failure"}`).Error)
		beforeFailure, err := model.GetXingyaCheckinWallet(user.Id)
		require.NoError(t, err)
		failedRequest := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"checkin-fixture-model","messages":[{"role":"user","content":"hi"}],"max_tokens":32}`))
		failedRequest.Header.Set("Content-Type", "application/json")
		failedRequest.Header.Set("Authorization", "Bearer "+token.GetFullKey())
		failedResponse := httptest.NewRecorder()
		router.ServeHTTP(failedResponse, failedRequest)
		assert.Equal(t, http.StatusServiceUnavailable, failedResponse.Code, failedResponse.Body.String())
		assert.Equal(t, int32(11), attempts.Load())
		require.Eventually(t, func() bool {
			wallet, err := model.GetXingyaCheckinWallet(user.Id)
			return err == nil && wallet.Reserved == 0 && wallet.Available == beforeFailure.Available && wallet.Consumed == beforeFailure.Consumed
		}, 3*time.Second, 10*time.Millisecond)
		var progress model.XingyaInviteRewardPending
		require.NoError(t, model.DB.First(&progress, reward.Id).Error)
		assert.Zero(t, progress.QualifyingCalls)
		assert.Zero(t, progress.QualifyingQuota)
		var saved model.User
		require.NoError(t, model.DB.First(&saved, user.Id).Error)
		assert.Equal(t, 90000, saved.Quota)
		assert.Zero(t, saved.UsedQuota)
		t.Run("context tiers reserve the maximum before upstream and settle actual input", func(t *testing.T) {
			previous := map[string]string{}
			require.NoError(t, settingconfig.GlobalConfig.SaveToDB(func(key, value string) error { previous[key] = value; return nil }))
			t.Cleanup(func() { require.NoError(t, settingconfig.GlobalConfig.LoadFromDB(previous)) })
			require.NoError(t, settingconfig.GlobalConfig.LoadFromDB(map[string]string{
				"billing_setting.billing_mode": `{"checkin-fixture-model":"tiered_expr"}`,
				"billing_setting.billing_expr": `{"checkin-fixture-model":"len <= 250000 ? tier(\"short\", fixed(0.01)) : tier(\"long\", fixed(0.02))"}`,
			}))
			require.NoError(t, model.DB.Model(&channel).Update("model_mapping", "").Error)
			for _, tc := range []struct{ input, credit, charge, status int }{
				{200000, 10000, 5000, http.StatusOK},
				{250000, 10000, 5000, http.StatusOK},
				{250001, 10000, 10000, http.StatusOK},
				{260000, 5000, 0, http.StatusForbidden},
			} {
				t.Run(fmt.Sprintf("input-%d-balance-%d", tc.input, tc.credit), func(t *testing.T) {
					require.NoError(t, model.DB.Model(&model.XingyaCheckinWallet{}).Where("user_id = ?", user.Id).Updates(map[string]any{"available": tc.credit, "reserved": 0, "granted": tc.credit, "consumed": 0}).Error)
					require.NoError(t, model.DB.Model(&model.XingyaCheckinCreditLot{}).Where("user_id = ?", user.Id).Updates(map[string]any{"available": tc.credit, "reserved": 0, "granted": tc.credit, "consumed": 0}).Error)
					tierInput.Store(int32(tc.input))
					observedReserve.Store(0)
					beforeAttempts := attempts.Load()
					request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"checkin-fixture-model","messages":[{"role":"user","content":"hi"}]}`))
					request.Header.Set("Content-Type", "application/json")
					request.Header.Set("Authorization", "Bearer "+token.GetFullKey())
					recorder := httptest.NewRecorder()
					router.ServeHTTP(recorder, request)
					require.Equal(t, tc.status, recorder.Code, recorder.Body.String())
					if tc.status == http.StatusOK {
						assert.Equal(t, beforeAttempts+1, attempts.Load())
						assert.Equal(t, int64(10000), observedReserve.Load())
					} else {
						assert.Equal(t, beforeAttempts, attempts.Load(), "insufficient balance must not call upstream")
					}
					wallet, err := model.GetXingyaCheckinWallet(user.Id)
					require.NoError(t, err)
					assert.Equal(t, tc.credit-tc.charge, wallet.Available)
					assert.Equal(t, tc.charge, wallet.Consumed)
					assert.Zero(t, wallet.Reserved)
				})
			}
		})
	})
	// Gin auth middleware is verified separately with live JWT sessions and PATs.
	// These handler tests deliberately initialize the authorized identity.
}

// A real client disconnects after receiving a chunk, rather than before auth or
// the upstream call. This recorder triggers that boundary deterministically.
type checkinCancelRecorder struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

func (r *checkinCancelRecorder) Flush() {
	r.ResponseRecorder.Flush()
	if r.Body.Len() > 0 {
		r.cancel()
	}
}
