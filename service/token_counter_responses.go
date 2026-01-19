package service

import (
	"encoding/json"
	"fmt"
	"one-api/dto"
	relaycommon "one-api/relay/common"
)

func CountTokenResponsesRequest(info *relaycommon.RelayInfo, request map[string]any) (int, error) {
	if request == nil {
		return 0, fmt.Errorf("request is nil")
	}
	model, _ := request["model"].(string)
	if model == "" {
		return 0, fmt.Errorf("model is required")
	}
	stream, _ := request["stream"].(bool)

	messages, err := ResponsesRequestToMessages(request)
	if err != nil {
		return 0, err
	}

	tokenNum, err := CountTokenMessages(info, messages, model, stream)
	if err != nil {
		return 0, err
	}

	if tools := request["tools"]; tools != nil {
		toolsData, _ := json.Marshal(tools)
		var openaiTools []dto.OpenAITools
		if err := json.Unmarshal(toolsData, &openaiTools); err == nil {
			countStr := ""
			for _, tool := range openaiTools {
				countStr += tool.Function.Name
				if tool.Function.Description != "" {
					countStr += tool.Function.Description
				}
				if tool.Function.Parameters != nil {
					countStr += fmt.Sprintf("%v", tool.Function.Parameters)
				}
			}
			toolTokens, err := CountTokenInput(countStr, model)
			if err != nil {
				return 0, err
			}
			tokenNum += 8
			tokenNum += toolTokens
		}
	}

	return tokenNum, nil
}

func ResponsesRequestToMessages(request map[string]any) ([]dto.Message, error) {
	if request == nil {
		return nil, fmt.Errorf("request is nil")
	}

	messages := make([]dto.Message, 0, 8)

	if instructions, ok := request["instructions"].(string); ok && instructions != "" {
		m := dto.Message{Role: "system"}
		m.SetStringContent(instructions)
		messages = append(messages, m)
	}

	input, ok := request["input"]
	if !ok || input == nil {
		return messages, nil
	}

	switch v := input.(type) {
	case string:
		m := dto.Message{Role: "user"}
		m.SetStringContent(v)
		messages = append(messages, m)
		return messages, nil
	case []any:
		for _, item := range v {
			itemMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			t, _ := itemMap["type"].(string)
			switch t {
			case "message":
				m, ok := buildChatMessageFromResponsesMessageItem(itemMap)
				if ok {
					messages = append(messages, m)
				}
			case "function_call":
				name, _ := itemMap["name"].(string)
				args, _ := itemMap["arguments"].(string)
				if name == "" && args == "" {
					continue
				}
				m := dto.Message{Role: "assistant"}
				m.SetStringContent(name + args)
				messages = append(messages, m)
			case "function_call_output":
				output := itemMap["output"]
				text := responsesContentToText(output)
				if text == "" {
					continue
				}
				m := dto.Message{Role: "tool"}
				m.SetStringContent(text)
				messages = append(messages, m)
			}
		}
		return messages, nil
	default:
		return messages, nil
	}
}

func buildChatMessageFromResponsesMessageItem(item map[string]any) (dto.Message, bool) {
	role, _ := item["role"].(string)
	if role == "" {
		return dto.Message{}, false
	}
	content, ok := item["content"]
	if !ok || content == nil {
		return dto.Message{}, false
	}

	msg := dto.Message{Role: role}
	switch c := content.(type) {
	case string:
		msg.SetStringContent(c)
		return msg, true
	case []any:
		parts := make([]map[string]any, 0, len(c))
		for _, raw := range c {
			part, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			pt, _ := part["type"].(string)
			switch pt {
			case "input_text":
				text, _ := part["text"].(string)
				if text == "" {
					continue
				}
				parts = append(parts, map[string]any{
					"type": "text",
					"text": text,
				})
			case "input_image":
				imageURL, _ := part["image_url"].(string)
				if imageURL == "" {
					continue
				}
				detail, _ := part["detail"].(string)
				if detail == "" {
					detail = "auto"
				}
				parts = append(parts, map[string]any{
					"type": "image_url",
					"image_url": map[string]any{
						"url":    imageURL,
						"detail": detail,
					},
				})
			}
		}
		if len(parts) == 0 {
			return dto.Message{}, false
		}
		b, _ := json.Marshal(parts)
		msg.Content = b
		return msg, true
	default:
		return dto.Message{}, false
	}
}

func responsesContentToText(output any) string {
	switch v := output.(type) {
	case string:
		return v
	case []any:
		out := ""
		for _, raw := range v {
			part, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			pt, _ := part["type"].(string)
			if pt == "input_text" {
				if text, ok := part["text"].(string); ok {
					out += text
				}
			}
		}
		return out
	default:
		return ""
	}
}
