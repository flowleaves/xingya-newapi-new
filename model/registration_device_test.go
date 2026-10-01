package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testChromeUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120.0"
const testFirefoxUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Gecko/20100101 Firefox/121.0"

// setupRegistrationDeviceTestState isolates the device table and restores the global
// limit settings, which default to off in production so the whitelist can be reviewed
// before the limit starts rejecting signups.
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

	// The limit ships disabled, so a repeat registration must pass even from a known device.
	require.NoError(t, GuardRegistrationDevice(nil, "203.0.113.10", testChromeUserAgent))
}

func TestGuardRegistrationDeviceDeniesExactAddressMatch(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	seedRegistrationDevice(t, "203.0.113.10", testChromeUserAgent, 60)

	err := GuardRegistrationDevice(nil, "203.0.113.10", testChromeUserAgent)
	assert.ErrorIs(t, err, ErrRegistrationDeviceLimited)
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

	// Address rotation inside one /64 is routine for a single subscriber, so the prefix
	// plus an identical client is the same device.
	err := GuardRegistrationDevice(nil, "2001:db8:1234:5678:aaaa:bbbb:cccc:dddd", testChromeUserAgent)
	assert.ErrorIs(t, err, ErrRegistrationDeviceLimited)
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

	// An unrelated address is still limited, so a whitelist entry cannot exempt everyone.
	common.RegistrationDeviceLimitWhitelist = []string{"10.0.0.0/8"}
	assert.ErrorIs(t, GuardRegistrationDevice(nil, "203.0.113.10", testChromeUserAgent), ErrRegistrationDeviceLimited)
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

func TestRegistrationDeviceCanonicalisesEquivalentAddressForms(t *testing.T) {
	setupRegistrationDeviceTestState(t, true)
	// The IPv4-mapped form of 203.0.113.10 must hash to the same digest as the plain form.
	seedRegistrationDevice(t, "203.0.113.10", testChromeUserAgent, 60)

	err := GuardRegistrationDevice(nil, "::ffff:203.0.113.10", testChromeUserAgent)
	assert.ErrorIs(t, err, ErrRegistrationDeviceLimited)
	// A client address carrying a port resolves to the same device.
	err = GuardRegistrationDevice(nil, "203.0.113.10:51234", testChromeUserAgent)
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
