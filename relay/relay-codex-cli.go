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
	"os"
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

	// Codex CLI 默认走 SSE；如果未显式指定，强制开启，避免上游不返回完成事件
	if _, ok := requestMap["stream"]; !ok {
		requestMap["stream"] = true
	}
	// Codex 上游要求显式传 store=false（若省略会报 “Store must be set to false”）
	requestMap["store"] = false

	// chatgpt backend-api 的 Codex 接口不支持部分参数（参照 CLIProxyAPI）
	base := strings.TrimRight(strings.TrimSpace(relayInfo.BaseUrl), "/")
	if !strings.HasSuffix(base, "/v1") {
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
		// Codex CLI 只认 SSE（并且要求 response.completed）。这里将上游错误包成 SSE 返回，避免 CLI 误报“stream closed”。
		raw, _ := io.ReadAll(httpResp.Body)
		_ = httpResp.Body.Close()

		msg := parseUpstreamBodyMessage(raw)
		if msg == "" {
			msg = fmt.Sprintf("bad response status code %d", httpResp.StatusCode)
		}
		codexCLILogLine(c, relayInfo, fmt.Sprintf("upstream_status=%d content_type=%s body=%s", httpResp.StatusCode, httpResp.Header.Get("Content-Type"), truncateForLog(string(raw), 2000)))
		service.SetEventStreamHeaders(c)
		c.Writer.WriteHeader(http.StatusOK)

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
		_, _ = c.Writer.Write([]byte("data: " + string(b) + "\n\n"))
		if fl, ok := c.Writer.(http.Flusher); ok {
			fl.Flush()
		}

		u, _ := service.ResponseText2Usage("", relayInfo.UpstreamModelName, relayInfo.PromptTokens)
		_ = writeCodexCLIResponseCompleted(c, responseID, u)
		_, _ = c.Writer.Write([]byte("data: [DONE]\n\n"))
		if fl, ok := c.Writer.(http.Flusher); ok {
			fl.Flush()
		}
		// 视为已处理响应：不再向上返回 openaiErr，避免 controller 再写 JSON
		// 手动返还预扣额度
		returnPreConsumedQuota(c, relayInfo, userQuota, preConsumedQuota)
		return nil
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
	_, err := c.Writer.Write([]byte("data: " + string(b) + "\n\n"))
	if f, ok := c.Writer.(http.Flusher); ok {
		f.Flush()
	}
	return err
}

func codexCLIDebugEnabled(c *gin.Context, info *relaycommon.RelayInfo) bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("CODEX_CLI_DEBUG")), "true") || common.DebugEnabled {
		return true
	}
	if info != nil && info.ChannelSetting != nil {
		if v, ok := info.ChannelSetting["codex_cli_debug"]; ok {
			switch vv := v.(type) {
			case bool:
				return vv
			case string:
				return strings.EqualFold(strings.TrimSpace(vv), "true")
			}
		}
	}
	return false
}

func codexCLILog(c *gin.Context, info *relaycommon.RelayInfo, label string, payload any) {
	if !codexCLIDebugEnabled(c, info) {
		return
	}
	b, _ := json.Marshal(payload)
	codexCLILogLine(c, info, fmt.Sprintf("%s=%s", label, truncateForLog(string(b), 4000)))
}

func codexCLILogLine(c *gin.Context, info *relaycommon.RelayInfo, line string) {
	if !codexCLIDebugEnabled(c, info) {
		return
	}
	reqID := ""
	if c != nil {
		reqID = c.GetString(common.RequestIdKey)
	}
	chID := 0
	if info != nil {
		chID = info.ChannelId
	}
	common.SysLog(fmt.Sprintf("[codex-cli] reqid=%s channel_id=%d %s", reqID, chID, truncateForLog(line, 4000)))
}

func truncateForLog(s string, max int) string {
	ss := strings.TrimSpace(s)
	if max <= 0 || len(ss) <= max {
		return ss
	}
	return ss[:max] + "…"
}
