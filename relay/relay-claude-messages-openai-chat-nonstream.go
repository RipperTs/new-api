package relay

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"one-api/common"
	"one-api/dto"
	relaycommon "one-api/relay/common"
	"one-api/service"
	"strings"

	"github.com/gin-gonic/gin"
)

func nonStreamOpenAIChatToClaude(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, error) {
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

	textContent := strings.TrimSpace(stripOpenAIChatVisibleControlTokens(choice.Message.StringContent()))
	if textContent == "" {
		parsedContent := choice.Message.ParseContent()
		var sb strings.Builder
		for _, content := range parsedContent {
			if content.Type == dto.ContentTypeText && strings.TrimSpace(content.Text) != "" {
				sb.WriteString(content.Text)
			}
		}
		textContent = strings.TrimSpace(stripOpenAIChatVisibleControlTokens(sb.String()))
	}
	if textContent != "" {
		contentBlocks = append(contentBlocks, map[string]any{
			"type": "text",
			"text": textContent,
		})
	}

	thinkingText := stripOpenAIChatVisibleControlTokens(extractReasoningFromMessage(&choice.Message))
	rawThinkingText = stripOpenAIChatVisibleControlTokens(rawThinkingText)
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
