package relay

import (
	"encoding/json"
	"fmt"
	"net/http"
	"one-api/dto"
	"strconv"
	"strings"
)

var openAIChatVisibleControlTokenReplacer = strings.NewReplacer(
	"<|begin_of_box|>", "",
	"<|end_of_box|>", "",
)

// 部分 OpenAI Chat 兼容视觉/多模态模型会返回 delta.content 为数组或对象。
// 这里先归一成字符串，避免严格结构体反序列化失败导致 chunk 被跳过。
func normalizeOpenAIChatStreamChunkData(data string) []byte {
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
				contentText := strings.TrimSpace(extractOpenAIChatChunkContentText(contentRaw))
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

func extractOpenAIChatChunkContentText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var sb strings.Builder
		for _, item := range v {
			text := extractOpenAIChatChunkContentText(item)
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
			text := extractOpenAIChatChunkContentText(sub)
			if strings.TrimSpace(text) != "" {
				sb.WriteString(text)
			}
		}
		return sb.String()
	default:
		return ""
	}
}

func extractOpenAIChatRefusalTexts(data []byte) []string {
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil || root == nil {
		return nil
	}
	choices, ok := root["choices"].([]any)
	if !ok || len(choices) == 0 {
		return nil
	}
	result := make([]string, 0, 2)
	seen := make(map[string]struct{}, 2)
	for _, choiceRaw := range choices {
		choice, ok := choiceRaw.(map[string]any)
		if !ok {
			continue
		}
		delta, ok := choice["delta"].(map[string]any)
		if !ok {
			continue
		}
		refusalRaw, ok := delta["refusal"]
		if !ok {
			continue
		}
		refusalText := strings.TrimSpace(extractOpenAIChatChunkContentText(refusalRaw))
		if refusalText == "" {
			continue
		}
		if _, exists := seen[refusalText]; exists {
			continue
		}
		seen[refusalText] = struct{}{}
		result = append(result, refusalText)
	}
	return result
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

func openAIChatClaudeStreamError(errType, errMsg string) *dto.OpenAIErrorWithStatusCode {
	errType = strings.TrimSpace(errType)
	if errType == "" {
		errType = "api_error"
	}
	errMsg = strings.TrimSpace(errMsg)
	if errMsg == "" {
		errMsg = "upstream error"
	}
	return &dto.OpenAIErrorWithStatusCode{
		Error: dto.OpenAIError{
			Message: errMsg,
			Type:    errType,
			Code:    errType,
		},
		StatusCode: openAIChatClaudeStreamErrorStatusCode(errType),
		LocalError: false,
	}
}

func openAIChatClaudeStreamErrorStatusCode(errType string) int {
	switch strings.ToLower(strings.TrimSpace(errType)) {
	case "rate_limit_error", "rate_limit_exceeded", "usage_limit_reached", "overloaded_error":
		return http.StatusTooManyRequests
	case "invalid_request_error":
		return http.StatusBadRequest
	case "authentication_error":
		return http.StatusUnauthorized
	case "permission_error":
		return http.StatusForbidden
	case "not_found_error":
		return http.StatusNotFound
	default:
		return http.StatusBadGateway
	}
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

func stripOpenAIChatVisibleControlTokens(s string) string {
	if s == "" {
		return ""
	}
	return openAIChatVisibleControlTokenReplacer.Replace(s)
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
