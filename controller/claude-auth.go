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

type claudeAuthStartReq struct {
	ChannelID int    `json:"channel_id,omitempty"`
	ProxyURL  string `json:"proxy_url,omitempty"`
}

func ClaudeAuthStart(c *gin.Context) {
	var req claudeAuthStartReq
	_ = c.ShouldBindJSON(&req)

	port := strings.TrimSpace(os.Getenv("CLAUDE_OAUTH_CALLBACK_PORT"))
	if port == "" {
		port = "54545"
	}
	redirectURI := fmt.Sprintf("http://localhost:%s/callback", port)

	pkce, err := service.ClaudeGeneratePKCECodes()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	state := common.GetUUID()
	sessionID := common.GetUUID()

	authURL, err := service.ClaudeBuildAuthURL(redirectURI, state, pkce.CodeChallenge)
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

	if err := service.ClaudeSaveOAuthState(&service.ClaudeOAuthState{
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

type claudeAuthCompleteReq struct {
	CallbackURL string `json:"callback_url"`
	Code        string `json:"code"`
	State       string `json:"state"`
}

func ClaudeAuthComplete(c *gin.Context) {
	var req claudeAuthCompleteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid request"})
		return
	}

	code := strings.TrimSpace(req.Code)
	state := strings.TrimSpace(req.State)
	if code == "" || state == "" {
		code, state = extractCodeStateFromCallbackInput(req.CallbackURL)
	}
	if code == "" || state == "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "missing code/state，请粘贴完整回调 URL（包含 code 和 state）"})
		return
	}

	sessionID, channelID, bound, td, err := finalizeClaudeOAuth(c, code, state)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"session_id": sessionID,
			"channel_id": channelID,
			"bound":      bound,
			"email":      td.Email,
			"expires_at": td.ExpiresAt,
		},
	})
}

func ClaudeAuthCallback(c *gin.Context) {
	code := strings.TrimSpace(c.Query("code"))
	state := strings.TrimSpace(c.Query("state"))
	if code == "" || state == "" {
		c.Data(http.StatusBadRequest, "text/html; charset=utf-8", []byte("missing code/state"))
		return
	}

	sessionID, channelID, bound, _, err := finalizeClaudeOAuth(c, code, state)
	if err != nil {
		c.Data(http.StatusBadRequest, "text/html; charset=utf-8", []byte("exchange token failed: "+err.Error()))
		return
	}

	html := fmt.Sprintf(`<!doctype html><html><head><meta charset="utf-8"><title>Claude 授权完成</title></head>
<body>
<script>
  try {
    if (window.opener) {
      window.opener.postMessage({
        type: "CLAUDE_OAUTH_DONE",
        session_id: %q,
        channel_id: %d,
        bound: %v
      }, "*");
    }
  } catch (e) {}
  setTimeout(() => { window.close(); }, 300);
</script>
<div style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Arial; padding: 24px;">
  <h2>Claude 授权完成</h2>
  <p>你可以关闭此窗口。</p>
</div>
</body></html>`, sessionID, channelID, bound)

	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
}

func ClaudeAuthGetSession(c *gin.Context) {
	sessionID := strings.TrimSpace(c.Param("session_id"))
	td, err := service.ClaudeLoadOAuthSession(sessionID)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "session not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"email":      td.Email,
			"expires_at": td.ExpiresAt,
			"has_tokens": td.RefreshToken != "",
			"session_id": sessionID,
		},
	})
}

type claudeAuthBindReq struct {
	SessionID string `json:"session_id"`
}

func ClaudeAuthBind(c *gin.Context) {
	id := common.String2Int(c.Param("id"))
	if id <= 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid channel id"})
		return
	}
	var req claudeAuthBindReq
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.SessionID) == "" {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid session_id"})
		return
	}
	td, err := service.ClaudeLoadOAuthSession(strings.TrimSpace(req.SessionID))
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "session not found"})
		return
	}
	if err := bindClaudeTokenToChannel(id, td); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	_ = service.ClaudeCacheChannelToken(id, td)
	service.ClaudeDeleteOAuthSession(req.SessionID)

	c.JSON(http.StatusOK, gin.H{"success": true, "message": ""})
}

func ClaudeAuthStatus(c *gin.Context) {
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
	email, _ := st["claude_email"].(string)
	cached, _ := service.ClaudePeekCachedChannelToken(id)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"auth_mode":   authMode,
			"has_refresh": strings.TrimSpace(ch.Key) != "",
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

func finalizeClaudeOAuth(c *gin.Context, code, state string) (sessionID string, channelID int, bound bool, td *service.ClaudeTokenData, err error) {
	st, err := service.ClaudeLoadOAuthState(state)
	if err != nil {
		return "", 0, false, nil, fmt.Errorf("invalid or expired state")
	}

	proxyURL := strings.TrimSpace(st.ProxyURL)
	if proxyURL == "" && st.ChannelID > 0 {
		if ch, err2 := model.GetChannelById(st.ChannelID, true); err2 == nil && ch != nil {
			proxyURL = strings.TrimSpace(ch.GetProxyURL())
		}
	}
	if proxyURL == "" {
		proxyURL = strings.TrimSpace(os.Getenv("CLAUDE_OAUTH_PROXY_URL"))
	}

	td, err = service.ClaudeExchangeCodeForTokens(c.Request.Context(), code, state, st.CodeVerifier, st.RedirectURI, proxyURL)
	if err != nil {
		return "", 0, false, nil, err
	}
	service.ClaudeDeleteOAuthState(state)

	_ = service.ClaudeSaveOAuthSession(st.SessionID, td)

	bound = false
	if st.ChannelID > 0 {
		if err := bindClaudeTokenToChannel(st.ChannelID, td); err == nil {
			bound = true
			_ = service.ClaudeCacheChannelToken(st.ChannelID, td)
			service.ClaudeDeleteOAuthSession(st.SessionID)
		}
	}

	return st.SessionID, st.ChannelID, bound, td, nil
}

func bindClaudeTokenToChannel(channelID int, td *service.ClaudeTokenData) error {
	if channelID <= 0 || td == nil {
		return fmt.Errorf("invalid channel/token")
	}
	ch, err := model.GetChannelById(channelID, true)
	if err != nil {
		return err
	}
	setting := ch.GetSetting()
	setting["auth_mode"] = "oauth"
	if strings.TrimSpace(td.Email) != "" {
		setting["claude_email"] = td.Email
	}
	ch.SetSetting(setting)

	if ch.GetBaseURL() == "" {
		base := "https://claude.ai"
		ch.BaseURL = &base
	}
	ch.Key = td.RefreshToken
	return ch.Save()
}
