package model

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noLimits disables the daily caps, for tests that are not exercising them.
var noLimits = RefundLimits{}

// prepareRefundFixture gives each refund test a known-empty slice of the tables
// the self-refund path touches. log_refunds is not in the shared TestMain
// migration list, so it is created here (AutoMigrate is idempotent).
func prepareRefundFixture(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&LogRefund{}))
	for _, table := range []string{"log_refunds", "logs", "users", "user_subscriptions"} {
		require.NoError(t, DB.Exec("DELETE FROM "+table).Error)
	}
	t.Cleanup(func() {
		for _, table := range []string{"log_refunds", "logs", "users", "user_subscriptions"} {
			DB.Exec("DELETE FROM " + table)
		}
	})
}

func newRefundUser(t *testing.T, suffix string, quota int) *User {
	t.Helper()
	user := &User{
		Username: "refund-" + suffix,
		Password: "password",
		AffCode:  "refund-" + suffix, // unique index; must not be empty for a second user
		Status:   common.UserStatusEnabled,
		Quota:    quota,
	}
	require.NoError(t, DB.Create(user).Error)
	return user
}

func newConsumeLog(t *testing.T, userId int, quota int, requestId string) *Log {
	t.Helper()
	log := &Log{
		UserId:           userId,
		Type:             LogTypeConsume,
		CreatedAt:        time.Now().Unix(),
		ModelName:        "gpt-test",
		Quota:            quota,
		CompletionTokens: 0,
		IsStream:         false,
		RequestId:        requestId,
	}
	require.NoError(t, LOG_DB.Create(log).Error)
	return log
}

// GetRefundableCandidates is the SQL prefilter behind the user-facing refundable
// list. Its `(completion_tokens = 0 OR is_stream = 1)` group is a multi-column
// condition: it must stay parenthesized, because every other predicate is
// appended with AND. An unparenthesized OR lets the trailing ANDs bind to only
// the first branch, so `is_stream = 1` matches on its own and the query returns
// *every* streaming log on the platform regardless of user, type or age.
func TestGetRefundableCandidatesScopesToOwner(t *testing.T) {
	prepareRefundFixture(t)
	now := time.Now().Unix()

	own := &Log{
		UserId: 11, Type: LogTypeConsume, CreatedAt: now, ModelName: "own-empty",
		Quota: 100, CompletionTokens: 0, IsStream: false, RequestId: "req-own",
	}
	otherStreaming := &Log{
		UserId: 22, Type: LogTypeConsume, CreatedAt: now, ModelName: "other-stream",
		Quota: 200, CompletionTokens: 5, IsStream: true, RequestId: "req-other",
	}
	require.NoError(t, LOG_DB.Create(own).Error)
	require.NoError(t, LOG_DB.Create(otherStreaming).Error)

	logs, total, err := GetRefundableCandidates(11, now-3600, 100)
	require.NoError(t, err)

	require.Len(t, logs, 1, "candidate prefilter must return only the caller's own log")
	require.Equal(t, int64(1), total, "candidate count must be scoped to the same filters")
	for _, candidate := range logs {
		require.Equal(t, 11, candidate.UserId,
			"candidate prefilter leaked another account's log (unparenthesized OR)")
	}
}

// The type and window predicates must survive the OR grouping: a streaming log
// of a non-consume type, or one outside the window, is not refundable.
func TestGetRefundableCandidatesKeepsTypeAndWindowFilters(t *testing.T) {
	prepareRefundFixture(t)
	now := time.Now().Unix()

	streamingTopUp := &Log{
		UserId: 33, Type: LogTypeTopup, CreatedAt: now, ModelName: "topup",
		Quota: 500, IsStream: true, RequestId: "req-topup",
	}
	oldStreaming := &Log{
		UserId: 33, Type: LogTypeConsume, CreatedAt: now - 90000, ModelName: "old",
		Quota: 500, IsStream: true, RequestId: "req-old",
	}
	eligible := &Log{
		UserId: 33, Type: LogTypeConsume, CreatedAt: now, ModelName: "eligible",
		Quota: 500, CompletionTokens: 0, IsStream: false, RequestId: "req-eligible",
	}
	require.NoError(t, LOG_DB.Create(streamingTopUp).Error)
	require.NoError(t, LOG_DB.Create(oldStreaming).Error)
	require.NoError(t, LOG_DB.Create(eligible).Error)

	logs, total, err := GetRefundableCandidates(33, now-3600, 100)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	require.Equal(t, int64(1), total)
	require.Equal(t, "eligible", logs[0].ModelName)
}

// A wallet refund must credit exactly once. The unique index on log_id is the
// idempotency boundary: a replay is rejected and, critically, must not credit a
// second time.
func TestDoSelfRefundWalletCreditsOnce(t *testing.T) {
	prepareRefundFixture(t)
	user := newRefundUser(t, "wallet-once", 1000)
	log := newConsumeLog(t, user.Id, 500, "req-wallet-once")

	require.NoError(t, DoSelfRefundWallet(user.Id, log.Id, 500, 250, "empty_response", "req-wallet-once", noLimits))

	var after User
	require.NoError(t, DB.First(&after, user.Id).Error)
	require.Equal(t, 1250, after.Quota)

	err := DoSelfRefundWallet(user.Id, log.Id, 500, 250, "empty_response", "req-wallet-once", noLimits)
	require.Error(t, err, "a replayed refund must be rejected")

	require.NoError(t, DB.First(&after, user.Id).Error)
	require.Equal(t, 1250, after.Quota, "a rejected replay must not credit the wallet again")

	var refundCount int64
	require.NoError(t, DB.Model(&LogRefund{}).Where("log_id = ?", log.Id).Count(&refundCount).Error)
	require.Equal(t, int64(1), refundCount)
}

func TestDoSelfRefundWalletPreservesOutstandingCacheReservation(t *testing.T) {
	prepareRefundFixture(t)
	server := useUserCacheMiniRedis(t)
	user := newRefundUser(t, "cache-credit", 1000)
	log := newConsumeLog(t, user.Id, 500, "req-cache-credit")
	require.NoError(t, populateUserCache(*user))
	// The cache already reserves 200 for an in-flight call not yet persisted.
	result, err := cacheTryReserveUserQuota(user.Id, 200)
	require.NoError(t, err)
	require.Equal(t, cacheQuotaOK, result)
	require.NoError(t, DoSelfRefundWallet(user.Id, log.Id, 500, 250, "empty_response", log.RequestId, noLimits))
	assert.Equal(t, "1050", server.HGet(getUserCacheKey(user.Id), "Quota"))
	var after User
	require.NoError(t, DB.First(&after, user.Id).Error)
	assert.Equal(t, 1250, after.Quota)
	require.Error(t, DoSelfRefundWallet(user.Id, log.Id, 500, 250, "empty_response", log.RequestId, noLimits))
	assert.Equal(t, "1050", server.HGet(getUserCacheKey(user.Id), "Quota"), "replay must not credit the cache")
}

// The wallet credit is bounded by common.MaxWalletQuota, the same ceiling the
// wallet's own credit path enforces. When the ceiling would be breached the whole
// transaction must roll back, including the idempotency row, so the user is not
// left permanently unable to retry.
func TestDoSelfRefundWalletRefusesToBreachCeiling(t *testing.T) {
	prepareRefundFixture(t)
	user := newRefundUser(t, "wallet-ceiling", common.MaxWalletQuota-10)
	log := newConsumeLog(t, user.Id, 500, "req-wallet-ceiling")

	err := DoSelfRefundWallet(user.Id, log.Id, 500, 250, "empty_response", "req-wallet-ceiling", noLimits)
	require.Error(t, err)

	var after User
	require.NoError(t, DB.First(&after, user.Id).Error)
	require.Equal(t, common.MaxWalletQuota-10, after.Quota, "a refused refund must not change the wallet")

	var refundCount int64
	require.NoError(t, DB.Model(&LogRefund{}).Where("log_id = ?", log.Id).Count(&refundCount).Error)
	require.Equal(t, int64(0), refundCount,
		"a failed credit must roll back its idempotency row, otherwise the request can never be retried")
}

// The wallet ceiling must match the wallet's own credit path. A balance that a
// top-up legitimately grew past the int32 single-request bound is still a valid
// wallet balance and must stay refundable; capping refunds at int32 would deny
// them permanently.
func TestDoSelfRefundWalletWorksAboveInt32Balance(t *testing.T) {
	prepareRefundFixture(t)
	highBalance := common.MaxQuota + 1_000_000
	require.Less(t, highBalance, common.MaxWalletQuota, "fixture must stay inside the wallet domain")

	user := newRefundUser(t, "wallet-above-int32", highBalance)
	log := newConsumeLog(t, user.Id, 500, "req-above-int32")

	require.NoError(t, DoSelfRefundWallet(user.Id, log.Id, 500, 250, "empty_response", "req-above-int32", noLimits))

	var after User
	require.NoError(t, DB.First(&after, user.Id).Error)
	require.Equal(t, highBalance+250, after.Quota,
		"a wallet above the int32 bound must still receive a refund")
}

// A soft-deleted or missing user must fail the credit rather than committing a
// refund record with no money moved.
func TestDoSelfRefundWalletRejectsMissingUser(t *testing.T) {
	prepareRefundFixture(t)
	log := &Log{
		UserId: 9999, Type: LogTypeConsume, CreatedAt: time.Now().Unix(),
		ModelName: "gone", Quota: 500, CompletionTokens: 0, RequestId: "req-gone",
	}
	require.NoError(t, LOG_DB.Create(log).Error)

	require.Error(t, DoSelfRefundWallet(9999, log.Id, 500, 250, "empty_response", "req-gone", noLimits))

	var refundCount int64
	require.NoError(t, DB.Model(&LogRefund{}).Where("log_id = ?", log.Id).Count(&refundCount).Error)
	require.Equal(t, int64(0), refundCount)
}

func newActiveSubscription(t *testing.T, userId int, total, used int64) *UserSubscription {
	t.Helper()
	now := time.Now().Unix()
	sub := &UserSubscription{
		UserId:      userId,
		AmountTotal: total,
		AmountUsed:  used,
		StartTime:   now - 3600,
		EndTime:     now + 86400,
		Status:      "active",
		Source:      "order",
		CreatedAt:   now,
	}
	require.NoError(t, DB.Create(sub).Error)
	return sub
}

// A subscription-funded refund restores the consumed allowance and must never
// mint wallet quota. This is the invariant that keeps the two funding sources
// from being confused into free money.
func TestDoSelfRefundSubscriptionRestoresUsedQuotaOnly(t *testing.T) {
	prepareRefundFixture(t)
	user := newRefundUser(t, "sub-restore", 5000)
	sub := newActiveSubscription(t, user.Id, 100000, 40000)

	log := &Log{
		UserId: user.Id, Type: LogTypeConsume, CreatedAt: time.Now().Unix(),
		ModelName: "gpt-sub", Quota: 0, CompletionTokens: 0, IsStream: false,
		RequestId: "req-sub-restore",
	}
	require.NoError(t, LOG_DB.Create(log).Error)

	require.NoError(t, DoSelfRefundSubscription(user.Id, log.Id, sub.Id, 20000, 10000, "empty_response", "req-sub-restore", noLimits))

	var afterSub UserSubscription
	require.NoError(t, DB.First(&afterSub, sub.Id).Error)
	require.Equal(t, int64(30000), afterSub.AmountUsed, "the consumed subscription allowance must be restored")

	var afterUser User
	require.NoError(t, DB.First(&afterUser, user.Id).Error)
	require.Equal(t, 5000, afterUser.Quota, "a subscription refund must never credit the wallet")
}

// The restored allowance must not go negative when the refund exceeds what the
// cycle actually consumed.
func TestDoSelfRefundSubscriptionClampsAmountUsedAtZero(t *testing.T) {
	prepareRefundFixture(t)
	user := newRefundUser(t, "sub-clamp", 5000)
	sub := newActiveSubscription(t, user.Id, 100000, 1000)

	log := &Log{
		UserId: user.Id, Type: LogTypeConsume, CreatedAt: time.Now().Unix(),
		ModelName: "gpt-clamp", Quota: 0, RequestId: "req-sub-clamp",
	}
	require.NoError(t, LOG_DB.Create(log).Error)

	require.NoError(t, DoSelfRefundSubscription(user.Id, log.Id, sub.Id, 50000, 50000, "empty_response", "req-sub-clamp", noLimits))

	var afterSub UserSubscription
	require.NoError(t, DB.First(&afterSub, sub.Id).Error)
	require.Equal(t, int64(0), afterSub.AmountUsed, "amount_used must be clamped at zero, never negative")
}

// Ineligible subscriptions fail closed and leave the allowance untouched.
func TestDoSelfRefundSubscriptionRejectsIneligibleSubscription(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(sub *UserSubscription)
	}{
		{"cancelled", func(sub *UserSubscription) { sub.Status = "cancelled" }},
		{"expired", func(sub *UserSubscription) { sub.Status = "expired" }},
		{"past end time", func(sub *UserSubscription) { sub.EndTime = time.Now().Unix() - 10 }},
		{"unlimited", func(sub *UserSubscription) { sub.AmountTotal = 0 }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prepareRefundFixture(t)
			user := newRefundUser(t, "sub-reject", 5000)
			sub := newActiveSubscription(t, user.Id, 100000, 40000)
			tc.mutate(sub)
			require.NoError(t, DB.Save(sub).Error)

			log := &Log{
				UserId: user.Id, Type: LogTypeConsume, CreatedAt: time.Now().Unix(),
				ModelName: "gpt-reject", Quota: 0,
				RequestId: fmt.Sprintf("req-sub-reject-%s", tc.name),
			}
			require.NoError(t, LOG_DB.Create(log).Error)

			require.Error(t, DoSelfRefundSubscription(user.Id, log.Id, sub.Id, 20000, 10000, "empty_response", log.RequestId, noLimits))

			var afterSub UserSubscription
			require.NoError(t, DB.First(&afterSub, sub.Id).Error)
			require.Equal(t, int64(40000), afterSub.AmountUsed, "a rejected refund must not touch the allowance")

			var afterUser User
			require.NoError(t, DB.First(&afterUser, user.Id).Error)
			require.Equal(t, 5000, afterUser.Quota)
		})
	}
}

// A subscription refund must be scoped to its owner: a subscription id that
// belongs to someone else is not refundable.
func TestDoSelfRefundSubscriptionRejectsForeignSubscription(t *testing.T) {
	prepareRefundFixture(t)
	owner := newRefundUser(t, "sub-owner", 5000)
	attacker := newRefundUser(t, "sub-attacker", 5000)
	sub := newActiveSubscription(t, owner.Id, 100000, 40000)

	log := &Log{
		UserId: attacker.Id, Type: LogTypeConsume, CreatedAt: time.Now().Unix(),
		ModelName: "gpt-foreign", Quota: 0, RequestId: "req-sub-foreign",
	}
	require.NoError(t, LOG_DB.Create(log).Error)

	require.Error(t, DoSelfRefundSubscription(attacker.Id, log.Id, sub.Id, 20000, 10000, "empty_response", "req-sub-foreign", noLimits))

	var afterSub UserSubscription
	require.NoError(t, DB.First(&afterSub, sub.Id).Error)
	require.Equal(t, int64(40000), afterSub.AmountUsed, "another account's subscription must not be refundable")
}

// The daily count cap is enforced inside the refund transaction, together with
// the idempotency insert. Checking it before the transaction would let two
// in-flight requests for different logs both observe the pre-cap total and both
// be paid.
func TestDoSelfRefundEnforcesDailyCountLimitInsideTransaction(t *testing.T) {
	prepareRefundFixture(t)
	user := newRefundUser(t, "daily-count", 1000)
	first := newConsumeLog(t, user.Id, 500, "req-daily-1")
	second := newConsumeLog(t, user.Id, 500, "req-daily-2")

	limits := RefundLimits{MaxCount: 1}

	require.NoError(t, DoSelfRefundWallet(user.Id, first.Id, 500, 250, "empty_response", "req-daily-1", limits))

	err := DoSelfRefundWallet(user.Id, second.Id, 500, 250, "empty_response", "req-daily-2", limits)
	require.Error(t, err, "the second refund of the day must be rejected by the count cap")

	var after User
	require.NoError(t, DB.First(&after, user.Id).Error)
	require.Equal(t, 1250, after.Quota, "only the first refund may be credited")

	var refundCount int64
	require.NoError(t, DB.Model(&LogRefund{}).Where("user_id = ?", user.Id).Count(&refundCount).Error)
	require.Equal(t, int64(1), refundCount)
}

// The daily quota cap is enforced on the same basis and must account for the
// total already refunded today.
func TestDoSelfRefundEnforcesDailyQuotaLimit(t *testing.T) {
	prepareRefundFixture(t)
	user := newRefundUser(t, "daily-quota", 1000)
	first := newConsumeLog(t, user.Id, 500, "req-quota-1")
	second := newConsumeLog(t, user.Id, 500, "req-quota-2")

	// Room for one 250 refund but not for two.
	limits := RefundLimits{MaxQuota: 300}

	require.NoError(t, DoSelfRefundWallet(user.Id, first.Id, 500, 250, "empty_response", "req-quota-1", limits))

	err := DoSelfRefundWallet(user.Id, second.Id, 500, 250, "empty_response", "req-quota-2", limits)
	require.Error(t, err, "the day's quota cap must account for what was already refunded")

	var after User
	require.NoError(t, DB.First(&after, user.Id).Error)
	require.Equal(t, 1250, after.Quota)
}

// Concurrent refunds of *different* logs must not both slip past a daily count
// cap of one. With the check outside the transaction both would pass and the
// configured cap would be silently exceeded.
func TestDoSelfRefundDailyLimitSurvivesConcurrentRequests(t *testing.T) {
	prepareRefundFixture(t)
	user := newRefundUser(t, "daily-race", 1000)
	logs := []*Log{
		newConsumeLog(t, user.Id, 500, "req-race-1"),
		newConsumeLog(t, user.Id, 500, "req-race-2"),
	}

	limits := RefundLimits{MaxCount: 1}
	errs := make([]error, len(logs))
	var wg sync.WaitGroup
	for i, log := range logs {
		wg.Add(1)
		go func(i int, log *Log) {
			defer wg.Done()
			errs[i] = DoSelfRefundWallet(user.Id, log.Id, 500, 250, "empty_response", log.RequestId, limits)
		}(i, log)
	}
	wg.Wait()

	succeeded := 0
	for _, err := range errs {
		if err == nil {
			succeeded++
		}
	}
	require.Equal(t, 1, succeeded, "exactly one of the concurrent refunds may pass a cap of one")

	var refundCount int64
	require.NoError(t, DB.Model(&LogRefund{}).Where("user_id = ?", user.Id).Count(&refundCount).Error)
	require.Equal(t, int64(1), refundCount, "the cap must hold under concurrency")

	var after User
	require.NoError(t, DB.First(&after, user.Id).Error)
	require.Equal(t, 1250, after.Quota, "only one refund may be credited")
}

// A subscription refund may only ever change amount_used. Anything else on the
// row (status, cycle bounds, plan, downgrade target, wallet-overflow policy) is
// owned by billing and administration, and a full-row write would silently revert
// concurrent changes to those columns.
func TestDoSelfRefundSubscriptionChangesOnlyAmountUsed(t *testing.T) {
	prepareRefundFixture(t)
	user := newRefundUser(t, "sub-narrow", 5000)
	sub := newActiveSubscription(t, user.Id, 100000, 40000)
	sub.PlanId = 7
	sub.AllowWalletOverflow = true
	sub.DowngradeGroup = "basic"
	sub.NextResetTime = sub.EndTime
	require.NoError(t, DB.Save(sub).Error)

	var before UserSubscription
	require.NoError(t, DB.First(&before, sub.Id).Error)

	log := &Log{
		UserId: user.Id, Type: LogTypeConsume, CreatedAt: time.Now().Unix(),
		ModelName: "gpt-narrow", Quota: 0, RequestId: "req-sub-narrow",
	}
	require.NoError(t, LOG_DB.Create(log).Error)

	require.NoError(t, DoSelfRefundSubscription(user.Id, log.Id, sub.Id, 20000, 10000, "empty_response", "req-sub-narrow", noLimits))

	var after UserSubscription
	require.NoError(t, DB.First(&after, sub.Id).Error)

	expected := before
	expected.AmountUsed = before.AmountUsed - 10000
	require.Equal(t, expected, after, "a subscription refund may only change amount_used")
}

// log_refunds predates this code in production and is a financial audit table.
// When it already exists, the application must not reshape it: a missing index is
// an operator concern, not something to be created behind their back from the Go
// struct.
func TestEnsureLogRefundTableNeverReshapesAnExistingTable(t *testing.T) {
	require.NoError(t, DB.Migrator().DropTable(&LogRefund{}))
	t.Cleanup(func() {
		DB.Migrator().DropTable(&LogRefund{})
		DB.AutoMigrate(&LogRefund{})
	})

	// An existing table that deliberately lacks the declared log_id unique index.
	require.NoError(t, DB.Exec(`CREATE TABLE log_refunds (
		id integer PRIMARY KEY AUTOINCREMENT,
		user_id integer NOT NULL,
		log_id integer NOT NULL,
		funding_source varchar(16) NOT NULL,
		subscription_id integer,
		base_quota integer NOT NULL,
		quota integer NOT NULL,
		refund_date varchar(10),
		reason varchar(32),
		request_id varchar(64),
		created_at bigint NOT NULL
	)`).Error)

	require.True(t, DB.Migrator().HasTable(&LogRefund{}))
	require.False(t, DB.Migrator().HasIndex(&LogRefund{}, "idx_log_refund_log_id"))

	require.NoError(t, ensureLogRefundTable(DB))

	require.False(t, DB.Migrator().HasIndex(&LogRefund{}, "idx_log_refund_log_id"),
		"an existing log_refunds must be left exactly as the operator created it")
}

// A fresh database still needs the table, and it must carry the unique index that
// makes the refund insert its own idempotency check.
func TestEnsureLogRefundTableCreatesWhenAbsent(t *testing.T) {
	require.NoError(t, DB.Migrator().DropTable(&LogRefund{}))
	t.Cleanup(func() {
		DB.Migrator().DropTable(&LogRefund{})
		DB.AutoMigrate(&LogRefund{})
	})
	require.False(t, DB.Migrator().HasTable(&LogRefund{}))

	require.NoError(t, ensureLogRefundTable(DB))

	require.True(t, DB.Migrator().HasTable(&LogRefund{}))
	require.True(t, DB.Migrator().HasIndex(&LogRefund{}, "idx_log_refund_log_id"),
		"a fresh database must get the declared unique index")

	// Created, therefore usable: the same call is idempotent.
	require.NoError(t, ensureLogRefundTable(DB))
}

// The source request log is audit evidence. A refund must not rewrite it, in
// particular not its `other` payload: the removed backfill merged JSON into that
// column, which errors on the empty values production actually stores and would
// silently mangle any non-object payload.
func TestSelfRefundNeverRewritesSourceLog(t *testing.T) {
	prepareRefundFixture(t)

	for _, testCase := range []struct {
		name  string
		other string
	}{
		{"json object", `{"request_path":"/v1/chat/completions","billing_source":"wallet"}`},
		{"empty string", ""},
		{"json array", `[]`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			user := newRefundUser(t, "src-"+testCase.name, 1000)
			log := &Log{
				UserId: user.Id, Type: LogTypeConsume, CreatedAt: time.Now().Unix(),
				ModelName: "gpt-src", Quota: 500, CompletionTokens: 0,
				RequestId: "req-src-" + testCase.name, Other: testCase.other,
			}
			require.NoError(t, LOG_DB.Create(log).Error)

			var before Log
			require.NoError(t, LOG_DB.First(&before, log.Id).Error)

			require.NoError(t, DoSelfRefundWallet(user.Id, log.Id, 500, 250, "empty_response", log.RequestId, RefundLimits{MaxCount: 1}))

			var after Log
			require.NoError(t, LOG_DB.First(&after, log.Id).Error)
			require.Equal(t, before, after, "the source request log must be read-only to the refund")
		})
	}
}
