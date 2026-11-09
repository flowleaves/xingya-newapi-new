package operation_setting

import (
	"math"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"

	"github.com/shopspring/decimal"
)

const (
	InviteRebateSourceTopUp      = "topup"
	InviteRebateSourceRedemption = "redemption"

	DefaultInviteRebateRatioPercent    = 5.0
	DefaultInviteRebateWindowDays      = 15
	DefaultInviteRebateMaxPerInvitee   = 5000.0 // 芽点; 100 芽点 = 1 USD
	maxInviteRebateWindowDays          = 3650
	maxInviteRebateRatioPercent        = 100.0
	maxInviteRebatePerInviteeConfigure = 1000000.0 // defensive ceiling for the admin form
)

// InviteRebateSetting configures the post-registration recharge rebate.
//
// The rebate is paid to the inviter's native affiliate balance. A single invited
// account is capped across its whole 15-day window, not per recharge.
type InviteRebateSetting struct {
	Enabled             bool    `json:"enabled"`
	RatioPercent        float64 `json:"ratio_percent"`
	WindowDays          int     `json:"window_days"`
	MaxRebatePerInvitee float64 `json:"max_rebate_per_invitee"` // 芽点
	IncludeTopUp        bool    `json:"include_topup"`
	IncludeRedemption   bool    `json:"include_redemption"`
}

var inviteRebateSetting = InviteRebateSetting{
	Enabled:             true,
	RatioPercent:        DefaultInviteRebateRatioPercent,
	WindowDays:          DefaultInviteRebateWindowDays,
	MaxRebatePerInvitee: DefaultInviteRebateMaxPerInvitee,
	IncludeTopUp:        true,
	IncludeRedemption:   true,
}

func init() {
	config.GlobalConfig.Register("invite_rebate_setting", &inviteRebateSetting)
}

func GetInviteRebateSetting() *InviteRebateSetting {
	return &inviteRebateSetting
}

// Sanitized returns a defensive copy. Every field controls money or a time
// boundary, so malformed options fail closed instead of producing a payout.
func (s *InviteRebateSetting) Sanitized() InviteRebateSetting {
	if s == nil {
		return InviteRebateSetting{}
	}
	out := *s
	if math.IsNaN(out.RatioPercent) || math.IsInf(out.RatioPercent, 0) || out.RatioPercent < 0 {
		out.RatioPercent = 0
	}
	if out.RatioPercent > maxInviteRebateRatioPercent {
		out.RatioPercent = maxInviteRebateRatioPercent
	}
	if out.WindowDays < 0 {
		out.WindowDays = 0
	}
	if out.WindowDays > maxInviteRebateWindowDays {
		out.WindowDays = maxInviteRebateWindowDays
	}
	if math.IsNaN(out.MaxRebatePerInvitee) || math.IsInf(out.MaxRebatePerInvitee, 0) || out.MaxRebatePerInvitee < 0 {
		out.MaxRebatePerInvitee = 0
	}
	if out.MaxRebatePerInvitee > maxInviteRebatePerInviteeConfigure {
		out.MaxRebatePerInvitee = maxInviteRebatePerInviteeConfigure
	}
	return out
}

// ActiveFor reports whether this source type may produce a rebate right now.
func (s *InviteRebateSetting) ActiveFor(sourceType string) bool {
	setting := s.Sanitized()
	if !setting.Enabled || setting.RatioPercent <= 0 || setting.WindowDays <= 0 || setting.MaxRebatePerInvitee <= 0 {
		return false
	}
	switch sourceType {
	case InviteRebateSourceTopUp:
		return setting.IncludeTopUp
	case InviteRebateSourceRedemption:
		return setting.IncludeRedemption
	default:
		return false
	}
}

// MaxRebateQuota converts the 芽点 cap into internal quota units.
func (s *InviteRebateSetting) MaxRebateQuota() int {
	setting := s.Sanitized()
	if setting.MaxRebatePerInvitee <= 0 || common.QuotaPerUnit <= 0 {
		return 0
	}
	quota := common.QuotaFromDecimal(
		decimal.NewFromFloat(setting.MaxRebatePerInvitee).
			Mul(decimal.NewFromFloat(common.QuotaPerUnit)).
			Div(decimal.NewFromInt(100)),
	)
	if quota > common.MaxQuota {
		quota = common.MaxQuota
	}
	return max(quota, 0)
}

// RatioBasisPoints returns the configured percentage in basis points.
func (s *InviteRebateSetting) RatioBasisPoints() int {
	setting := s.Sanitized()
	return int(math.Round(setting.RatioPercent * 100))
}
