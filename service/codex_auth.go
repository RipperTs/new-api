package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"one-api/common"
	"one-api/model"
	"strings"
	"sync"
	"time"
)

const (
	codexOpenAIAuthURL  = "https://auth.openai.com/oauth/authorize"
	codexOpenAITokenURL = "https://auth.openai.com/oauth/token"
	// 来自 Codex CLI / CLIProxyAPI 的公开 client_id
	codexOpenAIClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
)

type CodexPKCECodes struct {
	CodeVerifier  string `json:"code_verifier"`
	CodeChallenge string `json:"code_challenge"`
}

type CodexOAuthState struct {
	State        string `json:"state"`
	SessionID    string `json:"session_id"`
	ChannelID    int    `json:"channel_id,omitempty"`
	RedirectURI  string `json:"redirect_uri"`
	CodeVerifier string `json:"code_verifier"`
	ProxyURL     string `json:"proxy_url,omitempty"`
	CreatedAt    int64  `json:"created_at"`
}

type CodexTokenData struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	AccountID    string `json:"account_id,omitempty"`
	Email        string `json:"email,omitempty"`
	ExpiresAt    int64  `json:"expires_at"`
}

// JWT claims（不做签名校验，仅用于提取 account_id / email）
type codexJWTClaims struct {
	Email     string `json:"email"`
	CodexAuth struct {
		ChatgptAccountID string `json:"chatgpt_account_id"`
	} `json:"https://api.openai.com/auth"`
}

func (c *codexJWTClaims) GetUserEmail() string { return c.Email }
func (c *codexJWTClaims) GetAccountID() string { return c.CodexAuth.ChatgptAccountID }

func codexParseJWTToken(token string) (*codexJWTClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid jwt")
	}
	payload := parts[1]
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}
	b, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return nil, err
	}
	var claims codexJWTClaims
	if err := json.Unmarshal(b, &claims); err != nil {
		return nil, err
	}
	return &claims, nil
}

// ========= OAuth：生成 URL / 交换 code =========

func CodexGeneratePKCECodes() (*CodexPKCECodes, error) {
	verifierBytes := make([]byte, 96)
	if _, err := rand.Read(verifierBytes); err != nil {
		return nil, fmt.Errorf("generate pkce verifier failed: %w", err)
	}
	verifier := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(verifierBytes)

	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(sum[:])

	return &CodexPKCECodes{CodeVerifier: verifier, CodeChallenge: challenge}, nil
}

func CodexBuildAuthURL(redirectURI, state, codeChallenge string) (string, error) {
	if redirectURI == "" || state == "" || codeChallenge == "" {
		return "", errors.New("redirect_uri/state/code_challenge is required")
	}
	params := url.Values{
		"client_id":                  {codexOpenAIClientID},
		"response_type":              {"code"},
		"redirect_uri":               {redirectURI},
		"scope":                      {"openid email profile offline_access"},
		"state":                      {state},
		"code_challenge":             {codeChallenge},
		"code_challenge_method":      {"S256"},
		"prompt":                     {"login"},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
	}
	return fmt.Sprintf("%s?%s", codexOpenAIAuthURL, params.Encode()), nil
}

func CodexExchangeCodeForTokens(ctx context.Context, code, codeVerifier, redirectURI, proxyURL string) (*CodexTokenData, error) {
	if strings.TrimSpace(code) == "" || strings.TrimSpace(codeVerifier) == "" || strings.TrimSpace(redirectURI) == "" {
		return nil, errors.New("code/code_verifier/redirect_uri is required")
	}

	data := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {codexOpenAIClientID},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {codeVerifier},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexOpenAITokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := GetHttpClientWithProxy(proxyURL)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed: status=%d body=%s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, err
	}

	td := &CodexTokenData{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		IDToken:      tokenResp.IDToken,
		ExpiresAt:    time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).Unix(),
	}
	if claims, _ := codexParseJWTToken(tokenResp.IDToken); claims != nil {
		td.AccountID = claims.GetAccountID()
		td.Email = claims.GetUserEmail()
	}
	return td, nil
}

func CodexRefreshTokens(ctx context.Context, refreshToken, proxyURL string) (*CodexTokenData, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return nil, errors.New("refresh_token is required")
	}
	data := url.Values{
		"client_id":     {codexOpenAIClientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"scope":         {"openid profile email"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexOpenAITokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := GetHttpClientWithProxy(proxyURL)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token refresh failed: status=%d body=%s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, err
	}

	td := &CodexTokenData{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		IDToken:      tokenResp.IDToken,
		ExpiresAt:    time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).Unix(),
	}
	if claims, _ := codexParseJWTToken(tokenResp.IDToken); claims != nil {
		td.AccountID = claims.GetAccountID()
		td.Email = claims.GetUserEmail()
	}
	return td, nil
}

// ========= Token 缓存（Redis 优先，fallback 内存） =========

func codexOAuthStateKey(state string) string  { return "codex:oauth:state:" + state }
func codexSessionKey(sessionID string) string { return "codex:oauth:session:" + sessionID }
func codexChannelTokenKey(channelID int) string {
	return fmt.Sprintf("codex:token:channel:%d", channelID)
}

var (
	codexMemMu     sync.RWMutex
	codexMemKV     = map[string]memEntry{}
	codexMemTicker sync.Once
)

type memEntry struct {
	Value     []byte
	ExpiresAt time.Time
}

func codexMemInitJanitor() {
	codexMemTicker.Do(func() {
		go func() {
			t := time.NewTicker(30 * time.Second)
			defer t.Stop()
			for range t.C {
				now := time.Now()
				codexMemMu.Lock()
				for k, v := range codexMemKV {
					if !v.ExpiresAt.IsZero() && now.After(v.ExpiresAt) {
						delete(codexMemKV, k)
					}
				}
				codexMemMu.Unlock()
			}
		}()
	})
}

func codexStoreJSON(key string, v any, ttl time.Duration) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if common.RedisEnabled && common.RDB != nil {
		return common.RedisSet(key, string(b), ttl)
	}
	codexMemInitJanitor()
	codexMemMu.Lock()
	defer codexMemMu.Unlock()
	exp := time.Time{}
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}
	codexMemKV[key] = memEntry{Value: b, ExpiresAt: exp}
	return nil
}

func codexLoadJSON(key string, out any) error {
	if common.RedisEnabled && common.RDB != nil {
		s, err := common.RedisGet(key)
		if err != nil {
			return err
		}
		return json.Unmarshal([]byte(s), out)
	}
	codexMemInitJanitor()
	codexMemMu.RLock()
	ent, ok := codexMemKV[key]
	codexMemMu.RUnlock()
	if !ok {
		return errors.New("not found")
	}
	if !ent.ExpiresAt.IsZero() && time.Now().After(ent.ExpiresAt) {
		codexMemMu.Lock()
		delete(codexMemKV, key)
		codexMemMu.Unlock()
		return errors.New("expired")
	}
	return json.Unmarshal(ent.Value, out)
}

func codexDel(key string) {
	if common.RedisEnabled && common.RDB != nil {
		_ = common.RedisDel(key)
		return
	}
	codexMemMu.Lock()
	delete(codexMemKV, key)
	codexMemMu.Unlock()
}

// ========= 对外：通道 access token 获取（带续期） =========

func CodexGetAccessToken(ctx context.Context, channelID int, refreshToken, proxyURL string) (accessToken string, tokenData *CodexTokenData, err error) {
	if channelID <= 0 {
		return "", nil, errors.New("invalid channel_id")
	}
	if strings.TrimSpace(refreshToken) == "" {
		return "", nil, errors.New("missing refresh token")
	}

	// 1) cache hit
	var cached CodexTokenData
	if err := codexLoadJSON(codexChannelTokenKey(channelID), &cached); err == nil {
		// 预留 60s 缓冲
		if cached.AccessToken != "" && cached.ExpiresAt > time.Now().Add(60*time.Second).Unix() {
			return cached.AccessToken, &cached, nil
		}
	}

	// 2) refresh
	td, err := CodexRefreshTokens(ctx, refreshToken, proxyURL)
	if err != nil {
		return "", nil, err
	}
	_ = codexStoreJSON(codexChannelTokenKey(channelID), td, 30*24*time.Hour)

	// refresh_token 可能轮换：尽量落库，避免下次无法续期
	if td.RefreshToken != "" && td.RefreshToken != refreshToken {
		_ = model.DB.Model(&model.Channel{}).Where("id = ?", channelID).Update("key", td.RefreshToken).Error
	}
	return td.AccessToken, td, nil
}

func CodexSaveOAuthState(st *CodexOAuthState) error {
	if st == nil || st.State == "" {
		return errors.New("invalid oauth state")
	}
	return codexStoreJSON(codexOAuthStateKey(st.State), st, 10*time.Minute)
}

func CodexLoadOAuthState(state string) (*CodexOAuthState, error) {
	var st CodexOAuthState
	if err := codexLoadJSON(codexOAuthStateKey(state), &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func CodexDeleteOAuthState(state string) { codexDel(codexOAuthStateKey(state)) }

func CodexSaveOAuthSession(sessionID string, td *CodexTokenData) error {
	if sessionID == "" || td == nil {
		return errors.New("invalid session")
	}
	return codexStoreJSON(codexSessionKey(sessionID), td, 24*time.Hour)
}

func CodexLoadOAuthSession(sessionID string) (*CodexTokenData, error) {
	var td CodexTokenData
	if err := codexLoadJSON(codexSessionKey(sessionID), &td); err != nil {
		return nil, err
	}
	return &td, nil
}

func CodexDeleteOAuthSession(sessionID string) { codexDel(codexSessionKey(sessionID)) }

func CodexCacheChannelToken(channelID int, td *CodexTokenData) error {
	if channelID <= 0 || td == nil {
		return errors.New("invalid channel token")
	}
	return codexStoreJSON(codexChannelTokenKey(channelID), td, 30*24*time.Hour)
}

func CodexPeekCachedChannelToken(channelID int) (*CodexTokenData, error) {
	if channelID <= 0 {
		return nil, errors.New("invalid channel id")
	}
	var td CodexTokenData
	if err := codexLoadJSON(codexChannelTokenKey(channelID), &td); err != nil {
		return nil, err
	}
	return &td, nil
}
