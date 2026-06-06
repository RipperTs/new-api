package relay

import (
	"encoding/json"
	"errors"
	"one-api/common"
	"one-api/relay/channel/claudecode"
	"strings"
)

func buildOpenAIChatRequestFromClaudeMessages(req *claudecode.ClaudeRequest, reasoning map[string]any) (map[string]any, error) {
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
	if reasoning != nil {
		openaiReq["reasoning"] = reasoning
	}

	return openaiReq, nil
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
