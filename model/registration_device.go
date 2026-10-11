package model

import (
	"context"
	"errors"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrRegistrationDeviceLimited is kept for compatibility with callers that used the old
// hard-blocking policy. New registration flows never return it: a matching device is
// allowed to create an account and only loses the registration trial quota.
var ErrRegistrationDeviceLimited = errors.New("registration device limit reached")

// registrationDedupWindowSeconds is how long a successful registration consumes the trial
// quota for one exact address together with one normalized client class.
const registrationDedupWindowSeconds int64 = 7 * 24 * 60 * 60

// registrationTrialReservationSeconds is how long a claim is held while the account is
// being created, so two concurrent signups cannot both take the trial quota.
const registrationTrialReservationSeconds int64 = 10 * 60

// registrationDeviceRetentionSeconds bounds how long device rows are kept. The rows only
// exist to enforce the deduplication window, so keeping them far past it would retain
// registration metadata for no benefit.
const registrationDeviceRetentionSeconds int64 = 180 * 24 * 60 * 60

// XingyaRegistrationDevice records the device signals observed for one registration.
//
// It is the enforcement record for the registration trial limit. Two values are kept for
// the address because they answer different questions: IpHash matches one exact client
// address, while IpPrefix matches the network that address belongs to (IPv4 /24, IPv6 /64)
// and exists so an administrator can see one allocation rotating through its addresses.
// Only the exact address is matched for enforcement: refusing by network would reject an
// ordinary user behind a shared carrier NAT after the first person there registered. The
// network is bounded by a budget instead of by a match.
//
// Two digests are kept for the client string for the same reason. UaHash is the exact
// truncated user agent, and UaBucketHash is the same agent reduced to its platform and
// browser family. Matching the exact string alone is defeated by any build-number change
// — which a browser upgrade causes for legitimate users too — so the normalized digest is
// what the trial limit compares.
//
// UserAgent is truncated to the same 512 characters AuditLog uses. Neither digest is
// reversible, and the truncated agent is stored for human review only.
type XingyaRegistrationDevice struct {
	Id        int    `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId    int    `json:"user_id" gorm:"not null;uniqueIndex:idx_xingya_registration_devices_user"`
	IpHash    string `json:"-" gorm:"type:char(64);not null;index:idx_xingya_regdev_ip_ua_time,priority:1"`
	IpPrefix  string `json:"-" gorm:"type:varchar(64)"`
	UserAgent string `json:"user_agent" gorm:"type:varchar(512)"`
	UaHash    string `json:"-" gorm:"type:char(64);not null;index:idx_xingya_regdev_ip_ua_time,priority:2"`
	// This column is deliberately nullable. It was added after the table shipped, and both
	// SQLite and PostgreSQL refuse to add a NOT NULL column without a default to a table
	// that already has rows, which would have failed the deployment at startup. The write
	// path always supplies a value; rows written before the upgrade simply do not match the
	// normalized comparison, which is the honest answer for a row with no digest.
	UaBucketHash  string `json:"-" gorm:"type:char(64)"`
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

// registrationIPPrefix returns the masked network an address belongs to: IPv4 /24, IPv6 /64.
//
// The value is never used to refuse a registration on its own. It answers two weaker
// questions: whether a request arrives from the same allocation that recently claimed a
// trial, and which allocation a row belongs to when an administrator reviews registrations.
// A /24 is a whole neighbourhood and a carrier NAT can cover thousands of subscribers, so
// matching it is only ever combined with a much wider budget than a single account.
func registrationIPPrefix(addr netip.Addr) string {
	bits := 24
	if addr.Is6() {
		bits = 64
	}
	prefix, err := addr.Prefix(bits)
	if err != nil {
		return ""
	}
	return prefix.String()
}

// registrationSubnetKey is the digest of the network used as the subnet budget bucket.
// An address this server cannot parse yields no bucket, and a missing bucket must never
// consume or exhaust a budget the caller cannot be held to.
func registrationSubnetKey(addr netip.Addr) string {
	prefix := registrationIPPrefix(addr)
	if prefix == "" {
		return ""
	}
	return common.GenerateHMAC("registration-subnet\x00" + prefix)
}

// registrationSubnetBudgetKey is the Redis counter that caps how many trials one network
// may claim. It carries the secret generation for the same reason the device digests do: a
// rotation must not silently merge two generations into one budget.
func registrationSubnetBudgetKey(subnetKey string) string {
	return "xingya:registration-subnet:" + registrationSecretVersion() + ":" + subnetKey
}

// unknownRegistrationIPHash stands in for the address of a registration this server could
// not parse, so those rows are excluded from the network rollups instead of collapsing
// every unparseable request into one apparent network.
func unknownRegistrationIPHash() string {
	return common.GenerateHMAC("unknown-registration-ip")
}

// Mobile platforms must precede their compatibility tokens (Android includes Linux).
var uaMobilePlatformPattern = regexp.MustCompile(`(?i)Android|iPhone|iPad`)
var uaPlatformPattern = regexp.MustCompile(`(?i)Windows NT|Android|iPhone|iPad|Macintosh|Mac OS X|CrOS|Linux|FreeBSD|OpenBSD|NetBSD|SunOS`)

// uaBrowserPattern maps an engine token to the browser family bucket, capturing the version
// that belongs to that token. AppleWebKit is deliberately absent: every Chrome and Edge
// agent carries it, so it identifies nothing. Family priority, not position in the header,
// distinguishes Edge/Opera from their earlier Chrome compatibility token.
var uaBrowserPattern = regexp.MustCompile(`(?i)(Edg|EdgA|EdgiOS|OPR|Opera|Firefox|FxiOS|Chrome|CriOS|MSIE|Trident|Version)/([0-9]+)`)

// uaBrowserCanonical maps a matched token to its canonical family name. The mapping is
// explicit rather than using the matched text because the match is case-insensitive and a
// client could otherwise claim a bucket of its own by changing the case of one letter.
var uaBrowserCanonical = map[string]string{
	"edg":     "Edge",
	"edga":    "Edge",
	"edgios":  "Edge",
	"opr":     "Opera",
	"opera":   "Opera",
	"firefox": "Firefox",
	"fxios":   "Firefox",
	"chrome":  "Chrome",
	"crios":   "Chrome",
	"msie":    "MSIE",
	"trident": "MSIE",
	"version": "Safari",
}

var uaBrowserPriority = map[string]int{
	"Edge": 0, "Opera": 1, "Firefox": 2, "Chrome": 3, "MSIE": 4, "Safari": 5,
}

// truncateRegistrationUserAgent bounds a user agent to the stored column width without
// cutting a UTF-8 code point in half.
func truncateRegistrationUserAgent(userAgent string) string {
	const maxBytes = 512
	userAgent = strings.TrimSpace(userAgent)
	if len(userAgent) <= maxBytes {
		return userAgent
	}
	end := maxBytes
	for end > 0 && !utf8.RuneStart(userAgent[end]) {
		end--
	}
	return userAgent[:end]
}

// registrationUserAgentBucket reduces a user agent to `<platform> <browser> <major>`, for
// example `WindowsNT Chrome 141`.
//
// The exact agent string changes with every build of every browser, so treating it as the
// device identity both misses the same browser after an update and lets an attacker reset
// the identity by editing one digit. The platform and browser family are what actually
// distinguish one class of client from another, so the comparison uses them and the major
// version keeps the bucket from collapsing across a genuinely different engine generation.
//
// A client that presents no user agent, a typical scripted or command-line client, has no
// platform or browser to distinguish and collapses into one shared fallback bucket. That is
// intended: a missing header is not evidence of a device, but it must not become a bucket
// that escapes comparison either.
func registrationUserAgentBucket(userAgent string) string {
	truncated := truncateRegistrationUserAgent(userAgent)
	platform := "unknown"
	// The platform token is matched case-insensitively, so its case is normalized too:
	// otherwise changing one letter's case would mint a bucket of the client's own.
	match := uaMobilePlatformPattern.FindString(truncated)
	if match == "" {
		match = uaPlatformPattern.FindString(truncated)
	}
	if match != "" {
		platform = strings.ReplaceAll(strings.ToLower(match), " ", "")
	}
	browser := "other"
	major := "0"
	// The digits have to be read from the browser token itself: agents carry other version
	// strings earlier in the header (AppleWebKit/537.36 always precedes Chrome/141), so the
	// first number in the string is never the browser version.
	priority := len(uaBrowserPriority)
	for _, groups := range uaBrowserPattern.FindAllStringSubmatch(truncated, -1) {
		family := uaBrowserCanonical[strings.ToLower(groups[1])]
		if uaBrowserPriority[family] >= priority {
			continue
		}
		browser, priority = family, uaBrowserPriority[family]
		if parsed, err := strconv.Atoi(groups[2]); err == nil {
			major = strconv.Itoa(parsed)
		}
	}
	return platform + " " + browser + " " + major
}

// registrationUserAgentBucketHash is the indexed form of the bucket.
func registrationUserAgentBucketHash(userAgent string) string {
	return common.GenerateHMAC(registrationUserAgentBucket(userAgent))
}

// registrationMatch answers whether a previous registration should consume this one's trial
// quota, and why.
type registrationMatch struct {
	// ExactAddress is true when a recent registration was written for this precise client
	// address and this client class.
	ExactAddress bool
	// WithinBudget is true while the caller's allocation has claimed fewer than the allowed
	// number of trials in the rolling window.
	WithinBudget bool
}

// ConsumesTrialQuota reports whether this registration repeats a client the trial quota was
// already spent on.
//
// The rule is deliberately narrow. A repeat must arrive from the exact same address: two
// different households behind one carrier NAT are different people, and refusing the second
// one is a false positive that costs a real signup. Within one address, a client is treated
// as the same when it presents the same platform and browser family, which is what survives
// a browser update.
//
// The network budget is a separate condition, not a refinement of the address rule. It is
// the backstop for the one case the address can never catch — a client rotating through the
// addresses of one allocation — and there the request has no history at all, so requiring a
// matching row would disable the backstop exactly when it is needed. It is only reached once
// the allocation has spent its allowance, so a shared network keeps its first honest signups.
func (match registrationMatch) ConsumesTrialQuota() bool {
	return match.ExactAddress || !match.WithinBudget
}

// findRecentRegistrationMatch reports whether the given client repeats a recent
// registration, and whether its network still has trial budget left.
//
// The budget comes from its own counter rather than from the registration rows: a rotation
// is exactly the case where the address has no history at all, so the rows cannot carry it.
func findRecentRegistrationMatch(tx *gorm.DB, clientIp string, userAgent string, subnetKey string) (registrationMatch, error) {
	match := registrationMatch{WithinBudget: true}
	endpoint := tx
	if endpoint == nil {
		endpoint = DB
	}
	addr, ok := normalizeClientIP(clientIp)
	if !ok || endpoint == nil {
		// An address this server cannot parse cannot be enforced against, and refusing
		// would block registrations for an infrastructure reason.
		return match, nil
	}

	var repeats int64
	err := endpoint.Model(&XingyaRegistrationDevice{}).
		Where("reg_time >= ? AND secret_version = ? AND ua_bucket_hash = ? AND ip_hash = ?",
			common.GetTimestamp()-registrationDedupWindowSeconds,
			registrationSecretVersion(),
			registrationUserAgentBucketHash(userAgent),
			common.GenerateHMAC(addr.String())).
		Limit(1).Count(&repeats).Error
	if err != nil {
		return match, err
	}
	match.ExactAddress = repeats > 0
	match.WithinBudget = registrationSubnetWithinBudget(subnetKey)
	return match, nil
}

// registrationSubnetWithinBudget reports whether the network may still claim a trial.
//
// Redis is an accelerator and a failure is deliberately fail-open: a cache outage must not
// turn into a registration outage, which is the same rule the claim itself follows.
// A budget of zero or less disables the backstop.
func registrationSubnetWithinBudget(subnetKey string) bool {
	if common.RegistrationSubnetBudget <= 0 {
		return true
	}
	if subnetKey == "" || !common.RedisEnabled || common.RDB == nil {
		return true
	}
	value, err := common.RDB.Get(context.Background(), registrationSubnetBudgetKey(subnetKey)).Int()
	if err != nil {
		// A missing key and an outage are indistinguishable here, and both mean "this
		// network has not been recorded as exhausted".
		return true
	}
	return value < common.RegistrationSubnetBudget
}

// CheckRegistrationDevice reports whether the client repeats a recent registration. It is
// diagnostic-only and must not be used to reject a registration.
func CheckRegistrationDevice(tx *gorm.DB, clientIp string, userAgent string) error {
	if !common.RegistrationDeviceLimitEnabled {
		return nil
	}
	addr, ok := normalizeClientIP(clientIp)
	if !ok {
		return nil
	}
	match, err := findRecentRegistrationMatch(tx, clientIp, userAgent, registrationSubnetKey(addr))
	if err != nil {
		return err
	}
	if match.ConsumesTrialQuota() {
		return ErrRegistrationDeviceLimited
	}
	return nil
}

// RegistrationTrialReservation represents the short Redis reservation made while an
// account is being created. A reservation that reaches Commit becomes the seven-day
// fingerprint marker and charges the network budget; a failed registration releases the
// fingerprint so a normal retry keeps its trial quota.
type RegistrationTrialReservation struct {
	key          string
	token        string
	budgetKey    string
	budgetWindow int64
	quota        int
	committed    bool
}

// BeginRegistrationTrial decides whether a signup receives the one-time registration quota.
//
// Two independent signals can withhold it, and either is enough: the client repeats an
// address and client class the quota was already spent on, or its network has already
// claimed its allowance of trials. The account is created either way; only the gift is
// withheld, because refusing the signup itself would block a real user for a signal that
// is only ever circumstantial.
//
// Redis serves the same decision as the database rows, so this stays one indexed lookup on
// the common path. It is an accelerator, and every Redis failure is deliberately fail-open
// so that a cache outage cannot become a registration outage.
func BeginRegistrationTrial(ctx context.Context, clientIp string, userAgent string) *RegistrationTrialReservation {
	reservation := &RegistrationTrialReservation{quota: common.QuotaForNewUser}
	if reservation.quota <= 0 || !common.RegistrationDeviceLimitEnabled || common.IsRegistrationDeviceWhitelisted(clientIp) {
		return reservation
	}

	addr, ok := normalizeClientIP(clientIp)
	if !ok {
		return reservation
	}
	subnetKey := registrationSubnetKey(addr)

	match, err := findRecentRegistrationMatch(nil, clientIp, userAgent, subnetKey)
	if err != nil {
		common.SysError("failed to read registration history: " + err.Error())
	} else if match.ExactAddress {
		reservation.quota = 0
		return reservation
	}

	if !common.RedisEnabled || common.RDB == nil {
		return reservation
	}

	token, err := common.GenerateRandomCharsKey(32)
	if err != nil {
		common.SysError("failed to generate registration trial reservation token: " + err.Error())
		return reservation
	}
	// The claim key is the claimed client, not the masked network, so that two different
	// people who happen to share a network do not lock each other out of the bonus while
	// one of them is still signing up.
	claimKey := "xingya:registration-trial:" + registrationSecretVersion() + ":" +
		common.GenerateHMAC(addr.String()+"\x00"+registrationUserAgentBucket(userAgent))
	budgetKey := registrationSubnetBudgetKey(subnetKey)
	// Reserve both the client and one subnet slot in the same atomic operation. Live
	// leases count against the budget, while failed or expired signups release capacity.
	const reserveScript = `
if redis.call('exists', KEYS[1]) == 1 then return 0 end
if tonumber(ARGV[3]) > 0 then
  local now = tonumber(redis.call('time')[1])
  redis.call('zremrangebyscore', KEYS[3], '-inf', now)
  local claimed = tonumber(redis.call('get', KEYS[2]) or '0')
  if claimed + redis.call('zcard', KEYS[3]) >= tonumber(ARGV[3]) then return 0 end
  redis.call('zadd', KEYS[3], now + tonumber(ARGV[2]), ARGV[1])
  redis.call('expire', KEYS[3], ARGV[2])
end
redis.call('set', KEYS[1], ARGV[1], 'EX', ARGV[2])
return 1`
	// Account insertion is not cancelled with the HTTP context. Use a bounded server
	// context too, so a client disconnect cannot turn the gift check into fail-open.
	redisCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	claimed, err := common.RDB.Eval(redisCtx, reserveScript, []string{claimKey, budgetKey, budgetKey + ":pending"},
		token, registrationTrialReservationSeconds, common.RegistrationSubnetBudget).Int()
	if err != nil {
		common.SysError("failed to reserve registration trial in Redis: " + err.Error())
		return reservation
	}
	if claimed == 0 {
		reservation.quota = 0
		return reservation
	}
	reservation.key = claimKey
	reservation.token = token
	if common.RegistrationSubnetBudget > 0 {
		reservation.budgetKey = budgetKey
		reservation.budgetWindow = common.RegistrationSubnetBudgetWindowSeconds
	}
	return reservation
}

// Quota returns the quota that should be assigned to the newly created account.
func (reservation *RegistrationTrialReservation) Quota() int {
	if reservation == nil {
		return common.QuotaForNewUser
	}
	return reservation.quota
}

// Commit promotes a successful registration reservation to the full deduplication TTL and
// converts its reserved subnet slot to one issued trial. A token receipt prevents repeated
// commits from charging twice, including retries after the client lease expired.
func (reservation *RegistrationTrialReservation) Commit() {
	if reservation == nil || reservation.key == "" || reservation.quota <= 0 || reservation.committed || common.RDB == nil {
		return
	}
	const script = `
if tonumber(ARGV[3]) > 0 then
  if redis.call('set', KEYS[4], '1', 'EX', ARGV[3], 'NX') then
    redis.call('incr', KEYS[2])
    redis.call('expire', KEYS[2], ARGV[3])
  end
  redis.call('zrem', KEYS[3], ARGV[1])
end
if redis.call('get', KEYS[1]) == ARGV[1] then
  redis.call('set', KEYS[1], 'committed:' .. ARGV[1], 'EX', ARGV[2])
end
return 1`
	if err := common.RDB.Eval(context.Background(), script, []string{reservation.key, reservation.budgetKey,
		reservation.budgetKey + ":pending", reservation.budgetKey + ":committed:" + reservation.token},
		reservation.token, registrationDedupWindowSeconds, reservation.budgetWindow).Err(); err != nil {
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
	const script = `
if redis.call('get', KEYS[1]) == ARGV[1] then redis.call('del', KEYS[1]) end
if tonumber(ARGV[2]) > 0 and redis.call('exists', KEYS[3]) == 0 then redis.call('zrem', KEYS[2], ARGV[1]) end
return 1`
	if err := common.RDB.Eval(context.Background(), script, []string{reservation.key,
		reservation.budgetKey + ":pending", reservation.budgetKey + ":committed:" + reservation.token},
		reservation.token, reservation.budgetWindow).Err(); err != nil {
		common.SysError("failed to release registration trial reservation: " + err.Error())
		return
	}
	reservation.committed = true
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
		UaBucketHash:  registrationUserAgentBucketHash(truncatedUa),
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
