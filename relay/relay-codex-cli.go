package relay

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"one-api/common"
	"one-api/dto"
	relaycommon "one-api/relay/common"
	relayconstant "one-api/relay/constant"
	"one-api/service"
	"one-api/setting"
	"strings"
)

// CodexCLIHelper 用于 /v1/codex-cli：请求/响应保持 OpenAI Responses 格式原样透传，确保 Codex CLI 正常使用。
// 仅支持 Codex 渠道类型，不影响其它渠道。
func CodexCLIHelper(c *gin.Context) (openaiErr *dto.OpenAIErrorWithStatusCode) {
	relayInfo := relaycommon.GenRelayInfo(c)
	if relayInfo.RelayMode != relayconstant.RelayModeCodexCLI {
		return service.OpenAIErrorWrapperLocal(errors.New("invalid relay mode"), "invalid_relay_mode", http.StatusBadRequest)
	}
	if relayInfo.ChannelType != common.ChannelTypeCodex {
		return service.OpenAIErrorWrapperLocal(errors.New("当前渠道不支持 Codex CLI"), "codex_cli_not_supported", http.StatusBadRequest)
	}

	codexCLILogLine(c, relayInfo, fmt.Sprintf("incoming method=%s path=%s content_type=%s accept=%s", c.Request.Method, c.Request.URL.Path, c.Request.Header.Get("Content-Type"), c.Request.Header.Get("Accept")))

	var requestMap map[string]any
	if err := common.UnmarshalBodyReusable(c, &requestMap); err != nil {
		return service.OpenAIErrorWrapperLocal(err, "unmarshal_request_failed", http.StatusBadRequest)
	}

	modelName, _ := requestMap["model"].(string)
	if modelName == "" {
		return service.OpenAIErrorWrapperLocal(errors.New("model is required"), "model_required", http.StatusBadRequest)
	}

	isCompact := strings.HasSuffix(strings.TrimSpace(c.Request.URL.Path), "/responses/compact")
	if isCompact {
		// /responses/compact 规范为非流式 JSON；同时把 RelayMode 改为 Responses，确保 DoResponse 走 JSON passthrough。
		relayInfo.RelayMode = relayconstant.RelayModeResponses
		// 避免旧客户端/调用方带入 stream/store 导致上游报 Unsupported parameter。
		delete(requestMap, "stream")
		delete(requestMap, "store")
	} else {
		// Codex CLI 默认走 SSE；如果未显式指定，强制开启，避免上游不返回完成事件
		if _, ok := requestMap["stream"]; !ok {
			requestMap["stream"] = true
		}
		// Codex 上游要求显式传 store=false（若省略会报 “Store must be set to false”）
		requestMap["store"] = false
	}

	// chatgpt backend-api 的 Codex 接口不支持部分参数（参照 CLIProxyAPI）
	base := strings.TrimRight(strings.TrimSpace(relayInfo.BaseUrl), "/")
	// 经验：API Key（秘钥）模式的上游通常也不支持这些参数（例如会报 Unsupported parameter: temperature），
	// 为了与 OAuth 行为一致并减少 400，这里统一过滤。
	isOAuth := false
	if m, ok := relayInfo.ChannelSetting["auth_mode"].(string); ok && strings.EqualFold(m, "oauth") {
		isOAuth = true
	}
	if !strings.HasSuffix(base, "/v1") || !isOAuth {
		delete(requestMap, "max_output_tokens")
		delete(requestMap, "temperature")
		delete(requestMap, "top_p")
		delete(requestMap, "top_k")
		delete(requestMap, "seed")
	}
	stream, _ := requestMap["stream"].(bool)
	relayInfo.IsStream = stream

	// map model name
	modelMapping := c.GetString("model_mapping")
	if modelMapping != "" && modelMapping != "{}" {
		modelMap := make(map[string]string)
		if err := json.Unmarshal([]byte(modelMapping), &modelMap); err != nil {
			return service.OpenAIErrorWrapperLocal(err, "unmarshal_model_mapping_failed", http.StatusInternalServerError)
		}
		if modelMap[modelName] != "" {
			modelName = modelMap[modelName]
			requestMap["model"] = modelName
		}
	}
	relayInfo.UpstreamModelName = modelName
	codexCLILog(c, relayInfo, "request", requestMap)

	// 敏感词检测：尽量复用 Responses 输入转换
	if setting.ShouldCheckPromptSensitive() {
		msgs, err := service.ResponsesRequestToMessages(requestMap)
		if err != nil {
			return service.OpenAIErrorWrapperLocal(err, "parse_responses_input_failed", http.StatusBadRequest)
		}
		if err := service.CheckSensitiveMessages(msgs); err != nil {
			return service.OpenAIErrorWrapperLocal(err, "sensitive_words_detected", http.StatusBadRequest)
		}
	}

	// prompt tokens
	promptTokens, err := service.CountTokenResponsesRequest(relayInfo, requestMap)
	if err != nil {
		return service.OpenAIErrorWrapper(err, "count_token_failed", http.StatusInternalServerError)
	}
	relayInfo.PromptTokens = promptTokens
	c.Set("prompt_tokens", promptTokens)

	// 预扣配额：按 Responses 的 max_output_tokens 估算
	groupRatio := setting.GetGroupRatio(relayInfo.Group)
	modelPrice, getModelPriceSuccess := common.GetModelPrice(modelName, false)
	modelRatio := common.GetModelRatio(modelName)
	ratio := modelRatio * groupRatio

	maxOutputTokens := getIntFromAny(requestMap["max_output_tokens"])
	preConsumedTokens := common.PreConsumedQuota
	if maxOutputTokens > 0 {
		preConsumedTokens = promptTokens + maxOutputTokens
	}

	var preConsumedQuota int
	if !getModelPriceSuccess {
		preConsumedQuota = int(float64(preConsumedTokens) * ratio)
	} else {
		preConsumedQuota = int(modelPrice * common.QuotaPerUnit * groupRatio)
	}

	preConsumedQuota, userQuota, openaiErr := preConsumeQuota(c, preConsumedQuota, relayInfo)
	if openaiErr != nil {
		return openaiErr
	}
	defer func() {
		if openaiErr != nil {
			returnPreConsumedQuota(c, relayInfo, userQuota, preConsumedQuota)
		}
	}()

	adaptor := GetAdaptor(relayInfo.ApiType)
	if adaptor == nil {
		return service.OpenAIErrorWrapperLocal(fmt.Errorf("invalid api type: %d", relayInfo.ApiType), "invalid_api_type", http.StatusBadRequest)
	}
	adaptor.Init(relayInfo)
	if u, err := adaptor.GetRequestURL(relayInfo); err == nil {
		codexCLILogLine(c, relayInfo, fmt.Sprintf("upstream_url=%s base_url=%s", u, relayInfo.BaseUrl))
	}

	jsonData, err := json.Marshal(requestMap)
	if err != nil {
		return service.OpenAIErrorWrapperLocal(err, "json_marshal_failed", http.StatusInternalServerError)
	}

	statusCodeMappingStr := c.GetString("status_code_mapping")
	respAny, err := adaptor.DoRequest(c, relayInfo, bytes.NewBuffer(jsonData))
	if err != nil {
		codexCLILogLine(c, relayInfo, fmt.Sprintf("do_request_failed=%v", err))
		return service.OpenAIErrorWrapper(err, "do_request_failed", http.StatusInternalServerError)
	}
	httpResp := respAny.(*http.Response)
	if httpResp.StatusCode != http.StatusOK {
		// 上游返回非 200：返回 OpenAIErrorWithStatusCode 让 controller 走统一的重试/自动禁用逻辑。
		// （最终失败时由 controller 负责为 Codex CLI 输出 SSE 形式的错误，避免 CLI 静默）
		raw, _ := io.ReadAll(httpResp.Body)
		_ = httpResp.Body.Close()

		errResp := dto.GeneralErrorResponse{}
		_ = json.Unmarshal(bytes.TrimSpace(raw), &errResp)

		msg := strings.TrimSpace(errResp.ToMessage())
		if msg == "" {
			msg = parseUpstreamBodyMessage(raw)
		}
		msg = AugmentUsageLimitReachedMessage(raw, errResp.Error.Type, msg)
		if msg == "" {
			msg = fmt.Sprintf("bad response status code %d", httpResp.StatusCode)
		}
		codexCLILogLine(c, relayInfo, fmt.Sprintf("upstream_status=%d content_type=%s body=%s", httpResp.StatusCode, httpResp.Header.Get("Content-Type"), truncateForLog(string(raw), 2000)))

		openaiErr = &dto.OpenAIErrorWithStatusCode{
			StatusCode: httpResp.StatusCode,
			LocalError: false,
			Error:      errResp.Error,
		}
		// 部分上游不返回标准 error 结构，这里兜底补全 message/type
		// msg 来自“尽量全”的上游响应信息（兼容非标准结构/补充 usage_limit_reached 的重置时间），优先使用它。
		if strings.TrimSpace(msg) != "" {
			openaiErr.Error.Message = msg
		}
		if strings.TrimSpace(openaiErr.Error.Type) == "" {
			openaiErr.Error.Type = "upstream_error"
		}
		service.ResetStatusCode(openaiErr, statusCodeMappingStr)
		return openaiErr
	}

	usageAny, openaiErr := adaptor.DoResponse(c, httpResp, relayInfo)
	if openaiErr != nil {
		service.ResetStatusCode(openaiErr, statusCodeMappingStr)
		return openaiErr
	}

	usage, _ := usageAny.(*dto.Usage)
	postConsumeQuota(c, relayInfo, modelName, usage, ratio, preConsumedQuota, userQuota, modelRatio, groupRatio, modelPrice, getModelPriceSuccess, "")
	return nil
}

func parseUpstreamBodyMessage(raw []byte) string {
	b := bytes.TrimSpace(raw)
	if len(b) == 0 {
		return ""
	}
	var errResp dto.GeneralErrorResponse
	if json.Unmarshal(b, &errResp) == nil {
		if strings.TrimSpace(errResp.Error.Message) != "" {
			return errResp.Error.Message
		}
		if s := strings.TrimSpace(errResp.ToMessage()); s != "" {
			return s
		}
	}
	s := strings.TrimSpace(string(b))
	if len(s) > 500 {
		s = s[:500]
	}
	return s
}

func writeCodexCLIResponseCompleted(c *gin.Context, responseID string, usage *dto.Usage) error {
	if c == nil {
		return nil
	}
	if responseID == "" {
		responseID = "resp_" + common.GetUUID()
	}
	u := usage
	if u == nil {
		u = &dto.Usage{PromptTokens: 0, CompletionTokens: 0, TotalTokens: 0}
	}
	payload := map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     responseID,
			"status": "completed",
			"usage": map[string]any{
				"input_tokens":  u.PromptTokens,
				"output_tokens": u.CompletionTokens,
				"total_tokens":  u.TotalTokens,
			},
		},
	}
	b, _ := json.Marshal(payload)
	_, err := c.Writer.Write([]byte("event: response.completed\n" + "data: " + string(b) + "\n\n"))
	if f, ok := c.Writer.(http.Flusher); ok {
		f.Flush()
	}
	return err
}

// WriteCodexCLIErrorSSE 用于 controller 最终失败时输出可被 Codex CLI 接受的 SSE 错误。
// 设计目标：避免 CLI “无输出/静默中断”，同时不影响 controller 的统一重试逻辑。
func WriteCodexCLIErrorSSE(c *gin.Context, message string) {
	if c == nil {
		return
	}
	// 如果已经开始写响应（例如上游已部分输出），则不要再二次写入。
	if c.Writer.Written() {
		return
	}

	service.SetEventStreamHeaders(c)
	c.Writer.WriteHeader(http.StatusOK)

	msg := strings.TrimSpace(message)
	// 只对“网络/转发层失败”做脱敏，业务错误（如参数错误/权限）仍原样透传给用户。
	// message 一般已经带了 (request id: xxx)，这里保留该后缀方便排查。
	if common.IsUpstreamTransportFailureMessage(msg) || strings.Contains(strings.ToLower(msg), "internal_error") {
		msg = "上游服务暂时不可用，请稍后重试" + extractRequestIDSuffix(msg)
	}
	if msg != "" {
		evt := map[string]any{"type": "response.output_text.delta", "delta": msg}
		b, _ := json.Marshal(evt)
		_, _ = c.Writer.Write([]byte("event: response.output_text.delta\n" + "data: " + string(b) + "\n\n"))
		if f, ok := c.Writer.(http.Flusher); ok {
			f.Flush()
		}
	}

	responseID := "resp_" + common.GetUUID()
	failed := map[string]any{
		"type": "response.failed",
		"response": map[string]any{
			"id":     responseID,
			"status": "failed",
		},
		"error": map[string]any{
			"message": msg,
			"type":    "upstream_error",
		},
	}
	b, _ := json.Marshal(failed)
	_, _ = c.Writer.Write([]byte("event: response.failed\n" + "data: " + string(b) + "\n\n"))
	if f, ok := c.Writer.(http.Flusher); ok {
		f.Flush()
	}

	_ = writeCodexCLIResponseCompleted(c, responseID, &dto.Usage{PromptTokens: 0, CompletionTokens: 0, TotalTokens: 0})
	_, _ = c.Writer.Write([]byte("data: [DONE]\n\n"))
	if f, ok := c.Writer.(http.Flusher); ok {
		f.Flush()
	}
}

func extractRequestIDSuffix(msg string) string {
	s := strings.TrimSpace(msg)
	if s == "" {
		return ""
	}
	// common.MessageWithRequestId 的格式："... (request id: xxx)"
	if i := strings.LastIndex(s, " (request id:"); i >= 0 {
		return s[i:]
	}
	return ""
}

func codexCLIDebugEnabled(c *gin.Context, info *relaycommon.RelayInfo) bool {
	return false
}

func codexCLILog(c *gin.Context, info *relaycommon.RelayInfo, label string, payload any) {
	return
}

func codexCLILogLine(c *gin.Context, info *relaycommon.RelayInfo, line string) {
	return
}

func truncateForLog(s string, max int) string {
	ss := strings.TrimSpace(s)
	if max <= 0 || len(ss) <= max {
		return ss
	}
	return ss[:max] + "…"
}
