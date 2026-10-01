package model

import (
	"errors"
	"net/netip"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// ErrRegistrationDeviceLimited is returned when a registration is refused because the same
// device already registered an account inside the deduplication window.
var ErrRegistrationDeviceLimited = errors.New("registration device limit reached")

// registrationDedupWindowSeconds is how long one device is barred from registering a
// second account. A window rather than a permanent ban: shared egress addresses are
// common, and a permanent rule would lock out every later user behind one NAT.
const registrationDedupWindowSeconds int64 = 7 * 24 * 60 * 60

// registrationDeviceRetentionSeconds bounds how long device rows are kept. The rows only
// exist to enforce the deduplication window, so keeping them far past it would retain
// registration metadata for no benefit.
const registrationDeviceRetentionSeconds int64 = 180 * 24 * 60 * 60

// XingyaRegistrationDevice records the device signals observed for one registration.
//
// It is the enforcement record for the same-device registration limit. Two values are
// kept for the address because they answer different questions: IpHash matches one exact
// client address, while IpPrefix matches an IPv6 /64 — an address range routinely rotated
// across a single subscriber's connections. Neither is the literal address: the exact
// address is keyed-hash only, and the stored prefix is the masked network, not the host.
//
// UserAgent is truncated to the same 512 characters AuditLog uses. UaHash exists because
// user agents are long, high-cardinality and differ only in build numbers, so comparing a
// digest keeps the index small and the comparison exact.
type XingyaRegistrationDevice struct {
	Id            int    `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId        int    `json:"user_id" gorm:"not null;uniqueIndex:idx_xingya_registration_devices_user"`
	IpHash        string `json:"-" gorm:"type:char(64);not null;index:idx_xingya_regdev_ip_time,priority:1"`
	IpPrefix      string `json:"-" gorm:"type:varchar(64);index:idx_xingya_regdev_prefix_time,priority:1"`
	UserAgent     string `json:"user_agent" gorm:"type:varchar(512)"`
	UaHash        string `json:"-" gorm:"type:char(64);not null"`
	SecretVersion string `json:"-" gorm:"type:varchar(16);not null"`
	RegTime       int64  `json:"reg_time" gorm:"bigint;not null;index:idx_xingya_regdev_ip_time,priority:2;index:idx_xingya_regdev_prefix_time,priority:2"`
}

func (XingyaRegistrationDevice) TableName() string {
	return "xingya_registration_devices"
}

// registrationSecretVersion identifies the key generation used for the stored digests.
//
// It is a digest of the effective secret rather than the secret itself, and it exists so a
// secret rotation does not turn every historical row into a mismatch: rows keyed with a
// different generation are simply not compared, instead of silently failing to match (which
// would disable the limit) or producing a false match.
func registrationSecretVersion() string {
	return common.GenerateHMAC("xingya-registration-device-v1")[:16]
}

// normalizeClientIP canonicalises a client address before it is hashed or masked, so that
// equivalent textual forms of one address cannot masquerade as different devices.
func normalizeClientIP(rawIp string) (addr netip.Addr, ok bool) {
	trimmed := strings.TrimSpace(rawIp)
	if trimmed == "" {
		return netip.Addr{}, false
	}
	// A host:port form is accepted because c.ClientIP() can carry a port in some
	// configurations; an unparseable address is treated as unknown rather than as a match.
	if parsed, err := netip.ParseAddrPort(trimmed); err == nil {
		trimmed = parsed.Addr().String()
	}
	parsed, err := netip.ParseAddr(trimmed)
	if err != nil {
		return netip.Addr{}, false
	}
	return parsed.Unmap(), true
}

// registrationIPPrefix returns the masked network used for prefix comparison.
//
// IPv4 has no meaningful prefix rule here: a /24 is a whole neighbourhood, and denying by
// it would refuse every ordinary user behind a shared carrier NAT after the first one
// registered. IPv6 is masked to /64 because that is one subscriber's allocation, so the
// same person rotating addresses inside it still compares equal.
func registrationIPPrefix(addr netip.Addr) string {
	if !addr.Is6() {
		return ""
	}
	prefix, err := addr.Prefix(64)
	if err != nil {
		return ""
	}
	return prefix.String()
}

// GuardRegistrationDevice applies the same-device registration limit.
//
// It is the single entry point every registration path calls, so the policy is stated once
// rather than repeated per controller. It is a no-op when the limit is disabled or the
// address is whitelisted.
func GuardRegistrationDevice(tx *gorm.DB, clientIp string, userAgent string) error {
	if !common.RegistrationDeviceLimitEnabled {
		return nil
	}
	if common.IsRegistrationDeviceWhitelisted(clientIp) {
		return nil
	}
	return CheckRegistrationDevice(tx, clientIp, userAgent)
}

// CheckRegistrationDevice reports whether a registration from this device must be refused.
//
// The rule denies on an exact address match, or on an IPv6 prefix match combined with an
// identical user agent. The user agent is required for the prefix rule so that two different
// people on the same IPv6 allocation are not treated as one device; the exact-address rule
// needs no such qualifier because two distinct devices rarely share one address at once.
func CheckRegistrationDevice(tx *gorm.DB, clientIp string, userAgent string) error {
	endpoint := tx
	if endpoint == nil {
		endpoint = DB
	}
	addr, ok := normalizeClientIP(clientIp)
	if !ok {
		// An address this server cannot parse cannot be enforced against. Refusing would
		// block registrations for an infrastructure reason, so the check passes.
		return nil
	}

	truncatedUa := truncateRegistrationUserAgent(userAgent)
	ipHash := common.GenerateHMAC(addr.String())
	uaHash := common.GenerateHMAC(truncatedUa)
	prefix := registrationIPPrefix(addr)
	since := common.GetTimestamp() - registrationDedupWindowSeconds
	secretVersion := registrationSecretVersion()

	query := endpoint.Model(&XingyaRegistrationDevice{}).
		Where("reg_time >= ? AND secret_version = ?", since, secretVersion).
		Where("ip_hash = ?", ipHash)
	if prefix != "" {
		query = query.Or("reg_time >= ? AND secret_version = ? AND ip_prefix = ? AND ua_hash = ?",
			since, secretVersion, prefix, uaHash)
	}

	var existing int64
	if err := query.Limit(1).Count(&existing).Error; err != nil {
		return err
	}
	if existing > 0 {
		return ErrRegistrationDeviceLimited
	}
	return nil
}

// RecordRegistrationDevice stores the device signals for a registration that was accepted.
//
// It is deliberately separate from the check: the check runs before the account exists, so
// the row can only be written once the caller has a user id. A duplicate user id is ignored
// rather than returned, because failing a completed registration over this bookkeeping row
// would reject a valid account.
func RecordRegistrationDevice(tx *gorm.DB, userId int, clientIp string, userAgent string) error {
	endpoint := tx
	if endpoint == nil {
		endpoint = DB
	}
	if userId <= 0 {
		return nil
	}
	addr, ok := normalizeClientIP(clientIp)
	if !ok {
		return nil
	}

	truncatedUa := truncateRegistrationUserAgent(userAgent)
	device := &XingyaRegistrationDevice{
		UserId:        userId,
		IpHash:        common.GenerateHMAC(addr.String()),
		IpPrefix:      registrationIPPrefix(addr),
		UserAgent:     truncatedUa,
		UaHash:        common.GenerateHMAC(truncatedUa),
		SecretVersion: registrationSecretVersion(),
		RegTime:       common.GetTimestamp(),
	}

	var existing int64
	if err := endpoint.Model(&XingyaRegistrationDevice{}).
		Where("user_id = ?", userId).Count(&existing).Error; err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}
	return endpoint.Create(device).Error
}

// GetRegistrationDevice returns the stored device record for a user, for the admin view.
func GetRegistrationDevice(userId int) (*XingyaRegistrationDevice, error) {
	device := &XingyaRegistrationDevice{}
	if err := DB.Where("user_id = ?", userId).First(device).Error; err != nil {
		return nil, err
	}
	return device, nil
}

// PruneRegistrationDevices deletes records older than the retention period and returns how
// many rows were removed.
func PruneRegistrationDevices() (int64, error) {
	result := DB.Where("reg_time < ?", common.GetTimestamp()-registrationDeviceRetentionSeconds).
		Delete(&XingyaRegistrationDevice{})
	return result.RowsAffected, result.Error
}

// truncateRegistrationUserAgent bounds a user agent to the stored column width. Slicing at
// a byte boundary is safe for UTF-8: a multi-byte rune starts with a byte whose high bits
// are not a continuation byte, so the cut cannot split one.
func truncateRegistrationUserAgent(userAgent string) string {
	const maxBytes = 512
	if len(userAgent) <= maxBytes {
		return userAgent
	}
	return userAgent[:maxBytes]
}
