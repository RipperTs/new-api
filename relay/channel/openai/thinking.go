package openai

import (
	"encoding/json"
	"one-api/dto"
)

func applyThinkingPolicyToMessage(message *dto.Message, thinkingEnabled bool) {
	if message == nil {
		return
	}
	if thinkingEnabled {
		message.EnsureReasoningCompatibility()
		return
	}
	message.Reasoning = nil
	message.ReasoningContent = nil
}

func applyThinkingPolicyToDelta(delta *dto.ChatCompletionsStreamResponseChoiceDelta, thinkingEnabled bool) {
	if delta == nil {
		return
	}
	if thinkingEnabled {
		delta.EnsureReasoningCompatibility()
		return
	}
	delta.Reasoning = nil
	delta.ReasoningContent = nil
}

func sanitizeThinkingPayload(body []byte) ([]byte, error) {
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	stripThinkingFields(payload)
	return json.Marshal(payload)
}

func stripThinkingFields(payload any) {
	switch v := payload.(type) {
	case map[string]any:
		delete(v, "thinking")
		delete(v, "reasoning")
		delete(v, "reasoning_content")
		for _, sub := range v {
			stripThinkingFields(sub)
		}
	case []any:
		for _, item := range v {
			stripThinkingFields(item)
		}
	}
}
