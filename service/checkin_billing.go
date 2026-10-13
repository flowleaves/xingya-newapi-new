package service

import (
	"errors"
	"math"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/tidwall/gjson"
)

// CheckinFunding uses a durable request ledger, not the ordinary wallet or
// subscriptions. Reserves and retries target a total, refunds finalize at zero.
type CheckinFunding struct {
	spend    model.XingyaCheckinSpend
	consumed int
}

func (f *CheckinFunding) Source() string { return BillingSourceCheckin }
func (f *CheckinFunding) PreConsume(amount int) error {
	if err := model.ReserveCheckinQuota(f.spend, amount); err != nil {
		return err
	}
	f.consumed = amount
	return nil
}
func (f *CheckinFunding) Settle(delta int) error {
	return model.FinalizeCheckinSpend(f.spend.UserID, f.spend.RequestID, f.consumed+delta, false)
}
func (f *CheckinFunding) Refund() error {
	return model.FinalizeCheckinSpend(f.spend.UserID, f.spend.RequestID, 0, true)
}

func CheckinBillingError(err error) *types.NewAPIError {
	return types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
}

// PrepareCheckinRequest keeps synchronous requests on their dedicated wallet.
// Pricing, tools and converted channel parameters use ordinary estimation and
// settlement; unexpected actual fees are bounded by the wallet overdraft ledger.
func PrepareCheckinRequest(c *gin.Context, info *relaycommon.RelayInfo) error {
	if info.TokenQuotaSource != BillingSourceCheckin {
		return nil
	}
	info.InitChannelMeta(c)
	s, err := model.GetXingyaCheckinSettings()
	if err != nil {
		return err
	}
	if !s.SpendEnabled || s.Group == "" || info.UsingGroup != s.Group || info.TokenGroup != s.Group {
		return errors.New("签到密钥分组不可用，请切换普通密钥")
	}
	switch request := info.Request.(type) {
	case *dto.GeneralOpenAIRequest:
		if request.N != nil && *request.N != 1 {
			return errors.New("签到密钥每次仅支持一个输出")
		}
	case *dto.OpenAIResponsesRequest:
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			return err
		}
		raw, err := storage.Bytes()
		if err != nil {
			return err
		}
		if gjson.GetBytes(raw, "background").Bool() {
			return errors.New("后台请求尚不支持签到额度，请切换普通密钥")
		}
	case *dto.ClaudeRequest, *dto.EmbeddingRequest:
	default:
		return errors.New("此接口尚未接入签到额度，请切换普通密钥")
	}
	return nil
}

func ValidateCheckinChannel(info *relaycommon.RelayInfo) error {
	if info.TokenQuotaSource == BillingSourceCheckin && info.ChannelMeta == nil {
		return errors.New("签到请求缺少渠道信息")
	}
	return nil
}

func CheckinRequestBudget(info *relaycommon.RelayInfo) (int, error) {
	if err := ValidateCheckinChannel(info); err != nil {
		return 0, err
	}
	p := info.PriceData
	var cost float64
	if snap := info.TieredBillingSnapshot; snap != nil {
		bound, err := billingexpr.MaximumFixedRequestCost(snap.ExprString)
		if err != nil {
			if snap.EstimatedQuotaAfterGroup < 0 || snap.EstimatedQuotaAfterGroup > common.MaxQuota {
				return 0, errors.New("签到预算无效")
			}
			return snap.EstimatedQuotaAfterGroup, nil
		}
		cost = bound / 1e6 * snap.QuotaPerUnit * snap.GroupRatio
	} else if p.UsePrice {
		for _, value := range []float64{p.ModelPrice, common.QuotaPerUnit, p.GroupRatioInfo.GroupRatio} {
			if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
				return 0, errors.New("签到预算无效")
			}
		}
		// Match ordinary request-price settlement, including decimal rounding.
		amount := decimal.NewFromFloat(p.ModelPrice).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).Mul(decimal.NewFromFloat(p.GroupRatioInfo.GroupRatio))
		amount = p.ApplyOtherRatiosToDecimal(amount)
		cost, _ = amount.Round(0).Float64()
	} else {
		if p.QuotaToPreConsume < 0 || p.QuotaToPreConsume > common.MaxQuota {
			return 0, errors.New("签到预算无效")
		}
		return p.QuotaToPreConsume, nil
	}
	if cost < 0 || math.IsNaN(cost) || math.IsInf(cost, 0) {
		return 0, errors.New("签到预算无效")
	}
	if info.TieredBillingSnapshot != nil {
		return billingexpr.QuotaRoundStrict(cost)
	}
	return common.QuotaFromFloatStrict(cost)
}

// Converted bodies, prompt injection and passthrough no longer require a fixed
// upper bound. Keep the ordinary pricing snapshot and reservation on every retry.
func ReserveCheckinOutbound(info *relaycommon.RelayInfo, request *http.Request) error {
	if info.TokenQuotaSource != BillingSourceCheckin {
		return nil
	}
	if info.Billing == nil {
		return errors.New("签到计费会话缺失")
	}
	// Channel overrides and Chat-to-Responses conversion can introduce the
	// asynchronous flag after the original client request was checked. Inspect
	// a fresh reader so validation cannot consume the body sent to the provider.
	if info.GetFinalRequestRelayFormat() == types.RelayFormatOpenAIResponses {
		if request == nil || request.GetBody == nil {
			return errors.New("无法核实签到请求是否为后台任务，请切换普通密钥")
		}
		body, err := request.GetBody()
		if err != nil {
			return err
		}
		defer body.Close()
		var flags struct {
			Background bool `json:"background"`
		}
		if err = common.DecodeJson(body, &flags); err != nil {
			return errors.New("无法核实签到请求是否为后台任务，请切换普通密钥")
		}
		if flags.Background {
			return errors.New("后台请求尚不支持签到额度，请切换普通密钥")
		}
	}
	budget, err := CheckinRequestBudget(info)
	if err != nil {
		return err
	}
	return info.Billing.Reserve(budget)
}

// RecordUserBillableUsage keeps real request counts without adding check-in
// spending to ordinary wallet/subscription consumption statistics.
func RecordUserBillableUsage(info *relaycommon.RelayInfo, quota int) {
	if info.TokenQuotaSource == BillingSourceCheckin {
		quota = 0
	}
	model.UpdateUserUsedQuotaAndRequestCount(info.UserId, quota)
}
