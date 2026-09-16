package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// SelfRefundReason 补回原因
type SelfRefundReason string

const (
	RefundReasonEmpty       SelfRefundReason = "empty_response"
	RefundReasonStreamTrunc SelfRefundReason = "stream_truncated"
)

// RefundableLogInfo 可补回日志的判定结果
type RefundableLogInfo struct {
	LogId          int    `json:"log_id"`
	CreatedAt      int64  `json:"created_at"`
	ModelName      string `json:"model_name"`
	Quota          int    `json:"quota"`
	BaseQuota      int    `json:"base_quota"`
	FundingSource  string `json:"funding_source"`
	SubscriptionId int    `json:"subscription_id"`
	RefundAmount   int    `json:"refund_amount"`
	Reason         string `json:"reason"`
	RequestId      string `json:"request_id"`
}

// JudgeSelfRefund 判定单条日志是否可补回，返回补回信息（nil=不可补）
// 判定规则：
//
//	A. 空返回：chat 路径 + completion_tokens=0 + 基数>0 + 无 tool_surcharges + 窗口内 + 未补回
//	B. 流式截断：chat 路径 + is_stream + end_reason ∈ {timeout, scanner_error, panic, ping_fail} + 未补回
//
// 钱包来源与订阅来源共用同一套判定，只有「基数从哪来」不同：
// 钱包取 log.Quota，订阅取 other.subscription_consumed（订阅日志 quota 可能为 0）。
func JudgeSelfRefund(log *model.Log, refundedLogIds map[int]bool) *RefundableLogInfo {
	setting := operation_setting.GetSelfRefundSetting()
	if !setting.Enabled {
		return nil
	}

	// 通用排除：仅 type=2（消费日志）
	if log.Type != model.LogTypeConsume {
		return nil
	}

	// 通用排除：已补回
	if refundedLogIds[log.Id] {
		return nil
	}

	// 通用排除：窗口
	windowSeconds := int64(setting.WindowHours) * 3600
	now := time.Now().Unix()
	if now-log.CreatedAt > windowSeconds {
		return nil
	}

	// 解析 other JSON
	var other map[string]interface{}
	if log.Other != "" {
		if err := common.UnmarshalJsonStr(log.Other, &other); err != nil {
			return nil
		}
	}

	// 通用排除：非 chat 路径（request_path 不是 chat/completions、responses 或 messages）
	requestPath, _ := other["request_path"].(string)
	if !isChatRequestPath(requestPath) {
		return nil
	}

	// 通用排除：工具附加费（tool_surcharges 非空）
	if toolSurchargesNotEmpty(other) {
		return nil
	}

	// 获取资金来源
	fundingSource := getFundingSource(other)
	subscriptionId := getSubscriptionId(other)

	// 计算基数
	baseQuota := 0
	if fundingSource == "subscription" {
		// 订阅请求：基数 = subscription_consumed（可能不存在，此时 base_quota=0 拒绝）
		if consumed, ok := other["subscription_consumed"].(float64); ok && consumed > 0 {
			baseQuota = common.QuotaFromFloat(consumed)
		}
	} else {
		// 钱包请求：基数 = log.Quota
		baseQuota = log.Quota
	}

	if baseQuota <= 0 {
		return nil
	}

	// 钱包额外检查：log.Quota 必须 > 0（订阅日志 quota=0 是正常的）
	if fundingSource != "subscription" && log.Quota <= 0 {
		return nil
	}

	// 计算补回金额（比例经 [0,1] 钳制，非法值按 0 处理 → 不放款）
	ratio := setting.SafeRatio()
	if ratio <= 0 {
		return nil
	}
	refundAmount := common.QuotaFromFloat(float64(baseQuota) * ratio)
	// A ratio tiny enough to round the refund down to zero must not be offered:
	// the list would advertise a refundable request and the POST would then
	// reject it as an invalid amount.
	if refundAmount <= 0 || refundAmount < setting.MinRefundQuota {
		return nil
	}

	// 判定 A：空返回（completion_tokens=0 且 baseQuota>0）
	if log.CompletionTokens == 0 && baseQuota > 0 {
		return &RefundableLogInfo{
			LogId:          log.Id,
			CreatedAt:      log.CreatedAt,
			ModelName:      log.ModelName,
			Quota:          log.Quota,
			BaseQuota:      baseQuota,
			FundingSource:  fundingSource,
			SubscriptionId: subscriptionId,
			RefundAmount:   refundAmount,
			Reason:         string(RefundReasonEmpty),
			RequestId:      log.RequestId,
		}
	}

	// 判定 B：流式截断（is_stream 且 end_reason ∈ 截断集合，且未产出完整回答）
	if log.IsStream {
		endReason := getStreamEndReason(other)
		if isTruncationEndReason(endReason) && log.CompletionTokens <= truncationRefundMaxCompletionTokens {
			return &RefundableLogInfo{
				LogId:          log.Id,
				CreatedAt:      log.CreatedAt,
				ModelName:      log.ModelName,
				Quota:          log.Quota,
				BaseQuota:      baseQuota,
				FundingSource:  fundingSource,
				SubscriptionId: subscriptionId,
				RefundAmount:   refundAmount,
				Reason:         string(RefundReasonStreamTrunc),
				RequestId:      log.RequestId,
			}
		}
	}

	return nil
}

// isChatRequestPath 判断是否为 chat/completions、responses 或 messages 路径
func isChatRequestPath(path string) bool {
	return strings.Contains(path, "/v1/chat/completions") ||
		strings.Contains(path, "/v1/responses") ||
		strings.Contains(path, "/v1/messages")
}

// toolSurchargesNotEmpty 检查 tool_surcharges 是否非空
// 空判断：无键→false，空数组→false，非空数组→true
func toolSurchargesNotEmpty(other map[string]interface{}) bool {
	raw, ok := other["tool_surcharges"]
	if !ok {
		return false
	}
	items, ok := raw.([]interface{})
	if !ok || len(items) == 0 {
		return false
	}
	return true
}

// getFundingSource 获取资金来源（默认 wallet）
func getFundingSource(other map[string]interface{}) string {
	if source, ok := other["billing_source"].(string); ok {
		return source
	}
	return "wallet"
}

// getSubscriptionId 获取订阅ID
func getSubscriptionId(other map[string]interface{}) int {
	if id, ok := other["subscription_id"].(float64); ok {
		return int(id)
	}
	return 0
}

// getStreamEndReason 从 other.stream_status 中提取 end_reason
func getStreamEndReason(other map[string]interface{}) string {
	raw, ok := other["stream_status"]
	if !ok {
		return ""
	}
	status, ok := raw.(map[string]interface{})
	if !ok {
		return ""
	}
	reason, _ := status["end_reason"].(string)
	return reason
}

// isTruncationEndReason 是否为可补回的截断原因
// 白名单：timeout, scanner_error, panic, ping_fail
func isTruncationEndReason(reason string) bool {
	switch reason {
	case "timeout", "scanner_error", "panic", "ping_fail":
		return true
	}
	return false
}

// truncationRefundMaxCompletionTokens caps how much the model may have already
// delivered before a truncated stream still counts as a failed request.
//
// A stream that died after producing a full answer (for example the upstream
// never sent [DONE] and the request hit the platform's streaming deadline)
// already delivered the service the user paid for; refunding it turns the
// platform's own stream deadline into a 50% discount. The cap keeps the refund
// aimed at the cases this feature exists for — the answer was cut off, so the
// user got materially less than they were charged for.
const truncationRefundMaxCompletionTokens = 512

// RefundableLogQuery 查询可补回日志的查询参数
type RefundableLogQuery struct {
	StartIdx int
	Num      int
	// RequestId, when set, restricts the lookup to that single log and is the
	// preferred selector: it is the identifier the user-facing log list actually
	// carries. The list payload's `id` must NOT be used for this — formatUserLogs
	// rewrites it into a page-relative display index, so it is not a primary key.
	RequestId string
	// LogId is the fallback selector (real primary key) used by the standalone
	// refund page, which receives log ids from the server directly. When it is
	// supplied together with RequestId the two must identify the same row, so a
	// stale or mismatched UI state is rejected instead of refunding a different
	// request.
	LogId int
}

// selectorSet reports whether the query targets exactly one log.
func (q RefundableLogQuery) selectorSet() bool {
	return q.RequestId != "" || q.LogId > 0
}

// resolveRefundLog loads the single log a request targets, enforcing ownership.
// request_id wins when both selectors are present; a RequestId/LogId mismatch is
// treated as "not this log" rather than resolved to the wrong row.
func resolveRefundLog(userId int, query RefundableLogQuery) (*model.Log, error) {
	if query.RequestId != "" {
		log, err := model.GetLogByRequestId(userId, query.RequestId)
		if err != nil {
			return nil, err
		}
		if query.LogId > 0 && log.Id != query.LogId {
			return nil, fmt.Errorf("log reference mismatch")
		}
		return log, nil
	}
	log, err := model.GetLogById(query.LogId)
	if err != nil {
		return nil, err
	}
	if log.UserId != userId {
		return nil, fmt.Errorf("log not found")
	}
	return log, nil
}

// GetRefundableLogs 获取用户可补回的日志列表
// 查询过滤：type=2 + 窗口内 + (completion_tokens=0 OR is_stream=1)
// 内存过滤：已补回 + JudgeSelfRefund 逐条判定
func GetRefundableLogs(userId int, query RefundableLogQuery) ([]*RefundableLogInfo, int, error) {
	setting := operation_setting.GetSelfRefundSetting()
	if !setting.Enabled {
		return nil, 0, nil
	}

	windowSeconds := int64(setting.WindowHours) * 3600
	now := time.Now().Unix()
	minCreatedAt := now - windowSeconds

	// 获取已补回集合
	refundedIds, err := model.GetRefundedLogIds(userId)
	if err != nil {
		return nil, 0, err
	}

	// 单条日志查询：供日志详情内联补回卡使用
	if query.selectorSet() {
		log, err := resolveRefundLog(userId, query)
		if err != nil {
			return []*RefundableLogInfo{}, 0, nil
		}
		if info := JudgeSelfRefund(log, refundedIds); info != nil {
			return []*RefundableLogInfo{info}, 1, nil
		}
		return []*RefundableLogInfo{}, 0, nil
	}

	// 扫描上限 500 条
	scanLimit := 500
	if query.Num > 0 && query.Num < scanLimit {
		scanLimit = query.Num
	}

	// 查询符合条件的日志（type=2 + 窗口内 + (completion=0 OR is_stream=1)）
	logs, total, err := model.GetRefundableCandidates(userId, minCreatedAt, scanLimit)
	if err != nil {
		return nil, 0, err
	}

	// 逐条判定
	result := make([]*RefundableLogInfo, 0, len(logs))
	for _, log := range logs {
		if info := JudgeSelfRefund(log, refundedIds); info != nil {
			result = append(result, info)
		}
	}

	return result, int(total), nil
}

// DoSelfRefund 执行自助补回编排
// 在 POST 内完整重验（不接受 GET 结果），确保安全
func DoSelfRefund(userId int, logId int, requestId string) (*RefundableLogInfo, error) {
	setting := operation_setting.GetSelfRefundSetting()
	if !setting.Enabled {
		return nil, fmt.Errorf("自助补回功能未启用")
	}
	if !common.LogConsumeEnabled {
		return nil, fmt.Errorf("消费日志未启用，补回功能不可用")
	}

	query := RefundableLogQuery{RequestId: requestId, LogId: logId}
	if !query.selectorSet() {
		return nil, fmt.Errorf("无效的日志ID")
	}

	// 1. 解析目标日志（request_id 优先；两者同时给出时必须指向同一条）
	log, err := resolveRefundLog(userId, query)
	if err != nil {
		return nil, fmt.Errorf("日志不存在")
	}

	// 2. 获取已补回集合
	refundedIds, err := model.GetRefundedLogIds(userId)
	if err != nil {
		return nil, fmt.Errorf("查询失败")
	}

	// 3. 全量重验
	info := JudgeSelfRefund(log, refundedIds)
	if info == nil {
		return nil, fmt.Errorf("该日志不符合补回条件")
	}

	// 4. 执行补回（按资金来源分流）。每日限额与幂等插入同处一个事务：在事务外
	// 判定限额会让并发请求同时通过检查，实际发放超出配置的每日上限。
	// 一律使用重验解析出的真实主键，绝不用客户端传入的标识符，避免展示序号/
	// 主键混淆把款补到别的日志上。
	limits := model.RefundLimits{
		MaxCount: setting.DailyMaxCount,
		MaxQuota: setting.DailyMaxQuota,
	}
	if info.FundingSource == "subscription" {
		err = model.DoSelfRefundSubscription(userId, log.Id, info.SubscriptionId, info.BaseQuota, info.RefundAmount, info.Reason, log.RequestId, limits)
	} else {
		err = model.DoSelfRefundWallet(userId, log.Id, info.BaseQuota, info.RefundAmount, info.Reason, log.RequestId, limits)
	}
	if err != nil {
		return nil, err
	}

	// 不再回写原日志 other.refunded。原因：
	//   1. 没有任何读取方——已补回状态以 log_refunds 为准（GetRefundedLogIds
	//      带 log_id 唯一索引），这个标记既不被本服务读，也没有前端/管理端消费者；
	//   2. 为了写它必须对 logs 表（生产最大表）做整列 JSON 合并，
	//      而 `other::jsonb || '{...}'` 只在 other 是 JSON 对象时安全。
	//      生产 dump 实测：525,601 行是对象，但 54,938 行是空串
	//      （''::jsonb 直接报错），且一旦 relay 写入数组/标量，合并会静默改写
	//      原载荷（数组会被追加成 [ {...} ]）。
	// 一个没人读的标记不值得让补回功能获得改写请求日志的能力。

	return info, nil
}

// GetRefundableSetting 获取补回配置（含累计已用额度）
func GetRefundableSetting(userId int) (map[string]interface{}, error) {
	setting := operation_setting.GetSelfRefundSetting()
	count, totalQuota, err := model.GetCumulativeRefundStats(userId)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"enabled":          setting.Enabled,
		"ratio":            setting.Ratio,
		"window_hours":     setting.WindowHours,
		"daily_max_count":  setting.DailyMaxCount,
		"daily_max_quota":  setting.DailyMaxQuota,
		"min_refund_quota": setting.MinRefundQuota,
		"total_used": map[string]interface{}{
			"count": count,
			"quota": totalQuota,
		},
	}, nil
}
