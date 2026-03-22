package claudecode

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"one-api/dto"
	"one-api/relay/channel"
	relaycommon "one-api/relay/common"
	relayconstant "one-api/relay/constant"
	"one-api/service"
	"strings"
	"time"
)

const (
	RequestModeCompletion = 1
	RequestModeMessage    = 2
)

type Adaptor struct {
	RequestMode int
}

func shouldUseAnthropicBeta(setting map[string]interface{}) bool {
	if setting == nil {
		return true
	}
	raw, ok := setting["use_anthropic_beta"]
	if !ok {
		return true
	}
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		s := strings.ToLower(strings.TrimSpace(v))
		switch s {
		case "", "true", "1", "yes", "on":
			return true
		case "false", "0", "no", "off":
			return false
		default:
			return true
		}
	case float64:
		return v != 0
	default:
		return true
	}
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	//TODO implement me
	return nil, errors.New("not implemented")
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	//TODO implement me
	return nil, errors.New("not implemented")
}

// 不再兼容 claud 2 版本模型
func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
	a.RequestMode = RequestModeMessage
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if a.RequestMode == RequestModeMessage {
		return fmt.Sprintf("%s/v1/messages?beta=true", info.BaseUrl), nil
	} else {
		return fmt.Sprintf("%s/v1/complete", info.BaseUrl), nil
	}
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, req)
	apiKey := info.ApiKey
	if m, ok := info.ChannelSetting["auth_mode"].(string); ok && strings.EqualFold(m, "oauth") {
		token, _, err := service.ClaudeGetAccessToken(c.Request.Context(), info.ChannelId, info.ApiKey, info.ProxyURL)
		if err != nil {
			return err
		}
		apiKey = token
	}
	req.Set("x-api-key", apiKey)
	anthropicVersion := c.Request.Header.Get("anthropic-version")
	if anthropicVersion == "" {
		anthropicVersion = "2023-06-01"
	}
	req.Set("anthropic-version", anthropicVersion)

	// 设置 Claude Code 特有的请求头
	req.Set("X-Stainless-Retry-Count", "0")
	req.Set("X-Stainless-Timeout", "60")
	req.Set("X-Stainless-Lang", "js")
	req.Set("X-Stainless-Package-Version", "0.55.1")
	req.Set("X-Stainless-OS", "MacOS")
	req.Set("X-Stainless-Arch", "arm64")
	req.Set("X-Stainless-Runtime", "node")
	req.Set("x-stainless-helper-method", "stream")
	req.Set("x-app", "cli")
	req.Set("User-Agent", "claude-cli/1.0.44 (external, cli)")
	// interleaved-thinking 会引入 thinking signature 校验。
	// OpenAI 兼容模式下我们无法可靠透传 thinking block/signature，因此默认禁用；
	// 仅在 Claude /v1/messages 原生请求时开启，保持与 Claude Code CLI 行为一致。
	if shouldUseAnthropicBeta(info.ChannelSetting) {
		clientBeta := strings.TrimSpace(c.GetHeader("anthropic-beta"))
		if c.GetBool("claude_is_official_cli") && clientBeta != "" {
			req.Set("anthropic-beta", clientBeta)
		} else {
			beta := "claude-code-20250219,oauth-2025-04-20,fine-grained-tool-streaming-2025-05-14"
			if !c.GetBool("claude_disable_interleaved_thinking") && info != nil && info.RelayMode == relayconstant.RelayModeClaudeMessages {
				beta = "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,fine-grained-tool-streaming-2025-05-14"
			}
			req.Set("anthropic-beta", beta)
		}
	} else {
		req.Del("anthropic-beta")
	}
	req.Set("X-Stainless-Runtime-Version", "v20.18.1")
	req.Set("anthropic-dangerous-direct-browser-access", "true")
	// 兼容验证要求的额外头
	req.Set("Authorization", fmt.Sprintf("Bearer %s", apiKey))
	req.Set("accept-language", "*")
	req.Set("sec-fetch-mode", "cors")

	return nil
}

func stripThinkingBlocks(body []byte) []byte {
	// 仅用于失败重试：移除 thinking/redacted_thinking block，避免 signature 校验导致 400。
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return body
	}
	delete(root, "thinking")

	msgs, ok := root["messages"].([]any)
	if !ok {
		out, err := json.Marshal(root)
		if err != nil {
			return body
		}
		return out
	}
	for i := range msgs {
		m, ok := msgs[i].(map[string]any)
		if !ok {
			continue
		}
		blocks, ok := m["content"].([]any)
		if !ok {
			continue
		}
		filtered := make([]any, 0, len(blocks))
		for _, b := range blocks {
			bm, ok := b.(map[string]any)
			if !ok {
				filtered = append(filtered, b)
				continue
			}
			t, _ := bm["type"].(string)
			if t == "thinking" || t == "redacted_thinking" {
				continue
			}
			filtered = append(filtered, b)
		}
		m["content"] = filtered
		msgs[i] = m
	}
	root["messages"] = msgs

	out, err := json.Marshal(root)
	if err != nil {
		return body
	}
	return out
}

func (a *Adaptor) ConvertRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}

	if a.RequestMode == RequestModeCompletion {
		return RequestOpenAI2ClaudeComplete(*request), nil
	} else {
		return RequestOpenAI2ClaudeMessage(*request, info)
	}
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return nil, nil
}

func isThinkingSignatureError(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "thinking signature has expired or is invalid") ||
		strings.Contains(m, "invalid `signature` in `thinking` block") ||
		strings.Contains(m, "invalid signature in thinking block")
}

func ensureMetadataUserID(body []byte, apiKey string) []byte {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return body
	}

	metadata, ok := root["metadata"].(map[string]any)
	if ok {
		if userID, exists := metadata["user_id"].(string); exists && strings.TrimSpace(userID) != "" {
			return body
		}
	} else {
		metadata = make(map[string]any)
	}

	metadata["user_id"] = generateClaudeCodeUserID(apiKey)
	root["metadata"] = metadata

	patched, err := json.Marshal(root)
	if err != nil {
		return body
	}
	return patched
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	// 读取请求体，便于重试时重复发送（Claude Code 偶发返回 thinking signature 相关 400）。
	bodyBytes, err := io.ReadAll(requestBody)
	if err != nil {
		fmt.Printf("[ClaudeCode] Error reading request body: %v\n", err)
		return nil, err
	}
	if info != nil {
		bodyBytes = ensureMetadataUserID(bodyBytes, info.ApiKey)
	}

	doOnce := func() (*http.Response, error) {
		return channel.DoApiRequest(a, c, info, bytes.NewReader(bodyBytes))
	}

	resp, err := doOnce()
	if err != nil {
		return nil, err
	}
	if resp != nil && resp.StatusCode == http.StatusBadRequest {
		// 读取 body 判断是否为可重试的 thinking signature 错误；读完后恢复 body，避免影响后续错误处理逻辑。
		b, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(b))
		if readErr == nil {
			msg := string(b)
			if isThinkingSignatureError(msg) {
				// 该错误可能是上游偶发，也可能是请求里携带了过期 signature。
				// 重试时禁用 interleaved-thinking，并移除 thinking block，尽量做到“自动开新会话”效果。
				time.Sleep(800 * time.Millisecond)
				c.Set("claude_disable_interleaved_thinking", true)
				retryBody := bodyBytes
				if info != nil && info.RelayMode == relayconstant.RelayModeClaudeMessages {
					retryBody = stripThinkingBlocks(bodyBytes)
				}
				resp2, err2 := channel.DoApiRequest(a, c, info, bytes.NewReader(retryBody))
				if err2 == nil && resp2 != nil {
					return resp2, nil
				}
				// 重试失败则返回首次响应，保留原始错误信息。
			}
		}
	}
	return resp, nil
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *dto.OpenAIErrorWithStatusCode) {
	// 检查响应类型，如果是 text/event-stream，强制使用流式处理
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/event-stream") {
		err, usage = ClaudeStreamHandler(c, resp, info, a.RequestMode)
	} else if info.IsStream {
		err, usage = ClaudeStreamHandler(c, resp, info, a.RequestMode)
	} else {
		err, usage = ClaudeHandler(c, resp, a.RequestMode, info)
	}

	return
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
