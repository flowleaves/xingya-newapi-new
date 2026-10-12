package service

import (
	"errors"
	"io"
	"math"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
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

// PrepareCheckinRequest limits requests to the supported synchronous protocols.
// Standard local function tools are text; hosted tools and opaque remote context
// need their own proven budget and therefore fail before any upstream request.
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
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return err
	}
	raw, err := storage.Bytes()
	if err != nil {
		return err
	}
	body := gjson.ParseBytes(raw)
	for _, field := range []string{"messages", "input", "system"} {
		if err := validateCheckinContent(body.Get(field), 0); err != nil {
			return err
		}
	}
	for _, field := range []string{"extra_body", "conversation", "prompt", "previous_response_id", "audio", "modalities", "web_search_options", "web_search", "search_parameters", "enable_search", "search_mode", "mcp_servers", "container", "context_management", "prediction", "functions"} {
		if body.Get(field).Exists() {
			return errors.New("此请求的额外费用尚不支持签到额度，请切换普通密钥")
		}
	}
	if body.Get("background").Bool() {
		return errors.New("后台请求尚不支持签到额度，请切换普通密钥")
	}
	for _, message := range body.Get("messages").Array() {
		if message.Get("tools").Exists() {
			return errors.New("动态工具费用尚不支持签到额度，请切换普通密钥")
		}
	}
	_, claude := info.Request.(*dto.ClaudeRequest)
	info.CheckinToolNames = nil
	for _, tool := range body.Get("tools").Array() {
		kind := tool.Get("type").String()
		if kind != "function" && !(claude && kind == "" && tool.Get("name").String() != "") {
			return errors.New("托管工具费用尚不支持签到额度，请切换普通密钥")
		}
		name := tool.Get("name").String()
		if name == "" {
			name = tool.Get("function.name").String()
		}
		if operation_setting.GetToolPriceForModel(name, info.GetBillingModelName()) > 0 {
			return errors.New("该工具配置了额外调用费用，请切换普通密钥")
		}
		info.CheckinToolNames = append(info.CheckinToolNames, name)
	}
	if strings.Contains(info.OriginModelName, "gizmo") || strings.HasSuffix(info.OriginModelName, "search-preview") {
		return errors.New("远程上下文或搜索模型尚不支持签到额度，请切换普通密钥")
	}
	meta := info.Request.GetTokenCountMeta()
	if len(meta.Files) > 0 {
		return errors.New("多媒体费用尚不支持签到额度，请切换普通密钥")
	}
	switch request := info.Request.(type) {
	case *dto.GeneralOpenAIRequest:
		if request.N != nil && *request.N != 1 {
			return errors.New("签到密钥每次仅支持一个输出")
		}
	case *dto.OpenAIResponsesRequest:
	case *dto.ClaudeRequest:
	case *dto.EmbeddingRequest:
	default:
		return errors.New("此接口尚未接入签到额度，请切换普通密钥")
	}
	return nil
}

// DTO token counters do not recognize every file/document/reference extension.
// Only local textual history is budgetable; inspect content blocks as well.
func validateCheckinContent(value gjson.Result, depth int) error {
	if depth > 32 {
		return errors.New("签到请求内容嵌套过深")
	}
	if value.IsArray() {
		for _, item := range value.Array() {
			if err := validateCheckinContent(item, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if !value.IsObject() {
		return nil
	}
	switch value.Get("type").String() {
	case "", "message", "text", "input_text", "output_text", "tool_use", "tool_result", "function_call", "function_call_output", "thinking", "redacted_thinking", "reasoning", "refusal":
	default:
		return errors.New("文件、多媒体或远程引用尚不支持签到额度，请切换普通密钥")
	}
	if value.Get("file_id").Exists() || value.Get("encrypted_content").Exists() || value.Get("image_url").Exists() {
		return errors.New("远程或隐藏内容尚不支持签到额度，请切换普通密钥")
	}
	// Responses tool results carry their content in output rather than content.
	// These blocks can also contain remote files or images, absent from the DTO's
	// token metadata; apply the same restriction before forwarding them.
	if value.Get("type").String() == "function_call_output" {
		if err := validateCheckinContent(value.Get("output"), depth+1); err != nil {
			return err
		}
	}
	return validateCheckinContent(value.Get("content"), depth+1)
}

func ValidateCheckinChannel(info *relaycommon.RelayInfo) error {
	if info.TokenQuotaSource != BillingSourceCheckin {
		return nil
	}
	if info.ChannelMeta == nil || len(info.ParamOverride) > 0 || info.ChannelSetting.SystemPrompt != "" || info.ChannelSetting.PassThroughBodyEnabled || model_setting.GetGlobalSettings().PassThroughRequestEnabled {
		return errors.New("该渠道的参数或隐藏上下文无法核实签到预算，请切换普通密钥")
	}
	if strings.Contains(info.UpstreamModelName, "gizmo") || strings.HasSuffix(info.UpstreamModelName, "search-preview") {
		return errors.New("该上游模型的额外费用无法核实，请切换普通密钥")
	}
	// Pricing aliases resolve after request preparation. Recheck with the actual
	// billing identity before reservation and again before every upstream attempt.
	for _, name := range info.CheckinToolNames {
		if operation_setting.GetToolPriceForModel(name, info.GetBillingModelName()) > 0 {
			return errors.New("该工具配置了额外调用费用，请切换普通密钥")
		}
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
			return 0, errors.New("签到额度仅支持所有档位均按次计费的模型，请切换普通密钥")
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
		return 0, errors.New("签到额度仅支持按次计费的模型，请切换普通密钥")
	}
	if cost < 0 || math.IsNaN(cost) || math.IsInf(cost, 0) {
		return 0, errors.New("签到预算无效")
	}
	if info.TieredBillingSnapshot != nil {
		return billingexpr.QuotaRoundStrict(cost)
	}
	return common.QuotaFromFloatStrict(cost)
}

// Recheck the final payload and fixed-price reservation before each attempt.
// Reading a replay never advances the request's actual outbound body.
func ReserveCheckinOutbound(info *relaycommon.RelayInfo, request *http.Request) error {
	if info.TokenQuotaSource != BillingSourceCheckin {
		return nil
	}
	if info.Billing == nil || request.GetBody == nil {
		return errors.New("无法核实最终签到请求预算，请切换普通密钥")
	}
	body, err := request.GetBody()
	if err != nil {
		return err
	}
	defer body.Close()
	raw, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if !gjson.ValidBytes(raw) {
		return errors.New("签到额度仅支持可核实的 JSON 请求")
	}
	budget, err := CheckinRequestBudget(info)
	if err != nil {
		return err
	}
	if err = info.Billing.Reserve(budget); err != nil {
		return err
	}
	return nil
}

// RecordUserBillableUsage keeps real request counts without adding check-in
// spending to ordinary wallet/subscription consumption statistics.
func RecordUserBillableUsage(info *relaycommon.RelayInfo, quota int) {
	if info.TokenQuotaSource == BillingSourceCheckin {
		quota = 0
	}
	model.UpdateUserUsedQuotaAndRequestCount(info.UserId, quota)
}
