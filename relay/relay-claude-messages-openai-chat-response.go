package relay

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"one-api/common"
	"one-api/constant"
	"one-api/dto"
	relaycommon "one-api/relay/common"
	"one-api/service"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
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

func streamOpenRouterChatToClaude(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *dto.OpenAIErrorWithStatusCode, error) {
	defer resp.Body.Close()

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
					"input_tokens":  usage.PromptTokens,
					"output_tokens": 0,
				},
			},
		}
		return writeClaudeStreamEvent(c, "message_start", payload)
	}
	writeTextDelta := func(text string) error {
		content := stripOpenRouterVisibleControlTokens(text)
		if strings.TrimSpace(content) == "" {
			return nil
		}
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
				return err
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
		return writeClaudeStreamEvent(c, "content_block_delta", deltaPayload)
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
			return nil, nil, errors.New("openrouter stream timeout")
		case line, ok := <-lineCh:
			if !ok {
				if err := <-scanErrCh; err != nil {
					return nil, nil, err
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
					errType, errMsg := parseOpenAIStreamError(errObj)
					openaiErr := openRouterClaudeStreamError(errType, errMsg)
					if !started {
						return nil, openaiErr, nil
					}
					payload := map[string]any{
						"type": "error",
						"error": map[string]any{
							"type":    errType,
							"message": errMsg,
						},
					}
					if writeErr := writeClaudeStreamEvent(c, "error", payload); writeErr != nil {
						return nil, nil, writeErr
					}
					return nil, nil, errors.New("openrouter upstream stream error: " + errMsg)
				}
			}

			var chunk dto.ChatCompletionsStreamResponse
			normalizedData := normalizeOpenRouterStreamChunkData(data)
			refusalTexts := extractOpenRouterRefusalTexts(normalizedData)
			if err := json.Unmarshal(normalizedData, &chunk); err != nil {
				if len(refusalTexts) > 0 {
					if err = startMessage(); err != nil {
						return nil, nil, err
					}
					for _, refusalText := range refusalTexts {
						if err = writeTextDelta(refusalText); err != nil {
							return nil, nil, err
						}
					}
				}
				continue
			}
			if strings.TrimSpace(chunk.Id) != "" {
				messageID = chunk.Id
			}
			if strings.TrimSpace(chunk.Model) != "" {
				modelName = chunk.Model
				info.UpstreamModelName = modelName
			}

			if chunk.Usage != nil {
				usage.PromptTokens = chunk.Usage.PromptTokens
				usage.CompletionTokens = chunk.Usage.CompletionTokens
				usage.TotalTokens = chunk.Usage.TotalTokens
			}

			for _, choice := range chunk.Choices {
				delta := choice.Delta

				if reasoningText := stripOpenRouterVisibleControlTokens(extractReasoningFromDelta(&delta)); strings.TrimSpace(reasoningText) != "" {
					if err := startMessage(); err != nil {
						return nil, nil, err
					}
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
							return nil, nil, err
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
						return nil, nil, err
					}
					thinkingHasDelta = true
				}

				if content := delta.GetContentString(); content != "" {
					if err := startMessage(); err != nil {
						return nil, nil, err
					}
					if err := writeTextDelta(content); err != nil {
						return nil, nil, err
					}
				}
				for _, refusalText := range refusalTexts {
					if err := startMessage(); err != nil {
						return nil, nil, err
					}
					if err := writeTextDelta(refusalText); err != nil {
						return nil, nil, err
					}
				}

				if len(delta.ToolCalls) > 0 {
					if err := startMessage(); err != nil {
						return nil, nil, err
					}
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
								return nil, nil, err
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
								return nil, nil, err
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
		return nil, nil, io.ErrUnexpectedEOF
	}
	if !started {
		if err := startMessage(); err != nil {
			return nil, nil, err
		}
	}
	if stopReason == "end_turn" && hasToolCall {
		stopReason = "tool_use"
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
			return nil, nil, err
		}
		thinkingHasSignature = true
	}

	for _, idx := range openBlockIndexes {
		stopPayload := map[string]any{
			"type":  "content_block_stop",
			"index": idx,
		}
		if err := writeClaudeStreamEvent(c, "content_block_stop", stopPayload); err != nil {
			return nil, nil, err
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
		return nil, nil, err
	}

	messageStop := map[string]any{
		"type": "message_stop",
	}
	if err := writeClaudeStreamEvent(c, "message_stop", messageStop); err != nil {
		return nil, nil, err
	}
	return usage, nil, nil
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

func extractOpenRouterRefusalTexts(data []byte) []string {
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
		refusalText := strings.TrimSpace(extractOpenRouterChunkContentText(refusalRaw))
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

func openRouterClaudeStreamError(errType, errMsg string) *dto.OpenAIErrorWithStatusCode {
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
		StatusCode: openRouterClaudeStreamErrorStatusCode(errType),
		LocalError: false,
	}
}

func openRouterClaudeStreamErrorStatusCode(errType string) int {
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

func writeClaudeStreamEvent(c *gin.Context, event string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if !c.Writer.Written() {
		service.SetEventStreamHeaders(c)
		c.Writer.WriteHeader(http.StatusOK)
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
