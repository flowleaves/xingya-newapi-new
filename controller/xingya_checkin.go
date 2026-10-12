package controller

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

func GetXingyaCheckinStatus(c *gin.Context) {
	now := model.CheckinBusinessTime(time.Now())
	month := c.DefaultQuery("month", now.Format("2006-01"))
	start, err := time.Parse("2006-01", month)
	if err != nil || start.Format("2006-01") != month || start.Year() < 2000 {
		common.ApiErrorMsg(c, "月份格式必须为 YYYY-MM")
		return
	}
	s, err := model.GetXingyaCheckinSettings()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	userID := c.GetInt("id")
	balance, err := model.GetXingyaCheckinBalance(userID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	from, to := start.Format("2006-01-02"), start.AddDate(0, 1, 0).Format("2006-01-02")
	records := []gin.H{}
	var grants []model.XingyaCheckinGrant
	if err = model.DB.Where("user_id = ? AND checkin_date >= ? AND checkin_date < ?", userID, from, to).Order("checkin_date DESC").Find(&grants).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	monthQuota := 0
	grantIDs := make([]int, 0, len(grants))
	for _, grant := range grants {
		grantIDs = append(grantIDs, grant.ID)
	}
	lotsByGrant := make(map[int]model.XingyaCheckinCreditLot, len(grants))
	if len(grantIDs) > 0 {
		var lots []model.XingyaCheckinCreditLot
		if err = model.DB.Where("user_id = ? AND grant_id IN ?", userID, grantIDs).Find(&lots).Error; err != nil {
			common.ApiError(c, err)
			return
		}
		for _, lot := range lots {
			lotsByGrant[lot.GrantID] = lot
		}
	}
	for _, grant := range grants {
		record := gin.H{"checkin_date": grant.CheckinDate, "quota_awarded": grant.QuotaAwarded, "source": "checkin"}
		if lot, ok := lotsByGrant[grant.ID]; ok {
			record["credit"] = lot
		} else {
			record["opening_balance"] = true
		}
		records = append(records, record)
		monthQuota += grant.QuotaAwarded
	}
	var legacy []model.Checkin
	if err = model.DB.Where("user_id = ? AND checkin_date >= ? AND checkin_date < ?", userID, from, to).Order("checkin_date DESC").Find(&legacy).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	for _, grant := range legacy {
		records = append(records, gin.H{"checkin_date": grant.CheckinDate, "quota_awarded": grant.QuotaAwarded, "source": "legacy"})
	}
	// Today's award is queried separately from the selected history month.
	today := now.Format("2006-01-02")
	todayRecord := gin.H{"checked_in": false, "quota_awarded": 0, "source": ""}
	var todayNew []model.XingyaCheckinGrant
	if err = model.DB.Where("user_id = ? AND checkin_date = ?", userID, today).Limit(1).Find(&todayNew).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	if len(todayNew) > 0 {
		todayRecord = gin.H{"checked_in": true, "quota_awarded": todayNew[0].QuotaAwarded, "source": "checkin"}
	} else {
		var todayOld []model.Checkin
		if err = model.DB.Where("user_id = ? AND checkin_date = ?", userID, today).Limit(1).Find(&todayOld).Error; err != nil {
			common.ApiError(c, err)
			return
		}
		if len(todayOld) > 0 {
			todayRecord = gin.H{"checked_in": true, "quota_awarded": todayOld[0].QuotaAwarded, "source": "legacy"}
		}
	}
	group, err := model.GetUserGroup(userID, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var pending int64
	if err = model.DB.Model(&model.XingyaCheckinSpend{}).Where("user_id = ? AND (state = ? OR (state = ? AND updated_at < ?))", userID, "review", "reserved", now.Add(-24*time.Hour).Unix()).Count(&pending).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"today": today, "today_record": todayRecord, "month": month, "records": records, "month_quota": monthQuota, "wallet": balance.Wallet, "expiry": balance.Expiry,
		"claim_enabled": s.ClaimEnabled, "spend_enabled": s.SpendEnabled, "min_reward": s.MinReward, "max_reward": s.MaxReward, "group": s.Group,
		"group_accessible": service.IsUserSelectableGroup(group, s.Group), "pending_review": pending})
}

func ClaimXingyaDailyCredit(c *gin.Context) {
	s, err := model.GetXingyaCheckinSettings()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	group, err := model.GetUserGroup(c.GetInt("id"), false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !service.IsUserSelectableGroup(group, s.Group) {
		common.ApiErrorMsg(c, "当前账号暂不能使用签到分组")
		return
	}
	grant, already, err := model.ClaimXingyaCheckin(c.GetInt("id"), time.Time{})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	source := "checkin"
	if grant.ID == 0 {
		source = "legacy"
	}
	if !already {
		model.RecordLog(c.GetInt("id"), model.LogTypeSystem, "星芽签到专用额度已入账")
	}
	common.ApiSuccess(c, gin.H{"checkin_date": grant.CheckinDate, "quota_awarded": grant.QuotaAwarded, "source": source, "already_claimed": already})
}

func CreateXingyaCheckinKey(c *gin.Context) {
	s, err := model.GetXingyaCheckinSettings()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	group, err := model.GetUserGroup(c.GetInt("id"), false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !s.SpendEnabled || !service.IsUserSelectableGroup(group, s.Group) {
		common.ApiErrorMsg(c, "签到消费已暂停或分组不可用")
		return
	}
	token, reused, err := model.CreateCheckinToken(c.GetInt("id"), s.Group)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"token": buildMaskedTokenResponse(token), "reused": reused})
	recordUserSecurityAudit(c, c.GetInt("id"), "checkin.key.create", map[string]any{"success": true, "token_id": token.Id, "group": token.Group, "reused": reused})
}

func GetXingyaCheckinSettings(c *gin.Context) {
	s, err := model.GetXingyaCheckinSettings()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"settings": s, "groups": ratio_setting.GetGroupRatioCopy()})
}

func UpdateXingyaCheckinSettings(c *gin.Context) {
	var request model.XingyaCheckinSettings
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiError(c, err)
		return
	}
	if request.Group != "" && !ratio_setting.ContainsGroupRatio(request.Group) {
		common.ApiErrorMsg(c, "指定分组不存在，请先按普通流程创建分组")
		return
	}
	s, err := model.SaveXingyaCheckinSettings(request)
	if errors.Is(err, model.ErrCheckinConfigConflict) {
		c.JSON(http.StatusConflict, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "checkin.settings.update", map[string]any{"group": s.Group, "revision": s.Revision, "claim_enabled": s.ClaimEnabled, "spend_enabled": s.SpendEnabled, "min_reward": s.MinReward, "max_reward": s.MaxReward})
	common.ApiSuccess(c, s)
}

func ListXingyaCheckinReviews(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("p", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	if page < 1 || page > 1000000 || size < 1 || size > 100 {
		common.ApiErrorMsg(c, "分页参数无效")
		return
	}
	query := model.DB.Model(&model.XingyaCheckinSpend{}).Where("state = ? OR (state = ? AND updated_at < ?)", "review", "reserved", common.GetTimestamp()-24*60*60)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	rows := []model.XingyaCheckinSpend{}
	if err := query.Order("updated_at ASC, request_id ASC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"items": rows, "total": total, "page": page, "page_size": size})
}

func ResolveXingyaCheckinReview(c *gin.Context) {
	var request struct {
		RequestID   string `json:"request_id"`
		UpdatedAt   int64  `json:"updated_at"`
		Action      string `json:"action"`
		ActualQuota int    `json:"actual_quota"`
		Reason      string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ApiError(c, err)
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if len(request.Reason) < 4 || len(request.Reason) > 500 {
		common.ApiErrorMsg(c, "请填写已核实的依据（4～500字节）")
		return
	}
	if err := model.ResolveCheckinSpend(request.RequestID, request.UpdatedAt, request.Action, request.ActualQuota); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "checkin.spend.resolve", map[string]any{"request_id": request.RequestID, "action": request.Action, "actual_quota": request.ActualQuota, "reason": request.Reason})
	common.ApiSuccess(c, nil)
}
