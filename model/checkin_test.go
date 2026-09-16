package model

import (
	"math"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

// deterministicCheckinSetting removes every source of randomness and returns a
// setting whose only payout path is the fixed fallback reward.
func deterministicCheckinSetting(fallback float64) *operation_setting.CheckinSetting {
	return &operation_setting.CheckinSetting{
		Enabled:        true,
		CountEnabled:   false,
		QuotaEnabled:   false,
		CEnabled:       false,
		FallbackReward: fallback,
	}
}

// Rule C pays a fixed base at the threshold, adds a step per further increment,
// and never exceeds the cap.
func TestCalcCheckinTierCBoundaries(t *testing.T) {
	setting := &operation_setting.CheckinSetting{
		CEnabled:       true,
		CBaseThreshold: 1000,
		CBaseReward:    10,
		CStepQuota:     1000,
		CStepReward:    5,
		CMaxReward:     30,
	}

	reward, hit := CalcCheckinTierC(999, setting)
	require.False(t, hit, "below the base threshold must not hit")
	require.Equal(t, 0.0, reward)

	reward, hit = CalcCheckinTierC(1000, setting)
	require.True(t, hit)
	require.Equal(t, 10.0, reward, "exactly at the threshold pays the base reward")

	reward, _ = CalcCheckinTierC(1999, setting)
	require.Equal(t, 10.0, reward, "the next step starts only after a full increment")

	reward, _ = CalcCheckinTierC(2000, setting)
	require.Equal(t, 15.0, reward)

	reward, _ = CalcCheckinTierC(1_000_000, setting)
	require.Equal(t, 30.0, reward, "the reward is capped")

	// A zero step must not divide by zero; it degrades to a single tier.
	zeroStep := *setting
	zeroStep.CStepQuota = 0
	reward, hit = CalcCheckinTierC(5000, &zeroStep)
	require.True(t, hit)
	require.Equal(t, 10.0, reward)
}

// A disabled rule never pays, and the configured reward is what is returned.
func TestCalcCheckinTierCDisabled(t *testing.T) {
	off := &operation_setting.CheckinSetting{CEnabled: false, CBaseThreshold: 0, CBaseReward: 10}
	reward, hit := CalcCheckinTierC(1e9, off)
	require.False(t, hit)
	require.Equal(t, 0.0, reward)
}

// Operator-entered configuration is normalized before it can influence a payout:
// NaN becomes zero, an inverted range is sorted (a swapped min/max used to pay
// the larger bound), and every reward is clamped to the hard ceiling.
func TestSanitizedNormalizesOperatorConfiguration(t *testing.T) {
	setting := &operation_setting.CheckinSetting{
		CountTiers: []operation_setting.CheckinTier{
			{Threshold: -5, MinReward: 30, MaxReward: 10},        // inverted + negative threshold
			{Threshold: 10, MinReward: math.NaN(), MaxReward: 5}, // NaN lower bound
			{Threshold: 20, MinReward: 1, MaxReward: 1e9},        // above the hard ceiling
		},
		FallbackReward: math.NaN(),
		CBaseThreshold: -100,
		CBaseReward:    math.NaN(),
		CStepQuota:     -50,
		CStepReward:    math.Inf(1),
		CMaxReward:     math.NaN(),
	}

	out := setting.Sanitized()

	require.Equal(t, 0, out.CountTiers[0].Threshold, "a negative threshold becomes 0")
	require.Equal(t, 10.0, out.CountTiers[0].MinReward, "an inverted range is sorted")
	require.Equal(t, 30.0, out.CountTiers[0].MaxReward)
	require.Equal(t, 0.0, out.CountTiers[1].MinReward, "NaN becomes 0 so it cannot be paid out")
	require.LessOrEqual(t, out.CountTiers[2].MaxReward, operation_setting.MaxCheckinRewardTier)
	require.Equal(t, 0.0, out.FallbackReward)
	require.Equal(t, 0.0, out.CBaseThreshold)
	require.Equal(t, 0.0, out.CBaseReward)
	require.Equal(t, 0.0, out.CStepQuota)
	require.LessOrEqual(t, out.CStepReward, operation_setting.MaxCheckinRewardTier)
	require.Equal(t, 0.0, out.CMaxReward)

	// The live setting must not be mutated by sanitization.
	require.Equal(t, 30.0, setting.CountTiers[0].MinReward)
}

// An inverted tier range must pay within the two configured bounds, never the
// larger one (the historical defect paid min, which after a swap was the max).
func TestSanitizedInvertedRangeDoesNotOverpay(t *testing.T) {
	setting := &operation_setting.CheckinSetting{
		CountEnabled: true,
		CountTiers:   []operation_setting.CheckinTier{{Threshold: 1, MinReward: 30, MaxReward: 10}},
	}
	out := setting.Sanitized()

	reward, source := CalcRewardTierQuota(5, 0, 0, &out)
	require.Equal(t, "count", source)
	require.GreaterOrEqual(t, reward, 10.0)
	require.LessOrEqual(t, reward, 30.0)
}

// The three rules are evaluated and the highest payout wins.
func TestCalcRewardTierQuotaTakesHighestRule(t *testing.T) {
	setting := operation_setting.CheckinSetting{
		CountEnabled:   true,
		CountTiers:     []operation_setting.CheckinTier{{Threshold: 1, MinReward: 5, MaxReward: 5}},
		QuotaEnabled:   true,
		QuotaTiers:     []operation_setting.CheckinTier{{Threshold: 100, MinReward: 20, MaxReward: 20}},
		CEnabled:       true,
		CBaseThreshold: 1000,
		CBaseReward:    30,
		CStepQuota:     1000,
		CStepReward:    5,
		CMaxReward:     30,
		FallbackReward: 1,
	}
	sanitized := setting.Sanitized()
	perTier := checkinQuotaPerTier()

	// Rule A only.
	reward, source := CalcRewardTierQuota(1, 0, 0, &sanitized)
	require.Equal(t, "count", source)
	require.Equal(t, 5.0, reward)

	// Rule B beats rule A: 100 platform units of consumption.
	reward, source = CalcRewardTierQuota(1, int(100*perTier), 0, &sanitized)
	require.Equal(t, "quota", source)
	require.Equal(t, 20.0, reward)

	// Rule C beats both.
	reward, source = CalcRewardTierQuota(1, int(100*perTier), int(1000*perTier), &sanitized)
	require.Equal(t, "progressive", source)
	require.Equal(t, 30.0, reward)

	// Nothing met falls back.
	reward, source = CalcRewardTierQuota(0, 0, 0, &sanitized)
	require.Equal(t, "fallback", source)
	require.Equal(t, 1.0, reward)
}

// The platform-currency conversion must produce an integer internal quota and
// must never emit a value for a non-finite or non-positive input.
func TestQuotaFromCheckinTierRejectsNonFinite(t *testing.T) {
	require.Equal(t, 0, QuotaFromCheckinTier(math.NaN()))
	require.Equal(t, 0, QuotaFromCheckinTier(math.Inf(1)))
	require.Equal(t, 0, QuotaFromCheckinTier(-1))
	require.Equal(t, 0, QuotaFromCheckinTier(0))

	native := QuotaFromCheckinTier(5)
	// 1 🌱 = QuotaPerUnit/100 internal units, so the conversion is exact.
	require.Equal(t, int(5*(common.QuotaPerUnit/100)), native)
	require.Greater(t, native, 0)
}

// The unique (user_id, checkin_date) index is the concurrency guard; a second
// claim on the same day must be rejected without paying twice.
func TestUserCheckinRejectsDuplicateSameDayClaim(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.AutoMigrate(&Checkin{}))
	require.NoError(t, DB.Exec("DELETE FROM checkins").Error)
	t.Cleanup(func() { DB.Exec("DELETE FROM checkins") })

	user := newRefundUser(t, "checkin-once", 0)
	useCheckinSetting(t, deterministicCheckinSetting(5))

	first, err := UserCheckin(user.Id)
	require.NoError(t, err)
	require.Greater(t, first.QuotaAwarded, 0)

	var afterFirst User
	require.NoError(t, DB.First(&afterFirst, user.Id).Error)
	require.Equal(t, first.QuotaAwarded, afterFirst.Quota)

	_, err = UserCheckin(user.Id)
	require.Error(t, err, "a same-day replay must be rejected")

	var afterSecond User
	require.NoError(t, DB.First(&afterSecond, user.Id).Error)
	require.Equal(t, afterFirst.Quota, afterSecond.Quota, "a rejected replay must not pay again")

	var checkinCount int64
	require.NoError(t, DB.Model(&Checkin{}).Where("user_id = ?", user.Id).Count(&checkinCount).Error)
	require.Equal(t, int64(1), checkinCount)
}

// A payout that would breach the wallet ceiling must fail and must not leave a
// check-in record behind that would consume the user's daily claim.
func TestUserCheckinRefusesToBreachWalletCeiling(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.AutoMigrate(&Checkin{}))
	require.NoError(t, DB.Exec("DELETE FROM checkins").Error)
	t.Cleanup(func() { DB.Exec("DELETE FROM checkins") })

	user := newRefundUser(t, "checkin-ceiling", common.MaxWalletQuota-100)
	useCheckinSetting(t, deterministicCheckinSetting(5))

	_, err := UserCheckin(user.Id)
	require.Error(t, err, "a payout past the wallet ceiling must be refused")

	var after User
	require.NoError(t, DB.First(&after, user.Id).Error)
	require.Equal(t, common.MaxWalletQuota-100, after.Quota, "a refused check-in must not change the wallet")

	var checkinCount int64
	require.NoError(t, DB.Model(&Checkin{}).Where("user_id = ?", user.Id).Count(&checkinCount).Error)
	require.Equal(t, int64(0), checkinCount,
		"a failed payout must roll back the check-in row so the day is not burned")
}

// A zero payout must not consume the daily claim either: it would lock the user
// out for the day while paying nothing.
func TestUserCheckinRejectsZeroReward(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.AutoMigrate(&Checkin{}))
	require.NoError(t, DB.Exec("DELETE FROM checkins").Error)
	t.Cleanup(func() { DB.Exec("DELETE FROM checkins") })

	user := newRefundUser(t, "checkin-zero", 0)
	useCheckinSetting(t, deterministicCheckinSetting(0))

	_, err := UserCheckin(user.Id)
	require.Error(t, err)

	var checkinCount int64
	require.NoError(t, DB.Model(&Checkin{}).Where("user_id = ?", user.Id).Count(&checkinCount).Error)
	require.Equal(t, int64(0), checkinCount)
}

// useCheckinSetting installs a deterministic global check-in configuration for
// the duration of a test.
func useCheckinSetting(t *testing.T, setting *operation_setting.CheckinSetting) {
	t.Helper()
	live := operation_setting.GetCheckinSetting()
	previous := *live
	*live = *setting
	t.Cleanup(func() { *live = previous })
}
