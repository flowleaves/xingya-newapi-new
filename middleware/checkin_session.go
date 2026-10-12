package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// UserAuth must run first: it verifies the session in the authentication store.
// An API key or PAT can never substitute for this live browser-session identity.
func CheckinBrowserSession() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := GetSessionAuthIdentity(c); !ok || c.GetBool("use_access_token") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"success": false, "code": "checkin_session_required", "message": "请登录网页后领取签到额度"})
			return
		}
		c.Next()
	}
}
