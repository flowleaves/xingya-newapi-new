package model

import (
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"

	"gorm.io/gorm"
)

// LogRefund 自助补回记录表（主库，与 user_subscriptions 同库）
type LogRefund struct {
	Id             int    `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId         int    `json:"user_id" gorm:"not null;index"`
	LogId          int    `json:"log_id" gorm:"not null;uniqueIndex:idx_log_refund_log_id"`
	FundingSource  string `json:"funding_source" gorm:"size:16;not null"` // wallet | subscription
	SubscriptionId int    `json:"subscription_id" gorm:"index"`           // 订阅退款时的目标订阅
	BaseQuota      int    `json:"base_quota" gorm:"not null"`             // 退款基数（钱包=log.Quota；订阅=subscription_consumed）
	Quota          int    `json:"quota" gorm:"not null"`                  // 实退金额
	RefundDate     string `json:"refund_date" gorm:"size:10;index"`       // YYYY-MM-DD（服务器本地时区），每日限额按它统计
	Reason         string `json:"reason" gorm:"size:32"`                  // empty_response | stream_truncated
	RequestId      string `json:"request_id" gorm:"size:64;index"`
	CreatedAt      int64  `json:"created_at" gorm:"bigint;index;not null"`
}

func (LogRefund) TableName() string {
	return "log_refunds"
}

// ensureLogRefundTable creates log_refunds on a database that does not have it,
// and does nothing at all when it already exists.
//
// log_refunds is the refund idempotency and audit boundary for real money, and in
// production it predates this code (created by a reviewed migration, and carrying
// two historical unique objects on log_id). Letting AutoMigrate inspect an
// existing table would let the Go struct — not a reviewed migration — decide to
// add or rewrite columns and indexes on that table. Creating a missing table is
// safe and idempotent; reshaping an existing one is neither.
func ensureLogRefundTable(db *gorm.DB) error {
	if db == nil {
		return errors.New("log_refunds 初始化失败：数据库未就绪")
	}
	if db.Migrator().HasTable(&LogRefund{}) {
		return nil
	}
	if err := db.AutoMigrate(&LogRefund{}); err != nil {
		return fmt.Errorf("create log_refunds: %w", err)
	}
	return nil
}

// GetCumulativeRefundStats 获取用户累计补回次数与累计补回总额（不限日期）
func GetCumulativeRefundStats(userId int) (count int64, totalQuota int64, err error) {
	err = DB.Model(&LogRefund{}).
		Where("user_id = ?", userId).
		Count(&count).Error
	if err != nil {
		return 0, 0, err
	}
	err = DB.Model(&LogRefund{}).
		Where("user_id = ?", userId).
		Select("COALESCE(SUM(quota), 0)").Scan(&totalQuota).Error
	return
}

// GetRefundedLogIds checks the supplied candidates when present. With no
// candidates it preserves the legacy all-history behavior for callers outside
// the self-refund list path.
func GetRefundedLogIds(userId int, logIds ...int) (map[int]bool, error) {
	var refunds []LogRefund
	tx := DB.Model(&LogRefund{}).Where("user_id = ?", userId)
	if len(logIds) > 0 {
		tx = tx.Where("log_id IN ?", logIds)
	}
	err := tx.
		Select("log_id").
		Find(&refunds).Error
	if err != nil {
		return nil, err
	}
	result := make(map[int]bool, len(refunds))
	for _, r := range refunds {
		result[r.LogId] = true
	}
	return result, nil
}

// RefundLimits bounds how much one user may be refunded per day. Zero means
// unlimited.
//
// These are enforced *inside* the refund transaction, not before it. Reading the
// day's totals outside the transaction is a time-of-check/time-of-use race: two
// in-flight requests for different logs would both observe the pre-cap total and
// both be paid, silently exceeding the configured daily limit.
type RefundLimits struct {
	MaxCount int
	MaxQuota int
}

// lockUserForRefundTx takes a row lock on the refunding user.
//
// Every refund for a user must take this lock before counting the day's
// refunds, so the count and the idempotency insert happen in one critical
// section. Locking the user row (rather than the day's refund rows) also works
// for the first refund of a day, where there is no refund row to lock yet.
//
// The user row is always acquired before any subscription row, so the two
// refund paths share one lock order and cannot deadlock against each other.
func lockUserForRefundTx(tx *gorm.DB, userId int) error {
	var user User
	if err := lockForUpdate(tx).Where("id = ?", userId).First(&user).Error; err != nil {
		return errors.New("补回失败：用户不存在")
	}
	return nil
}

// checkDailyRefundLimitsTx counts the refunds already recorded for today and
// rejects the request when this one would breach a configured cap. The caller
// must hold the user row lock acquired by lockUserForRefundTx.
func checkDailyRefundLimitsTx(tx *gorm.DB, userId int, refundAmount int, limits RefundLimits) error {
	if limits.MaxCount <= 0 && limits.MaxQuota <= 0 {
		return nil
	}
	today := time.Now().Format("2006-01-02")

	var count int64
	if err := tx.Model(&LogRefund{}).
		Where("user_id = ? AND refund_date = ?", userId, today).
		Count(&count).Error; err != nil {
		return errors.New("查询限额失败")
	}
	if limits.MaxCount > 0 && count >= int64(limits.MaxCount) {
		return fmt.Errorf("今日补回次数已达上限（%d次）", limits.MaxCount)
	}

	if limits.MaxQuota > 0 {
		var total int64
		if err := tx.Model(&LogRefund{}).
			Where("user_id = ? AND refund_date = ?", userId, today).
			Select("COALESCE(SUM(quota), 0)").Scan(&total).Error; err != nil {
			return errors.New("查询限额失败")
		}
		if total+int64(refundAmount) > int64(limits.MaxQuota) {
			return errors.New("今日补回总额已达上限")
		}
	}
	return nil
}

// creditWalletQuotaTx applies a wallet credit inside a transaction, bounded and
// verified.
//
// It replaces a bare `Update("quota", gorm.Expr("quota + ?"))`, which had two
// defects: no ceiling (a corrupted or absurd amount could push the wallet past
// the representable quota range) and no RowsAffected check (a missing/soft-deleted
// user produced a nil error, so the caller committed an idempotency row and a
// success response without ever crediting anyone).
//
// The ceiling is common.MaxWalletQuota, the same bound the wallet's own credit
// path (increaseUserQuota) enforces. Using the smaller single-request int32
// `common.MaxQuota` here instead would permanently deny refunds to any wallet
// that a top-up had legitimately grown past int32, because the conditional
// update would never match.
func creditWalletQuotaTx(tx *gorm.DB, userId int, amount int) error {
	if amount <= 0 {
		return errors.New("补回金额无效")
	}
	if err := common.ValidateWalletQuota(amount); err != nil {
		return errors.New("补回金额超出上限")
	}
	result := tx.Model(&User{}).
		Where("id = ? AND quota <= ?", userId, common.MaxWalletQuota-amount).
		Update("quota", gorm.Expr("quota + ?", amount))
	if result.Error != nil {
		return errors.New("补回失败：更新余额出错")
	}
	if result.RowsAffected == 0 {
		var count int64
		if err := tx.Model(&User{}).Where("id = ?", userId).Count(&count).Error; err != nil {
			return errors.New("补回失败：更新余额出错")
		}
		if count == 0 {
			return errors.New("补回失败：用户不存在")
		}
		return errors.New("补回失败：钱包额度已达上限")
	}
	return nil
}

// SyncUserQuotaCacheAfterCredit invalidates the user cache for legacy callers
// that do not have the credited amount. New credit paths should use the
// amount-aware syncCreditUserQuotaCache helper so an outstanding reservation
// is preserved.
func SyncUserQuotaCacheAfterCredit(userId int) {
	if err := invalidateUserCache(userId); err != nil {
		common.SysLog(fmt.Sprintf("failed to invalidate user quota cache after refund for user %d: %v", userId, err))
	}
}

// DoSelfRefundWallet 执行自助补回（钱包来源）
// 事务内完成：1. 锁用户行 2. 校验每日限额 3. 插幂等行 4. 按带上界的规范路径加余额。
// 补回日志与缓存同步放在事务外：SQLite 下 LOG_DB==DB，事务内写日志会死锁。
func DoSelfRefundWallet(userId int, logId int, baseQuota int, refundAmount int, reason string, requestId string, limits RefundLimits) error {
	if refundAmount <= 0 {
		return errors.New("补回金额无效")
	}
	refund := &LogRefund{
		UserId:        userId,
		LogId:         logId,
		FundingSource: "wallet",
		BaseQuota:     baseQuota,
		Quota:         refundAmount,
		RefundDate:    time.Now().Format("2006-01-02"),
		Reason:        reason,
		RequestId:     requestId,
		CreatedAt:     time.Now().Unix(),
	}

	if err := DB.Transaction(func(tx *gorm.DB) error {
		// 1. 串行化同一用户的并发补回，使限额校验与幂等插入处于同一临界区
		if err := lockUserForRefundTx(tx, userId); err != nil {
			return err
		}

		// 2. 每日限额（在事务内判定，避免 TOCTOU）
		if err := checkDailyRefundLimitsTx(tx, userId, refundAmount, limits); err != nil {
			return err
		}

		// 3. 插入幂等行（唯一索引防重复）
		if err := tx.Create(refund).Error; err != nil {
			return errors.New("该请求已补回")
		}

		// 4. 增加用户余额（带上界 + RowsAffected 校验）
		return creditWalletQuotaTx(tx, userId, refundAmount)
	}); err != nil {
		return err
	}

	// 事务已提交：让缓存立即反映新余额，否则预扣费仍按旧值判定，
	// 补回到账的额度在缓存过期前不可用。
	syncCreditUserQuotaCache(userId, refundAmount, "self refund")

	// 5. 记录补回日志（事务外：SQLite 下 LOG_DB==DB，事务内写日志会死锁）
	RecordRefundLog(userId, refundAmount, refundLogContent(reason, refundAmount, requestId, 0))

	return nil
}

// dbTimestampTx reads the database clock through the caller's transaction.
//
// The subscription expiry check must NOT call GetDBTimestamp(): that helper
// queries the package-global DB, which inside an open transaction needs a second
// connection. On a single-connection pool — a supported SQLite configuration,
// and what the model test harness uses — it blocks forever while the
// transaction holds the only connection. Reading through tx also keeps the check
// inside the transaction's own snapshot.
//
// The dialect switch mirrors GetDBTimestamp; this copy exists so the fix stays
// confined to Xingya-owned files instead of editing the shared upstream helper.
func dbTimestampTx(tx *gorm.DB) int64 {
	if tx == nil {
		return common.GetTimestamp()
	}
	var ts int64
	var err error
	switch {
	case common.UsingMainDatabase(common.DatabaseTypePostgreSQL):
		err = tx.Raw("SELECT EXTRACT(EPOCH FROM NOW())::bigint").Scan(&ts).Error
	case common.UsingMainDatabase(common.DatabaseTypeSQLite):
		err = tx.Raw("SELECT strftime('%s','now')").Scan(&ts).Error
	default:
		err = tx.Raw("SELECT UNIX_TIMESTAMP()").Scan(&ts).Error
	}
	if err != nil || ts <= 0 {
		return common.GetTimestamp()
	}
	return ts
}

// DoSelfRefundSubscription 执行自助补回（订阅来源）
// 事务内完成：1. 锁用户行 2. 校验每日限额 3. 插幂等行 4. 行锁订阅 5. 回减 amount_used。
// 订阅补回不铸钱包额度，只恢复订阅已用额度，故补回日志金额记 0：日志金额代表
// 钱包变动，写成负数会让按日志核对钱包余额时虚高。明细见 log_refunds。
func DoSelfRefundSubscription(userId int, logId int, subscriptionId int, baseQuota int, refundAmount int, reason string, requestId string, limits RefundLimits) error {
	if refundAmount <= 0 {
		return errors.New("补回金额无效")
	}
	refund := &LogRefund{
		UserId:         userId,
		LogId:          logId,
		FundingSource:  "subscription",
		SubscriptionId: subscriptionId,
		BaseQuota:      baseQuota,
		Quota:          refundAmount,
		RefundDate:     time.Now().Format("2006-01-02"),
		Reason:         reason,
		RequestId:      requestId,
		CreatedAt:      time.Now().Unix(),
	}

	if err := DB.Transaction(func(tx *gorm.DB) error {
		// 1. 先锁用户行（与钱包路径同一把锁、同一顺序，避免互相死锁）
		if err := lockUserForRefundTx(tx, userId); err != nil {
			return err
		}

		// 2. 每日限额（在事务内判定，避免 TOCTOU）
		if err := checkDailyRefundLimitsTx(tx, userId, refundAmount, limits); err != nil {
			return err
		}

		// 3. 插入幂等行（唯一索引防重复）
		if err := tx.Create(refund).Error; err != nil {
			return errors.New("该请求已补回")
		}

		// 4. 行锁订阅 + 校验排除条件
		var sub UserSubscription
		if err := lockForUpdate(tx).
			Where("id = ? AND user_id = ?", subscriptionId, userId).
			First(&sub).Error; err != nil {
			return errors.New("订阅记录不存在")
		}

		// 排除情形：无限量
		if sub.AmountTotal == 0 {
			return errors.New("该请求由不限量订阅计费，无需补回")
		}
		// 排除情形：已过期
		now := dbTimestampTx(tx)
		if sub.EndTime > 0 && sub.EndTime <= now {
			return errors.New("订阅已过期，无法补回")
		}
		// 排除情形：管理端已删除/失效
		if sub.Status == "deleted" || sub.Status == "expired" || sub.Status == "cancelled" {
			return errors.New("订阅已失效，无法补回")
		}
		// 排除情形：跨周期重置（amount_used 已清零，退款会被 clamp 吃掉新周期额度）
		if sub.LastResetTime > 0 && sub.LastResetTime > sub.StartTime && sub.AmountUsed == 0 {
			return errors.New("订阅周期已重置，无法补回")
		}

		// 5. 回减 amount_used（下界 0；按次退款不会触发跨周期）。
		// 只更新 amount_used 这一列：Save 会写回整行，一旦有并发改动（例如
		// 计费结算或周期重置同时修改同一订阅的其它字段），整行写回会把那些
		// 字段覆盖回本次读到的旧值。补回不该有能力改动订阅的其它列。
		newUsed := sub.AmountUsed - int64(refundAmount)
		if newUsed < 0 {
			newUsed = 0
		}
		result := tx.Model(&UserSubscription{}).
			Where("id = ? AND user_id = ?", subscriptionId, userId).
			Update("amount_used", newUsed)
		if result.Error != nil {
			return errors.New("补回失败：更新订阅额度出错")
		}
		if result.RowsAffected == 0 {
			return errors.New("补回失败：更新订阅额度出错")
		}
		return nil
	}); err != nil {
		return err
	}

	// 6. 记录补回日志（事务外，理由同钱包路径）。金额记 0：钱包未变动。
	RecordRefundLog(userId, 0, refundLogContent(reason, refundAmount, requestId, subscriptionId))

	return nil
}

// refundLogContent builds the user-visible refund description. The frontend
// renders it verbatim and offers a copy button, so it must not embed an internal
// reason token or a raw internal-quota number in place of a formatted amount.
func refundLogContent(reason string, refundAmount int, requestId string, subscriptionId int) string {
	target := "wallet"
	if subscriptionId > 0 {
		target = fmt.Sprintf("subscription #%d", subscriptionId)
	}
	return fmt.Sprintf("Self refund (%s): refunded %s to %s, request_id=%s",
		refundReasonLabel(reason), logger.LogQuota(refundAmount), target, requestId)
}

// refundReasonLabel maps the internal reason to a readable label.
func refundReasonLabel(reason string) string {
	switch reason {
	case "empty_response":
		return "empty response"
	case "stream_truncated":
		return "truncated stream"
	default:
		return reason
	}
}
