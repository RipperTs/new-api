package relay

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"one-api/common"
	"one-api/constant"
	"one-api/dto"
	"one-api/relay/channel/claudecode"
	relaycommon "one-api/relay/common"
	relayconstant "one-api/relay/constant"
	"one-api/service"
	"one-api/setting"
	"strconv"
	"strings"
	"time"
)

type openRouterToolBlockState struct {
	blockIndex int
	id         string
	name       string
}

var openRouterVisibleControlTokenReplacer = strings.NewReplacer(
	"<|begin_of_box|>", "",
	"<|end_of_box|>", "",
)

func ClaudeMessagesHelper(c *gin.Context) (openaiErr *dto.OpenAIErrorWithStatusCode) {
	channelType := c.GetInt("channel_type")
	switch channelType {
	case common.ChannelTypeClaudeCode:
		return ClaudeCodeMessagesHelper(c)
	case common.ChannelTypeOpenRouter:
		return OpenRouterClaudeMessagesHelper(c)
	default:
		writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", "当前渠道不支持 /v1/messages")
		return nil
	}
}

func OpenRouterClaudeMessagesHelper(c *gin.Context) (openaiErr *dto.OpenAIErrorWithStatusCode) {
	relayInfo := relaycommon.GenRelayInfo(c)
	if relayInfo.RelayMode != relayconstant.RelayModeClaudeMessages {
		writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", "invalid relay mode")
		return nil
	}
	if relayInfo.ChannelType != common.ChannelTypeOpenRouter {
		writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", "当前渠道不支持 OpenRouter /v1/messages 兼容")
		return nil
	}

	var claudeReq claudecode.ClaudeRequest
	if err := common.UnmarshalBodyReusable(c, &claudeReq); err != nil {
		writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil
	}
	if claudeReq.Model == "" {
		writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil
	}
	if len(claudeReq.Messages) == 0 {
		writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", "messages is required")
		return nil
	}
	relayInfo.IsStream = claudeReq.Stream

	var bodyMap map[string]json.RawMessage
	if rawBody, err := common.GetRequestBody(c); err == nil {
		_ = json.Unmarshal(rawBody, &bodyMap)
	}

	modelMapping := c.GetString("model_mapping")
	if modelMapping != "" && modelMapping != "{}" {
		modelMap := make(map[string]string)
		if err := json.Unmarshal([]byte(modelMapping), &modelMap); err != nil {
			writeClaudeMaybeStreamError(c, relayInfo, http.StatusInternalServerError, "api_error", err.Error())
			return nil
		}
		if modelMap[claudeReq.Model] != "" {
			claudeReq.Model = modelMap[claudeReq.Model]
		}
	}
	relayInfo.UpstreamModelName = claudeReq.Model

	openaiMessages := claudeRequestToMessages(&claudeReq)
	if setting.ShouldCheckPromptSensitive() {
		if err := service.CheckSensitiveMessages(openaiMessages); err != nil {
			writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
			return nil
		}
	}

	promptTokens, err := service.CountTokenMessages(relayInfo, openaiMessages, claudeReq.Model, claudeReq.Stream)
	if err != nil {
		writeClaudeMaybeStreamError(c, relayInfo, http.StatusInternalServerError, "api_error", err.Error())
		return nil
	}
	relayInfo.PromptTokens = promptTokens

	modelPrice, getModelPriceSuccess := common.GetModelPrice(claudeReq.Model, false)
	groupRatio := setting.GetGroupRatio(relayInfo.Group)
	var preConsumedQuota int
	var ratio float64
	var modelRatio float64
	if !getModelPriceSuccess {
		preConsumedTokens := common.PreConsumedQuota
		if claudeReq.MaxTokens > 0 {
			preConsumedTokens = promptTokens + int(claudeReq.MaxTokens)
		}
		modelRatio = common.GetModelRatio(claudeReq.Model)
		ratio = modelRatio * groupRatio
		preConsumedQuota = int(float64(preConsumedTokens) * ratio)
	} else {
		preConsumedQuota = int(modelPrice * common.QuotaPerUnit * groupRatio)
	}

	preConsumedQuota, userQuota, openaiErr := preConsumeQuota(c, preConsumedQuota, relayInfo)
	if openaiErr != nil {
		writeClaudeMaybeStreamError(c, relayInfo, openaiErr.StatusCode, mapOpenAIErrorTypeToClaude(openaiErr.Error.Type), openaiErr.Error.Message)
		return nil
	}

	openaiReqMap, err := convertClaudeMessagesToOpenRouterRequest(&claudeReq, bodyMap)
	if err != nil {
		returnPreConsumedQuota(c, relayInfo, userQuota, preConsumedQuota)
		writeClaudeMaybeStreamError(c, relayInfo, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil
	}
	applyOpenRouterProviderPreference(openaiReqMap, relayInfo.ChannelSetting)
	jsonData, err := json.Marshal(openaiReqMap)
	if err != nil {
		returnPreConsumedQuota(c, relayInfo, userQuota, preConsumedQuota)
		writeClaudeMaybeStreamError(c, relayInfo, http.StatusInternalServerError, "api_error", err.Error())
		return nil
	}

	resp, err := doOpenRouterChatCompletionsRequest(c, relayInfo, jsonData)
	if err != nil {
		returnPreConsumedQuota(c, relayInfo, userQuota, preConsumedQuota)
		writeClaudeMaybeStreamError(c, relayInfo, http.StatusInternalServerError, "api_error", err.Error())
		return nil
	}

	statusCodeMappingStr := c.GetString("status_code_mapping")
	if resp.StatusCode != http.StatusOK {
		openaiErr = service.RelayErrorHandler(resp)
		service.ResetStatusCode(openaiErr, statusCodeMappingStr)
		returnPreConsumedQuota(c, relayInfo, userQuota, preConsumedQuota)
		return openaiErr
	}

	var usage *dto.Usage
	if relayInfo.IsStream {
		usage, err = streamOpenRouterChatToClaude(c, resp, relayInfo)
	} else {
		usage, err = nonStreamOpenRouterChatToClaude(c, resp, relayInfo)
	}
	if err != nil {
		returnPreConsumedQuota(c, relayInfo, userQuota, preConsumedQuota)
		if common.IsClientDisconnectError(err) {
			return nil
		}
		writeClaudeMaybeStreamError(c, relayInfo, http.StatusInternalServerError, "api_error", err.Error())
		return nil
	}

	postConsumeQuota(c, relayInfo, claudeReq.Model, usage, ratio, preConsumedQuota, userQuota, modelRatio, groupRatio, modelPrice, getModelPriceSuccess, "")
	return nil
}

func convertClaudeMessagesToOpenRouterRequest(req *claudecode.ClaudeRequest, bodyMap map[string]json.RawMessage) (map[string]any, error) {
	if req == nil {
		return nil, errors.New("request is nil")
	}
	isClaudeModel := isClaudeModelName(req.Model)
	messages := convertClaudeMessagesToOpenAIMessages(req.Messages, isClaudeModel)
	if len(messages) == 0 {
		return nil, errors.New("messages is required")
	}

	systemText := strings.TrimSpace(extractSystemText(req.System))
	if systemText != "" {
		messages = append([]map[string]any{
			{
				"role":    "system",
				"content": systemText,
			},
		}, messages...)
	}

	openaiReq := map[string]any{
		"model":    req.Model,
		"messages": messages,
		"stream":   req.Stream,
	}
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = req.MaxTokensToSample
	}
	if maxTokens > 0 {
		openaiReq["max_tokens"] = maxTokens
	}
	if req.Temperature != nil {
		openaiReq["temperature"] = *req.Temperature
	}
	if req.TopP > 0 {
		openaiReq["top_p"] = req.TopP
	}
	if len(req.StopSequences) > 0 {
		openaiReq["stop"] = req.StopSequences
	}
	if len(req.Tools) > 0 {
		openaiReq["tools"] = convertClaudeToolsToOpenAITools(req.Tools)
	}
	if req.ToolChoice != nil {
		if toolChoice := convertClaudeToolChoiceToOpenAI(req.ToolChoice); toolChoice != nil {
			openaiReq["tool_choice"] = toolChoice
		}
	}
	if reasoning := convertClaudeThinkingToReasoning(bodyMap); reasoning != nil {
		openaiReq["reasoning"] = reasoning
	}

	// 非 Claude 模型清洗策略：
	// 1) 删除所有 cache_control（转换阶段天然移除）
	// 2) 删除 messages 内 thinking/redacted_thinking block
	// 3) 顶层 thinking 统一转为 reasoning（与 claude-code-router 行为一致）
	return openaiReq, nil
}

func convertClaudeThinkingToReasoning(bodyMap map[string]json.RawMessage) map[string]any {
	if bodyMap == nil {
		return nil
	}
	raw, ok := bodyMap["thinking"]
	if !ok || len(raw) == 0 {
		return nil
	}
	var thinkingMap map[string]any
	if err := json.Unmarshal(raw, &thinkingMap); err != nil || thinkingMap == nil {
		return nil
	}
	reasoning := map[string]any{}
	if t, ok := thinkingMap["type"].(string); ok && strings.TrimSpace(t) != "" {
		reasoning["enabled"] = strings.EqualFold(strings.TrimSpace(t), "enabled")
	}
	budgetTokens := parseNumberToInt(thinkingMap["budget_tokens"])
	if budgetTokens != 0 {
		if effort := mapBudgetTokensToEffort(budgetTokens); effort != "" {
			reasoning["effort"] = effort
		}
	}
	if len(reasoning) == 0 {
		return nil
	}
	return reasoning
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

func isClaudeModelName(model string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(model)), "claude")
}

func extractSystemText(system []claudecode.ClaudeContent) string {
	if len(system) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, item := range system {
		if strings.TrimSpace(item.Text) == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(item.Text)
	}
	return sb.String()
}

func convertClaudeToolsToOpenAITools(tools []claudecode.Tool) []map[string]any {
	result := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		item := map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  tool.InputSchema,
			},
		}
		result = append(result, item)
	}
	return result
}

func convertClaudeToolChoiceToOpenAI(toolChoice any) any {
	switch t := toolChoice.(type) {
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "auto", "none", "required":
			return strings.ToLower(strings.TrimSpace(t))
		case "any":
			return "required"
		default:
			return t
		}
	case map[string]any:
		choiceType, _ := t["type"].(string)
		switch strings.ToLower(strings.TrimSpace(choiceType)) {
		case "auto":
			return "auto"
		case "none":
			return "none"
		case "any":
			return "required"
		case "tool":
			name, _ := t["name"].(string)
			if strings.TrimSpace(name) == "" {
				return "auto"
			}
			return map[string]any{
				"type": "function",
				"function": map[string]any{
					"name": name,
				},
			}
		default:
			return t
		}
	default:
		return toolChoice
	}
}

func convertClaudeMessagesToOpenAIMessages(messages []claudecode.ClaudeMessage, isClaudeModel bool) []map[string]any {
	result := make([]map[string]any, 0, len(messages)+1)
	for _, message := range messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		switch role {
		case "user":
			result = append(result, convertClaudeUserMessageToOpenAI(message.Content, isClaudeModel)...)
		case "assistant":
			if msg := convertClaudeAssistantMessageToOpenAI(message.Content, isClaudeModel); msg != nil {
				result = append(result, msg)
			}
		default:
			text := strings.TrimSpace(claudeContentToText(message.Content))
			if text != "" {
				result = append(result, map[string]any{
					"role":    "user",
					"content": text,
				})
			}
		}
	}
	return result
}

func convertClaudeUserMessageToOpenAI(content any, isClaudeModel bool) []map[string]any {
	result := make([]map[string]any, 0, 2)
	parts := make([]map[string]any, 0)
	toolMessages := make([]map[string]any, 0, 1)

	switch v := content.(type) {
	case string:
		text := strings.TrimSpace(v)
		if text != "" {
			result = append(result, map[string]any{
				"role":    "user",
				"content": text,
			})
		}
		return result
	case []any:
		for _, item := range v {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			blockType, _ := block["type"].(string)
			switch blockType {
			case "text":
				text, _ := block["text"].(string)
				text = strings.TrimSpace(text)
				if text != "" {
					parts = append(parts, map[string]any{
						"type": "text",
						"text": text,
					})
				}
			case "image":
				if imagePart := convertClaudeImageBlockToOpenAIPart(block); imagePart != nil {
					parts = append(parts, imagePart)
				}
			case "tool_result":
				toolUseID, _ := block["tool_use_id"].(string)
				toolContent := stringifyToolResultContent(block["content"])
				if strings.TrimSpace(toolUseID) != "" {
					toolMessages = append(toolMessages, map[string]any{
						"role":         "tool",
						"tool_call_id": toolUseID,
						"content":      toolContent,
					})
				} else if toolContent != "" {
					parts = append(parts, map[string]any{
						"type": "text",
						"text": toolContent,
					})
				}
			case "thinking", "redacted_thinking":
				if isClaudeModel {
					text := extractThinkingText(block)
					if text != "" {
						parts = append(parts, map[string]any{
							"type": "text",
							"text": text,
						})
					}
				}
			}
		}
	default:
		text := strings.TrimSpace(claudeContentToText(content))
		if text != "" {
			result = append(result, map[string]any{
				"role":    "user",
				"content": text,
			})
		}
		return result
	}

	if len(parts) > 0 {
		result = append(result, map[string]any{
			"role":    "user",
			"content": parts,
		})
	}
	result = append(result, toolMessages...)
	return result
}

func convertClaudeAssistantMessageToOpenAI(content any, isClaudeModel bool) map[string]any {
	msg := map[string]any{
		"role": "assistant",
	}
	switch v := content.(type) {
	case string:
		msg["content"] = v
		return msg
	case []any:
		var textBuilder strings.Builder
		toolCalls := make([]map[string]any, 0)
		for _, item := range v {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			blockType, _ := block["type"].(string)
			switch blockType {
			case "text":
				text, _ := block["text"].(string)
				if strings.TrimSpace(text) != "" {
					textBuilder.WriteString(text)
				}
			case "tool_use":
				toolID, _ := block["id"].(string)
				toolName, _ := block["name"].(string)
				args, _ := json.Marshal(block["input"])
				if strings.TrimSpace(string(args)) == "" || string(args) == "null" {
					args = []byte("{}")
				}
				if strings.TrimSpace(toolID) == "" {
					toolID = "call_" + common.GetUUID()
				}
				toolCalls = append(toolCalls, map[string]any{
					"id":   toolID,
					"type": "function",
					"function": map[string]any{
						"name":      toolName,
						"arguments": string(args),
					},
				})
			case "thinking", "redacted_thinking":
				if isClaudeModel {
					text := extractThinkingText(block)
					if text != "" {
						textBuilder.WriteString(text)
					}
				}
			}
		}
		msg["content"] = textBuilder.String()
		if len(toolCalls) > 0 {
			msg["tool_calls"] = toolCalls
		}
		if strings.TrimSpace(textBuilder.String()) == "" && len(toolCalls) == 0 {
			return nil
		}
		return msg
	default:
		text := claudeContentToText(content)
		msg["content"] = text
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return msg
	}
}

func convertClaudeImageBlockToOpenAIPart(block map[string]any) map[string]any {
	sourceRaw, ok := block["source"].(map[string]any)
	if !ok {
		return nil
	}
	sourceType, _ := sourceRaw["type"].(string)
	var url string
	switch strings.ToLower(strings.TrimSpace(sourceType)) {
	case "base64":
		data, _ := sourceRaw["data"].(string)
		mediaType, _ := sourceRaw["media_type"].(string)
		if strings.TrimSpace(data) != "" {
			if strings.HasPrefix(data, "data:") {
				url = data
			} else {
				if strings.TrimSpace(mediaType) == "" {
					mediaType = "image/png"
				}
				url = "data:" + mediaType + ";base64," + data
			}
		}
	case "url":
		url, _ = sourceRaw["url"].(string)
	}
	if strings.TrimSpace(url) == "" {
		return nil
	}
	return map[string]any{
		"type": "image_url",
		"image_url": map[string]any{
			"url": url,
		},
	}
}

func stringifyToolResultContent(content any) string {
	switch v := content.(type) {
	case nil:
		return ""
	case string:
		return v
	case []any:
		var sb strings.Builder
		for _, item := range v {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			blockType, _ := block["type"].(string)
			if blockType == "text" {
				text, _ := block["text"].(string)
				if text != "" {
					sb.WriteString(text)
				}
			}
		}
		if sb.Len() > 0 {
			return sb.String()
		}
		b, _ := json.Marshal(v)
		return string(b)
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

func claudeContentToText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var sb strings.Builder
		for _, item := range v {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			blockType, _ := block["type"].(string)
			if blockType == "text" {
				text, _ := block["text"].(string)
				sb.WriteString(text)
			}
		}
		return sb.String()
	default:
		return ""
	}
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

func streamOpenRouterChatToClaude(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, error) {
	defer resp.Body.Close()
	service.SetEventStreamHeaders(c)
	c.Writer.WriteHeader(http.StatusOK)

	usage := &dto.Usage{}
	var responseText strings.Builder
	messageID := "msg_" + common.GetUUID()
	modelName := info.UpstreamModelName
	started := false
	stopReason := "end_turn"
	hasToolCall := false
	nextBlockIndex := 0
	openBlockIndexes := make([]int, 0, 8)
	textBlockIndex := -1
	thinkingBlockIndex := -1
	thinkingHasDelta := false
	thinkingHasSignature := false
	toolBlocks := make(map[int]*openRouterToolBlockState)
	sawAnyChunk := false

	startMessage := func() error {
		if started {
			return nil
		}
		started = true
		payload := map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":            messageID,
				"type":          "message",
				"role":          "assistant",
				"content":       make([]any, 0),
				"model":         modelName,
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage": map[string]any{
					"input_tokens":  0,
					"output_tokens": 0,
				},
			},
		}
		return writeClaudeStreamEvent(c, "message_start", payload)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	streamingTimeout := time.Duration(constant.StreamingTimeout) * time.Second
	if streamingTimeout <= 0 {
		streamingTimeout = 60 * time.Second
	}
	ticker := time.NewTicker(streamingTimeout)
	defer ticker.Stop()

	lineCh := make(chan string, 64)
	scanErrCh := make(chan error, 1)
	stopReaderCh := make(chan struct{})
	defer close(stopReaderCh)

	go func() {
		defer close(lineCh)
		for scanner.Scan() {
			line := scanner.Text()
			select {
			case lineCh <- line:
			case <-stopReaderCh:
				return
			}
		}
		scanErrCh <- scanner.Err()
	}()

streamReadLoop:
	for {
		select {
		case <-ticker.C:
			_ = resp.Body.Close()
			return nil, errors.New("openrouter stream timeout")
		case line, ok := <-lineCh:
			if !ok {
				if err := <-scanErrCh; err != nil {
					return nil, err
				}
				goto streamReadDone
			}
			info.SetFirstResponseTime()
			ticker.Reset(streamingTimeout)
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" {
				continue
			}
			if data == "[DONE]" {
				break streamReadLoop
			}
			sawAnyChunk = true

			var errorChunk map[string]any
			if err := json.Unmarshal([]byte(data), &errorChunk); err == nil {
				if errObj, ok := errorChunk["error"]; ok && errObj != nil {
					if err = startMessage(); err != nil {
						return nil, err
					}
					errType, errMsg := parseOpenAIStreamError(errObj)
					payload := map[string]any{
						"type": "error",
						"error": map[string]any{
							"type":    errType,
							"message": errMsg,
						},
					}
					if writeErr := writeClaudeStreamEvent(c, "error", payload); writeErr != nil {
						return nil, writeErr
					}
					return nil, errors.New("openrouter upstream stream error: " + errMsg)
				}
			}

			var chunk dto.ChatCompletionsStreamResponse
			normalizedData := normalizeOpenRouterStreamChunkData(data)
			if err := json.Unmarshal(normalizedData, &chunk); err != nil {
				continue
			}
			if strings.TrimSpace(chunk.Id) != "" {
				messageID = chunk.Id
			}
			if strings.TrimSpace(chunk.Model) != "" {
				modelName = chunk.Model
				info.UpstreamModelName = modelName
			}
			if err := startMessage(); err != nil {
				return nil, err
			}

			if chunk.Usage != nil {
				usage.PromptTokens = chunk.Usage.PromptTokens
				usage.CompletionTokens = chunk.Usage.CompletionTokens
				usage.TotalTokens = chunk.Usage.TotalTokens
			}

			for _, choice := range chunk.Choices {
				delta := choice.Delta

				if reasoningText := stripOpenRouterVisibleControlTokens(extractReasoningFromDelta(&delta)); strings.TrimSpace(reasoningText) != "" {
					if thinkingBlockIndex < 0 {
						thinkingBlockIndex = nextBlockIndex
						nextBlockIndex++
						openBlockIndexes = append(openBlockIndexes, thinkingBlockIndex)
						startPayload := map[string]any{
							"type":  "content_block_start",
							"index": thinkingBlockIndex,
							"content_block": map[string]any{
								"type":     "thinking",
								"thinking": "",
							},
						}
						if err := writeClaudeStreamEvent(c, "content_block_start", startPayload); err != nil {
							return nil, err
						}
					}
					deltaPayload := map[string]any{
						"type":  "content_block_delta",
						"index": thinkingBlockIndex,
						"delta": map[string]any{
							"type":     "thinking_delta",
							"thinking": reasoningText,
						},
					}
					if err := writeClaudeStreamEvent(c, "content_block_delta", deltaPayload); err != nil {
						return nil, err
					}
					thinkingHasDelta = true
				}

				if content := stripOpenRouterVisibleControlTokens(delta.GetContentString()); content != "" {
					responseText.WriteString(content)
					if textBlockIndex < 0 {
						textBlockIndex = nextBlockIndex
						nextBlockIndex++
						openBlockIndexes = append(openBlockIndexes, textBlockIndex)
						startPayload := map[string]any{
							"type":  "content_block_start",
							"index": textBlockIndex,
							"content_block": map[string]any{
								"type": "text",
								"text": "",
							},
						}
						if err := writeClaudeStreamEvent(c, "content_block_start", startPayload); err != nil {
							return nil, err
						}
					}
					deltaPayload := map[string]any{
						"type":  "content_block_delta",
						"index": textBlockIndex,
						"delta": map[string]any{
							"type": "text_delta",
							"text": content,
						},
					}
					if err := writeClaudeStreamEvent(c, "content_block_delta", deltaPayload); err != nil {
						return nil, err
					}
				}

				if len(delta.ToolCalls) > 0 {
					hasToolCall = true
					for _, toolCall := range delta.ToolCalls {
						toolCallIndex := 0
						if toolCall.Index != nil {
							toolCallIndex = *toolCall.Index
						}
						state, ok := toolBlocks[toolCallIndex]
						if !ok {
							blockIndex := nextBlockIndex
							nextBlockIndex++
							openBlockIndexes = append(openBlockIndexes, blockIndex)
							toolID := strings.TrimSpace(toolCall.ID)
							if toolID == "" {
								toolID = fmt.Sprintf("call_%s_%d", common.GetUUID(), toolCallIndex)
							}
							toolName := strings.TrimSpace(toolCall.Function.Name)
							if toolName == "" {
								toolName = fmt.Sprintf("tool_%d", toolCallIndex)
							}
							state = &openRouterToolBlockState{
								blockIndex: blockIndex,
								id:         toolID,
								name:       toolName,
							}
							toolBlocks[toolCallIndex] = state
							startPayload := map[string]any{
								"type":  "content_block_start",
								"index": state.blockIndex,
								"content_block": map[string]any{
									"type":  "tool_use",
									"id":    state.id,
									"name":  state.name,
									"input": map[string]any{},
								},
							}
							if err := writeClaudeStreamEvent(c, "content_block_start", startPayload); err != nil {
								return nil, err
							}
						}
						if strings.TrimSpace(toolCall.Function.Name) != "" {
							state.name = toolCall.Function.Name
						}
						if strings.TrimSpace(toolCall.ID) != "" {
							state.id = toolCall.ID
						}
						if strings.TrimSpace(toolCall.Function.Arguments) != "" {
							responseText.WriteString(state.name)
							responseText.WriteString(toolCall.Function.Arguments)
							deltaPayload := map[string]any{
								"type":  "content_block_delta",
								"index": state.blockIndex,
								"delta": map[string]any{
									"type":         "input_json_delta",
									"partial_json": toolCall.Function.Arguments,
								},
							}
							if err := writeClaudeStreamEvent(c, "content_block_delta", deltaPayload); err != nil {
								return nil, err
							}
						}
					}
				}

				if choice.FinishReason != nil {
					stopReason = mapOpenAIFinishReasonToClaude(*choice.FinishReason)
				}
			}
		}
	}

streamReadDone:
	if !sawAnyChunk {
		return nil, io.ErrUnexpectedEOF
	}
	if !started {
		if err := startMessage(); err != nil {
			return nil, err
		}
	}
	if stopReason == "end_turn" && hasToolCall {
		stopReason = "tool_use"
	}
	if responseText.Len() == 0 && !hasToolCall && !thinkingHasDelta {
		errPayload := map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    "api_error",
				"message": "upstream returned empty stream response",
			},
		}
		if err := writeClaudeStreamEvent(c, "error", errPayload); err != nil {
			return nil, err
		}
		return nil, errors.New("openrouter upstream returned empty stream response")
	}

	if thinkingBlockIndex >= 0 && thinkingHasDelta && !thinkingHasSignature {
		signaturePayload := map[string]any{
			"type":  "content_block_delta",
			"index": thinkingBlockIndex,
			"delta": map[string]any{
				"type":      "signature_delta",
				"signature": fmt.Sprintf("%d", common.GetTimestamp()),
			},
		}
		if err := writeClaudeStreamEvent(c, "content_block_delta", signaturePayload); err != nil {
			return nil, err
		}
		thinkingHasSignature = true
	}

	for _, idx := range openBlockIndexes {
		stopPayload := map[string]any{
			"type":  "content_block_stop",
			"index": idx,
		}
		if err := writeClaudeStreamEvent(c, "content_block_stop", stopPayload); err != nil {
			return nil, err
		}
	}

	if usage.PromptTokens == 0 {
		usage.PromptTokens = info.PromptTokens
	}
	if usage.CompletionTokens == 0 {
		u, _ := service.ResponseText2Usage(responseText.String(), info.UpstreamModelName, usage.PromptTokens)
		usage.CompletionTokens = u.CompletionTokens
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	} else if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}

	messageDelta := map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]any{
			"input_tokens":  usage.PromptTokens,
			"output_tokens": usage.CompletionTokens,
		},
	}
	if err := writeClaudeStreamEvent(c, "message_delta", messageDelta); err != nil {
		return nil, err
	}

	messageStop := map[string]any{
		"type": "message_stop",
	}
	if err := writeClaudeStreamEvent(c, "message_stop", messageStop); err != nil {
		return nil, err
	}
	return usage, nil
}

// OpenRouter 部分视觉/多模态模型会返回 delta.content 为数组或对象。
// 这里先归一成字符串，避免严格结构体反序列化失败导致 chunk 被跳过。
func normalizeOpenRouterStreamChunkData(data string) []byte {
	raw := []byte(data)
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return raw
	}
	choices, ok := root["choices"].([]any)
	if !ok || len(choices) == 0 {
		return raw
	}
	changed := false
	for _, choiceRaw := range choices {
		choice, ok := choiceRaw.(map[string]any)
		if !ok {
			continue
		}
		delta, ok := choice["delta"].(map[string]any)
		if !ok {
			continue
		}
		contentRaw, exists := delta["content"]
		if exists {
			if _, isString := contentRaw.(string); !isString {
				contentText := strings.TrimSpace(extractOpenRouterChunkContentText(contentRaw))
				if contentText == "" {
					delete(delta, "content")
				} else {
					delta["content"] = contentText
				}
				changed = true
			}
		}

		toolCallsRaw, hasToolCalls := delta["tool_calls"]
		if !hasToolCalls {
			continue
		}
		toolCalls, ok := toolCallsRaw.([]any)
		if !ok {
			continue
		}
		for _, tcRaw := range toolCalls {
			tc, ok := tcRaw.(map[string]any)
			if !ok {
				continue
			}
			if id, ok := tc["id"]; ok {
				if normalizedID, normalized := normalizeToolCallID(id); normalized {
					tc["id"] = normalizedID
					changed = true
				}
			}
			if fn, ok := tc["function"].(map[string]any); ok {
				if argsRaw, ok := fn["arguments"]; ok {
					if argsStr, normalized := normalizeToolCallArguments(argsRaw); normalized {
						fn["arguments"] = argsStr
						changed = true
					}
				}
			}
		}
	}
	if !changed {
		return raw
	}
	patched, err := json.Marshal(root)
	if err != nil {
		return raw
	}
	return patched
}

func normalizeToolCallID(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, false
	case float64:
		return strconv.FormatInt(int64(t), 10), true
	case float32:
		return strconv.FormatInt(int64(t), 10), true
	case int:
		return strconv.Itoa(t), true
	case int8:
		return strconv.FormatInt(int64(t), 10), true
	case int16:
		return strconv.FormatInt(int64(t), 10), true
	case int32:
		return strconv.FormatInt(int64(t), 10), true
	case int64:
		return strconv.FormatInt(t, 10), true
	case uint:
		return strconv.FormatUint(uint64(t), 10), true
	case uint8:
		return strconv.FormatUint(uint64(t), 10), true
	case uint16:
		return strconv.FormatUint(uint64(t), 10), true
	case uint32:
		return strconv.FormatUint(uint64(t), 10), true
	case uint64:
		return strconv.FormatUint(t, 10), true
	case json.Number:
		return t.String(), true
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return "", false
		}
		s := strings.Trim(string(b), "\"")
		if strings.TrimSpace(s) == "" {
			return "", false
		}
		return s, true
	}
}

func normalizeToolCallArguments(v any) (string, bool) {
	if s, ok := v.(string); ok {
		return s, false
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", false
	}
	return string(b), true
}

func extractOpenRouterChunkContentText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var sb strings.Builder
		for _, item := range v {
			text := extractOpenRouterChunkContentText(item)
			if strings.TrimSpace(text) != "" {
				sb.WriteString(text)
			}
		}
		return sb.String()
	case map[string]any:
		for _, key := range []string{"text", "content", "value", "output_text"} {
			if s, ok := v[key].(string); ok && strings.TrimSpace(s) != "" {
				return s
			}
		}
		var sb strings.Builder
		for _, sub := range v {
			text := extractOpenRouterChunkContentText(sub)
			if strings.TrimSpace(text) != "" {
				sb.WriteString(text)
			}
		}
		return sb.String()
	default:
		return ""
	}
}

func parseOpenAIStreamError(errObj any) (string, string) {
	errType := "api_error"
	errMsg := "upstream error"
	switch v := errObj.(type) {
	case map[string]any:
		if t, ok := v["type"].(string); ok && strings.TrimSpace(t) != "" {
			errType = mapOpenAIErrorTypeToClaude(t)
		}
		if m, ok := v["message"].(string); ok && strings.TrimSpace(m) != "" {
			errMsg = m
		}
		if nestedType, nestedMsg, ok := parseNestedProviderError(errMsg); ok {
			if strings.TrimSpace(nestedType) != "" {
				errType = mapOpenAIErrorTypeToClaude(nestedType)
			}
			if strings.TrimSpace(nestedMsg) != "" {
				errMsg = nestedMsg
			}
		}
	case string:
		if strings.TrimSpace(v) != "" {
			errMsg = v
		}
		if nestedType, nestedMsg, ok := parseNestedProviderError(errMsg); ok {
			if strings.TrimSpace(nestedType) != "" {
				errType = mapOpenAIErrorTypeToClaude(nestedType)
			}
			if strings.TrimSpace(nestedMsg) != "" {
				errMsg = nestedMsg
			}
		}
	}
	return errType, errMsg
}

func parseNestedProviderError(raw string) (string, string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", "", false
	}
	if !strings.HasPrefix(s, "{") || !strings.HasSuffix(s, "}") {
		return "", "", false
	}
	var nested map[string]any
	if err := json.Unmarshal([]byte(s), &nested); err != nil {
		return "", "", false
	}
	nestedType, _ := nested["type"].(string)
	nestedMsg, _ := nested["message"].(string)
	httpStatus := parseNumberToInt(nested["httpStatus"])

	compactType := strings.TrimSpace(nestedType)
	if idx := strings.LastIndex(compactType, "."); idx >= 0 && idx < len(compactType)-1 {
		compactType = compactType[idx+1:]
	}
	finalMsg := strings.TrimSpace(nestedMsg)
	if finalMsg != "" && compactType != "" && httpStatus > 0 {
		finalMsg = fmt.Sprintf("%s (%d): %s", compactType, httpStatus, finalMsg)
	} else if finalMsg != "" && compactType != "" {
		finalMsg = fmt.Sprintf("%s: %s", compactType, finalMsg)
	} else if finalMsg == "" {
		if compactType != "" && httpStatus > 0 {
			finalMsg = fmt.Sprintf("%s (%d)", compactType, httpStatus)
		} else if compactType != "" {
			finalMsg = compactType
		} else if httpStatus > 0 {
			finalMsg = fmt.Sprintf("httpStatus=%d", httpStatus)
		}
	}
	if strings.TrimSpace(finalMsg) == "" && strings.TrimSpace(nestedType) == "" {
		return "", "", false
	}
	return strings.TrimSpace(nestedType), strings.TrimSpace(finalMsg), true
}

func writeClaudeStreamEvent(c *gin.Context, event string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if event != "" {
		if _, err = c.Writer.Write([]byte("event: " + event + "\n")); err != nil {
			return err
		}
	}
	if _, err = c.Writer.Write([]byte("data: " + string(data) + "\n\n")); err != nil {
		return err
	}
	if f, ok := c.Writer.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}

func extractReasoningFromDelta(delta *dto.ChatCompletionsStreamResponseChoiceDelta) string {
	if delta == nil {
		return ""
	}
	if s := extractReasoningRaw(delta.Reasoning); s != "" {
		return s
	}
	return extractReasoningRaw(delta.ReasoningContent)
}

func extractReasoningFromMessage(msg *dto.Message) string {
	if msg == nil {
		return ""
	}
	if s := extractReasoningRaw(msg.Reasoning); s != "" {
		return s
	}
	return extractReasoningRaw(msg.ReasoningContent)
}

func extractReasoningRaw(raw *json.RawMessage) string {
	if raw == nil || len(*raw) == 0 {
		return ""
	}
	var str string
	if err := json.Unmarshal(*raw, &str); err == nil {
		return strings.TrimSpace(str)
	}
	var obj map[string]any
	if err := json.Unmarshal(*raw, &obj); err == nil {
		for _, key := range []string{"content", "text", "reasoning", "thinking"} {
			if v, ok := obj[key].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
	}
	return ""
}

func nonStreamOpenRouterChatToClaude(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var openaiResp dto.OpenAITextResponse
	if err := json.Unmarshal(body, &openaiResp); err != nil {
		return nil, err
	}
	if strings.TrimSpace(openaiResp.Model) != "" {
		info.UpstreamModelName = openaiResp.Model
	}
	if strings.TrimSpace(openaiResp.Id) == "" {
		openaiResp.Id = "msg_" + common.GetUUID()
	}

	if len(openaiResp.Choices) == 0 {
		return nil, errors.New("empty choices in upstream response")
	}
	choice := openaiResp.Choices[0]
	contentBlocks := make([]map[string]any, 0, 4)
	rawThinkingText, rawThinkingSignature := extractThinkingFromOpenAIResponseRaw(body)

	textContent := strings.TrimSpace(stripOpenRouterVisibleControlTokens(choice.Message.StringContent()))
	if textContent == "" {
		parsedContent := choice.Message.ParseContent()
		var sb strings.Builder
		for _, content := range parsedContent {
			if content.Type == dto.ContentTypeText && strings.TrimSpace(content.Text) != "" {
				sb.WriteString(content.Text)
			}
		}
		textContent = strings.TrimSpace(stripOpenRouterVisibleControlTokens(sb.String()))
	}
	if textContent != "" {
		contentBlocks = append(contentBlocks, map[string]any{
			"type": "text",
			"text": textContent,
		})
	}

	thinkingText := stripOpenRouterVisibleControlTokens(extractReasoningFromMessage(&choice.Message))
	rawThinkingText = stripOpenRouterVisibleControlTokens(rawThinkingText)
	if thinkingText != "" || rawThinkingText != "" {
		if thinkingText == "" {
			thinkingText = rawThinkingText
		}
		signature := rawThinkingSignature
		if signature == "" {
			signature = "skip_thought_signature_validator"
		}
		contentBlocks = append(contentBlocks, map[string]any{
			"type":      "thinking",
			"thinking":  thinkingText,
			"signature": signature,
		})
	}

	toolCalls := choice.Message.ParseToolCalls()
	for _, toolCall := range toolCalls {
		inputObj := make(map[string]any)
		argsStr := strings.TrimSpace(toolCall.Function.Arguments)
		if argsStr == "" {
			inputObj = map[string]any{}
		} else if err := json.Unmarshal([]byte(argsStr), &inputObj); err != nil {
			inputObj = map[string]any{
				"text": argsStr,
			}
		}
		toolID := strings.TrimSpace(toolCall.ID)
		if toolID == "" {
			toolID = "call_" + common.GetUUID()
		}
		contentBlocks = append(contentBlocks, map[string]any{
			"type":  "tool_use",
			"id":    toolID,
			"name":  toolCall.Function.Name,
			"input": inputObj,
		})
	}

	anthropicResp := map[string]any{
		"id":            openaiResp.Id,
		"type":          "message",
		"role":          "assistant",
		"model":         common.GetStringIfEmpty(openaiResp.Model, info.UpstreamModelName),
		"content":       contentBlocks,
		"stop_reason":   mapOpenAIFinishReasonToClaude(choice.FinishReason),
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  openaiResp.Usage.PromptTokens,
			"output_tokens": openaiResp.Usage.CompletionTokens,
		},
	}
	if len(contentBlocks) == 0 {
		anthropicResp["content"] = []map[string]any{
			{
				"type": "text",
				"text": "",
			},
		}
	}

	if usageMap, ok := anthropicResp["usage"].(map[string]any); ok {
		if cached := openaiResp.Usage.PromptTokensDetails.CachedTokens; cached > 0 {
			usageMap["cache_read_input_tokens"] = cached
		}
	}

	responseBody, err := json.Marshal(anthropicResp)
	if err != nil {
		return nil, err
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(http.StatusOK)
	if _, err = c.Writer.Write(responseBody); err != nil {
		return nil, err
	}

	usage := &dto.Usage{
		PromptTokens:     openaiResp.Usage.PromptTokens,
		CompletionTokens: openaiResp.Usage.CompletionTokens,
		TotalTokens:      openaiResp.Usage.TotalTokens,
	}
	if usage.PromptTokens == 0 {
		usage.PromptTokens = info.PromptTokens
	}
	if usage.CompletionTokens == 0 {
		u, _ := service.ResponseText2Usage(textContent, info.UpstreamModelName, usage.PromptTokens)
		usage.CompletionTokens = u.CompletionTokens
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	} else if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	return usage, nil
}

func extractThinkingFromOpenAIResponseRaw(body []byte) (string, string) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return "", ""
	}
	choices, ok := root["choices"].([]any)
	if !ok || len(choices) == 0 {
		return "", ""
	}
	firstChoice, ok := choices[0].(map[string]any)
	if !ok {
		return "", ""
	}
	message, ok := firstChoice["message"].(map[string]any)
	if !ok {
		return "", ""
	}
	if thinkingMap, ok := message["thinking"].(map[string]any); ok {
		text, _ := thinkingMap["content"].(string)
		signature, _ := thinkingMap["signature"].(string)
		return strings.TrimSpace(text), strings.TrimSpace(signature)
	}
	if reasoningContent, ok := message["reasoning_content"].(string); ok && strings.TrimSpace(reasoningContent) != "" {
		return strings.TrimSpace(reasoningContent), ""
	}
	if reasoningText, ok := message["reasoning"].(string); ok && strings.TrimSpace(reasoningText) != "" {
		return strings.TrimSpace(reasoningText), ""
	}
	return "", ""
}

func mapOpenAIFinishReasonToClaude(reason string) string {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "stop":
		return "end_turn"
	case "length", "max_tokens":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	case "content_filter":
		return "stop_sequence"
	default:
		return "end_turn"
	}
}

func stripOpenRouterVisibleControlTokens(s string) string {
	if s == "" {
		return ""
	}
	return openRouterVisibleControlTokenReplacer.Replace(s)
}
