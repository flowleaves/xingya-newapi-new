package operation_setting

import (
	"math"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

// InviteRewardRequiredCalls is how many successful billable calls the invited account
// must complete before either side of the invite reward is paid. It stays a code
// constant because it is a shape-of-abuse bound, not a business dial.
const InviteRewardRequiredCalls = 10

// InviteRewardSetting configures the invite reward.
//
// The spend gate is stored in USD because the platform's own accounting unit is USD
// (QuotaPerUnit internal units per USD). The UI renders it in 芽点 by deriving the
// conversion at runtime, so changing the USD amount never requires a frontend change.
type InviteRewardSetting struct {
	// RequiredConsumeUSD is the cumulative billable spend the invited account must
	// reach, in USD. Zero disables the spend gate and leaves the call count alone.
	RequiredConsumeUSD float64 `json:"required_consume_usd"`
}

// defaultRequiredConsumeUSD is one US dollar, matching the published referral terms.
const defaultRequiredConsumeUSD = 1.0

var inviteRewardSetting = InviteRewardSetting{
	RequiredConsumeUSD: defaultRequiredConsumeUSD,
}

func init() {
	config.GlobalConfig.Register("invite_reward_setting", &inviteRewardSetting)
}

// GetInviteRewardSetting returns the live invite reward configuration.
func GetInviteRewardSetting() *InviteRewardSetting {
	return &inviteRewardSetting
}

// RequiredConsumeQuota converts the configured USD gate into internal quota units,
// using the runtime QuotaPerUnit so the gate tracks the configured exchange rate
// instead of a hardcoded one.
//
// It returns 0 when the gate is disabled or the configuration is unusable, which the
// caller reads as "no spend requirement". QuotaPerUnit and the configured amount are
// both operator-controlled, so a non-finite or non-positive product must not become a
// requirement: an unreachable threshold would silently stop every payout.
//
// The conversion goes through common.QuotaRound, the project's single rounding
// boundary, so an absurd configured amount saturates at the quota ceiling and is
// audited instead of wrapping into a nonsense threshold.
func (s *InviteRewardSetting) RequiredConsumeQuota(quotaPerUnit float64) int {
	if s == nil {
		return 0
	}
	usd := s.RequiredConsumeUSD
	if math.IsNaN(usd) || math.IsInf(usd, 0) || usd <= 0 {
		return 0
	}
	if math.IsNaN(quotaPerUnit) || math.IsInf(quotaPerUnit, 0) || quotaPerUnit <= 0 {
		return 0
	}
	return common.QuotaRound(usd * quotaPerUnit)
}
