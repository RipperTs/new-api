package controller

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"net/http"
	"one-api/common"
	"one-api/model"
	"one-api/service"
	"os"
	"strings"
	"time"
)

type codexAuthStartReq struct {
	ChannelID int    `json:"channel_id,omitempty"`
	ProxyURL  string `json:"proxy_url,omitempty"`
}

func CodexAuthStart(c *gin.Context) {
	var req codexAuthStartReq
	_ = c.ShouldBindJSON(&req)

	// 注意：Codex 官方 OAuth 的 client_id 仅允许 localhost 回调（CLIProxyAPI 同款）
	// 这里必须使用 http://localhost:1455/auth/callback，否则会在 auth.openai.com 直接报错
	port := strings.TrimSpace(os.Getenv("CODEX_OAUTH_CALLBACK_PORT"))
	if port == "" {
		port = "1455"
	}
	redirectURI := fmt.Sprintf("http://localhost:%s/auth/callback", port)

	pkce, err := service.CodexGeneratePKCECodes()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	state := common.GetUUID()
	sessionID := common.GetUUID()

	authURL, err := service.CodexBuildAuthURL(redirectURI, state, pkce.CodeChallenge)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	proxyURL := strings.TrimSpace(req.ProxyURL)
	if proxyURL == "" && req.ChannelID > 0 {
		if ch, err := model.GetChannelById(req.ChannelID, true); err == nil && ch != nil {
			proxyURL = strings.TrimSpace(ch.GetProxyURL())
		}
	}

	if err := service.CodexSaveOAuthState(&service.CodexOAuthState{
		State:        state,
		SessionID:    sessionID,
		ChannelID:    req.ChannelID,
		RedirectURI:  redirectURI,
		CodeVerifier: pkce.CodeVerifier,
		ProxyURL:     proxyURL,
		CreatedAt:    time.Now().Unix(),
	}); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"auth_url":   authURL,
			"session_id": sessionID,
		},
	})
}

// CodexAuthCallback OAuth 回调：用 code 换 token，写入 Redis session（以及可选直接绑定到渠道）
func CodexAuthCallback(c *gin.Context) {
	code := strings.TrimSpace(c.Query("code"))
	state := strings.TrimSpace(c.Query("state"))
	if code == "" || state == "" {
		c.Data(http.StatusBadRequest, "text/html; charset=utf-8", []byte("missing code/state"))
		return
	}

	st, err := service.CodexLoadOAuthState(state)
	if err != nil {
		c.Data(http.StatusBadRequest, "text/html; charset=utf-8", []byte("invalid or expired state"))
		return
	}
	service.CodexDeleteOAuthState(state)

	proxyURL := strings.TrimSpace(st.ProxyURL)
	if proxyURL == "" && st.ChannelID > 0 {
		if ch, err := model.GetChannelById(st.ChannelID, true); err == nil && ch != nil {
			proxyURL = strings.TrimSpace(ch.GetProxyURL())
		}
	}
	if proxyURL == "" {
		proxyURL = strings.TrimSpace(os.Getenv("CODEX_OAUTH_PROXY_URL"))
	}

	td, err := service.CodexExchangeCodeForTokens(c.Request.Context(), code, st.CodeVerifier, st.RedirectURI, proxyURL)
	if err != nil {
		c.Data(http.StatusBadRequest, "text/html; charset=utf-8", []byte("exchange token failed: "+err.Error()))
		return
	}
	_ = service.CodexSaveOAuthSession(st.SessionID, td)

	bound := false
	if st.ChannelID > 0 {
		if err := bindCodexTokenToChannel(st.ChannelID, td); err == nil {
			bound = true
			_ = service.CodexCacheChannelToken(st.ChannelID, td)
			service.CodexDeleteOAuthSession(st.SessionID)
		}
	}

	html := fmt.Sprintf(`<!doctype html><html><head><meta charset="utf-8"><title>Codex 授权完成</title></head>
<body>
<script>
  try {
    if (window.opener) {
      window.opener.postMessage({
        type: "CODEX_OAUTH_DONE",
        session_id: %q,
        channel_id: %d,
        bound: %v
      }, "*");
    }
  } catch (e) {}
  setTimeout(() => { window.close(); }, 300);
</script>
<div style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Arial; padding: 24px;">
  <h2>Codex 授权完成</h2>
  <p>你可以关闭此窗口。</p>
</div>
</body></html>`, st.SessionID, st.ChannelID, bound)

	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
}

func CodexAuthGetSession(c *gin.Context) {
	sessionID := strings.TrimSpace(c.Param("session_id"))
	td, err := service.CodexLoadOAuthSession(sessionID)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "session not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"account_id": td.AccountID,
			"email":      td.Email,
			"expires_at": td.ExpiresAt,
			"has_tokens": td.RefreshToken != "",
			"session_id": sessionID,
		},
	})
}

type codexAuthBindReq struct {
	SessionID string `json:"session_id"`
}

func CodexAuthBind(c *gin.Context) {
	id := common.String2Int(c.Param("id"))
	if id <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid channel id"})
		return
	}
	var req codexAuthBindReq
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.SessionID) == "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid session_id"})
		return
	}
	td, err := service.CodexLoadOAuthSession(strings.TrimSpace(req.SessionID))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "session not found"})
		return
	}
	if err := bindCodexTokenToChannel(id, td); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	_ = service.CodexCacheChannelToken(id, td)
	service.CodexDeleteOAuthSession(req.SessionID)

	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

func CodexAuthStatus(c *gin.Context) {
	id := common.String2Int(c.Param("id"))
	if id <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid channel id"})
		return
	}
	ch, err := model.GetChannelById(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	st := ch.GetSetting()
	authMode, _ := st["auth_mode"].(string)
	email, _ := st["codex_email"].(string)
	accountID, _ := st["chatgpt_account_id"].(string)

	cached, _ := service.CodexPeekCachedChannelToken(id)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"auth_mode":   authMode,
			"has_refresh": strings.TrimSpace(ch.Key) != "",
			"account_id":  accountID,
			"email":       email,
			"cached_expire": func() int64 {
				if cached != nil {
					return cached.ExpiresAt
				}
				return 0
			}(),
		},
	})
}

func bindCodexTokenToChannel(channelID int, td *service.CodexTokenData) error {
	ch, err := model.GetChannelById(channelID, true)
	if err != nil {
		return err
	}
	if ch.Type != common.ChannelTypeCodex {
		return fmt.Errorf("channel is not codex")
	}
	if strings.TrimSpace(td.RefreshToken) == "" {
		return fmt.Errorf("missing refresh token")
	}

	setting := ch.GetSetting()
	setting["auth_mode"] = "oauth"
	if strings.TrimSpace(td.Email) != "" {
		setting["codex_email"] = td.Email
	}
	if strings.TrimSpace(td.AccountID) != "" {
		if v, ok := setting["chatgpt_account_id"].(string); !ok || strings.TrimSpace(v) == "" {
			setting["chatgpt_account_id"] = td.AccountID
		}
	}
	ch.SetSetting(setting)

	// 默认走官方 backend-api
	if ch.GetBaseURL() == "" {
		base := "https://chatgpt.com/backend-api"
		ch.BaseURL = &base
	}
	ch.Key = td.RefreshToken
	return ch.Save()
}

func getRequestScheme(c *gin.Context) string {
	if p := strings.TrimSpace(c.Request.Header.Get("X-Forwarded-Proto")); p != "" {
		return p
	}
	if c.Request.TLS != nil {
		return "https"
	}
	return "http"
}
