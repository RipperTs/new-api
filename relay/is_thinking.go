package relay

import (
	"encoding/json"
	"errors"
	"net/http"
	"one-api/common"
	"one-api/dto"
	relaycommon "one-api/relay/common"
	"one-api/service"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	isThinkingEnable  = "enable"
	isThinkingDisable = "disabled"
)

func applyIsThinkingOption(c *gin.Context, relayInfo *relaycommon.RelayInfo) *dto.OpenAIErrorWithStatusCode {
	if relayInfo == nil {
		return nil
	}
	relayInfo.ThinkingEnabled = true

	body, err := common.GetRequestBody(c)
	if err != nil {
		return service.OpenAIErrorWrapperLocal(err, "get_request_body_failed", http.StatusInternalServerError)
	}

	requestMap := make(map[string]any)
	if err = json.Unmarshal(body, &requestMap); err != nil {
		// 请求体是否有效由原有解析流程负责，这里不重复报错。
		return nil
	}

	rawValue, exists := requestMap["is_thinking"]
	if !exists {
		return nil
	}

	value, ok := rawValue.(string)
	if !ok {
		err = errors.New("is_thinking must be a string: enable or disabled")
		return service.OpenAIErrorWrapperLocal(err, "invalid_is_thinking", http.StatusBadRequest)
	}

	switch strings.ToLower(strings.TrimSpace(value)) {
	case isThinkingEnable:
		relayInfo.ThinkingEnabled = true
	case isThinkingDisable:
		relayInfo.ThinkingEnabled = false
	default:
		err = errors.New("is_thinking must be enable or disabled")
		return service.OpenAIErrorWrapperLocal(err, "invalid_is_thinking", http.StatusBadRequest)
	}
	return nil
}
