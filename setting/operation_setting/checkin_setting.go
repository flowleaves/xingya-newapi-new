package operation_setting

import (
	"math"

	"github.com/QuantumNous/new-api/setting/config"
)

// CheckinTier 签到分档。单位约定：
// threshold/minReward/maxReward 均按【平台货币🌱】配置（用户直观数值，如 5~10、20~30）。
// 平台货币 🌱 与 new-api 内部额度单位的关系：1 🌱 = QuotaPerUnit / 100 = 5000 内部额度单位。
// 内部换算在 service/model 层完成，配置与前端展示均用 🌱。
type CheckinTier struct {
	Threshold int     `json:"threshold"`  // 档位阈值（A=昨日调用次数；B=昨日消耗的平台货币🌱数）
	MinReward float64 `json:"min_reward"` // 奖励下限（平台货币🌱，可为小数）
	MaxReward float64 `json:"max_reward"` // 奖励上限（平台货币🌱，可为小数）
}

// CheckinSetting 签到功能配置
type CheckinSetting struct {
	Enabled bool `json:"enabled"`

	// 规则 A：按昨日调用次数
	CountEnabled bool          `json:"count_enabled"`
	CountTiers   []CheckinTier `json:"count_tiers"`

	// 规则 B：按昨日消耗额度（含订阅开关，单位=平台货币🌱）
	QuotaEnabled        bool          `json:"quota_enabled"`
	QuotaTiers          []CheckinTier `json:"quota_tiers"`
	IncludeSubscription bool          `json:"include_subscription"`

	// 规则 C：按历史累计消耗分档（复用 users.used_quota，含订阅；单位=平台货币🌱）
	CEnabled       bool    `json:"c_enabled"`
	CBaseThreshold float64 `json:"c_base_threshold"` // 起始门槛（芽点），默认 1000
	CBaseReward    float64 `json:"c_base_reward"`    // 基础奖励（芽点），默认 10
	CStepQuota     float64 `json:"c_step_quota"`     // 每档消耗增量（芽点），默认 1000
	CStepReward    float64 `json:"c_step_reward"`    // 每档奖励增量（芽点），默认 5
	CMaxReward     float64 `json:"c_max_reward"`     // 封顶奖励（芽点），默认 30

	// 未达标兜底（平台货币🌱，可为小数）
	FallbackReward float64 `json:"fallback_reward"`
}

// 默认配置（单位=平台货币🌱，1🌱=5000内部额度单位）
var checkinSetting = CheckinSetting{
	Enabled: false,

	// 规则A默认：>10次 -> 5~10 🌱
	CountEnabled: true,
	CountTiers: []CheckinTier{
		{Threshold: 10, MinReward: 5.0, MaxReward: 10.0},
	},

	// 规则B默认：消耗 >100🌱 -> 5~15；>1000🌱 -> 20~30 🌱
	QuotaEnabled:        true,
	IncludeSubscription: true,
	QuotaTiers: []CheckinTier{
		{Threshold: 100, MinReward: 5.0, MaxReward: 15.0},
		{Threshold: 1000, MinReward: 20.0, MaxReward: 30.0},
	},

	// 规则C默认：历史累计消耗 ≥1000🌱 -> 10；每+1000🌱 +5；封顶 30 🌱
	CEnabled:       true,
	CBaseThreshold: 1000.0,
	CBaseReward:    10.0,
	CStepQuota:     1000.0,
	CStepReward:    5.0,
	CMaxReward:     30.0,

	FallbackReward: 5.0, // 未达标兜底 5 🌱
}

// MaxCheckinRewardTier is the hard ceiling for a single check-in payout, in
// platform currency (🌱). Every check-in credit is real wallet quota and the
// bounds come from hand-typed admin settings, so a typo (an extra zero) must not
// be able to hand out an arbitrary amount. Values above this are clamped.
const MaxCheckinRewardTier = 100000.0

// Sanitized returns a defensive copy of the check-in settings with every
// operator-entered number normalized, so reward calculation can never be skewed
// by an out-of-range or inverted configuration:
//
//   - negative thresholds become 0 (always met) rather than acting as a discount;
//   - an inverted reward range is sorted instead of silently paying the larger
//     bound (a swapped min/max used to return min, i.e. more than the maximum);
//   - every reward is clamped to [0, MaxCheckinRewardTier];
//   - a negative step for rule C becomes 0, which CalcCheckinTierC already treats
//     as "single tier".
//
// The result shares no slice storage with the live setting, so callers can
// iterate it without racing a concurrent hot reload of the same slices.
func (s *CheckinSetting) Sanitized() CheckinSetting {
	if s == nil {
		return CheckinSetting{}
	}
	out := *s
	out.CountTiers = sanitizeCheckinTiers(s.CountTiers)
	out.QuotaTiers = sanitizeCheckinTiers(s.QuotaTiers)
	out.FallbackReward = clampCheckinRewardTier(s.FallbackReward)
	out.CBaseThreshold = math.Max(s.CBaseThreshold, 0)
	out.CBaseReward = clampCheckinRewardTier(s.CBaseReward)
	out.CStepQuota = math.Max(s.CStepQuota, 0)
	out.CStepReward = clampCheckinRewardTier(s.CStepReward)
	out.CMaxReward = clampCheckinRewardTier(s.CMaxReward)
	// A cap below the base reward would make rule C self-contradictory; the cap
	// always wins so the payout stays bounded.
	if out.CMaxReward < out.CBaseReward {
		out.CMaxReward = out.CBaseReward
	}
	return out
}

func sanitizeCheckinTiers(tiers []CheckinTier) []CheckinTier {
	if len(tiers) == 0 {
		return nil
	}
	out := make([]CheckinTier, 0, len(tiers))
	for _, tier := range tiers {
		if tier.Threshold < 0 {
			tier.Threshold = 0
		}
		if tier.MinReward > tier.MaxReward {
			tier.MinReward, tier.MaxReward = tier.MaxReward, tier.MinReward
		}
		tier.MinReward = clampCheckinRewardTier(tier.MinReward)
		tier.MaxReward = clampCheckinRewardTier(tier.MaxReward)
		out = append(out, tier)
	}
	return out
}

// clampCheckinRewardTier bounds a single reward value to [0, MaxCheckinRewardTier]
// and maps NaN to 0, so a malformed config cannot produce a NaN reward that would
// later be converted to a nonsense quota integer.
func clampCheckinRewardTier(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	if v < 0 {
		return 0
	}
	if v > MaxCheckinRewardTier {
		return MaxCheckinRewardTier
	}
	return v
}

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("checkin_setting", &checkinSetting)
}

// GetCheckinSetting 获取签到配置
func GetCheckinSetting() *CheckinSetting {
	return &checkinSetting
}

// IsCheckinEnabled 是否启用签到功能
func IsCheckinEnabled() bool {
	return checkinSetting.Enabled
}
