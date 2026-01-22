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
	claudeAuthURL  = "https://claude.ai/oauth/authorize"
	claudeTokenURL = "https://console.anthropic.com/v1/oauth/token"
	claudeClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
)

type ClaudePKCECodes struct {
	CodeVerifier  string `json:"code_verifier"`
	CodeChallenge string `json:"code_challenge"`
}

type ClaudeOAuthState struct {
	State        string `json:"state"`
	SessionID    string `json:"session_id"`
	ChannelID    int    `json:"channel_id,omitempty"`
	RedirectURI  string `json:"redirect_uri"`
	CodeVerifier string `json:"code_verifier"`
	ProxyURL     string `json:"proxy_url,omitempty"`
	CreatedAt    int64  `json:"created_at"`
}

type ClaudeTokenData struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Email        string `json:"email,omitempty"`
	ExpiresAt    int64  `json:"expires_at"`
}

type claudeTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Account      struct {
		UUID         string `json:"uuid"`
		EmailAddress string `json:"email_address"`
	} `json:"account"`
}

func ClaudeGeneratePKCECodes() (*ClaudePKCECodes, error) {
	codeVerifier, err := claudeGenerateCodeVerifier()
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(sum[:])
	return &ClaudePKCECodes{CodeVerifier: codeVerifier, CodeChallenge: codeChallenge}, nil
}

func claudeGenerateCodeVerifier() (string, error) {
	bytes := make([]byte, 96)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate code verifier failed: %w", err)
	}
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(bytes), nil
}

func ClaudeBuildAuthURL(redirectURI, state, codeChallenge string) (string, error) {
	if redirectURI == "" || state == "" || codeChallenge == "" {
		return "", errors.New("redirect_uri/state/code_challenge is required")
	}
	params := url.Values{
		"code":                  {"true"},
		"client_id":             {claudeClientID},
		"response_type":         {"code"},
		"redirect_uri":          {redirectURI},
		"scope":                 {"org:create_api_key user:profile user:inference"},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
	}
	return fmt.Sprintf("%s?%s", claudeAuthURL, params.Encode()), nil
}

func ClaudeExchangeCodeForTokens(ctx context.Context, code, state, codeVerifier, redirectURI, proxyURL string) (*ClaudeTokenData, error) {
	if strings.TrimSpace(code) == "" || strings.TrimSpace(codeVerifier) == "" || strings.TrimSpace(redirectURI) == "" {
		return nil, errors.New("code/code_verifier/redirect_uri is required")
	}
	parsedCode, parsedState := claudeParseCodeAndState(code)
	reqState := state
	if parsedState != "" {
		reqState = parsedState
	}
	reqBody := map[string]interface{}{
		"code":          parsedCode,
		"state":         reqState,
		"grant_type":    "authorization_code",
		"client_id":     claudeClientID,
		"redirect_uri":  redirectURI,
		"code_verifier": codeVerifier,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request failed: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, claudeTokenURL, strings.NewReader(string(jsonBody)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
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
	var tokenResp claudeTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, err
	}
	td := &ClaudeTokenData{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		Email:        tokenResp.Account.EmailAddress,
		ExpiresAt:    time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).Unix(),
	}
	return td, nil
}

func ClaudeRefreshTokens(ctx context.Context, refreshToken, proxyURL string) (*ClaudeTokenData, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return nil, errors.New("refresh_token is required")
	}
	reqBody := map[string]interface{}{
		"client_id":     claudeClientID,
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
	}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request failed: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, claudeTokenURL, strings.NewReader(string(jsonBody)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
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
	var tokenResp claudeTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, err
	}
	td := &ClaudeTokenData{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		Email:        tokenResp.Account.EmailAddress,
		ExpiresAt:    time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).Unix(),
	}
	return td, nil
}

// ========= Token 缓存（Redis 优先，fallback 内存） =========

func claudeOAuthStateKey(state string) string  { return "claude:oauth:state:" + state }
func claudeSessionKey(sessionID string) string { return "claude:oauth:session:" + sessionID }
func claudeChannelTokenKey(channelID int) string {
	return fmt.Sprintf("claude:token:channel:%d", channelID)
}

var (
	claudeMemMu     sync.RWMutex
	claudeMemKV     = map[string]memEntry{}
	claudeMemTicker sync.Once
)

func claudeMemInitJanitor() {
	claudeMemTicker.Do(func() {
		go func() {
			t := time.NewTicker(30 * time.Second)
			defer t.Stop()
			for range t.C {
				now := time.Now()
				claudeMemMu.Lock()
				for k, v := range claudeMemKV {
					if !v.ExpiresAt.IsZero() && now.After(v.ExpiresAt) {
						delete(claudeMemKV, k)
					}
				}
				claudeMemMu.Unlock()
			}
		}()
	})
}

func claudeStoreJSON(key string, v any, ttl time.Duration) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if common.RedisEnabled && common.RDB != nil {
		return common.RedisSet(key, string(b), ttl)
	}
	claudeMemInitJanitor()
	claudeMemMu.Lock()
	defer claudeMemMu.Unlock()
	exp := time.Time{}
	if ttl > 0 {
		exp = time.Now().Add(ttl)
	}
	claudeMemKV[key] = memEntry{Value: b, ExpiresAt: exp}
	return nil
}

func claudeLoadJSON(key string, out any) error {
	if common.RedisEnabled && common.RDB != nil {
		s, err := common.RedisGet(key)
		if err != nil {
			return err
		}
		return json.Unmarshal([]byte(s), out)
	}
	claudeMemInitJanitor()
	claudeMemMu.RLock()
	ent, ok := claudeMemKV[key]
	claudeMemMu.RUnlock()
	if !ok {
		return errors.New("not found")
	}
	if !ent.ExpiresAt.IsZero() && time.Now().After(ent.ExpiresAt) {
		claudeMemMu.Lock()
		delete(claudeMemKV, key)
		claudeMemMu.Unlock()
		return errors.New("expired")
	}
	return json.Unmarshal(ent.Value, out)
}

func claudeDel(key string) {
	if common.RedisEnabled && common.RDB != nil {
		_ = common.RedisDel(key)
		return
	}
	claudeMemMu.Lock()
	delete(claudeMemKV, key)
	claudeMemMu.Unlock()
}

// ========= 对外：通道 access token 获取（带续期） =========

func ClaudeGetAccessToken(ctx context.Context, channelID int, refreshToken, proxyURL string) (accessToken string, tokenData *ClaudeTokenData, err error) {
	if channelID <= 0 {
		return "", nil, errors.New("invalid channel_id")
	}
	if strings.TrimSpace(refreshToken) == "" {
		return "", nil, errors.New("missing refresh token")
	}

	var cached ClaudeTokenData
	if err := claudeLoadJSON(claudeChannelTokenKey(channelID), &cached); err == nil {
		if cached.AccessToken != "" && cached.ExpiresAt > time.Now().Add(60*time.Second).Unix() {
			return cached.AccessToken, &cached, nil
		}
	}

	td, err := ClaudeRefreshTokens(ctx, refreshToken, proxyURL)
	if err != nil {
		return "", nil, err
	}
	_ = claudeStoreJSON(claudeChannelTokenKey(channelID), td, 30*24*time.Hour)

	if td.RefreshToken != "" && td.RefreshToken != refreshToken {
		_ = model.DB.Model(&model.Channel{}).Where("id = ?", channelID).Update("key", td.RefreshToken).Error
	}
	return td.AccessToken, td, nil
}

func ClaudeSaveOAuthState(st *ClaudeOAuthState) error {
	if st == nil || st.State == "" {
		return errors.New("invalid oauth state")
	}
	return claudeStoreJSON(claudeOAuthStateKey(st.State), st, 10*time.Minute)
}

func ClaudeLoadOAuthState(state string) (*ClaudeOAuthState, error) {
	var st ClaudeOAuthState
	if err := claudeLoadJSON(claudeOAuthStateKey(state), &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func ClaudeDeleteOAuthState(state string) { claudeDel(claudeOAuthStateKey(state)) }

func ClaudeSaveOAuthSession(sessionID string, td *ClaudeTokenData) error {
	if sessionID == "" || td == nil {
		return errors.New("invalid session")
	}
	return claudeStoreJSON(claudeSessionKey(sessionID), td, 24*time.Hour)
}

func ClaudeLoadOAuthSession(sessionID string) (*ClaudeTokenData, error) {
	var td ClaudeTokenData
	if err := claudeLoadJSON(claudeSessionKey(sessionID), &td); err != nil {
		return nil, err
	}
	return &td, nil
}

func ClaudeDeleteOAuthSession(sessionID string) { claudeDel(claudeSessionKey(sessionID)) }

func ClaudeCacheChannelToken(channelID int, td *ClaudeTokenData) error {
	if channelID <= 0 || td == nil {
		return errors.New("invalid channel token")
	}
	return claudeStoreJSON(claudeChannelTokenKey(channelID), td, 30*24*time.Hour)
}

func ClaudePeekCachedChannelToken(channelID int) (*ClaudeTokenData, error) {
	if channelID <= 0 {
		return nil, errors.New("invalid channel id")
	}
	var td ClaudeTokenData
	if err := claudeLoadJSON(claudeChannelTokenKey(channelID), &td); err != nil {
		return nil, err
	}
	return &td, nil
}

func claudeParseCodeAndState(code string) (string, string) {
	parts := strings.Split(code, "#")
	parsedCode := parts[0]
	parsedState := ""
	if len(parts) > 1 {
		parsedState = parts[1]
	}
	return parsedCode, parsedState
}
