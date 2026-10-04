package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testChromeUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120.0"
const testFirefoxUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Gecko/20100101 Firefox/121.0"

// setupRegistrationDeviceTestState isolates the device table and restores the global
// trial-protection settings after each case. The setting is enabled by default, but the
// protection only changes the trial quota and never rejects the signup itself.
func setupRegistrationDeviceTestState(t *testing.T, enabled bool) {
	t.Helper()
	truncateTables(t)
	require.NoError(t, ensureXingyaTables(DB))
	require.NoError(t, DB.Exec("DELETE FROM xingya_registration_devices").Error)

	oldEnabled := common.RegistrationDeviceLimitEnabled
	oldWhitelist := common.RegistrationDeviceLimitWhitelist
	common.RegistrationDeviceLimitEnabled = enabled
	common.RegistrationDeviceLimitWhitelist = []string{}
	t.Cleanup(func() {
		common.RegistrationDeviceLimitEnabled = oldEnabled
		common.RegistrationDeviceLimitWhitelist = oldWhitelist
		DB.Exec("DELETE FROM xingya_registration_devices")
	})
}

// seedRegistrationDevice stores one accepted registration the way a controller would, then
// backdates it so a test can place it outside the deduplication window without sleeping.
func seedRegistrationDevice(t *testing.T, clientIp string, userAgent string, registeredAgo int64) int {
	t.Helper()
	user := createReserveTestUser(t, 1000)
	require.NoError(t, RecordRegistrationDevice(nil, user.Id, clientIp, userAgent))
	require.NoError(t, DB.Model(&XingyaRegistrationDevice{}).Where("user_id = ?", user.Id).
		Update("reg_time", common.GetTimestamp()-registeredAgo).Error)
	return user.Id
}

func TestGuardRegistrationDeviceIsOffByDefault(t *testing.T) {
	setupRegistrationDeviceTestState(t, false)
	seedRegistrationDevice(t, "203.0.113.10", testChromeUserAgent, 60)

	// The compatibility setting can be disabled, and a repeat registration still passes.
	require.NoError(t, GuardRegistrationDevice(nil, "203.0.113.10", testChromeUserAgent))
}

func TestGuardRegistrationDeviceDeniesExactAddressMatch(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	seedRegistrationDevice(t, "203.0.113.10", testChromeUserAgent, 60)

	// Registration remains successful; the exact match is diagnostic and trial-quota
	// protection is handled by BeginRegistrationTrial.
	require.NoError(t, GuardRegistrationDevice(nil, "203.0.113.10", testChromeUserAgent))
	assert.ErrorIs(t, CheckRegistrationDevice(nil, "203.0.113.10", testChromeUserAgent), ErrRegistrationDeviceLimited)
}

func TestGuardRegistrationDeviceAllowsDifferentAddressInSameIPv4Slash24(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	seedRegistrationDevice(t, "203.0.113.10", testChromeUserAgent, 60)

	// This is the shared-carrier-NAT case: a whole /24 of ordinary users behind one
	// carrier must not be refused because a neighbour registered first. IPv4 has no
	// prefix rule for exactly this reason.
	require.NoError(t, GuardRegistrationDevice(nil, "203.0.113.11", testChromeUserAgent))
}

func TestGuardRegistrationDeviceDeniesRotatedIPv6WithSameUserAgent(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	seedRegistrationDevice(t, "2001:db8:1234:5678::1", testChromeUserAgent, 60)

	// The production rule is exact IP + UA. A rotated address is a different fingerprint.
	require.NoError(t, GuardRegistrationDevice(nil, "2001:db8:1234:5678:aaaa:bbbb:cccc:dddd", testChromeUserAgent))
}

func TestGuardRegistrationDeviceAllowsDifferentUserAgentInSameIPv6Prefix(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	seedRegistrationDevice(t, "2001:db8:1234:5678::1", testChromeUserAgent, 60)

	// A shared IPv6 allocation serves many people, so a different client in the same
	// prefix is a different device.
	require.NoError(t, GuardRegistrationDevice(nil, "2001:db8:1234:5678:aaaa::9", testFirefoxUserAgent))
}

func TestGuardRegistrationDeviceAllowsAfterTheWindow(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	seedRegistrationDevice(t, "203.0.113.10", testChromeUserAgent, registrationDedupWindowSeconds+60)

	require.NoError(t, GuardRegistrationDevice(nil, "203.0.113.10", testChromeUserAgent))
}

func TestGuardRegistrationDeviceHonoursTheWhitelist(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	seedRegistrationDevice(t, "203.0.113.10", testChromeUserAgent, 60)

	common.RegistrationDeviceLimitWhitelist = []string{"203.0.113.10", "10.0.0.0/8"}
	require.NoError(t, GuardRegistrationDevice(nil, "203.0.113.10", testChromeUserAgent))
	require.NoError(t, GuardRegistrationDevice(nil, "10.4.5.6", testChromeUserAgent))

	// Registration is never blocked by this compatibility shim, including unrelated addresses.
	common.RegistrationDeviceLimitWhitelist = []string{"10.0.0.0/8"}
	require.NoError(t, GuardRegistrationDevice(nil, "203.0.113.10", testChromeUserAgent))
}

// TestGuardRegistrationDeviceIgnoresOtherSecretGeneration covers what happens when the
// effective CRYPTO_SECRET changes: stored digests are no longer comparable, so the record
// must neither match (which would be a false positive) nor error out. Reaching the limit
// would prove the generation filter had been dropped.
func TestGuardRegistrationDeviceIgnoresOtherSecretGeneration(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	user := createReserveTestUser(t, 1000)
	require.NoError(t, RecordRegistrationDevice(nil, user.Id, "203.0.113.10", testChromeUserAgent))
	require.NoError(t, DB.Model(&XingyaRegistrationDevice{}).Where("user_id = ?", user.Id).
		Update("secret_version", "stale-generation").Error)

	require.NoError(t, GuardRegistrationDevice(nil, "203.0.113.10", testChromeUserAgent))
}

func TestGuardRegistrationDeviceAllowsUnparseableAddress(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)

	// An address this server cannot parse cannot be enforced against. Refusing would block
	// registrations for an infrastructure reason, so the check passes.
	require.NoError(t, GuardRegistrationDevice(nil, "", testChromeUserAgent))
	require.NoError(t, GuardRegistrationDevice(nil, "not-an-address", testChromeUserAgent))
}

func TestBeginRegistrationTrialUsesOneRedisClaimPerFingerprint(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	server := useUserCacheMiniRedis(t)
	oldQuota := common.QuotaForNewUser
	common.QuotaForNewUser = 100
	t.Cleanup(func() { common.QuotaForNewUser = oldQuota })

	first := BeginRegistrationTrial("203.0.113.10", testChromeUserAgent)
	assert.Equal(t, 100, first.Quota())
	first.Commit()

	repeat := BeginRegistrationTrial("203.0.113.10", testChromeUserAgent)
	assert.Zero(t, repeat.Quota())

	differentUserAgent := BeginRegistrationTrial("203.0.113.10", testFirefoxUserAgent)
	assert.Equal(t, 100, differentUserAgent.Quota())
	differentUserAgent.Rollback()

	server.FastForward(7 * 24 * time.Hour)
	afterWindow := BeginRegistrationTrial("203.0.113.10", testChromeUserAgent)
	assert.Equal(t, 100, afterWindow.Quota())
	afterWindow.Rollback()
}

func TestConcurrentRegistrationTrialsOnlyOneGetsTrialQuota(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	useUserCacheMiniRedis(t)
	oldQuota := common.QuotaForNewUser
	common.QuotaForNewUser = 100
	t.Cleanup(func() { common.QuotaForNewUser = oldQuota })

	start := make(chan struct{})
	results := make(chan *RegistrationTrialReservation, 2)
	for range 2 {
		go func() {
			<-start
			results <- BeginRegistrationTrial("203.0.113.20", testChromeUserAgent)
		}()
	}
	close(start)

	trialQuotaCount := 0
	for range 2 {
		reservation := <-results
		if reservation.Quota() > 0 {
			trialQuotaCount++
			reservation.Commit()
		} else {
			reservation.Rollback()
		}
	}
	assert.Equal(t, 1, trialQuotaCount)
}

func TestRegistrationTrialFailsOpenWhenRedisIsUnavailable(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	server := useUserCacheMiniRedis(t)
	server.Close()
	oldQuota := common.QuotaForNewUser
	common.QuotaForNewUser = 100
	t.Cleanup(func() { common.QuotaForNewUser = oldQuota })

	reservation := BeginRegistrationTrial("203.0.113.30", testChromeUserAgent)
	assert.Equal(t, 100, reservation.Quota())
}

func TestRegistrationDeviceCanonicalisesEquivalentAddressForms(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	// The IPv4-mapped form of 203.0.113.10 must hash to the same digest as the plain form.
	seedRegistrationDevice(t, "203.0.113.10", testChromeUserAgent, 60)

	require.NoError(t, GuardRegistrationDevice(nil, "::ffff:203.0.113.10", testChromeUserAgent))
	err := CheckRegistrationDevice(nil, "::ffff:203.0.113.10", testChromeUserAgent)
	assert.ErrorIs(t, err, ErrRegistrationDeviceLimited)
	// A client address carrying a port resolves to the same device.
	err = CheckRegistrationDevice(nil, "203.0.113.10:51234", testChromeUserAgent)
	assert.ErrorIs(t, err, ErrRegistrationDeviceLimited)
}

func TestRecordRegistrationDeviceIsIdempotentPerUser(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	user := createReserveTestUser(t, 1000)

	require.NoError(t, RecordRegistrationDevice(nil, user.Id, "203.0.113.10", testChromeUserAgent))
	require.NoError(t, RecordRegistrationDevice(nil, user.Id, "203.0.113.99", testFirefoxUserAgent))

	var count int64
	require.NoError(t, DB.Model(&XingyaRegistrationDevice{}).Where("user_id = ?", user.Id).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestGetRegistrationRiskGroupsReturnsDuplicateAccountsWithoutRawIP(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	first := seedRegistrationDevice(t, "203.0.113.40", testChromeUserAgent, 60)
	second := seedRegistrationDevice(t, "203.0.113.40", testChromeUserAgent, 30)
	unknown := seedRegistrationDevice(t, "not-an-address", testChromeUserAgent, 15)

	groups, total, err := GetRegistrationRiskGroups(common.GetTimestamp()-int64(time.Hour/time.Second), common.GetTimestamp(), 2, 0, 20)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, groups, 1)
	assert.EqualValues(t, 2, groups[0].RepeatCount)
	assert.NotContains(t, groups[0].IPFingerprint, "203.0.113.40")
	assert.NotContains(t, groups[0].IPFingerprint, "not-an-address")
	require.Len(t, groups[0].Registrations, 2)
	assert.ElementsMatch(t, []int{first, second}, []int{groups[0].Registrations[0].UserId, groups[0].Registrations[1].UserId})
	assert.NotEqual(t, unknown, groups[0].Registrations[0].UserId)
}

func TestGetRegistrationRiskGroupsPaginatesGroupsBeforeLoadingRegistrations(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	firstGroupFirst := seedRegistrationDevice(t, "203.0.113.50", testChromeUserAgent, 60)
	firstGroupSecond := seedRegistrationDevice(t, "203.0.113.50", testChromeUserAgent, 30)
	secondGroupFirst := seedRegistrationDevice(t, "203.0.113.51", testFirefoxUserAgent, 90)
	secondGroupSecond := seedRegistrationDevice(t, "203.0.113.51", testFirefoxUserAgent, 45)

	now := common.GetTimestamp()
	groups, total, err := GetRegistrationRiskGroups(now-int64(time.Hour/time.Second), now, 2, 0, 1)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	require.Len(t, groups, 1)
	assert.ElementsMatch(t, []int{firstGroupFirst, firstGroupSecond}, []int{
		groups[0].Registrations[0].UserId,
		groups[0].Registrations[1].UserId,
	})

	groups, total, err = GetRegistrationRiskGroups(now-int64(time.Hour/time.Second), now, 2, 1, 1)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	require.Len(t, groups, 1)
	assert.ElementsMatch(t, []int{secondGroupFirst, secondGroupSecond}, []int{
		groups[0].Registrations[0].UserId,
		groups[0].Registrations[1].UserId,
	})
}

func TestPruneRegistrationDevicesRemovesOnlyExpiredRows(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	stale := seedRegistrationDevice(t, "203.0.113.10", testChromeUserAgent, registrationDeviceRetentionSeconds+60)
	fresh := seedRegistrationDevice(t, "203.0.113.11", testFirefoxUserAgent, 60)

	removed, err := PruneRegistrationDevices()
	require.NoError(t, err)
	assert.EqualValues(t, 1, removed)

	var remaining []XingyaRegistrationDevice
	require.NoError(t, DB.Find(&remaining).Error)
	require.Len(t, remaining, 1)
	assert.Equal(t, fresh, remaining[0].UserId)
	assert.NotEqual(t, stale, remaining[0].UserId)
}
