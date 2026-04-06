package middleware

import (
	"crypto/subtle"
	"net/http"
	"one-api/common"
	"strings"

	"github.com/gin-gonic/gin"
)

func NotificationWebhookAuth() func(c *gin.Context) {
	return func(c *gin.Context) {
		secret := strings.TrimSpace(common.NotificationWebhookSecret)
		if secret == "" {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"success": false,
				"message": "通知鉴权密钥未配置",
			})
			c.Abort()
			return
		}

		authorization := strings.TrimSpace(c.GetHeader("Authorization"))
		if authorization == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "未提供 Authorization 请求头",
			})
			c.Abort()
			return
		}

		if !strings.HasPrefix(strings.ToLower(authorization), "bearer ") {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "Authorization 格式错误，需使用 Bearer 方案",
			})
			c.Abort()
			return
		}

		token := strings.TrimSpace(authorization[len("Bearer "):])
		if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(secret)) != 1 {
			c.JSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"message": "通知鉴权失败",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}
