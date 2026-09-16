package operation_setting

import (
	"math"

	"github.com/QuantumNous/new-api/setting/config"
)

// SelfRefundSetting 自助补回功能配置
type SelfRefundSetting struct {
	Enabled        bool    `json:"enabled"`          // 是否启用自助补回功能
	Ratio          float64 `json:"ratio"`            // 补回比例（默认 0.5 = 50%）
	WindowHours    int     `json:"window_hours"`     // 可补回时间窗口（小时，默认 48）
	DailyMaxCount  int     `json:"daily_max_count"`  // 每日最大补回次数（默认 3）
	DailyMaxQuota  int     `json:"daily_max_quota"`  // 每日最大补回总额（内部额度单位，默认 0 = 不限）
	MinRefundQuota int     `json:"min_refund_quota"` // 单次最低补回（内部额度单位；默认 0 = 不限，实际最小基数为 2）
}

var selfRefundSetting = SelfRefundSetting{
	Enabled:        false,
	Ratio:          0.5,
	WindowHours:    48,
	DailyMaxCount:  3,
	DailyMaxQuota:  0,
	MinRefundQuota: 0,
}

func init() {
	config.GlobalConfig.Register("self_refund_setting", &selfRefundSetting)
}

// GetSelfRefundSetting 获取自助补回配置
func GetSelfRefundSetting() *SelfRefundSetting {
	return &selfRefundSetting
}

// SafeRatio returns the refund ratio clamped to the only range that can be
// honored: [0, 1]. The configured ratio decides how much of a charge is paid
// back, so it is a money parameter and must not be trusted from configuration
// alone — a value above 1 (writable through the option API or directly in the
// database, and otherwise constrained only by the admin form's client-side
// schema) would refund more than the user was ever charged. Non-finite values
// fall back to 0, i.e. no refund, which fails closed.
func (s *SelfRefundSetting) SafeRatio() float64 {
	if s == nil {
		return 0
	}
	ratio := s.Ratio
	if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio <= 0 {
		return 0
	}
	if ratio > 1 {
		return 1
	}
	return ratio
}

// IsSelfRefundEnabled 是否启用自助补回功能
func IsSelfRefundEnabled() bool {
	return selfRefundSetting.Enabled
}
