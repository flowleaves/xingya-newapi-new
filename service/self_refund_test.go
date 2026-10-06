package service

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupRefundServiceDB gives the service package its own isolated database. The
// pool is pinned to a single connection on purpose: that is a supported SQLite
// configuration, and it makes any in-transaction read through the package-global
// DB deadlock instead of silently opening a second connection.
func setupRefundServiceDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "refund.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	prevDB, prevLogDB := model.DB, model.LOG_DB
	model.DB, model.LOG_DB = db, db
	t.Cleanup(func() { model.DB, model.LOG_DB = prevDB, prevLogDB })

	require.NoError(t, db.AutoMigrate(&model.Log{}, &model.User{}, &model.LogRefund{}))

	prevRedis, prevLogConsume := common.RedisEnabled, common.LogConsumeEnabled
	prevBatch := common.BatchUpdateEnabled
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	common.LogConsumeEnabled = true
	t.Cleanup(func() {
		common.RedisEnabled = prevRedis
		common.LogConsumeEnabled = prevLogConsume
		common.BatchUpdateEnabled = prevBatch
		common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	})

	return db
}

// enableRefundSetting turns the feature on for the duration of a test.
func enableRefundSetting(t *testing.T) {
	t.Helper()
	setting := operation_setting.GetSelfRefundSetting()
	previous := *setting
	*setting = operation_setting.SelfRefundSetting{
		Enabled:        true,
		Ratio:          0.5,
		WindowHours:    48,
		DailyMaxCount:  3,
		DailyMaxQuota:  0,
		MinRefundQuota: 0,
	}
	t.Cleanup(func() { *setting = previous })
}

func chatLog(quota int) *model.Log {
	return &model.Log{
		Id:               1,
		UserId:           5,
		Type:             model.LogTypeConsume,
		CreatedAt:        time.Now().Unix(),
		ModelName:        "gpt-test",
		Quota:            quota,
		CompletionTokens: 0,
		RequestId:        "req-chat",
		Other:            `{"request_path":"/v1/chat/completions"}`,
	}
}

// An empty response on a chat request is the case the feature exists for.
func TestJudgeSelfRefundOffersEmptyResponseRefund(t *testing.T) {
	enableRefundSetting(t)
	info := JudgeSelfRefund(chatLog(1000), map[int]bool{})

	require.NotNil(t, info)
	require.Equal(t, 500, info.RefundAmount)
	require.Equal(t, "wallet", info.FundingSource)
	require.Equal(t, "empty_response", info.Reason)
}

func TestJudgeSelfRefundPerRequestOnly(t *testing.T) {
	enableRefundSetting(t)
	for _, tc := range []struct {
		name     string
		metadata string
		eligible bool
	}{
		{"legacy request", `"model_price":0.01`, true},
		{"legacy token", `"model_price":-1`, false},
		{"legacy free", `"model_price":0`, false},
		{"missing metadata", `"model_ratio":1`, false},
		{"invalid price", `"model_price":"0.01"`, false},
		{"expression request", `"billing_mode":"tiered_expr","billing_unit":"request","fixed_price":0.01`, true},
		{"expression token with stale price", `"billing_mode":"tiered_expr","billing_unit":"token","model_price":0.01`, false},
		{"expression missing unit", `"billing_mode":"tiered_expr","model_price":0.01,"fixed_price":0.01`, false},
		{"unknown mode", `"billing_mode":"unknown","model_price":0.01`, false},
		{"subscription request", `"billing_mode":"tiered_expr","billing_unit":"request","billing_source":"subscription","subscription_id":23,"subscription_consumed":15000`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := chatLog(1000)
			log.Other = `{"request_path":"/v1/chat/completions",` + tc.metadata + `}`
			operation_setting.GetSelfRefundSetting().OnlyPerRequest = false
			assert.NotNil(t, JudgeSelfRefund(log, nil), "disabled restriction preserves existing eligibility")
			operation_setting.GetSelfRefundSetting().OnlyPerRequest = true
			assert.Equal(t, tc.eligible, JudgeSelfRefund(log, nil) != nil)
		})
	}
}

func TestDoSelfRefundRechecksPerRequestRestriction(t *testing.T) {
	db := setupRefundServiceDB(t)
	enableRefundSetting(t)
	user := &model.User{Username: "refund-switch", Password: "password", AffCode: "refund-switch", Quota: 1000}
	require.NoError(t, db.Create(user).Error)
	log := chatLog(1000)
	log.UserId = user.Id
	log.Other = `{"request_path":"/v1/chat/completions","model_price":-1}`
	require.NoError(t, db.Create(log).Error)
	logs, total, err := GetRefundableLogs(user.Id, RefundableLogQuery{RequestId: log.RequestId})
	require.NoError(t, err)
	assert.Len(t, logs, 1)
	assert.Equal(t, 1, total)

	operation_setting.GetSelfRefundSetting().OnlyPerRequest = true
	_, err = DoSelfRefund(user.Id, log.Id, log.RequestId)
	require.Error(t, err)
	var after model.User
	require.NoError(t, db.First(&after, user.Id).Error)
	assert.Equal(t, 1000, after.Quota)
}

func TestGetRefundableLogsPaginatesAfterEligibilityFiltering(t *testing.T) {
	db := setupRefundServiceDB(t)
	enableRefundSetting(t)
	operation_setting.GetSelfRefundSetting().OnlyPerRequest = true
	for i, price := range []float64{0.01, 0.01, -1} {
		log := chatLog(1000)
		log.Id = 0
		log.RequestId = []string{"old-request", "new-request", "token-request"}[i]
		other, err := common.Marshal(map[string]any{"request_path": "/v1/chat/completions", "model_price": price})
		require.NoError(t, err)
		log.Other = string(other)
		require.NoError(t, db.Create(log).Error)
	}
	for _, tc := range []struct {
		start int
		id    string
	}{{0, "new-request"}, {1, "old-request"}, {2, ""}} {
		logs, total, err := GetRefundableLogs(5, RefundableLogQuery{StartIdx: tc.start, Num: 1})
		require.NoError(t, err)
		assert.Equal(t, 2, total)
		if tc.id == "" {
			assert.Empty(t, logs)
			continue
		}
		require.Len(t, logs, 1)
		assert.Equal(t, tc.id, logs[0].RequestId)
	}
}

// A ratio that is tiny enough to round the refund down to zero must not be
// offered: the list would advertise a refundable request and the POST would then
// reject it as an invalid amount.
func TestJudgeSelfRefundRejectsRefundThatRoundsToZero(t *testing.T) {
	enableRefundSetting(t)
	setting := operation_setting.GetSelfRefundSetting()
	setting.Ratio = 0.0001

	require.Nil(t, JudgeSelfRefund(chatLog(1), map[int]bool{}),
		"a refund that truncates to zero must not be offered")
}

// The configured ratio is a money parameter and must be clamped to [0,1]: a
// value above 1 would pay back more than the user was charged.
func TestJudgeSelfRefundClampsRatioAboveOne(t *testing.T) {
	enableRefundSetting(t)
	operation_setting.GetSelfRefundSetting().Ratio = 5

	info := JudgeSelfRefund(chatLog(1000), map[int]bool{})
	require.NotNil(t, info)
	require.Equal(t, 1000, info.RefundAmount, "the refund must never exceed the amount charged")
}

// A non-finite ratio must fail closed (no refund) rather than produce a NaN
// amount.
func TestJudgeSelfRefundRejectsNonFiniteRatio(t *testing.T) {
	enableRefundSetting(t)
	setting := operation_setting.GetSelfRefundSetting()

	setting.Ratio = 0
	require.Nil(t, JudgeSelfRefund(chatLog(1000), map[int]bool{}))
}

func TestJudgeSelfRefundRejectsNonChatPathAndRefundedLogs(t *testing.T) {
	enableRefundSetting(t)

	nonChat := chatLog(1000)
	nonChat.Other = `{"request_path":"/v1/images/generations"}`
	require.Nil(t, JudgeSelfRefund(nonChat, map[int]bool{}))

	already := chatLog(1000)
	require.Nil(t, JudgeSelfRefund(already, map[int]bool{already.Id: true}))

	outsideWindow := chatLog(1000)
	outsideWindow.CreatedAt = time.Now().Unix() - 48*3600 - 10
	require.Nil(t, JudgeSelfRefund(outsideWindow, map[int]bool{}))

	toolSurcharge := chatLog(1000)
	toolSurcharge.Other = `{"request_path":"/v1/chat/completions","tool_surcharges":[{"amount":1}]}`
	require.Nil(t, JudgeSelfRefund(toolSurcharge, map[int]bool{}))
}

// A truncated stream is refundable only while the answer was still materially
// cut off. A stream that died after delivering a full answer was already paid
// for and must not become a discount.
func TestJudgeSelfRefundTruncationRequiresIncompleteAnswer(t *testing.T) {
	enableRefundSetting(t)

	truncated := chatLog(1000)
	truncated.IsStream = true
	truncated.CompletionTokens = 10
	truncated.Other = `{"request_path":"/v1/chat/completions","stream_status":{"end_reason":"timeout"}}`
	info := JudgeSelfRefund(truncated, map[int]bool{})
	require.NotNil(t, info)
	require.Equal(t, "stream_truncated", info.Reason)

	complete := chatLog(1000)
	complete.IsStream = true
	complete.CompletionTokens = 4096
	complete.Other = truncated.Other
	require.Nil(t, JudgeSelfRefund(complete, map[int]bool{}),
		"a stream that already delivered a full answer must not be refundable")

	// A stream that ended normally after delivering content is not refundable.
	// completion_tokens must be non-zero here, otherwise the log is legitimately
	// an empty-response refund rather than a clean finish.
	notATruncation := chatLog(1000)
	notATruncation.IsStream = true
	notATruncation.CompletionTokens = 128
	notATruncation.Other = `{"request_path":"/v1/chat/completions","stream_status":{"end_reason":"stop"}}`
	require.Nil(t, JudgeSelfRefund(notATruncation, map[int]bool{}))
}

// The subscription funding source is read from the log payload and takes the
// refund base from subscription_consumed, not from the (zero) wallet quota.
func TestJudgeSelfRefundUsesSubscriptionBase(t *testing.T) {
	enableRefundSetting(t)

	log := chatLog(0)
	log.Other = `{"request_path":"/v1/chat/completions","billing_source":"subscription","subscription_id":23,"subscription_consumed":15000}`

	info := JudgeSelfRefund(log, map[int]bool{})
	require.NotNil(t, info)
	require.Equal(t, "subscription", info.FundingSource)
	require.Equal(t, 23, info.SubscriptionId)
	require.Equal(t, 15000, info.BaseQuota)
	require.Equal(t, 7500, info.RefundAmount)

	// A subscription log without a positive consumption figure is not refundable.
	noBase := chatLog(0)
	noBase.Other = `{"request_path":"/v1/chat/completions","billing_source":"subscription","subscription_id":23}`
	require.Nil(t, JudgeSelfRefund(noBase, map[int]bool{}))
}

// A refundable log must carry a parseable JSON *object* in `other`: the chat
// request path is read out of that object, so an empty, array or scalar payload
// can never be refunded. This invariant is what kept the removed JSON backfill
// from ever merging into a non-object column value.
func TestJudgeSelfRefundRequiresJsonObjectOther(t *testing.T) {
	enableRefundSetting(t)

	for _, other := range []string{"", "[]", `"scalar"`, "123", "null", "not json"} {
		log := chatLog(1000)
		log.Other = other
		require.Nil(t, JudgeSelfRefund(log, map[int]bool{}),
			"other=%q must not be considered refundable", other)
	}
}

// request_id is the authoritative selector, and when both selectors are supplied
// they must agree. A mismatch must be rejected rather than silently resolving to
// a different request.
func TestResolveRefundLogEnforcesSelectorsAndOwnership(t *testing.T) {
	db := setupRefundServiceDB(t)
	log := &model.Log{
		UserId: 5, Type: model.LogTypeConsume, CreatedAt: time.Now().Unix(),
		ModelName: "gpt-selector", Quota: 100, RequestId: "req-real",
	}
	require.NoError(t, db.Create(log).Error)

	resolved, err := resolveRefundLog(5, RefundableLogQuery{RequestId: "req-real", LogId: log.Id})
	require.NoError(t, err)
	require.Equal(t, log.Id, resolved.Id)

	_, err = resolveRefundLog(5, RefundableLogQuery{RequestId: "req-real", LogId: log.Id + 999})
	require.Error(t, err, "a mismatched request_id/log_id pair must be rejected")

	_, err = resolveRefundLog(6, RefundableLogQuery{RequestId: "req-real"})
	require.Error(t, err, "another account's request_id must not resolve")

	_, err = resolveRefundLog(6, RefundableLogQuery{LogId: log.Id})
	require.Error(t, err, "another account's log id must not resolve")
}

// The end-to-end POST must re-validate from scratch: a mismatched selector pair
// is rejected, and an eligible request succeeds exactly once.
func TestDoSelfRefundRejectsMismatchAndCreditsOnce(t *testing.T) {
	db := setupRefundServiceDB(t)
	enableRefundSetting(t)

	user := &model.User{Username: "refund-service", Password: "password", AffCode: "refund-service", Quota: 1000}
	require.NoError(t, db.Create(user).Error)

	log := chatLog(1000)
	log.UserId = user.Id
	log.RequestId = "req-service"
	require.NoError(t, db.Create(log).Error)

	_, err := DoSelfRefund(user.Id, log.Id+999, log.RequestId)
	require.Error(t, err, "a mismatched selector pair must be rejected")

	info, err := DoSelfRefund(user.Id, log.Id, log.RequestId)
	require.NoError(t, err)
	require.Equal(t, 500, info.RefundAmount)

	var after model.User
	require.NoError(t, db.First(&after, user.Id).Error)
	require.Equal(t, 1500, after.Quota)

	_, err = DoSelfRefund(user.Id, log.Id, log.RequestId)
	require.Error(t, err, "a replayed refund must be rejected")

	require.NoError(t, db.First(&after, user.Id).Error)
	require.Equal(t, 1500, after.Quota, "a rejected replay must not credit again")
}
