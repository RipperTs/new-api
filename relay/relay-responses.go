package relay

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"one-api/common"
	"one-api/dto"
	relaycommon "one-api/relay/common"
	relayconstant "one-api/relay/constant"
	"one-api/service"
	"one-api/setting"
	"strings"

	"github.com/gin-gonic/gin"
)

func ResponsesHelper(c *gin.Context) (openaiErr *dto.OpenAIErrorWithStatusCode) {
	relayInfo := relaycommon.GenRelayInfo(c)

	if relayInfo.RelayMode != relayconstant.RelayModeResponses {
		return service.OpenAIErrorWrapperLocal(errors.New("invalid relay mode"), "invalid_relay_mode", http.StatusBadRequest)
	}
	if relayInfo.ChannelType != common.ChannelTypeOpenAI {
		return service.OpenAIErrorWrapperLocal(errors.New("当前渠道不支持 Responses API"), "responses_not_supported", http.StatusBadRequest)
	}

	var requestMap map[string]any
	if err := common.UnmarshalBodyReusable(c, &requestMap); err != nil {
		return service.OpenAIErrorWrapperLocal(err, "unmarshal_request_failed", http.StatusBadRequest)
	}

	modelName, _ := requestMap["model"].(string)
	if modelName == "" {
		return service.OpenAIErrorWrapperLocal(errors.New("model is required"), "model_required", http.StatusBadRequest)
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

	adaptor := GetAdaptor(relayInfo.ApiType)
	if adaptor == nil {
		return service.OpenAIErrorWrapperLocal(fmt.Errorf("invalid api type: %d", relayInfo.ApiType), "invalid_api_type", http.StatusBadRequest)
	}
	adaptor.Init(relayInfo)

	converter, ok := adaptor.(interface {
		ConvertResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request map[string]any) (any, error)
	})
	if !ok {
		return service.OpenAIErrorWrapperLocal(errors.New("当前渠道不支持 Responses API"), "responses_not_supported", http.StatusBadRequest)
	}

	convertedRequest, err := converter.ConvertResponsesRequest(c, relayInfo, requestMap)
	if err != nil {
		return service.OpenAIErrorWrapperLocal(err, "convert_request_failed", http.StatusInternalServerError)
	}
	convertedMap, ok := convertedRequest.(map[string]any)
	if ok {
		requestMap = convertedMap
	}
	modelName, _ = requestMap["model"].(string)
	if modelName != "" {
		relayInfo.UpstreamModelName = modelName
	}

	// 敏感词检测：尽量复用 messages 逻辑
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

	jsonData, err := json.Marshal(convertedRequest)
	if err != nil {
		return service.OpenAIErrorWrapperLocal(err, "json_marshal_failed", http.StatusInternalServerError)
	}

	statusCodeMappingStr := c.GetString("status_code_mapping")
	respAny, err := adaptor.DoRequest(c, relayInfo, bytes.NewBuffer(jsonData))
	if err != nil {
		return service.OpenAIErrorWrapper(err, "do_request_failed", http.StatusInternalServerError)
	}
	httpResp := respAny.(*http.Response)

	// 仅当客户端请求了 stream 时，才根据上游响应头确认是否继续走流式
	if relayInfo.IsStream {
		relayInfo.IsStream = strings.HasPrefix(httpResp.Header.Get("Content-Type"), "text/event-stream")
	}

	if httpResp.StatusCode != http.StatusOK {
		openaiErr = service.RelayErrorHandler(httpResp)
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

func getIntFromAny(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	default:
		return 0
	}
}
