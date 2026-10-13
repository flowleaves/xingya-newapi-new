package model

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const CheckinQuotaSource = "checkin"
const checkinOptionPrefix = "xingya_checkin_setting."

var (
	ErrCheckinInsufficient   = errors.New("签到额度不足，请切换普通密钥 / Check-in credit is insufficient; switch to a regular key")
	ErrCheckinPaused         = errors.New("签到领取已暂停")
	ErrCheckinConfigConflict = errors.New("签到设置已被更新，请刷新后重试")
	ErrCheckinReconciliation = errors.New("签到请求费用待核对，请切换普通密钥")
	checkinLocation          = time.FixedZone("Asia/Shanghai", 8*60*60)
)

// ResolveCheckinSpend is a maintenance action after the operator has checked the
// upstream/log evidence. Unknown in-flight funds are never released on a timer.
// A review row keeps its measured cost/unpaid amount even after a waiver.
func ResolveCheckinSpend(requestID string, updatedAt int64, action string, actual int) error {
	return resolveCheckinSpendAt(requestID, updatedAt, action, actual, 0)
}

func resolveCheckinSpendAt(requestID string, updatedAt int64, action string, actual int, at int64) error {
	if requestID == "" || updatedAt <= 0 || actual < 0 || actual > common.MaxQuota {
		return errors.New("核对参数无效")
	}
	var initial XingyaCheckinSpend
	if err := DB.Where("request_id = ?", requestID).Take(&initial).Error; err != nil {
		return err
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		wallet, err := lockCheckinWallet(tx, initial.UserID)
		if err != nil {
			return err
		}
		if at == 0 {
			at = common.GetTimestamp()
		}
		if err = expireCheckinCredit(tx, wallet, at); err != nil {
			return err
		}
		var spend XingyaCheckinSpend
		if err = lockForUpdate(tx).Where("request_id = ?", requestID).Take(&spend).Error; err != nil {
			return err
		}
		if spend.UpdatedAt != updatedAt {
			return errors.New("资金记录已变化，请刷新")
		}
		if spend.State == "review" && action == "waive" {
			spend.State = "waived"
		} else if spend.State == "reserved" && spend.UpdatedAt < at-24*60*60 && (action == "settle" || action == "release") {
			if action == "release" {
				actual = 0
			}
			if wallet.Reserved < spend.Reserved || actual > spend.Reserved {
				return errors.New("核对金额超出预留或钱包不一致")
			}
			if wallet.Consumed > common.MaxWalletQuota-actual {
				return errors.New("累计额度超出上限")
			}
			if err = settleCheckinAllocations(tx, wallet, &spend, actual, at); err != nil {
				return err
			}
			spend.ActualQuota, spend.Consumed = actual, actual
			spend.State = "settled"
			if action == "release" {
				spend.State = "released"
			}
			if err = tx.Save(wallet).Error; err != nil {
				return err
			}
		} else {
			return errors.New("该记录当前不允许此核对操作")
		}
		spend.UpdatedAt = at
		return tx.Save(&spend).Error
	})
}

type XingyaCheckinSettings struct {
	ClaimEnabled     bool    `json:"claim_enabled"`
	SpendEnabled     bool    `json:"spend_enabled"`
	MinReward        float64 `json:"min_reward"`
	MaxReward        float64 `json:"max_reward"`
	ConsumeThreshold float64 `json:"consume_threshold"`
	FixedReward      float64 `json:"fixed_reward"`
	Group            string  `json:"group"`
	Revision         int64   `json:"revision"`
}

// Settings are read together from the database, so every node observes a complete
// committed version. The generic option writer may not update individual fields.
func GetXingyaCheckinSettings() (XingyaCheckinSettings, error) {
	return readXingyaCheckinSettings(DB)
}

func readXingyaCheckinSettings(db *gorm.DB) (XingyaCheckinSettings, error) {
	var rows []Option
	keys := []string{}
	for _, name := range []string{"claim_enabled", "spend_enabled", "min_reward", "max_reward", "consume_threshold", "fixed_reward", "group", "revision"} {
		keys = append(keys, checkinOptionPrefix+name)
	}
	if err := db.Where("key IN ?", keys).Find(&rows).Error; err != nil {
		return XingyaCheckinSettings{}, err
	}
	s := XingyaCheckinSettings{ConsumeThreshold: 5000, FixedReward: 30}
	for _, row := range rows {
		var err error
		switch strings.TrimPrefix(row.Key, checkinOptionPrefix) {
		case "claim_enabled":
			s.ClaimEnabled, err = strconv.ParseBool(row.Value)
		case "spend_enabled":
			s.SpendEnabled, err = strconv.ParseBool(row.Value)
		case "min_reward":
			s.MinReward, err = strconv.ParseFloat(row.Value, 64)
		case "max_reward":
			s.MaxReward, err = strconv.ParseFloat(row.Value, 64)
		case "consume_threshold":
			s.ConsumeThreshold, err = strconv.ParseFloat(row.Value, 64)
		case "fixed_reward":
			s.FixedReward, err = strconv.ParseFloat(row.Value, 64)
		case "group":
			s.Group = row.Value
		case "revision":
			s.Revision, err = strconv.ParseInt(row.Value, 10, 64)
		}
		if err != nil {
			return s, fmt.Errorf("invalid check-in setting %s", row.Key)
		}
	}
	return s, ValidateXingyaCheckinSettings(s)
}

func ValidateXingyaCheckinSettings(s XingyaCheckinSettings) error {
	for _, value := range []float64{s.MinReward, s.MaxReward, s.ConsumeThreshold, s.FixedReward} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > operation_setting.MaxCheckinRewardTier || !decimal.NewFromFloat(value).Mul(decimal.NewFromInt(100)).IsInteger() {
			return errors.New("奖励金额必须为上限内的非负数，最多两位小数")
		}
	}
	if s.MinReward > s.MaxReward {
		return errors.New("签到奖励下限不能高于上限")
	}
	if s.ConsumeThreshold > 0 && s.FixedReward <= 0 {
		return errors.New("Fixed check-in reward must be positive when the spending threshold is enabled.")
	}
	if s.ClaimEnabled && (s.MinReward <= 0 || s.Group == "" || !s.SpendEnabled) {
		return errors.New("启用领取前请配置正数奖励区间、分组并允许消费")
	}
	if s.SpendEnabled && s.Group == "" || s.Group == "auto" || len(s.Group) > 64 || s.Group != strings.TrimSpace(s.Group) {
		return errors.New("请选择固定分组")
	}
	if s.Revision < 0 || s.Revision == math.MaxInt64 {
		return errors.New("签到配置版本无效")
	}
	for _, value := range []float64{s.MaxReward, s.ConsumeThreshold, s.FixedReward} {
		if _, err := CheckinRewardQuota(value); err != nil {
			return err
		}
	}
	return nil
}

func SaveXingyaCheckinSettings(s XingyaCheckinSettings) (XingyaCheckinSettings, error) {
	s.Group = strings.TrimSpace(s.Group)
	if err := ValidateXingyaCheckinSettings(s); err != nil {
		return s, err
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		key := Option{Key: checkinOptionPrefix + "revision", Value: "0"}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&key).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).Where("key = ?", key.Key).Take(&key).Error; err != nil {
			return err
		}
		current, err := readXingyaCheckinSettings(tx)
		if err != nil {
			return err
		}
		if current.Revision != s.Revision {
			return ErrCheckinConfigConflict
		}
		if current.Group != "" && current.Group != s.Group {
			var count int64
			if err := tx.Model(&XingyaCheckinGrant{}).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return errors.New("已有签到入账，不能直接更换固定分组")
			}
		}
		s.Revision++
		values := map[string]string{"claim_enabled": strconv.FormatBool(s.ClaimEnabled), "spend_enabled": strconv.FormatBool(s.SpendEnabled), "min_reward": strconv.FormatFloat(s.MinReward, 'f', 2, 64), "max_reward": strconv.FormatFloat(s.MaxReward, 'f', 2, 64), "group": s.Group, "revision": strconv.FormatInt(s.Revision, 10)}
		values["consume_threshold"] = strconv.FormatFloat(s.ConsumeThreshold, 'f', 2, 64)
		values["fixed_reward"] = strconv.FormatFloat(s.FixedReward, 'f', 2, 64)
		for name, value := range values {
			row := Option{Key: checkinOptionPrefix + name, Value: value}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value"})}).Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return s, err
}

// XingyaCheckinRewardRange uses persisted cumulative usage, which excludes
// check-in spending, less self-refunded credit. It does not depend on retained
// consume logs or cached counters. The claim caller holds the user's row lock,
// serializing the eligibility decision against self-refund transactions.
func XingyaCheckinRewardRange(db *gorm.DB, userID int, s XingyaCheckinSettings) (float64, float64, error) {
	if s.ConsumeThreshold <= 0 {
		return s.MinReward, s.MaxReward, nil
	}
	threshold, err := CheckinRewardQuota(s.ConsumeThreshold)
	if err != nil {
		return 0, 0, err
	}
	var user User
	if err = db.Select("used_quota").First(&user, userID).Error; err != nil {
		return 0, 0, err
	}
	var refunded int64
	if err = db.Model(&LogRefund{}).Where("user_id = ?", userID).Select("COALESCE(SUM(quota), 0)").Scan(&refunded).Error; err != nil {
		return 0, 0, err
	}
	if refunded < 0 {
		return 0, 0, errors.New("Invalid refunded check-in qualifying spend.")
	}
	// Subtract only after proving the difference cannot underflow.
	if int64(user.UsedQuota) >= refunded && int64(user.UsedQuota)-refunded >= int64(threshold) {
		return s.FixedReward, s.FixedReward, nil
	}
	return s.MinReward, s.MaxReward, nil
}

func CheckinRewardQuota(reward float64) (int, error) {
	if math.IsNaN(reward) || math.IsInf(reward, 0) || reward < 0 {
		return 0, errors.New("奖励金额无效")
	}
	if math.IsNaN(common.QuotaPerUnit) || math.IsInf(common.QuotaPerUnit, 0) || common.QuotaPerUnit <= 0 {
		return 0, errors.New("额度换算设置无效")
	}
	quota, err := common.WalletQuotaFromDecimalStrict(decimal.NewFromFloat(reward).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).Div(decimal.NewFromInt(100)))
	if err != nil {
		return 0, err
	}
	if reward > 0 && quota <= 0 {
		return 0, errors.New("奖励低于额度最小精度")
	}
	return quota, nil
}

type XingyaCheckinWallet struct {
	UserID    int `json:"user_id" gorm:"primaryKey;autoIncrement:false"`
	Available int `json:"available" gorm:"type:bigint;not null;default:0"`
	Reserved  int `json:"reserved" gorm:"type:bigint;not null;default:0"`
	Granted   int `json:"granted" gorm:"type:bigint;not null;default:0"`
	Consumed  int `json:"consumed" gorm:"type:bigint;not null;default:0"`
	Expired   int `json:"expired" gorm:"type:bigint;not null;default:0"`
}

func (XingyaCheckinWallet) TableName() string { return "xingya_checkin_wallets" }

type XingyaCheckinGrant struct {
	ID             int     `json:"id" gorm:"primaryKey"`
	UserID         int     `json:"-" gorm:"not null;uniqueIndex:idx_xingya_checkin_grant_day,priority:1"`
	CheckinDate    string  `json:"checkin_date" gorm:"type:varchar(10);not null;uniqueIndex:idx_xingya_checkin_grant_day,priority:2"`
	QuotaAwarded   int     `json:"quota_awarded" gorm:"type:bigint;not null"`
	MinReward      float64 `json:"-"`
	MaxReward      float64 `json:"-"`
	ConfigRevision int64   `json:"-"`
	Group          string  `json:"-"`
	QuotaPerUnit   float64 `json:"-"`
	CreatedAt      int64   `json:"created_at" gorm:"type:bigint"`
}

func (XingyaCheckinGrant) TableName() string { return "xingya_checkin_grants" }

type XingyaCheckinSpend struct {
	RequestID   string `json:"request_id" gorm:"primaryKey;type:varchar(191)"`
	UserID      int    `json:"user_id" gorm:"not null;index:idx_xingya_checkin_spend_user"`
	TokenID     int    `json:"token_id"`
	Group       string `json:"group"`
	Model       string `json:"model"`
	Reserved    int    `json:"reserved" gorm:"type:bigint"`
	ActualQuota int    `json:"actual_quota" gorm:"type:bigint"`
	Consumed    int    `json:"consumed" gorm:"type:bigint"`
	UnpaidQuota int    `json:"unpaid_quota" gorm:"type:bigint"`
	State       string `json:"state" gorm:"type:varchar(16);index:idx_xingya_checkin_spend_state"`
	CreatedAt   int64  `json:"created_at" gorm:"type:bigint;autoCreateTime:false"`
	UpdatedAt   int64  `json:"updated_at" gorm:"type:bigint;autoUpdateTime:false"`
}

func (XingyaCheckinSpend) TableName() string { return "xingya_checkin_spends" }

func GetXingyaCheckinWallet(userID int) (XingyaCheckinWallet, error) {
	balance, err := GetXingyaCheckinBalance(userID)
	return balance.Wallet, err
}

func validCheckinWallet(w XingyaCheckinWallet) bool {
	return w.Granted >= 0 && w.Granted <= common.MaxWalletQuota && w.Available >= 0 && w.Reserved >= 0 && w.Consumed >= 0 && w.Expired >= 0 &&
		w.Available <= w.Granted && w.Reserved <= w.Granted-w.Available && w.Consumed <= w.Granted-w.Available-w.Reserved && w.Expired == w.Granted-w.Available-w.Reserved-w.Consumed
}

func lockCheckinWallet(tx *gorm.DB, userID int) (*XingyaCheckinWallet, error) {
	w := &XingyaCheckinWallet{UserID: userID}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(w).Error; err != nil {
		return nil, err
	}
	if err := lockForUpdate(tx).Where("user_id = ?", userID).Take(w).Error; err != nil {
		return nil, err
	}
	if !validCheckinWallet(*w) {
		return nil, errors.New("签到钱包对账不一致，请联系管理员")
	}
	return w, nil
}

func CheckinBusinessTime(now time.Time) time.Time { return now.In(checkinLocation) }

// A wallet lock serializes the old-history check and new award. Randomness,
// uniqueness and the credit are committed together; retries return the same award.
// Production passes zero time to read the receipt time after acquiring locks;
// an explicit time is reserved for deterministic tests.
func ClaimXingyaCheckin(userID int, now time.Time) (*XingyaCheckinGrant, bool, error) {
	grant := &XingyaCheckinGrant{}
	already := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).Select("id", "status").First(&user, userID).Error; err != nil {
			return err
		}
		if user.Status != common.UserStatusEnabled {
			return errors.New("账号不可用")
		}
		// Serialize against a first group change/configuration save. A claim must
		// not commit an old-group grant after a concurrent save saw no grants.
		revision := Option{Key: checkinOptionPrefix + "revision", Value: "0"}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&revision).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).Where("key = ?", revision.Key).Take(&revision).Error; err != nil {
			return err
		}
		w, err := lockCheckinWallet(tx, userID)
		if err != nil {
			return err
		}
		if now.IsZero() {
			now = time.Now()
		}
		day := CheckinBusinessTime(now).Format("2006-01-02")
		if err = expireCheckinCredit(tx, w, now.Unix()); err != nil {
			return err
		}
		if err = tx.Where("user_id = ? AND checkin_date = ?", userID, day).Take(grant).Error; err == nil {
			already = true
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var old Checkin
		if err = tx.Where("user_id = ? AND checkin_date = ?", userID, day).Take(&old).Error; err == nil {
			grant.CheckinDate = day
			grant.QuotaAwarded = old.QuotaAwarded
			already = true
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		s, err := readXingyaCheckinSettings(tx)
		if err != nil {
			return err
		}
		if !s.ClaimEnabled {
			return ErrCheckinPaused
		}
		if s.MinReward <= 0 || s.MinReward > s.MaxReward || s.Group == "" || s.Group == "auto" {
			return errors.New("签到设置未完成")
		}
		minimum, maximum, err := XingyaCheckinRewardRange(tx, userID, s)
		if err != nil {
			return err
		}
		minCents := decimal.NewFromFloat(minimum).Mul(decimal.NewFromInt(100)).IntPart()
		maxCents := decimal.NewFromFloat(maximum).Mul(decimal.NewFromInt(100)).IntPart()
		cents := minCents
		if maxCents > minCents {
			n, err := rand.Int(rand.Reader, big.NewInt(maxCents-minCents+1))
			if err != nil {
				return err
			}
			cents += n.Int64()
		}
		reward := decimal.NewFromInt(cents).Div(decimal.NewFromInt(100)).InexactFloat64()
		quota, err := CheckinRewardQuota(reward)
		if err != nil {
			return err
		}
		if quota <= 0 || w.Granted > common.MaxWalletQuota-quota || w.Available > common.MaxWalletQuota-quota {
			return errors.New("签到额度达到上限")
		}
		*grant = XingyaCheckinGrant{UserID: userID, CheckinDate: day, QuotaAwarded: quota, MinReward: minimum, MaxReward: maximum, ConfigRevision: s.Revision, Group: s.Group, QuotaPerUnit: common.QuotaPerUnit, CreatedAt: now.Unix()}
		if err = tx.Create(grant).Error; err != nil {
			return err
		}
		lot := XingyaCheckinCreditLot{UserID: userID, GrantID: grant.ID, Source: "grant", SourceKey: fmt.Sprintf("grant:%d", grant.ID), Granted: quota, Available: quota, CreatedAt: now.Unix(), ExpiresAt: now.Add(CheckinCreditLifetime).Unix()}
		if err = tx.Create(&lot).Error; err != nil {
			return err
		}
		w.Available += quota
		w.Granted += quota
		return tx.Save(w).Error
	})
	return grant, already, err
}

// ReserveCheckinQuota sets the total reservation for one server-generated request.
// Repeating the same target never subtracts twice. Wallet-before-spend is the lock order.
func ReserveCheckinQuota(spend XingyaCheckinSpend, target int) error {
	return reserveCheckinQuotaAt(spend, target, 0)
}

func reserveCheckinQuotaAt(spend XingyaCheckinSpend, target int, at int64) error {
	if spend.UserID <= 0 || spend.RequestID == "" || target < 0 || target > common.MaxQuota {
		return errors.New("签到预扣参数无效")
	}
	var rejected error
	err := DB.Transaction(func(tx *gorm.DB) error {
		w, err := lockCheckinWallet(tx, spend.UserID)
		if err != nil {
			return err
		}
		if at == 0 {
			at = common.GetTimestamp()
		}
		if err = expireCheckinCredit(tx, w, at); err != nil {
			return err
		}
		var existing XingyaCheckinSpend
		err = lockForUpdate(tx).Where("request_id = ?", spend.RequestID).Take(&existing).Error
		if err == nil {
			if existing.UserID != spend.UserID || existing.TokenID != spend.TokenID || existing.Group != spend.Group || existing.Model != spend.Model || existing.State != "reserved" {
				return errors.New("签到请求资金记录不匹配")
			}
			spend = existing
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			spend.State = "reserved"
			spend.CreatedAt = at
		} else {
			return err
		}
		var unsettled int64
		pending := tx.Model(&XingyaCheckinSpend{}).Where("user_id = ? AND (state = ? OR (state = ? AND updated_at < ?))", spend.UserID, "review", "reserved", at-24*60*60)
		// A measured overcharge means this model's bound is no longer reliable.
		// Pause it for all check-in keys until the operator verifies the evidence;
		// unrelated models and ordinary funding keep their existing behavior.
		if spend.Model != "" {
			pending = pending.Or("model = ? AND state = ?", spend.Model, "review")
		}
		if err = pending.Count(&unsettled).Error; err != nil {
			return err
		}
		if unsettled > 0 && target > spend.Reserved {
			rejected = ErrCheckinReconciliation
			return nil
		}
		delta := target - spend.Reserved
		if w.Available < 0 || w.Reserved < 0 || delta > 0 && w.Reserved > common.MaxWalletQuota-delta {
			return errors.New("签到钱包数据无效")
		}
		if delta > 0 && w.Available < delta {
			rejected = ErrCheckinInsufficient
			return nil
		}
		if delta < 0 && w.Reserved < -delta {
			return errors.New("签到预扣资金不一致")
		}
		if err = adjustCheckinAllocations(tx, w, &spend, target, at); err != nil {
			return err
		}
		spend.Reserved = target
		spend.UpdatedAt = at
		if err = tx.Save(w).Error; err != nil {
			return err
		}
		return tx.Save(&spend).Error
	})
	if err != nil {
		return err
	}
	return rejected
}

// Finalization is durable and idempotent, including zero-cost requests. An
// unexpected upstream overcharge never touches ordinary money or disappears.
func FinalizeCheckinSpend(userID int, requestID string, actual int, release bool) error {
	return finalizeCheckinSpendAt(userID, requestID, actual, release, 0)
}

func finalizeCheckinSpendAt(userID int, requestID string, actual int, release bool, at int64) error {
	if actual < 0 || actual > common.MaxQuota {
		return errors.New("签到结算金额无效")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		w, err := lockCheckinWallet(tx, userID)
		if err != nil {
			return err
		}
		if at == 0 {
			at = common.GetTimestamp()
		}
		if err = expireCheckinCredit(tx, w, at); err != nil {
			return err
		}
		var spend XingyaCheckinSpend
		if err = lockForUpdate(tx).Where("request_id = ? AND user_id = ?", requestID, userID).Take(&spend).Error; err != nil {
			return err
		}
		if spend.State != "reserved" {
			return nil
		}
		if w.Reserved < spend.Reserved {
			return errors.New("签到预扣资金不一致")
		}
		charge := actual
		if release {
			charge = 0
		}
		paid := min(charge, spend.Reserved)
		if w.Consumed > common.MaxWalletQuota-paid {
			return errors.New("签到累计消费超过上限")
		}
		if err = settleCheckinAllocations(tx, w, &spend, paid, at); err != nil {
			return err
		}
		spend.ActualQuota = charge
		spend.Consumed = paid
		spend.UnpaidQuota = charge - paid
		spend.State = "settled"
		if release {
			spend.State = "released"
		}
		if spend.UnpaidQuota > 0 {
			spend.State = "review"
		}
		spend.UpdatedAt = at
		if err = tx.Save(w).Error; err != nil {
			return err
		}
		return tx.Save(&spend).Error
	})
}

// CreateCheckinToken serializes repeated button requests without changing user
// balances or existing restrictions. The response identifies the exact saved token.
func CreateCheckinToken(userID int, group string) (*Token, bool, error) {
	var result Token
	existing := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).First(&user, userID).Error; err != nil {
			return err
		}
		if user.Status != common.UserStatusEnabled {
			return errors.New("账号不可用")
		}
		var revision Option
		if err := lockForUpdate(tx).Where("key = ?", checkinOptionPrefix+"revision").Take(&revision).Error; err != nil {
			return err
		}
		settings, err := readXingyaCheckinSettings(tx)
		if err != nil {
			return err
		}
		if !settings.SpendEnabled || settings.Group != group {
			return errors.New("签到设置已变更，请刷新后创建密钥")
		}
		err = tx.Where("user_id = ? AND quota_source = ? AND "+commonGroupCol+" = ? AND status = ? AND (expired_time = -1 OR expired_time > ?) AND (unlimited_quota = ? OR remain_quota > 0)", userID, CheckinQuotaSource, group, common.TokenStatusEnabled, common.GetTimestamp(), true).Order("id DESC").Take(&result).Error
		if err == nil {
			existing = true
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var count int64
		if err = tx.Model(&Token{}).Where("user_id = ?", userID).Count(&count).Error; err != nil {
			return err
		}
		if count >= int64(operation_setting.GetMaxUserTokens()) {
			return errors.New("已达到最大密钥数量")
		}
		key, err := common.GenerateKey()
		if err != nil {
			return err
		}
		name := "签到-" + user.Username
		for len(name) > 50 {
			_, size := utf8.DecodeLastRuneInString(name)
			name = name[:len(name)-size]
		}
		result = Token{UserId: userID, Key: key, Name: name, CreatedTime: common.GetTimestamp(), AccessedTime: common.GetTimestamp(), ExpiredTime: -1, UnlimitedQuota: true, Group: group, QuotaSource: CheckinQuotaSource, Status: common.TokenStatusEnabled}
		return tx.Create(&result).Error
	})
	return &result, existing, err
}

// migrateInviteSpend080 runs once, including existing installations with a saved
// former value. Administrators can change it afterwards without startup undoing it.
func migrateInviteSpend080(db *gorm.DB) error {
	if !db.Migrator().HasTable(&Option{}) {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		marker := Option{Key: "xingya_migration.invite_spend_080", Value: "1"}
		created := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&marker)
		if created.Error != nil {
			return created.Error
		}
		if created.RowsAffected == 0 {
			return nil
		}
		gate := Option{Key: "invite_reward_setting.required_consume_usd", Value: "0.8"}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value"})}).Create(&gate).Error; err != nil {
			return err
		}
		// Existing legitimate progress can meet the lowered gate immediately.
		// Read the saved conversion here: runtime options load after migrations.
		unit := common.QuotaPerUnit
		var savedUnit Option
		if err := tx.Where("key = ?", "QuotaPerUnit").Take(&savedUnit).Error; err == nil {
			parsed, err := strconv.ParseFloat(savedUnit.Value, 64)
			if err != nil || parsed <= 0 || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
				return errors.New("邀请额度换算配置无效")
			}
			unit = parsed
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		threshold, err := common.QuotaRoundStrict(0.8 * unit)
		if err != nil || threshold <= 0 {
			return errors.New("邀请门槛换算无效")
		}
		now := common.GetTimestamp()
		return tx.Model(&XingyaInviteRewardPending{}).Where("state = ? AND qualifying_calls >= ? AND qualifying_quota >= ?", InviteRewardStatePending, InviteRewardRequiredCalls, threshold).
			Updates(map[string]any{"state": InviteRewardStateEligible, "eligible_at": now, "auto_grant_at": inviteRewardAutoGrantAt(now)}).Error
	})
}
