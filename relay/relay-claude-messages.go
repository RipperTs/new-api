package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"one-api/common"
	"one-api/dto"
	"one-api/relay/channel/claudecode"
	relaycommon "one-api/relay/common"
	relayconstant "one-api/relay/constant"
	"one-api/service"
	"one-api/setting"
	"strings"

	"github.com/gin-gonic/gin"
)

func ClaudeMessagesHelper(c *gin.Context) (openaiErr *dto.OpenAIErrorWithStatusCode) {
	relayInfo := relaycommon.GenRelayInfo(c)
	adapter, err := resolveClaudeMessagesAdapter(relayInfo)
	if err != nil {
		writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil
	}
	return handleClaudeMessagesWithAdapter(c, relayInfo, adapter)
}

func handleClaudeMessagesWithAdapter(c *gin.Context, relayInfo *relaycommon.RelayInfo, adapter ClaudeMessagesAdapter) (openaiErr *dto.OpenAIErrorWithStatusCode) {
	if relayInfo.RelayMode != relayconstant.RelayModeClaudeMessages {
		writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", "invalid relay mode")
		return nil
	}
	if err := adapter.ValidateChannel(relayInfo); err != nil {
		writeClaudeError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
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

	rawBody, err := common.GetRequestBody(c)
	if err != nil {
		writeClaudeMaybeStreamError(c, relayInfo, http.StatusInternalServerError, "api_error", err.Error())
		return nil
	}
	bodyMap := make(map[string]json.RawMessage)
	_ = json.Unmarshal(rawBody, &bodyMap)

	if err := adapter.NormalizeRequest(c, relayInfo, &claudeReq, bodyMap); err != nil {
		writeClaudeMaybeStreamError(c, relayInfo, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil
	}

	modelMapping := c.GetString("model_mapping")
	if modelMapping != "" && modelMapping != "{}" {
		modelMap := make(map[string]string)
		if err = json.Unmarshal([]byte(modelMapping), &modelMap); err != nil {
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

	prepared, err := adapter.PrepareRequest(c, relayInfo, &claudeReq, bodyMap)
	if err != nil {
		returnPreConsumedQuota(c, relayInfo, userQuota, preConsumedQuota)
		writeClaudeMaybeStreamError(c, relayInfo, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil
	}

	resp, err := adapter.DoRequest(c, relayInfo, prepared)
	if err != nil {
		returnPreConsumedQuota(c, relayInfo, userQuota, preConsumedQuota)
		return service.OpenAIErrorWrapper(err, "do_request_failed", http.StatusInternalServerError)
	}

	statusCodeMappingStr := c.GetString("status_code_mapping")
	if resp.StatusCode != http.StatusOK {
		openaiErr = service.RelayErrorHandler(resp)
		service.ResetStatusCode(openaiErr, statusCodeMappingStr)
		returnPreConsumedQuota(c, relayInfo, userQuota, preConsumedQuota)
		return openaiErr
	}

	usage, upstreamErr, err := adapter.HandleResponse(c, resp, relayInfo)
	if upstreamErr != nil {
		returnPreConsumedQuota(c, relayInfo, userQuota, preConsumedQuota)
		if c.Writer.Written() {
			writeClaudeMaybeStreamError(c, relayInfo, upstreamErr.StatusCode, mapOpenAIErrorTypeToClaude(upstreamErr.Error.Type), upstreamErr.Error.Message)
			return nil
		}
		service.ResetStatusCode(upstreamErr, statusCodeMappingStr)
		return upstreamErr
	}
	if err != nil {
		returnPreConsumedQuota(c, relayInfo, userQuota, preConsumedQuota)
		if common.IsClientDisconnectError(err) {
			return nil
		}
		if !c.Writer.Written() {
			return service.OpenAIErrorWrapper(err, "upstream_response_failed", http.StatusInternalServerError)
		}
		writeClaudeMaybeStreamError(c, relayInfo, http.StatusInternalServerError, "api_error", err.Error())
		return nil
	}

	postConsumeQuota(c, relayInfo, claudeReq.Model, usage, ratio, preConsumedQuota, userQuota, modelRatio, groupRatio, modelPrice, getModelPriceSuccess, "")
	return nil
}

func resolveClaudeMessagesAdapter(relayInfo *relaycommon.RelayInfo) (ClaudeMessagesAdapter, error) {
	if relayInfo == nil {
		return nil, errors.New("relay info is nil")
	}
	switch relayInfo.ChannelType {
	case common.ChannelTypeClaudeCode:
		return &nativeClaudeMessagesAdapter{}, nil
	case common.ChannelTypeOpenRouter:
		return &openRouterClaudeMessagesAdapter{}, nil
	default:
		return nil, fmt.Errorf("当前渠道不支持 /v1/messages")
	}
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
	writeClaudeError(c, status, errType, message)
}

func WriteClaudeMessagesError(c *gin.Context, openaiErr *dto.OpenAIErrorWithStatusCode) {
	if openaiErr == nil {
		return
	}
	info := &relaycommon.RelayInfo{IsStream: isClaudeMessagesStreamRequest(c)}
	writeClaudeMaybeStreamError(c, info, openaiErr.StatusCode, mapOpenAIErrorTypeToClaude(openaiErr.Error.Type), openaiErr.Error.Message)
}

func isClaudeMessagesStreamRequest(c *gin.Context) bool {
	if c == nil {
		return false
	}
	var req struct {
		Stream bool `json:"stream"`
	}
	body, err := common.GetRequestBody(c)
	if err != nil {
		return false
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return false
	}
	return req.Stream
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
