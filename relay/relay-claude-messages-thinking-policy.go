package relay

import (
	"encoding/json"
	relaycommon "one-api/relay/common"
	"strings"
)

func shouldUseDeepSeekV4Compatibility(relayInfo *relaycommon.RelayInfo) bool {
	if relayInfo == nil {
		return false
	}
	mode, ok := relayInfo.ChannelSetting["compatibility_mode"].(string)
	return ok && strings.EqualFold(strings.TrimSpace(mode), claudeCodeDeepSeekV4CompatibilityMode)
}

func applyClaudeCodeDefaultEffort(bodyMap map[string]json.RawMessage, relayInfo *relaycommon.RelayInfo) {
	if bodyMap == nil {
		return
	}

	effort := "high"
	if relayInfo != nil {
		if configured, ok := relayInfo.ChannelSetting["default_effort"].(string); ok {
			switch strings.ToLower(strings.TrimSpace(configured)) {
			case "auto":
				return
			case "low", "high", "max":
				effort = strings.ToLower(strings.TrimSpace(configured))
			}
		}
	}

	outputConfigRaw, exists := bodyMap["output_config"]
	if !exists || !hasClaudeRequestRawField(outputConfigRaw) {
		bodyMap["output_config"] = json.RawMessage(`{"effort":"` + effort + `"}`)
		return
	}

	var outputConfig map[string]any
	if err := json.Unmarshal(outputConfigRaw, &outputConfig); err != nil || outputConfig == nil {
		return
	}
	if _, exists := outputConfig["effort"]; exists {
		return
	}
	outputConfig["effort"] = effort
	if patched, err := json.Marshal(outputConfig); err == nil {
		bodyMap["output_config"] = patched
	}
}

func ensureDeepSeekV4ThinkingOptions(bodyMap map[string]json.RawMessage) {
	if bodyMap == nil {
		return
	}
	if !hasClaudeRequestField(bodyMap, "thinking") {
		bodyMap["thinking"] = json.RawMessage(`{"type":"enabled"}`)
	}
}

func applyDeepSeekV4ThinkingCompatibility(bodyMap map[string]json.RawMessage) {
	if isClaudeThinkingDisabled(bodyMap["thinking"]) || hasIncompleteDeepSeekV4ThinkingHistory(bodyMap["messages"]) {
		disableDeepSeekV4Thinking(bodyMap)
		return
	}
	ensureDeepSeekV4ThinkingOptions(bodyMap)
}

func isClaudeThinkingDisabled(raw json.RawMessage) bool {
	if !hasClaudeRequestRawField(raw) {
		return false
	}
	var thinking map[string]any
	if err := json.Unmarshal(raw, &thinking); err != nil || thinking == nil {
		return false
	}
	thinkingType, ok := thinking["type"].(string)
	return ok && strings.EqualFold(strings.TrimSpace(thinkingType), "disabled")
}

func hasIncompleteDeepSeekV4ThinkingHistory(raw json.RawMessage) bool {
	var messages []any
	if err := json.Unmarshal(raw, &messages); err != nil {
		return false
	}
	for _, msg := range messages {
		m, ok := msg.(map[string]any)
		if !ok || m["role"] != "assistant" {
			continue
		}
		blocks, ok := m["content"].([]any)
		if !ok {
			continue
		}
		hasToolUse := false
		hasThinking := false
		for _, block := range blocks {
			b, ok := block.(map[string]any)
			if !ok {
				continue
			}
			switch b["type"] {
			case "thinking":
				hasThinking = true
			case "tool_use":
				hasToolUse = true
			}
			if hasToolUse && hasThinking {
				break
			}
		}
		if hasToolUse && !hasThinking {
			return true
		}
	}
	return false
}

func disableDeepSeekV4Thinking(bodyMap map[string]json.RawMessage) {
	if bodyMap == nil {
		return
	}
	bodyMap["thinking"] = json.RawMessage(`{"type":"disabled"}`)
	delete(bodyMap, "reasoning_effort")
	delete(bodyMap, "context_management")
	if stripped, changed := stripThinkingBlocksFromMessagesRaw(bodyMap["messages"]); changed {
		bodyMap["messages"] = stripped
	}
}

func stripThinkingBlocksFromMessagesRaw(raw json.RawMessage) (json.RawMessage, bool) {
	var messages []any
	if err := json.Unmarshal(raw, &messages); err != nil {
		return raw, false
	}
	changed := false
	for i := range messages {
		msg, ok := messages[i].(map[string]any)
		if !ok {
			continue
		}
		blocks, ok := msg["content"].([]any)
		if !ok {
			continue
		}
		filtered := make([]any, 0, len(blocks))
		for _, block := range blocks {
			b, ok := block.(map[string]any)
			if ok && b["type"] == "thinking" {
				changed = true
				continue
			}
			filtered = append(filtered, block)
		}
		if changed {
			msg["content"] = filtered
			messages[i] = msg
		}
	}
	if !changed {
		return raw, false
	}
	patched, err := json.Marshal(messages)
	if err != nil {
		return raw, false
	}
	return patched, true
}

func hasClaudeRequestField(bodyMap map[string]json.RawMessage, key string) bool {
	raw, ok := bodyMap[key]
	if !ok {
		return false
	}
	return hasClaudeRequestRawField(raw)
}

func hasClaudeRequestRawField(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null"
}
