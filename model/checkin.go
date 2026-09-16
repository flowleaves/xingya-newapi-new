package model

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
	"gorm.io/gorm"
)

// Checkin 签到记录
type Checkin struct {
	Id           int    `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId       int    `json:"user_id" gorm:"not null;uniqueIndex:idx_user_checkin_date"`
	CheckinDate  string `json:"checkin_date" gorm:"type:varchar(10);not null;uniqueIndex:idx_user_checkin_date"` // 格式: YYYY-MM-DD
	QuotaAwarded int    `json:"quota_awarded" gorm:"not null"`
	CreatedAt    int64  `json:"created_at" gorm:"bigint"`
}

// RecordCheckinLog 记录签到日志（type=4）。签到涉及财务入账，写入失败时用 SysError
// 告警（区别于普通 RecordLog 的 SysLog），便于监控签到日志丢失。
func RecordCheckinLog(userId int, quotaAwarded int) {
	username, _ := GetUsernameById(userId, false)
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      LogTypeSystem,
		Content:   fmt.Sprintf("用户签到，获得额度 %s", logger.LogQuota(quotaAwarded)),
	}
	if err := createLog(log); err != nil {
		common.SysError(fmt.Sprintf("failed to record checkin log for user %d: %v", userId, err))
	}
}

// CheckinRecord 用于API返回的签到记录（不包含敏感字段）
type CheckinRecord struct {
	CheckinDate  string `json:"checkin_date"`
	QuotaAwarded int    `json:"quota_awarded"`
}

func (Checkin) TableName() string {
	return "checkins"
}

// GetUserCheckinRecords 获取用户在指定日期范围内的签到记录
func GetUserCheckinRecords(userId int, startDate, endDate string) ([]Checkin, error) {
	var records []Checkin
	err := DB.Where("user_id = ? AND checkin_date >= ? AND checkin_date <= ?",
		userId, startDate, endDate).
		Order("checkin_date DESC").
		Find(&records).Error
	return records, err
}

// HasCheckedInToday 检查用户今天是否已签到
func HasCheckedInToday(userId int) (bool, error) {
	today := time.Now().Format("2006-01-02")
	var count int64
	err := DB.Model(&Checkin{}).
		Where("user_id = ? AND checkin_date = ?", userId, today).
		Count(&count).Error
	return count > 0, err
}

// YesterdayRange 返回北京时间（Asia/Shanghai）前一天 [00:00, 24:00) 的 Unix 秒区间
func YesterdayRange() (start, end int64) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("CST", 8*60*60)
	}
	now := time.Now().In(loc)
	yesterday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -1)
	return yesterday.Unix(), yesterday.Add(24 * time.Hour).Unix()
}

// parseLogSubscriptionConsumed 从日志 other JSON 解析 subscription_consumed（可能不存在，返回 0）
func parseLogSubscriptionConsumed(other string) int {
	if other == "" {
		return 0
	}
	m, err := common.StrToMap(other)
	if err != nil {
		return 0
	}
	v, ok := m["subscription_consumed"].(float64)
	if !ok || !(v > 0) {
		return 0
	}
	// 比例换算统一走 common.QuotaFromFloat：裸 int(float64) 在大值/非有限值下
	// 行为由实现定义，属仓库明令禁止的计费换算写法。
	return common.QuotaFromFloat(v)
}

// subscriptionConsumedCond matches consume logs billed from a subscription. It is
// a portable LIKE prefilter for the JSON payload; parseLogSubscriptionConsumed
// still validates the actual value.
const subscriptionConsumedCond = "other LIKE '%subscription_consumed%'"

// GetYesterdayUsage 统计用户昨日**计费**调用次数与消耗额度（芽点，可含订阅计费）。
//
// 两条口径都刻意排除「零成本请求」（quota=0 且无订阅消耗：免费模型、零额度
// 调用等）。规则 A 的触发条件写的是「昨日调用次数」，如果按行数统计，用户用
// 零成本请求刷够次数就能领到按消耗分档的奖励；按计费请求统计才与奖励口径一致。
//
// 聚合在 SQL 侧完成：早期实现把整天日志（含 other JSON 列）整表读进内存再用
// len() 计数求和，而该函数在「打开签到页」与「领取签到」两条用户可反复触发的
// 路径上都会执行，高用量账号会造成显著的内存与 IO 放大。
func GetYesterdayUsage(userId int, start, end int64, includeSub bool) (count int, quota int, err error) {
	base := func() *gorm.DB {
		return LOG_DB.Model(&Log{}).
			Where("user_id = ?", userId).
			Where("type = ?", LogTypeConsume).
			Where("created_at >= ? AND created_at < ?", start, end)
	}

	// 计费请求数：quota > 0 的消费日志。订阅计费会同时写 subscription_consumed，
	// 其日志 quota 可能为 0，故另计为「有订阅消耗」的行。
	var paidCount int64
	if err := base().Where("quota > 0").Count(&paidCount).Error; err != nil {
		return 0, 0, err
	}
	var subCount int64
	if includeSub {
		if err := base().Where("quota <= 0").Where(subscriptionConsumedCond).Count(&subCount).Error; err != nil {
			return 0, 0, err
		}
	}
	count = int(paidCount + subCount)

	// 消耗额度：quota 求和仍在 SQL 侧完成。
	var quotaSum int64
	if err := base().Select("COALESCE(SUM(quota), 0)").Scan(&quotaSum).Error; err != nil {
		return 0, 0, err
	}
	quota = int(quotaSum)

	// 订阅消耗只有 JSON 内一层字段，跨三库没有统一的可移植写法，故仅对这一
	// 子集取 other 列（不是整窗口全表）。
	if includeSub {
		var subRows []struct {
			Other string
		}
		if err := base().Where("quota <= 0").Select("other").Find(&subRows).Error; err != nil {
			return 0, 0, err
		}
		for _, r := range subRows {
			quota += parseLogSubscriptionConsumed(r.Other)
		}
	}
	return count, quota, nil
}

// checkinQuotaPerTier 1 平台货币🌱 对应的内部额度单位数（1🌱=5000内部）。
//
// 从 common.QuotaPerUnit 推导而非写死 5000：兑换率是运行时可配的，写死会让
// 档位换算在改过兑换率的部署上静默偏离配置口径。
func checkinQuotaPerTier() float64 {
	return common.QuotaPerUnit / 100.0
}

// QuotaFromCheckinTier 将平台货币🌱金额转成内部额度单位（1🌱=5000内部）。
// 奖励可为浮点：浮点×5000 后经 common.QuotaRound（half-away-from-zero）整数化，
// 保证内部额度单位为整数（数据库 quota 列为 int32）。遵循计费安全红线（不裸 int()）。
//
// 非有限值一律按 0（不放款）处理，含 +Inf：QuotaRound 对 +Inf 会饱和到上限，
// 也就是说无穷大的配置值会变成「发放平台允许的最大额度」。这是把坏的输入
// 放大成最大支出，与「未达标不发」的兜底语义相反，故必须在转换边界先挡掉。
func QuotaFromCheckinTier(quotaTier float64) int {
	if math.IsNaN(quotaTier) || math.IsInf(quotaTier, 0) || quotaTier <= 0 {
		return 0
	}
	return common.QuotaRound(quotaTier * checkinQuotaPerTier())
}

// randFloatBetween 在 [min, max) 内产生浮点随机值，并归一化反转区间。
//
// 运营手填 min/max，写反时旧实现直接返回 min —— 也就是**发得比配置的上限还多**
// （写反后 min 才是那个大数）。归一化后发放始终落在两个配置值之间；min == max
// 时退化为固定奖励。
func randFloatBetween(min, max float64) float64 {
	if min > max {
		min, max = max, min
	}
	if min < 0 {
		min = 0
	}
	if max <= min {
		return min
	}
	return min + rand.Float64()*(max-min)
}

// CalcCheckinTierC 规则C：按历史累计消耗分档奖励（固定档位，纯函数，无随机，可单测）。
// usedQuotaTier = 历史累计消耗（平台货币🌱）；返回奖励（🌱，浮点）与是否命中。
// 档位：≥CBaseThreshold 起给 CBaseReward，每多 CStepQuota 芽点 +CStepReward，封顶 CMaxReward。
func CalcCheckinTierC(usedQuotaTier float64, setting *operation_setting.CheckinSetting) (reward float64, hit bool) {
	if !setting.CEnabled || usedQuotaTier < setting.CBaseThreshold {
		return 0, false
	}
	// 防 CStepQuota=0 除零：步长为 0 时按"恰好一档"处理（仅基础奖励）。
	steps := 1.0
	if setting.CStepQuota > 0 {
		steps = math.Floor((usedQuotaTier-setting.CBaseThreshold)/setting.CStepQuota) + 1
	}
	reward = setting.CBaseReward + (steps-1)*setting.CStepReward
	if reward > setting.CMaxReward {
		reward = setting.CMaxReward
	}
	return reward, true
}

// CalcRewardTierQuota 纯函数：返回 count/quota/usedQuota 命中的奖励（单位=平台货币🌱，可为浮点）。可单测。
// 三套规则取最高：A（昨日次数）/ B（昨日额度）/ C（历史累计额度分档）；未达标给兜底。
func CalcRewardTierQuota(count, quotaInternal, usedQuotaInternal int, setting *operation_setting.CheckinSetting) (reward float64, source string) {
	perTier := checkinQuotaPerTier()

	// 规则 A：按昨日调用次数（threshold=次数，命中取最高档）
	rewardA := 0.0
	if setting.CountEnabled {
		for _, t := range setting.CountTiers {
			if count >= t.Threshold && t.MinReward > 0 {
				r := randFloatBetween(t.MinReward, t.MaxReward)
				if r > rewardA {
					rewardA = r
				}
			}
		}
	}

	// 规则 B：按昨日消耗额度（threshold=平台货币🌱；quotaInternal 转🌱后比较）
	rewardB := 0.0
	if setting.QuotaEnabled && perTier > 0 {
		quotaTier := float64(quotaInternal) / perTier // 内部额度 → 平台货币🌱
		for _, t := range setting.QuotaTiers {
			if quotaTier >= float64(t.Threshold) && t.MinReward > 0 {
				r := randFloatBetween(t.MinReward, t.MaxReward)
				if r > rewardB {
					rewardB = r
				}
			}
		}
	}

	// 规则 C：按历史累计消耗分档（usedQuotaInternal 转🌱后分档）
	rewardC := 0.0
	if setting.CEnabled && perTier > 0 {
		usedQuotaTier := float64(usedQuotaInternal) / perTier // 内部额度 → 平台货币🌱
		if r, hit := CalcCheckinTierC(usedQuotaTier, setting); hit && r > rewardC {
			rewardC = r
		}
	}

	// 三套取更高
	if rewardC > rewardB && rewardC > rewardA {
		return rewardC, "progressive"
	}
	if rewardB > rewardA {
		return rewardB, "quota"
	}
	if rewardA > 0 {
		return rewardA, "count"
	}
	// 未达标
	if setting.FallbackReward > 0 {
		return setting.FallbackReward, "fallback"
	}
	return 0, "none"
}

// CalcRewardFromUsage 根据昨日使用量与历史累计消耗计算签到奖励（返回【内部额度单位】，供落库/加余额）。
func CalcRewardFromUsage(count, quotaInternal, usedQuotaInternal int, setting *operation_setting.CheckinSetting) (reward int, source string) {
	tierReward, src := CalcRewardTierQuota(count, quotaInternal, usedQuotaInternal, setting)
	// 奖励从平台货币🌱（浮点）转回内部额度单位（整数）
	return QuotaFromCheckinTier(tierReward), src
}

// CalcCheckinReward 根据昨日使用量与历史累计消耗计算签到奖励（三套规则取更高；未达标给兜底）
//
// 配置先经 Sanitized() 归一化：既保证运营手填的越界/反转区间不会放大发放，
// 也让算法读到的是一份与热更新无关的独立快照（切片不共享底层数组）。
func CalcCheckinReward(userId int) (reward int, source string, err error) {
	setting := operation_setting.GetCheckinSetting().Sanitized()
	start, end := YesterdayRange()
	count, quota, err := GetYesterdayUsage(userId, start, end, setting.IncludeSubscription)
	if err != nil {
		return 0, "", err
	}
	// 历史累计消耗（复用钱包 used_quota，含订阅，O(1) 读取）
	usedQuota, err := GetUserUsedQuota(userId)
	if err != nil {
		return 0, "", err
	}
	r, s := CalcRewardFromUsage(count, quota, usedQuota, &setting)
	return r, s, nil
}

// UserCheckin 执行用户签到
// MySQL 和 PostgreSQL 使用事务保证原子性
// SQLite 不支持嵌套事务，使用顺序操作 + 手动回滚
func UserCheckin(userId int) (*Checkin, error) {
	setting := operation_setting.GetCheckinSetting()
	if !setting.Enabled {
		return nil, errors.New("签到功能未启用")
	}

	// 检查今天是否已签到
	hasChecked, err := HasCheckedInToday(userId)
	if err != nil {
		return nil, err
	}
	if hasChecked {
		return nil, errors.New("今日已签到")
	}

	// 根据昨日使用量分档计算奖励（三套规则取更高；未达标给兜底）
	quotaAwarded, source, err := CalcCheckinReward(userId)
	if err != nil {
		return nil, errors.New("计算签到奖励失败")
	}
	// 奖励为 0 时不要占用当天机会：既没有钱到账，又让用户当天无法再签，
	// 只会变成客诉。配置问题（未配置兜底/档位全不命中）应显式失败。
	if quotaAwarded <= 0 {
		return nil, errors.New("今日无可领取奖励，请联系管理员")
	}

	today := time.Now().Format("2006-01-02")
	checkin := &Checkin{
		UserId:       userId,
		CheckinDate:  today,
		QuotaAwarded: quotaAwarded,
		CreatedAt:    time.Now().Unix(),
	}

	// 记录命中规则，便于运营核对发放口径（不落库，只进后端日志）。
	logger.LogInfo(nil, fmt.Sprintf("checkin reward resolved: user=%d rule=%s quota=%d", userId, source, quotaAwarded))

	// 根据数据库类型选择不同的策略
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		// SQLite 不支持嵌套事务，使用顺序操作 + 手动回滚
		return userCheckinWithoutTransaction(checkin, userId, quotaAwarded)
	}

	// MySQL 和 PostgreSQL 支持事务，使用事务保证原子性
	return userCheckinWithTransaction(checkin, userId, quotaAwarded)
}

// userCheckinWithTransaction 使用事务执行签到（适用于 MySQL 和 PostgreSQL）
func userCheckinWithTransaction(checkin *Checkin, userId int, quotaAwarded int) (*Checkin, error) {
	err := DB.Transaction(func(tx *gorm.DB) error {
		// 步骤1: 创建签到记录
		// 数据库有唯一约束 (user_id, checkin_date)，可以防止并发重复签到
		if err := tx.Create(checkin).Error; err != nil {
			return errors.New("签到失败，请稍后重试")
		}

		// 步骤2: 在事务中增加用户额度。带上界并对 RowsAffected 校验——0 行
		// （用户已被软删）必须让事务回滚，否则会留下一条 quota_awarded>0 的签到
		// 记录与成功响应，但额度从未入账。
		//
		// 上界用 common.MaxWalletQuota（钱包自身的额度域），与 SQLite 分支走的
		// increaseUserQuota 保持同一常量：此前这里用 int32 的 common.MaxQuota，
		// 导致同一笔签到在 MySQL/PG 上被拒、在 SQLite 上却成功。
		result := tx.Model(&User{}).
			Where("id = ? AND quota <= ?", userId, common.MaxWalletQuota-quotaAwarded).
			Update("quota", gorm.Expr("quota + ?", quotaAwarded))
		if result.Error != nil {
			return errors.New("签到失败：更新额度出错")
		}
		if result.RowsAffected == 0 {
			return errors.New("签到失败：用户不存在或额度已达上限")
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// 事务成功后同步缓存，与 IncreaseUserQuota 的既有约定一致（失败要出声，
	// 否则缓存放着旧余额，新到账的额度会显示不出来）。
	gopool.Go(func() {
		if err := cacheIncrUserQuota(userId, int64(quotaAwarded)); err != nil {
			common.SysLog("failed to increase user quota cache after checkin: " + err.Error())
		}
	})

	return checkin, nil
}

// userCheckinWithoutTransaction 不使用事务执行签到（适用于 SQLite）
func userCheckinWithoutTransaction(checkin *Checkin, userId int, quotaAwarded int) (*Checkin, error) {
	// 步骤1: 创建签到记录
	// 数据库有唯一约束 (user_id, checkin_date)，可以防止并发重复签到
	if err := DB.Create(checkin).Error; err != nil {
		return nil, errors.New("签到失败，请稍后重试")
	}

	// 步骤2: 增加用户额度
	// 使用 db=true 强制直接写入数据库，不使用批量更新
	if err := IncreaseUserQuota(userId, quotaAwarded, true); err != nil {
		// 如果增加额度失败，需要回滚签到记录
		DB.Delete(checkin)
		return nil, errors.New("签到失败：更新额度出错")
	}

	return checkin, nil
}

// GetUserCheckinStats 获取用户签到统计信息
func GetUserCheckinStats(userId int, month string) (map[string]interface{}, error) {
	// 获取指定月份的所有签到记录
	startDate := month + "-01"
	endDate := month + "-31"

	records, err := GetUserCheckinRecords(userId, startDate, endDate)
	if err != nil {
		return nil, err
	}

	// 转换为不包含敏感字段的记录
	checkinRecords := make([]CheckinRecord, len(records))
	for i, r := range records {
		checkinRecords[i] = CheckinRecord{
			CheckinDate:  r.CheckinDate,
			QuotaAwarded: r.QuotaAwarded,
		}
	}

	// 检查今天是否已签到
	hasCheckedToday, err := HasCheckedInToday(userId)
	if err != nil {
		return nil, err
	}

	// 获取用户所有时间的签到统计
	var totalCheckins int64
	var totalQuota int64
	if err := DB.Model(&Checkin{}).Where("user_id = ?", userId).Count(&totalCheckins).Error; err != nil {
		return nil, err
	}
	if err := DB.Model(&Checkin{}).Where("user_id = ?", userId).Select("COALESCE(SUM(quota_awarded), 0)").Scan(&totalQuota).Error; err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"total_quota":      totalQuota,      // 所有时间累计获得的额度
		"total_checkins":   totalCheckins,   // 所有时间累计签到次数
		"checkin_count":    len(records),    // 本月签到次数
		"checked_in_today": hasCheckedToday, // 今天是否已签到
		"records":          checkinRecords,  // 本月签到记录详情（不含id和user_id）
	}, nil
}
