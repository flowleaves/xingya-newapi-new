package controller

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// GetInviteRebates returns the inviter-owned summary and rebate ledger page.
func GetInviteRebates(c *gin.Context) {
	userId := c.GetInt("id")
	summary, err := model.GetInviteRebateSummary(userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo := common.GetPageQuery(c)
	items, total, err := model.GetInviteRebatesForInviter(userId, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	for _, item := range items {
		item.InviteeUsername = maskInviteeName(item.InviteeUsername)
	}
	c.JSON(200, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"summary":   summary,
			"items":     items,
			"page":      pageInfo.Page,
			"page_size": pageInfo.PageSize,
			"total":     total,
		},
	})
}

// maskInviteeName keeps enough shape for the inviter to recognise an invitee
// without exposing the full account name to the page payload.
func maskInviteeName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "用户"
	}
	runes := []rune(name)
	if len(runes) <= 2 {
		return string(runes[0]) + "***"
	}
	return string(runes[0]) + "***" + string(runes[len(runes)-1])
}
