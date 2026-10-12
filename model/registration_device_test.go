package model

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testChromeUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120.0"
const testFirefoxUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Gecko/20100101 Firefox/121.0"

// testChromeUserAgentNextBuild is the same browser after a minor-version bump. It differs
// from testChromeUserAgent in the digits of the build number only, which is exactly the
// change a routine browser update makes.
const testChromeUserAgentNextBuild = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120.0.6099.130"
const testSafariUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 Version/17.2 Safari/605.1.15"

// registrationTrialContext is the context the registration controllers pass through to the
// trial claim. Tests call the same entry point, so they use the same placeholder.
func registrationTrialContext() context.Context {
	return context.Background()
}

// subnetBudgetKeyFor mirrors the bucket the trial budget is counted in, so a test can drive
// the budget without routing four real registrations through Redis.
func subnetBudgetKeyFor(t *testing.T, clientIp string) string {
	t.Helper()
	addr, ok := normalizeClientIP(clientIp)
	require.True(t, ok, "test address %q must parse", clientIp)
	key := registrationSubnetKey(addr)
	require.NotEmpty(t, key)
	return registrationSubnetBudgetKey(key)
}

// setupRegistrationDeviceTestState isolates the device table and restores the global
// trial-protection settings after each case. The setting is enabled by default, but the
// protection only changes the trial quota and never rejects the signup itself.
func setupRegistrationDeviceTestState(t *testing.T, enabled bool) {
	t.Helper()
	truncateTables(t)
	require.NoError(t, InitXingyaTables(DB))
	require.NoError(t, DB.Exec("DELETE FROM xingya_registration_devices").Error)

	oldEnabled := common.RegistrationDeviceLimitEnabled
	oldWhitelist := common.RegistrationDeviceLimitWhitelist
	oldBudget := common.RegistrationSubnetBudget
	oldWindow := common.RegistrationSubnetBudgetWindowSeconds
	common.RegistrationDeviceLimitEnabled = enabled
	common.RegistrationDeviceLimitWhitelist = []string{}
	common.RegistrationSubnetBudget = 3
	common.RegistrationSubnetBudgetWindowSeconds = 30 * 24 * 60 * 60
	t.Cleanup(func() {
		common.RegistrationDeviceLimitEnabled = oldEnabled
		common.RegistrationDeviceLimitWhitelist = oldWhitelist
		common.RegistrationSubnetBudget = oldBudget
		common.RegistrationSubnetBudgetWindowSeconds = oldWindow
		DB.Exec("DELETE FROM xingya_registration_devices")
	})
}

// useTrialQuota pins the new-user gift for the duration of a test and returns the value the
// trial claim is expected to hand out.
func useTrialQuota(t *testing.T, quota int) int {
	t.Helper()
	oldQuota := common.QuotaForNewUser
	common.QuotaForNewUser = quota
	t.Cleanup(func() { common.QuotaForNewUser = oldQuota })
	return quota
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

func TestCheckRegistrationDeviceIsOffWhenTheLimitIsDisabled(t *testing.T) {
	setupRegistrationDeviceTestState(t, false)
	seedRegistrationDevice(t, "203.0.113.10", testChromeUserAgent, 60)

	require.NoError(t, CheckRegistrationDevice(nil, "203.0.113.10", testChromeUserAgent))
}

func TestCheckRegistrationDeviceDeniesTheSameAddressAndClientClass(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	seedRegistrationDevice(t, "203.0.113.10", testChromeUserAgent, 60)

	err := CheckRegistrationDevice(nil, "203.0.113.10", testChromeUserAgent)
	assert.ErrorIs(t, err, ErrRegistrationDeviceLimited)
}

// A browser update changes the exact user agent while leaving the platform and browser
// family untouched. The trial must still be recognised as already spent, otherwise the one
// action every legitimate user performs is also the action that resets the limit.
func TestCheckRegistrationDeviceDeniesTheSameAddressAfterABrowserBuildChange(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	seedRegistrationDevice(t, "203.0.113.10", testChromeUserAgent, 60)

	err := CheckRegistrationDevice(nil, "203.0.113.10", testChromeUserAgentNextBuild)
	assert.ErrorIs(t, err, ErrRegistrationDeviceLimited)
}

// Two people behind one carrier NAT are different people. A different address in the same
// /24 must keep its own trial allowance, which is why the address itself is matched exactly
// and the network is only charged against a budget.
func TestCheckRegistrationDeviceAllowsADifferentAddressInTheSameSlash24(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	seedRegistrationDevice(t, "203.0.113.10", testChromeUserAgent, 60)

	require.NoError(t, CheckRegistrationDevice(nil, "203.0.113.11", testChromeUserAgent))
}

// A rotated address inside one IPv6 allocation is the same subscriber. It is not refused by
// the address rule — the address genuinely differs — but it is what the subnet budget is
// for, so a single rotation still clears while the budget is unspent.
func TestCheckRegistrationDeviceAllowsARotatedIPv6AddressWithinBudget(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	seedRegistrationDevice(t, "2001:db8:1234:5678::1", testChromeUserAgent, 60)

	require.NoError(t, CheckRegistrationDevice(nil, "2001:db8:1234:5678:aaaa:bbbb:cccc:dddd", testChromeUserAgent))
}

func TestCheckRegistrationDeviceAllowsADifferentPlatformOnTheSameAddress(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	seedRegistrationDevice(t, "203.0.113.10", testChromeUserAgent, 60)

	// A macOS Safari client is a different client class, not merely a different build.
	require.NoError(t, CheckRegistrationDevice(nil, "203.0.113.10", testSafariUserAgent))
}

func TestCheckRegistrationDeviceAllowsAfterTheWindow(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	seedRegistrationDevice(t, "203.0.113.10", testChromeUserAgent, registrationDedupWindowSeconds+60)

	require.NoError(t, CheckRegistrationDevice(nil, "203.0.113.10", testChromeUserAgent))
}

func TestCheckRegistrationDeviceHonoursTheWhitelist(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	seedRegistrationDevice(t, "203.0.113.10", testChromeUserAgent, 60)

	common.RegistrationDeviceLimitWhitelist = []string{"203.0.113.10", "10.0.0.0/8"}
	reservation := BeginRegistrationTrial(registrationTrialContext(), "203.0.113.10", testChromeUserAgent)
	assert.Equal(t, common.QuotaForNewUser, reservation.Quota())

	common.RegistrationDeviceLimitWhitelist = []string{"10.0.0.0/8"}
	err := CheckRegistrationDevice(nil, "203.0.113.10", testChromeUserAgent)
	assert.ErrorIs(t, err, ErrRegistrationDeviceLimited)
}

// TestCheckRegistrationDeviceIgnoresOtherSecretGeneration covers what happens when the
// effective CRYPTO_SECRET changes: stored digests are no longer comparable, so the record
// must neither match (which would be a false positive) nor error out. Reaching the limit
// would prove the generation filter had been dropped.
func TestCheckRegistrationDeviceIgnoresOtherSecretGeneration(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	user := createReserveTestUser(t, 1000)
	require.NoError(t, RecordRegistrationDevice(nil, user.Id, "203.0.113.10", testChromeUserAgent))
	require.NoError(t, DB.Model(&XingyaRegistrationDevice{}).Where("user_id = ?", user.Id).
		Update("secret_version", "stale-generation").Error)

	require.NoError(t, CheckRegistrationDevice(nil, "203.0.113.10", testChromeUserAgent))
}

func TestCheckRegistrationDeviceAllowsUnparseableAddress(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)

	// An address this server cannot parse cannot be enforced against. Refusing would block
	// registrations for an infrastructure reason, so the check passes.
	require.NoError(t, CheckRegistrationDevice(nil, "", testChromeUserAgent))
	require.NoError(t, CheckRegistrationDevice(nil, "not-an-address", testChromeUserAgent))
}

func TestRegistrationUserAgentBucketGroupsBuildChanges(t *testing.T) {
	// The exact agents differ, so the bucket is the only reason these compare equal.
	require.NotEqual(t, testChromeUserAgent, testChromeUserAgentNextBuild)
	assert.Equal(t,
		registrationUserAgentBucket(testChromeUserAgent),
		registrationUserAgentBucket(testChromeUserAgentNextBuild))
	assert.Equal(t, "windowsnt Chrome 120", registrationUserAgentBucket(testChromeUserAgent))
}

func TestRegistrationUserAgentBucketSeparatesPlatformAndBrowserFamily(t *testing.T) {
	chrome := registrationUserAgentBucket(testChromeUserAgent)
	firefox := registrationUserAgentBucket(testFirefoxUserAgent)
	safari := registrationUserAgentBucket(testSafariUserAgent)

	// The platform is shared, the browser family is not.
	assert.NotEqual(t, chrome, firefox)
	assert.Contains(t, chrome, "windowsnt")
	assert.Contains(t, firefox, "windowsnt")
	assert.Contains(t, safari, "macintosh")
	assert.NotContains(t, safari, "windowsnt")
	assert.Contains(t, safari, "Safari")
}

func TestRegistrationUserAgentBucketFallsBackForUnknownAgents(t *testing.T) {
	// A scripted client presents no platform or browser token. It must still produce one
	// stable bucket, otherwise a missing header would escape comparison entirely.
	assert.Equal(t, "unknown other 0", registrationUserAgentBucket(""))
	assert.Equal(t, registrationUserAgentBucket(""), registrationUserAgentBucket("   "))
	assert.Equal(t, registrationUserAgentBucket(""), registrationUserAgentBucket("curl/8.4.0"))
}

func TestRegistrationUserAgentBucketPrioritizesSpecificFamilies(t *testing.T) {
	chrome := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36"
	for _, tc := range []struct{ name, agent, want string }{
		{"Edge", chrome + " Edg/141.0.0.0", "windowsnt Edge 141"},
		{"Opera", chrome + " OPR/123.0.0.0", "windowsnt Opera 123"},
		{"Android", "Mozilla/5.0 (Linux; Android 15) AppleWebKit/537.36 Chrome/141.0 Mobile Safari/537.36", "android Chrome 141"},
		{"iPhone", "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 CriOS/141.0 Mobile Safari/604.1", "iphone Chrome 141"},
	} {
		t.Run(tc.name, func(t *testing.T) { assert.Equal(t, tc.want, registrationUserAgentBucket(tc.agent)) })
	}
}

func TestRegistrationUserAgentTruncationPreservesUTF8(t *testing.T) {
	for _, prefixLength := range []int{509, 510, 511} {
		agent := strings.Repeat("a", prefixLength) + "星芽"
		truncated := truncateRegistrationUserAgent(agent)
		assert.True(t, utf8.ValidString(truncated))
		assert.LessOrEqual(t, len(truncated), 512)
		assert.True(t, strings.HasPrefix(agent, truncated))
	}
}

func TestDistinctBrowserFamiliesKeepSeparateTrialFingerprints(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	useUserCacheMiniRedis(t)
	quota := useTrialQuota(t, 100)
	chrome := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36"
	first := BeginRegistrationTrial(registrationTrialContext(), "203.0.113.80", chrome)
	require.Equal(t, quota, first.Quota())
	user := createReserveTestUser(t, first.Quota())
	first.Commit()
	require.NoError(t, RecordRegistrationDevice(nil, user.Id, "203.0.113.80", chrome))
	second := BeginRegistrationTrial(registrationTrialContext(), "203.0.113.80", chrome+" Edg/141.0.0.0")
	defer second.Rollback()
	assert.Equal(t, quota, second.Quota())
}

// The agent header is attacker-controlled, so cosmetic edits must not mint a new bucket.
func TestRegistrationUserAgentBucketIgnoresCosmeticAgentEdits(t *testing.T) {
	canonical := registrationUserAgentBucket(testChromeUserAgent)
	// The engine version always precedes the browser version; reading the wrong number
	// would put every client in one bucket.
	assert.Equal(t, "windowsnt Chrome 120", canonical)

	lowercased := "Mozilla/5.0 (windows nt 10.0; win64; x64) AppleWebKit/537.36 chrome/120.0"
	assert.Equal(t, canonical, registrationUserAgentBucket(lowercased))

	trimmed := "  " + testChromeUserAgent + "\t"
	assert.Equal(t, canonical, registrationUserAgentBucket(trimmed))
}

func TestRecordRegistrationDeviceStoresTheNetworkAndTheNormalizedAgent(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	user := createReserveTestUser(t, 1000)
	require.NoError(t, RecordRegistrationDevice(nil, user.Id, "203.0.113.10", testChromeUserAgent))

	device, err := GetRegistrationDevice(user.Id)
	require.NoError(t, err)
	assert.Equal(t, "203.0.113.0/24", device.IpPrefix)
	assert.Equal(t, "203.0.113.10", device.ClientIP)
	// The raw agent is still stored verbatim for review; only the comparison is normalized.
	assert.Equal(t, testChromeUserAgent, device.UserAgent)
	assert.Equal(t, common.GenerateHMAC(registrationUserAgentBucket(testChromeUserAgent)), device.UaBucketHash)
	assert.Equal(t, common.GenerateHMAC(testChromeUserAgent), device.UaHash)
}

func TestBeginRegistrationTrialUsesOneRedisClaimPerClientClass(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	server := useUserCacheMiniRedis(t)
	quota := useTrialQuota(t, 100)

	first := BeginRegistrationTrial(registrationTrialContext(), "203.0.113.10", testChromeUserAgent)
	assert.Equal(t, quota, first.Quota())
	first.Commit()

	repeat := BeginRegistrationTrial(registrationTrialContext(), "203.0.113.10", testChromeUserAgent)
	assert.Zero(t, repeat.Quota())

	// The claim is held for the client class, so a build change does not win a second trial
	// while the same registration is still in flight.
	bumped := BeginRegistrationTrial(registrationTrialContext(), "203.0.113.10", testChromeUserAgentNextBuild)
	assert.Zero(t, bumped.Quota())

	differentBrowser := BeginRegistrationTrial(registrationTrialContext(), "203.0.113.10", testFirefoxUserAgent)
	assert.Equal(t, quota, differentBrowser.Quota())
	differentBrowser.Rollback()

	server.FastForward(7 * 24 * time.Hour)
	afterWindow := BeginRegistrationTrial(registrationTrialContext(), "203.0.113.10", testChromeUserAgent)
	assert.Equal(t, quota, afterWindow.Quota())
	afterWindow.Rollback()
}

// The subnet budget is the backstop for a client that rotates its address inside one
// allocation, where the address rule can never match. Each registration below arrives from
// a different address in the same /24, so only the budget can stop it.
func TestBeginRegistrationTrialStopsAnAddressRotationOnceTheSubnetBudgetIsSpent(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	server := useUserCacheMiniRedis(t)
	quota := useTrialQuota(t, 100)
	// One slot is already spent, leaving two for the rotation below.
	require.NoError(t, server.Set(subnetBudgetKeyFor(t, "203.0.113.1"), "1"))

	for _, clientIp := range []string{"203.0.113.2", "203.0.113.3"} {
		reservation := BeginRegistrationTrial(registrationTrialContext(), clientIp, testChromeUserAgent)
		assert.Equal(t, quota, reservation.Quota(), "address %s is still within the budget", clientIp)
		reservation.Commit()
	}

	exhausted := BeginRegistrationTrial(registrationTrialContext(), "203.0.113.4", testChromeUserAgent)
	assert.Zero(t, exhausted.Quota())

	// A different allocation is unaffected: the budget bounds one network, not the site.
	otherNetwork := BeginRegistrationTrial(registrationTrialContext(), "198.51.100.4", testChromeUserAgent)
	assert.Equal(t, quota, otherNetwork.Quota())
	otherNetwork.Rollback()
}

func TestBeginRegistrationTrialSubnetBudgetCanBeDisabled(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	server := useUserCacheMiniRedis(t)
	quota := useTrialQuota(t, 100)
	common.RegistrationSubnetBudget = 0
	require.NoError(t, server.Set(subnetBudgetKeyFor(t, "203.0.113.1"), "99"))

	reservation := BeginRegistrationTrial(registrationTrialContext(), "203.0.113.2", testChromeUserAgent)
	assert.Equal(t, quota, reservation.Quota())
}

func TestConcurrentRegistrationTrialsOnlyOneGetsTrialQuota(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	useUserCacheMiniRedis(t)
	quota := useTrialQuota(t, 100)

	start := make(chan struct{})
	results := make(chan *RegistrationTrialReservation, 2)
	for range 2 {
		go func() {
			<-start
			results <- BeginRegistrationTrial(registrationTrialContext(), "203.0.113.20", testChromeUserAgent)
		}()
	}
	close(start)

	trialQuotaCount := 0
	for range 2 {
		reservation := <-results
		if reservation.Quota() > 0 {
			assert.Equal(t, quota, reservation.Quota())
			trialQuotaCount++
			reservation.Commit()
		} else {
			reservation.Rollback()
		}
	}
	assert.Equal(t, 1, trialQuotaCount)
}

func TestConcurrentSubnetTrialsStayWithinBudget(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	server := useUserCacheMiniRedis(t)
	quota := useTrialQuota(t, 100)
	start := make(chan struct{})
	results := make(chan *RegistrationTrialReservation, 6)
	for i := range 6 {
		go func() {
			<-start
			results <- BeginRegistrationTrial(registrationTrialContext(), fmt.Sprintf("203.0.113.%d", i+20), testChromeUserAgent)
		}()
	}
	close(start)
	reservations := make([]*RegistrationTrialReservation, 6)
	for i := range reservations {
		reservations[i] = <-results
	}
	total := 0
	for _, reservation := range reservations {
		total += reservation.Quota()
		reservation.Commit()
		reservation.Commit()
		reservation.Rollback()
	}
	assert.Equal(t, quota*common.RegistrationSubnetBudget, total)
	claimed, err := server.Get(subnetBudgetKeyFor(t, "203.0.113.20"))
	require.NoError(t, err)
	assert.Equal(t, "3", claimed, "repeated Commit must not charge another trial")
}

func TestZeroQuotaRegistrationsDoNotSpendSubnetBudget(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	server := useUserCacheMiniRedis(t)
	quota := useTrialQuota(t, 100)
	first := BeginRegistrationTrial(registrationTrialContext(), "203.0.113.40", testChromeUserAgent)
	second := BeginRegistrationTrial(registrationTrialContext(), "203.0.113.40", testChromeUserAgent)
	require.Equal(t, quota, first.Quota())
	require.Zero(t, second.Quota())
	first.Commit()
	second.Commit()
	claimed, err := server.Get(subnetBudgetKeyFor(t, "203.0.113.40"))
	require.NoError(t, err)
	assert.Equal(t, "1", claimed)
	next := BeginRegistrationTrial(registrationTrialContext(), "203.0.113.41", testFirefoxUserAgent)
	defer next.Rollback()
	assert.Equal(t, quota, next.Quota())
}

func TestCancelledSignupContextCannotBypassPendingSubnetBudget(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	useUserCacheMiniRedis(t)
	quota := useTrialQuota(t, 100)
	common.RegistrationSubnetBudget = 1
	first := BeginRegistrationTrial(registrationTrialContext(), "203.0.113.40", testChromeUserAgent)
	defer first.Rollback()
	require.Equal(t, quota, first.Quota())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	second := BeginRegistrationTrial(ctx, "203.0.113.41", testChromeUserAgent)
	defer second.Rollback()
	assert.Zero(t, second.Quota(), "client cancellation must not masquerade as a Redis outage")
}

func TestFailedOrExpiredSubnetReservationReleasesCapacity(t *testing.T) {
	for _, expire := range []bool{false, true} {
		t.Run(fmt.Sprintf("expired=%t", expire), func(t *testing.T) {
			setupRegistrationDeviceTestState(t, true)
			server := useUserCacheMiniRedis(t)
			quota := useTrialQuota(t, 100)
			common.RegistrationSubnetBudget = 1
			first := BeginRegistrationTrial(registrationTrialContext(), "203.0.113.40", testChromeUserAgent)
			require.Equal(t, quota, first.Quota())
			blocked := BeginRegistrationTrial(registrationTrialContext(), "203.0.113.41", testChromeUserAgent)
			assert.Zero(t, blocked.Quota())
			if expire {
				server.FastForward(time.Duration(registrationTrialReservationSeconds+1) * time.Second)
			} else {
				first.Rollback()
			}
			next := BeginRegistrationTrial(registrationTrialContext(), "203.0.113.41", testChromeUserAgent)
			defer next.Rollback()
			assert.Equal(t, quota, next.Quota())
		})
	}
}

func TestExpiredRegistrationRollbackCannotReleaseANewLease(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	server := useUserCacheMiniRedis(t)
	quota := useTrialQuota(t, 100)
	common.RegistrationSubnetBudget = 1
	old := BeginRegistrationTrial(registrationTrialContext(), "203.0.113.40", testChromeUserAgent)
	require.Equal(t, quota, old.Quota())
	server.FastForward(time.Duration(registrationTrialReservationSeconds+1) * time.Second)
	next := BeginRegistrationTrial(registrationTrialContext(), "203.0.113.40", testChromeUserAgent)
	require.Equal(t, quota, next.Quota())
	retry := *next
	old.Rollback()
	blocked := BeginRegistrationTrial(registrationTrialContext(), "203.0.113.41", testChromeUserAgent)
	assert.Zero(t, blocked.Quota())
	next.Commit()
	retry.Commit()
	claimed, err := server.Get(subnetBudgetKeyFor(t, "203.0.113.40"))
	require.NoError(t, err)
	assert.Equal(t, "1", claimed)
}

func TestRegistrationTrialFailsOpenWhenRedisIsUnavailable(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	server := useUserCacheMiniRedis(t)
	server.Close()
	quota := useTrialQuota(t, 100)

	reservation := BeginRegistrationTrial(registrationTrialContext(), "203.0.113.30", testChromeUserAgent)
	assert.Equal(t, quota, reservation.Quota())
}

func TestRegistrationDeviceCanonicalisesEquivalentAddressForms(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	// The IPv4-mapped form of 203.0.113.10 must hash to the same digest as the plain form.
	seedRegistrationDevice(t, "203.0.113.10", testChromeUserAgent, 60)

	err := CheckRegistrationDevice(nil, "::ffff:203.0.113.10", testChromeUserAgent)
	assert.ErrorIs(t, err, ErrRegistrationDeviceLimited)
	// A client address carrying a port resolves to the same device.
	err = CheckRegistrationDevice(nil, "203.0.113.10:51234", testChromeUserAgent)
	assert.ErrorIs(t, err, ErrRegistrationDeviceLimited)
	// And the network a mapped address belongs to is the network of the plain form.
	mapped, ok := normalizeClientIP("::ffff:203.0.113.10")
	require.True(t, ok)
	plain, ok := normalizeClientIP("203.0.113.10")
	require.True(t, ok)
	assert.Equal(t, registrationSubnetKey(plain), registrationSubnetKey(mapped))
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

func TestGetRegistrationRiskGroupsReturnsDuplicateAccountsWithExactIP(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	first := seedRegistrationDevice(t, "203.0.113.40", testChromeUserAgent, 60)
	second := seedRegistrationDevice(t, "203.0.113.40", testChromeUserAgent, 30)
	unknown := seedRegistrationDevice(t, "not-an-address", testChromeUserAgent, 15)

	groups, total, err := GetRegistrationRiskGroups(common.GetTimestamp()-int64(time.Hour/time.Second), common.GetTimestamp(), 2, 0, 20)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, groups, 1)
	assert.EqualValues(t, 2, groups[0].RepeatCount)
	assert.Equal(t, "203.0.113.40", groups[0].ClientIP)
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

	// Same-second groups must not be repeated or skipped when changing pages.
	require.NoError(t, DB.Model(&XingyaRegistrationDevice{}).Where("user_id IN ?", []int{firstGroupFirst, firstGroupSecond, secondGroupFirst, secondGroupSecond}).Update("reg_time", now).Error)
	firstPage, _, err := GetRegistrationRiskGroups(now, now, 2, 0, 1)
	require.NoError(t, err)
	secondPage, _, err := GetRegistrationRiskGroups(now, now, 2, 1, 1)
	require.NoError(t, err)
	require.Len(t, firstPage, 1)
	require.Len(t, secondPage, 1)
	assert.NotEqual(t, firstPage[0].IPFingerprint, secondPage[0].IPFingerprint)
	repeatedPage, _, err := GetRegistrationRiskGroups(now, now, 2, 0, 1)
	require.NoError(t, err)
	assert.Equal(t, firstPage, repeatedPage)
}

// The normalized digest was added after the table shipped, so the upgrade has to extend a
// populated table. This is the one path where a wrong column definition takes the whole
// deployment down: SQLite and PostgreSQL both refuse to add a NOT NULL column without a
// default to a table that already has rows.
func TestMigrationAddsTheNormalizedDigestToAPopulatedLegacyTable(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Migrator().DropTable(&XingyaRegistrationDevice{}))
	// This case builds its own table shape and seeds a row the other cases must not see.
	t.Cleanup(func() {
		DB.Exec("DELETE FROM xingya_registration_devices")
	})
	// Match the released model before UaBucketHash existed. Let GORM emit the shipped
	// table and its unique user index on every supported database engine.
	legacyDevice := struct {
		Id            int    `gorm:"primaryKey;autoIncrement"`
		UserId        int    `gorm:"not null;uniqueIndex:idx_xingya_registration_devices_user"`
		IpHash        string `gorm:"type:char(64);not null;index:idx_xingya_regdev_ip_ua_time,priority:1"`
		IpPrefix      string `gorm:"type:varchar(64)"`
		UserAgent     string `gorm:"type:varchar(512)"`
		UaHash        string `gorm:"type:char(64);not null;index:idx_xingya_regdev_ip_ua_time,priority:2"`
		SecretVersion string `gorm:"type:varchar(16);not null"`
		RegTime       int64  `gorm:"bigint;not null;index:idx_xingya_regdev_ip_ua_time,priority:3"`
	}{
		UserId: 77, IpHash: "legacy-ip", UserAgent: "legacy-agent", UaHash: "legacy-ua",
		SecretVersion: "legacy-secret", RegTime: common.GetTimestamp(),
	}
	require.NoError(t, DB.Table("xingya_registration_devices").AutoMigrate(&legacyDevice))
	require.NoError(t, DB.Table("xingya_registration_devices").Create(&legacyDevice).Error)

	require.NoError(t, InitXingyaTables(DB))
	assert.True(t, DB.Migrator().HasColumn(&XingyaRegistrationDevice{}, "UaBucketHash"))
	assert.True(t, DB.Migrator().HasColumn(&XingyaRegistrationDevice{}, "ClientIP"))

	var legacy XingyaRegistrationDevice
	require.NoError(t, DB.Where("user_id = ?", 77).First(&legacy).Error)
	assert.Equal(t, "legacy-agent", legacy.UserAgent, "the upgrade must preserve existing rows")
	assert.Empty(t, legacy.UaBucketHash, "a row predating the digest carries no normalized value")
	assert.Empty(t, legacy.ClientIP, "historical exact addresses cannot be reconstructed")

	// Startup runs this on every boot, so a second pass must be a no-op.
	require.NoError(t, InitXingyaTables(DB))
	assert.True(t, DB.Migrator().HasColumn(&XingyaRegistrationDevice{}, "UaBucketHash"))
	assert.True(t, DB.Migrator().HasIndex(&XingyaRegistrationDevice{}, "idx_xingya_registration_devices_user"))
	require.NoError(t, RecordRegistrationDevice(DB, 77, "203.0.113.77", testChromeUserAgent))
	var count int64
	require.NoError(t, DB.Model(&XingyaRegistrationDevice{}).Where("user_id = ?", 77).Count(&count).Error)
	assert.EqualValues(t, 1, count, "the upgrade must preserve registration uniqueness")
	var afterRestart XingyaRegistrationDevice
	require.NoError(t, DB.Where("user_id = ?", 77).First(&afterRestart).Error)
	assert.Equal(t, legacy, afterRestart, "repeated startup and duplicate writes must preserve legacy data")
}

func TestRegistrationAuditStoresNormalizedIPAndKeepsOriginalOnRetry(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	for _, test := range []struct{ address, expected string }{
		{"203.0.113.10:443", "203.0.113.10"},
		{"[2001:db8:0:0:0:0:0:1]:443", "2001:db8::1"},
		{"::ffff:203.0.113.11", "203.0.113.11"},
		{"not-an-ip", ""},
	} {
		user := createReserveTestUser(t, 0)
		require.NoError(t, RecordRegistrationDevice(nil, user.Id, test.address, testChromeUserAgent))
		require.NoError(t, RecordRegistrationDevice(nil, user.Id, "198.51.100.99", testFirefoxUserAgent))
		device, err := GetRegistrationDevice(user.Id)
		require.NoError(t, err)
		assert.Equal(t, test.expected, device.ClientIP)
		assert.Equal(t, testChromeUserAgent, device.UserAgent)
		encoded, err := common.Marshal(device)
		require.NoError(t, err)
		assert.NotContains(t, string(encoded), "client_ip", "raw model JSON must not expose the address")
	}
}

func TestRegistrationAuditPagesAcceptedSignupsAndPreservesLegacyRows(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	user := createReserveTestUser(t, 0)
	// Build the shipped rc42 shape, rather than DropColumn (SQLite rebuilds lose indexes).
	require.NoError(t, DB.Migrator().DropTable(&XingyaRegistrationDevice{}))
	legacy := struct {
		Id            int    `gorm:"primaryKey;autoIncrement"`
		UserId        int    `gorm:"not null;uniqueIndex:idx_xingya_registration_devices_user"`
		IpHash        string `gorm:"type:char(64);not null;index:idx_xingya_regdev_ip_ua_time,priority:1"`
		IpPrefix      string `gorm:"type:varchar(64)"`
		UserAgent     string `gorm:"type:varchar(512)"`
		UaHash        string `gorm:"type:char(64);not null;index:idx_xingya_regdev_ip_ua_time,priority:2"`
		UaBucketHash  string `gorm:"type:char(64)"`
		SecretVersion string `gorm:"type:varchar(16);not null"`
		RegTime       int64  `gorm:"bigint;not null;index:idx_xingya_regdev_ip_ua_time,priority:3"`
	}{UserId: user.Id, IpHash: common.GenerateHMAC("203.0.113.10"), IpPrefix: "203.0.113.0/24",
		UserAgent: testChromeUserAgent, UaHash: common.GenerateHMAC(testChromeUserAgent),
		UaBucketHash: registrationUserAgentBucketHash(testChromeUserAgent), SecretVersion: registrationSecretVersion(), RegTime: 200}
	require.NoError(t, DB.Table("xingya_registration_devices").AutoMigrate(&legacy))
	require.NoError(t, DB.Table("xingya_registration_devices").Create(&legacy).Error)
	require.NoError(t, InitXingyaTables(DB))
	require.NoError(t, InitXingyaTables(DB))
	assert.True(t, DB.Migrator().HasIndex(&XingyaRegistrationDevice{}, "idx_xingya_regdev_registration_time"))
	assert.True(t, DB.Migrator().HasIndex(&XingyaRegistrationDevice{}, "idx_xingya_registration_devices_user"))
	second := createReserveTestUser(t, 0)
	require.NoError(t, RecordRegistrationDevice(nil, second.Id, "2001:db8::2", testFirefoxUserAgent))
	require.NoError(t, DB.Model(&XingyaRegistrationDevice{}).Where("user_id = ?", second.Id).Update("reg_time", 200).Error)
	third := createReserveTestUser(t, 0)
	require.NoError(t, RecordRegistrationDevice(nil, third.Id, "203.0.113.12", testChromeUserAgent))
	require.NoError(t, DB.Model(&XingyaRegistrationDevice{}).Where("user_id = ?", third.Id).Update("reg_time", 300).Error)

	items, total, err := GetRegistrationDevices(200, 200, 0, 1)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total, "time bounds are inclusive and count is independent of pagination")
	require.Len(t, items, 1)
	assert.Equal(t, second.Id, items[0].UserId, "same-second signups use a stable id tie-breaker")
	assert.Equal(t, "2001:db8::2", items[0].ClientIP)
	assert.Equal(t, second.Username, items[0].Username)
	items, total, err = GetRegistrationDevices(200, 200, 1, 1)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	require.Len(t, items, 1)
	assert.Equal(t, user.Id, items[0].UserId)
	assert.Empty(t, items[0].ClientIP)
	assert.Equal(t, testChromeUserAgent, items[0].UserAgent)
	items, total, err = GetRegistrationDevices(200, 200, 2, 1)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	assert.Empty(t, items)
	items, total, err = GetRegistrationDevices(301, 400, 0, 20)
	require.NoError(t, err)
	assert.Zero(t, total)
	assert.NotNil(t, items)
	assert.Empty(t, items)
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
