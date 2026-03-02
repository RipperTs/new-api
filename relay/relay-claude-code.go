package relay

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"one-api/common"
	"one-api/dto"
	"one-api/relay/channel/claudecode"
	relaycommon "one-api/relay/common"
	relayconstant "one-api/relay/constant"
	"one-api/service"
	"one-api/setting"
	"strconv"
	"strings"
)

func ClaudeCodeMessagesHelper(c *gin.Context) (openaiErr *dto.OpenAIErrorWithStatusCode) {
	relayInfo := relaycommon.GenRelayInfo(c)
	if relayInfo.RelayMode != relayconstant.RelayModeClaudeMessages {
		writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", "invalid relay mode")
		return nil
	}
	if relayInfo.ChannelType != common.ChannelTypeClaudeCode {
		writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", "当前渠道不支持 /v1/messages")
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

	adaptor := GetAdaptor(relayInfo.ApiType)
	if adaptor == nil {
		returnPreConsumedQuota(c, relayInfo, userQuota, preConsumedQuota)
		writeClaudeMaybeStreamError(c, relayInfo, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("invalid api type: %d", relayInfo.ApiType))
		return nil
	}
	adaptor.Init(relayInfo)

	// 注意：/v1/messages 请求体可能包含 thinking 等 beta 字段。
	// 如果这里用 struct 反序列化再序列化，会丢失未知字段，导致上游出现 thinking signature 校验错误。
	// 因此转发时尽量保留原始 JSON，仅在需要时替换 model。
	jsonData, err := common.GetRequestBody(c)
	if err != nil {
		returnPreConsumedQuota(c, relayInfo, userQuota, preConsumedQuota)
		writeClaudeMaybeStreamError(c, relayInfo, http.StatusInternalServerError, "api_error", err.Error())
		return nil
	}
	var bodyMap map[string]json.RawMessage
	if err := json.Unmarshal(jsonData, &bodyMap); err == nil {
		bodyMap["model"] = []byte(strconv.Quote(claudeReq.Model))
		if patched, err := json.Marshal(bodyMap); err == nil {
			jsonData = patched
		}
	}

	resp, err := adaptor.DoRequest(c, relayInfo, bytes.NewBuffer(jsonData))
	if err != nil {
		returnPreConsumedQuota(c, relayInfo, userQuota, preConsumedQuota)
		writeClaudeMaybeStreamError(c, relayInfo, http.StatusInternalServerError, "api_error", err.Error())
		return nil
	}

	httpResp := resp.(*http.Response)
	statusCodeMappingStr := c.GetString("status_code_mapping")
	if httpResp.StatusCode != http.StatusOK {
		openaiErr = service.RelayErrorHandler(httpResp)
		service.ResetStatusCode(openaiErr, statusCodeMappingStr)
		returnPreConsumedQuota(c, relayInfo, userQuota, preConsumedQuota)
		return openaiErr
	}

	var usage *dto.Usage
	if relayInfo.IsStream {
		usage, err = streamClaudeCodePassthrough(c, httpResp, relayInfo)
	} else {
		usage, err = nonStreamClaudeCodePassthrough(c, httpResp, relayInfo)
	}
	if err != nil {
		returnPreConsumedQuota(c, relayInfo, userQuota, preConsumedQuota)
		// 客户端中断连接（broken pipe / connection reset）不应视为渠道失败，避免误报与误禁用
		if common.IsClientDisconnectError(err) {
			return nil
		}
		writeClaudeMaybeStreamError(c, relayInfo, http.StatusInternalServerError, "api_error", err.Error())
		return nil
	}

	postConsumeQuota(c, relayInfo, claudeReq.Model, usage, ratio, preConsumedQuota, userQuota, modelRatio, groupRatio, modelPrice, getModelPriceSuccess, "")
	return nil
}

func claudeRequestToMessages(req *claudecode.ClaudeRequest) []dto.Message {
	messages := make([]dto.Message, 0, len(req.Messages)+1)
	if len(req.System) > 0 {
		var sb strings.Builder
		for _, item := range req.System {
			if strings.TrimSpace(item.Text) != "" {
				sb.WriteString(item.Text)
			}
		}
		if sb.Len() > 0 {
			msg := dto.Message{Role: "system"}
			msg.SetStringContent(sb.String())
			messages = append(messages, msg)
		}
	}
	for _, m := range req.Messages {
		content := claudeMessageText(m.Content)
		msg := dto.Message{Role: m.Role}
		if content != "" {
			msg.SetStringContent(content)
		} else {
			msg.SetStringContent("")
		}
		messages = append(messages, msg)
	}
	return messages
}

func claudeMessageText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var sb strings.Builder
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				if text, ok := m["text"].(string); ok && text != "" {
					sb.WriteString(text)
				}
			}
		}
		return sb.String()
	case map[string]any:
		if text, ok := v["text"].(string); ok {
			return text
		}
	}
	return ""
}

func streamClaudeCodePassthrough(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, error) {
	service.SetEventStreamHeaders(c)
	defer resp.Body.Close()

	usage := &dto.Usage{}
	var responseText strings.Builder
	sawAnyEvent := false

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		info.SetFirstResponseTime()
		if _, err := c.Writer.Write([]byte(line + "\n")); err != nil {
			return nil, err
		}
		if flusher, ok := c.Writer.(http.Flusher); ok {
			flusher.Flush()
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var claudeResp claudecode.ClaudeResponse
		if err := json.Unmarshal([]byte(data), &claudeResp); err != nil {
			continue
		}
		sawAnyEvent = true
		if claudeResp.Type == "message_start" && claudeResp.Message != nil {
			info.UpstreamModelName = claudeResp.Message.Model
			usage.PromptTokens = claudeResp.Message.Usage.InputTokens
		} else if claudeResp.Type == "content_block_delta" && claudeResp.Delta != nil {
			responseText.WriteString(claudeResp.Delta.Text)
		} else if claudeResp.Type == "message_delta" {
			usage.CompletionTokens = claudeResp.Usage.OutputTokens
			usage.TotalTokens = claudeResp.Usage.InputTokens + claudeResp.Usage.OutputTokens
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	// 流式响应结束但没有任何有效事件：通常是上游异常断流，避免客户端“无输出/静默”。
	if !sawAnyEvent {
		return nil, io.ErrUnexpectedEOF
	}

	if usage.PromptTokens == 0 {
		usage.PromptTokens = info.PromptTokens
	}
	if usage.CompletionTokens == 0 {
		u, _ := service.ResponseText2Usage(responseText.String(), info.UpstreamModelName, usage.PromptTokens)
		usage.CompletionTokens = u.CompletionTokens
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	return usage, nil
}

func nonStreamClaudeCodePassthrough(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	c.Writer.Header().Set("Content-Type", contentType)
	c.Writer.WriteHeader(resp.StatusCode)
	if _, err := c.Writer.Write(body); err != nil {
		return nil, err
	}

	var claudeResp claudecode.ClaudeResponse
	if err := json.Unmarshal(body, &claudeResp); err != nil {
		return nil, err
	}
	if claudeResp.Model != "" {
		info.UpstreamModelName = claudeResp.Model
	}
	usage := &dto.Usage{
		PromptTokens:     claudeResp.Usage.InputTokens,
		CompletionTokens: claudeResp.Usage.OutputTokens,
		TotalTokens:      claudeResp.Usage.InputTokens + claudeResp.Usage.OutputTokens,
	}
	if usage.PromptTokens == 0 {
		usage.PromptTokens = info.PromptTokens
	}
	return usage, nil
}

func writeClaudeError(c *gin.Context, status int, errType, message string) {
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(status)
	_ = json.NewEncoder(c.Writer).Encode(map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    errType,
			"message": message,
		},
	})
}

func writeClaudeMaybeStreamError(c *gin.Context, info *relaycommon.RelayInfo, status int, errType, message string) {
	if c == nil || c.Writer.Written() {
		return
	}
	if info != nil && info.IsStream {
		service.SetEventStreamHeaders(c)
		c.Writer.WriteHeader(http.StatusOK)
		payload := map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    errType,
				"message": message,
			},
		}
		b, _ := json.Marshal(payload)
		_, _ = c.Writer.Write([]byte("event: error\n"))
		_, _ = c.Writer.Write([]byte("data: " + string(b) + "\n\n"))
		if f, ok := c.Writer.(http.Flusher); ok {
			f.Flush()
		}
		return
	}
	writeClaudeError(c, status, errType, message)
}

func mapOpenAIErrorTypeToClaude(t string) string {
	tt := strings.TrimSpace(t)
	if tt == "" {
		return "api_error"
	}
	switch tt {
	case "invalid_request_error":
		return "invalid_request_error"
	case "authentication_error":
		return "authentication_error"
	case "permission_error":
		return "permission_error"
	case "not_found_error":
		return "not_found_error"
	case "rate_limit_error", "rate_limit_exceeded", "usage_limit_reached":
		return "rate_limit_error"
	case "overloaded_error":
		return "overloaded_error"
	default:
		return "api_error"
	}
}
