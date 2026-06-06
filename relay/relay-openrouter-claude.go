package relay

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"one-api/common"
	"one-api/dto"
	"one-api/relay/channel/claudecode"
	relaycommon "one-api/relay/common"
	"one-api/service"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

type openRouterClaudeMessagesAdapter struct{}

func (a *openRouterClaudeMessagesAdapter) ValidateChannel(relayInfo *relaycommon.RelayInfo) error {
	if relayInfo.ChannelType != common.ChannelTypeOpenRouter {
		return errors.New("当前渠道不支持 OpenRouter /v1/messages 兼容")
	}
	return nil
}

func (a *openRouterClaudeMessagesAdapter) NormalizeRequest(c *gin.Context, relayInfo *relaycommon.RelayInfo, claudeReq *claudecode.ClaudeRequest, bodyMap map[string]json.RawMessage) error {
	claudeReq.System = filterOpenRouterRedundantSystem(claudeReq.System)
	return nil
}

func (a *openRouterClaudeMessagesAdapter) PrepareRequest(c *gin.Context, relayInfo *relaycommon.RelayInfo, claudeReq *claudecode.ClaudeRequest, bodyMap map[string]json.RawMessage) (*ClaudeMessagesPreparedRequest, error) {
	openaiReqMap, err := buildOpenAIChatRequestFromClaudeMessages(claudeReq, convertClaudeThinkingToReasoning(bodyMap))
	if err != nil {
		return nil, err
	}
	applyOpenRouterProviderPreference(openaiReqMap, relayInfo.ChannelSetting)
	jsonData, err := json.Marshal(openaiReqMap)
	if err != nil {
		return nil, err
	}
	return &ClaudeMessagesPreparedRequest{Payload: jsonData}, nil
}

func (a *openRouterClaudeMessagesAdapter) DoRequest(c *gin.Context, relayInfo *relaycommon.RelayInfo, prepared *ClaudeMessagesPreparedRequest) (*http.Response, error) {
	return doOpenRouterChatCompletionsRequest(c, relayInfo, prepared.Payload)
}

func (a *openRouterClaudeMessagesAdapter) HandleResponse(c *gin.Context, resp *http.Response, relayInfo *relaycommon.RelayInfo) (*dto.Usage, *dto.OpenAIErrorWithStatusCode, error) {
	if relayInfo.IsStream {
		return streamOpenAIChatToClaude(c, resp, relayInfo)
	}
	usage, err := nonStreamOpenAIChatToClaude(c, resp, relayInfo)
	return usage, nil, err
}

func OpenRouterClaudeMessagesHelper(c *gin.Context) (openaiErr *dto.OpenAIErrorWithStatusCode) {
	relayInfo := relaycommon.GenRelayInfo(c)
	return handleClaudeMessagesWithAdapter(c, relayInfo, &openRouterClaudeMessagesAdapter{})
}

func convertClaudeThinkingToReasoning(bodyMap map[string]json.RawMessage) map[string]any {
	if bodyMap == nil {
		return nil
	}
	reasoning := map[string]any{}

	if raw, ok := bodyMap["thinking"]; ok && len(raw) != 0 {
		var thinkingMap map[string]any
		if err := json.Unmarshal(raw, &thinkingMap); err == nil && thinkingMap != nil {
			if isClaudeThinkingDisabled(raw) {
				return map[string]any{"effort": "none"}
			}
			if t, ok := thinkingMap["type"].(string); ok && strings.TrimSpace(t) != "" {
				if strings.EqualFold(strings.TrimSpace(t), "enabled") {
					reasoning["enabled"] = true
				}
			}
			budgetTokens := parseNumberToInt(thinkingMap["budget_tokens"])
			if budgetTokens != 0 {
				setReasoningEffortIfAbsent(reasoning, mapBudgetTokensToEffort(budgetTokens))
			}
		}
	}
	if raw, ok := bodyMap["output_config"]; ok && len(raw) != 0 {
		var outputConfig map[string]any
		if err := json.Unmarshal(raw, &outputConfig); err == nil && outputConfig != nil {
			if effort, ok := outputConfig["effort"].(string); ok {
				setReasoningEffortIfAbsent(reasoning, normalizeOpenRouterReasoningEffort(effort))
			}
		}
	}

	if len(reasoning) == 0 {
		return nil
	}
	return reasoning
}

func setReasoningEffortIfAbsent(reasoning map[string]any, effort string) {
	if reasoning == nil || strings.TrimSpace(effort) == "" {
		return
	}
	if _, exists := reasoning["effort"]; !exists {
		reasoning["effort"] = effort
	}
}

func normalizeOpenRouterReasoningEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "max":
		return "xhigh"
	case "xhigh", "high", "medium", "low", "minimal", "none":
		return strings.ToLower(strings.TrimSpace(effort))
	default:
		return ""
	}
}

func parseNumberToInt(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case float32:
		return int(t)
	case int:
		return t
	case int8:
		return int(t)
	case int16:
		return int(t)
	case int32:
		return int(t)
	case int64:
		return int(t)
	case uint:
		return int(t)
	case uint8:
		return int(t)
	case uint16:
		return int(t)
	case uint32:
		return int(t)
	case uint64:
		return int(t)
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(t))
		return i
	default:
		return 0
	}
}

func mapBudgetTokensToEffort(budgetTokens int) string {
	if budgetTokens <= 0 {
		return "none"
	}
	if budgetTokens <= 1024 {
		return "low"
	}
	if budgetTokens <= 8192 {
		return "medium"
	}
	return "high"
}

func filterOpenRouterRedundantSystem(system []claudecode.ClaudeContent) []claudecode.ClaudeContent {
	if len(system) == 0 {
		return nil
	}
	filtered := make([]claudecode.ClaudeContent, 0, len(system))
	for _, item := range system {
		text := strings.TrimSpace(item.Text)
		if text == "" {
			continue
		}
		if text == claudeCodeSystemCLIKeyword || isClaudeBillingHeaderSystemText(text) {
			continue
		}
		filtered = append(filtered, item)
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

func doOpenRouterChatCompletionsRequest(c *gin.Context, info *relaycommon.RelayInfo, payload []byte) (*http.Response, error) {
	endpoint := buildOpenRouterChatCompletionsURL(info.BaseUrl)
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req = req.WithContext(c.Request.Context())
	req.Header.Set("Content-Type", "application/json")
	accept := strings.TrimSpace(c.GetHeader("Accept"))
	if accept == "" {
		if info.IsStream {
			accept = "text/event-stream"
		} else {
			accept = "application/json"
		}
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("Authorization", "Bearer "+info.ApiKey)

	httpReferer := firstNonEmptySetting(info.ChannelSetting, "http_referer", "referer", "HTTP-Referer")
	if httpReferer != "" {
		req.Header.Set("HTTP-Referer", httpReferer)
	}
	xTitle := firstNonEmptySetting(info.ChannelSetting, "x_title", "title", "X-Title")
	if xTitle != "" {
		req.Header.Set("X-Title", xTitle)
	}

	client := service.GetHttpClient()
	if info.IsStream {
		if strings.TrimSpace(info.ProxyURL) != "" {
			client = service.GetStreamingHttpClientWithProxy(info.ProxyURL)
		} else {
			client = service.GetStreamingHttpClient()
		}
	} else if strings.TrimSpace(info.ProxyURL) != "" {
		client = service.GetHttpClientWithProxy(info.ProxyURL)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func firstNonEmptySetting(m map[string]any, keys ...string) string {
	if m == nil {
		return ""
	}
	for _, key := range keys {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

func applyOpenRouterProviderPreference(openaiReq map[string]any, setting map[string]any) {
	if openaiReq == nil || setting == nil {
		return
	}
	if providerRaw, ok := setting["provider"]; ok {
		openaiReq["provider"] = providerRaw
		return
	}
	if providerRaw, ok := setting["providers"]; ok {
		switch v := providerRaw.(type) {
		case []any, map[string]any:
			openaiReq["provider"] = v
			return
		case []string:
			if len(v) > 0 {
				openaiReq["provider"] = map[string]any{"only": v}
				return
			}
		case string:
			if strings.TrimSpace(v) != "" {
				openaiReq["provider"] = map[string]any{"only": []string{strings.TrimSpace(v)}}
				return
			}
		}
	}
	if providerRaw, ok := setting["provider_only"]; ok {
		switch v := providerRaw.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				openaiReq["provider"] = map[string]any{"only": []string{strings.TrimSpace(v)}}
				return
			}
		case []string:
			if len(v) > 0 {
				openaiReq["provider"] = map[string]any{"only": v}
				return
			}
		case []any:
			if len(v) > 0 {
				openaiReq["provider"] = map[string]any{"only": v}
				return
			}
		}
	}
	if providerRaw, ok := setting["openrouter_provider"]; ok {
		switch v := providerRaw.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				openaiReq["provider"] = map[string]any{
					"only": []string{strings.TrimSpace(v)},
				}
			}
		case []string:
			if len(v) > 0 {
				openaiReq["provider"] = map[string]any{
					"only": v,
				}
			}
		case []any, map[string]any:
			openaiReq["provider"] = v
		}
	}
}

func buildOpenRouterChatCompletionsURL(baseURL string) string {
	base := strings.TrimSpace(baseURL)
	if base == "" {
		base = common.ChannelBaseURLs[common.ChannelTypeOpenRouter]
	}
	base = strings.TrimRight(base, "/")
	lowerBase := strings.ToLower(base)
	if strings.HasSuffix(lowerBase, "/chat/completions") {
		return base
	}
	if strings.HasSuffix(lowerBase, "/v1") {
		return base + "/chat/completions"
	}
	return base + "/v1/chat/completions"
}
