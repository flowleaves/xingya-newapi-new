package model

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrRegistrationDeviceLimited is kept for compatibility with callers that used the old
// hard-blocking policy. New registration flows never return it: a matching device is
// allowed to create an account and only loses the registration trial quota.
var ErrRegistrationDeviceLimited = errors.New("registration device limit reached")

// registrationDedupWindowSeconds is how long a successful registration consumes the trial
// quota for one exact IP + user-agent fingerprint.
const registrationDedupWindowSeconds int64 = 7 * 24 * 60 * 60

const registrationTrialReservationSeconds int64 = 10 * 60

// registrationDeviceRetentionSeconds bounds how long device rows are kept. The rows only
// exist to enforce the deduplication window, so keeping them far past it would retain
// registration metadata for no benefit.
const registrationDeviceRetentionSeconds int64 = 180 * 24 * 60 * 60

func unknownRegistrationIPHash() string {
	return common.GenerateHMAC("unknown-registration-ip")
}

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
	IpHash        string `json:"-" gorm:"type:char(64);not null;index:idx_xingya_regdev_ip_ua_time,priority:1"`
	IpPrefix      string `json:"-" gorm:"type:varchar(64)"`
	UserAgent     string `json:"user_agent" gorm:"type:varchar(512)"`
	UaHash        string `json:"-" gorm:"type:char(64);not null;index:idx_xingya_regdev_ip_ua_time,priority:2"`
	SecretVersion string `json:"-" gorm:"type:varchar(16);not null"`
	RegTime       int64  `json:"reg_time" gorm:"bigint;not null;index:idx_xingya_regdev_ip_ua_time,priority:3"`
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

// GuardRegistrationDevice is retained as a no-op compatibility shim. Registration is no
// longer refused because of a device match; callers should use BeginRegistrationTrial to
// decide whether the new account receives the one-time registration quota.
func GuardRegistrationDevice(tx *gorm.DB, clientIp string, userAgent string) error {
	return nil
}

// CheckRegistrationDevice reports whether an exact IP + user-agent fingerprint exists in
// the recent registration history. It is diagnostic-only and must not be used to reject a
// registration.
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
	since := common.GetTimestamp() - registrationDedupWindowSeconds
	secretVersion := registrationSecretVersion()

	query := endpoint.Model(&XingyaRegistrationDevice{}).
		Where("reg_time >= ? AND secret_version = ?", since, secretVersion).
		Where("ip_hash = ? AND ua_hash = ?", ipHash, uaHash)

	var existing int64
	if err := query.Limit(1).Count(&existing).Error; err != nil {
		return err
	}
	if existing > 0 {
		return ErrRegistrationDeviceLimited
	}
	return nil
}

// RegistrationTrialReservation represents the short Redis reservation made while an
// account is being created. A reservation that reaches Commit becomes the seven-day
// fingerprint marker; a failed registration releases it so a normal retry keeps its
// trial quota.
type RegistrationTrialReservation struct {
	key       string
	token     string
	quota     int
	committed bool
}

// BeginRegistrationTrial atomically claims the registration trial for an exact IP + UA
// fingerprint. Redis is an accelerator and a failure is deliberately fail-open so a cache
// outage cannot turn into a registration outage.
func BeginRegistrationTrial(clientIp string, userAgent string) *RegistrationTrialReservation {
	reservation := &RegistrationTrialReservation{quota: common.QuotaForNewUser}
	if !common.RegistrationDeviceLimitEnabled || common.IsRegistrationDeviceWhitelisted(clientIp) {
		return reservation
	}

	addr, ok := normalizeClientIP(clientIp)
	if !ok || !common.RedisEnabled || common.RDB == nil {
		return reservation
	}

	truncatedUa := truncateRegistrationUserAgent(userAgent)
	fingerprint := common.GenerateHMAC(addr.String() + "\x00" + truncatedUa)
	key := "xingya:registration-trial:" + registrationSecretVersion() + ":" + fingerprint
	token, err := common.GenerateRandomCharsKey(32)
	if err != nil {
		common.SysError("failed to generate registration trial reservation token: " + err.Error())
		return reservation
	}

	claimed, err := common.RDB.SetNX(context.Background(), key, token,
		time.Duration(registrationTrialReservationSeconds)*time.Second).Result()
	if err != nil {
		common.SysError("failed to reserve registration trial in Redis: " + err.Error())
		return reservation
	}
	if !claimed {
		reservation.quota = 0
		return reservation
	}
	reservation.key = key
	reservation.token = token
	return reservation
}

// Quota returns the quota that should be assigned to the newly created account.
func (reservation *RegistrationTrialReservation) Quota() int {
	if reservation == nil {
		return common.QuotaForNewUser
	}
	return reservation.quota
}

// Commit promotes a successful registration reservation to the full deduplication TTL.
func (reservation *RegistrationTrialReservation) Commit() {
	if reservation == nil || reservation.key == "" || reservation.committed {
		return
	}
	const script = `if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("expire", KEYS[1], ARGV[2]) else return 0 end`
	if err := common.RDB.Eval(context.Background(), script, []string{reservation.key},
		reservation.token, registrationDedupWindowSeconds).Err(); err != nil {
		common.SysError("failed to commit registration trial reservation: " + err.Error())
		return
	}
	reservation.committed = true
}

// Rollback releases a failed registration reservation without touching another request's
// reservation if the short lease has already been reused.
func (reservation *RegistrationTrialReservation) Rollback() {
	if reservation == nil || reservation.key == "" || reservation.committed || common.RDB == nil {
		return
	}
	const script = `if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("del", KEYS[1]) else return 0 end`
	if err := common.RDB.Eval(context.Background(), script, []string{reservation.key}, reservation.token).Err(); err != nil {
		common.SysError("failed to release registration trial reservation: " + err.Error())
	}
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
	truncatedUa := truncateRegistrationUserAgent(userAgent)
	ipHash := unknownRegistrationIPHash()
	ipPrefix := ""
	if ok {
		ipHash = common.GenerateHMAC(addr.String())
		ipPrefix = registrationIPPrefix(addr)
	}
	device := &XingyaRegistrationDevice{
		UserId:        userId,
		IpHash:        ipHash,
		IpPrefix:      ipPrefix,
		UserAgent:     truncatedUa,
		UaHash:        common.GenerateHMAC(truncatedUa),
		SecretVersion: registrationSecretVersion(),
		RegTime:       common.GetTimestamp(),
	}

	return endpoint.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoNothing: true,
	}).Create(device).Error
}

// GetRegistrationDevice returns the stored device record for a user, for the admin view.
func GetRegistrationDevice(userId int) (*XingyaRegistrationDevice, error) {
	device := &XingyaRegistrationDevice{}
	if err := DB.Where("user_id = ?", userId).First(device).Error; err != nil {
		return nil, err
	}
	return device, nil
}

type RegistrationRiskRegistration struct {
	UserId   int    `json:"user_id"`
	Username string `json:"username"`
	RegTime  int64  `json:"reg_time"`
}

type RegistrationRiskGroup struct {
	IPFingerprint string                         `json:"ip_fingerprint"`
	UAFingerprint string                         `json:"ua_fingerprint"`
	SecretVersion string                         `json:"secret_version"`
	RepeatCount   int64                          `json:"repeat_count"`
	LatestRegTime int64                          `json:"latest_reg_time"`
	UserAgent     string                         `json:"user_agent"`
	Registrations []RegistrationRiskRegistration `json:"registrations"`
}

type registrationRiskJoinedRow struct {
	IpHash        string `gorm:"column:ip_hash"`
	UaHash        string `gorm:"column:ua_hash"`
	SecretVersion string `gorm:"column:secret_version"`
	RepeatCount   int64  `gorm:"column:repeat_count"`
	LatestRegTime int64  `gorm:"column:latest_reg_time"`
	UserId        int    `gorm:"column:user_id"`
	Username      string `gorm:"column:username"`
	RegTime       int64  `gorm:"column:reg_time"`
	UserAgent     string `gorm:"column:user_agent"`
}

type registrationRiskGroupKey struct {
	ipHash        string
	uaHash        string
	secretVersion string
}

// GetRegistrationRiskGroups aggregates duplicate exact IP+UA fingerprints for the
// administrator view. It returns keyed-digest summaries and the truncated UA, never a
// literal client IP.
func GetRegistrationRiskGroups(from int64, to int64, minRepeats int, startIdx int, limit int) ([]*RegistrationRiskGroup, int64, error) {
	if minRepeats < 2 {
		minRepeats = 2
	}
	if limit <= 0 {
		limit = common.ItemsPerPage
	}
	buildGrouped := func() *gorm.DB {
		return DB.Model(&XingyaRegistrationDevice{}).
			Select("ip_hash, ua_hash, secret_version, COUNT(*) AS repeat_count, MAX(reg_time) AS latest_reg_time").
			Where("reg_time >= ? AND reg_time <= ? AND ip_hash <> ?", from, to, unknownRegistrationIPHash()).
			Group("ip_hash, ua_hash, secret_version").
			Having("COUNT(*) >= ?", minRepeats)
	}

	var total int64
	if err := DB.Table("(?) AS risk_groups", buildGrouped()).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	// Apply pagination inside the grouped subquery before joining registration rows.
	// This keeps the result to two database queries regardless of how many groups are
	// shown on the page, instead of issuing one account lookup per group.
	groupedPage := buildGrouped().
		Order("repeat_count DESC, latest_reg_time DESC").
		Limit(limit).Offset(startIdx)
	var rows []registrationRiskJoinedRow
	if err := DB.Table("xingya_registration_devices AS d").
		Select("g.ip_hash, g.ua_hash, g.secret_version, g.repeat_count, g.latest_reg_time, d.user_id, COALESCE(u.username, '') AS username, d.reg_time, d.user_agent").
		Joins("JOIN (?) AS g ON g.ip_hash = d.ip_hash AND g.ua_hash = d.ua_hash AND g.secret_version = d.secret_version", groupedPage).
		Joins("LEFT JOIN users AS u ON u.id = d.user_id").
		Where("d.reg_time >= ? AND d.reg_time <= ? AND d.ip_hash <> ?", from, to, unknownRegistrationIPHash()).
		Order("g.repeat_count DESC, g.latest_reg_time DESC, d.reg_time DESC").
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}

	groups := make([]*RegistrationRiskGroup, 0)
	groupIndexes := make(map[registrationRiskGroupKey]int, len(rows))
	for _, row := range rows {
		key := registrationRiskGroupKey{ipHash: row.IpHash, uaHash: row.UaHash, secretVersion: row.SecretVersion}
		groupIndex, exists := groupIndexes[key]
		if !exists {
			groupIndexes[key] = len(groups)
			groups = append(groups, &RegistrationRiskGroup{
				IPFingerprint: fingerprintSummary(row.IpHash),
				UAFingerprint: fingerprintSummary(row.UaHash),
				SecretVersion: row.SecretVersion,
				RepeatCount:   row.RepeatCount,
				LatestRegTime: row.LatestRegTime,
				UserAgent:     row.UserAgent,
				Registrations: make([]RegistrationRiskRegistration, 0, row.RepeatCount),
			})
			groupIndex = len(groups) - 1
		}
		groups[groupIndex].Registrations = append(groups[groupIndex].Registrations, RegistrationRiskRegistration{
			UserId: row.UserId, Username: row.Username, RegTime: row.RegTime,
		})
	}
	return groups, total, nil
}

func fingerprintSummary(value string) string {
	if len(value) <= 16 {
		return value
	}
	return value[:8] + "..." + value[len(value)-8:]
}

// PruneRegistrationDevices deletes records older than the retention period and returns how
// many rows were removed.
func PruneRegistrationDevices() (int64, error) {
	if DB == nil || !xingyaTablesReady.Load() {
		return 0, nil
	}
	result := DB.Where("reg_time < ?", common.GetTimestamp()-registrationDeviceRetentionSeconds).
		Delete(&XingyaRegistrationDevice{})
	return result.RowsAffected, result.Error
}

// truncateRegistrationUserAgent bounds a normalized user agent to the stored column width
// without cutting a UTF-8 code point in half.
func truncateRegistrationUserAgent(userAgent string) string {
	const maxBytes = 512
	userAgent = strings.TrimSpace(userAgent)
	if len(userAgent) <= maxBytes {
		return userAgent
	}
	truncated := userAgent[:maxBytes]
	for len(truncated) > 0 && (truncated[len(truncated)-1]&0xc0) == 0x80 {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated
}
