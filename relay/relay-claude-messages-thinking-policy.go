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

func applyClaudeCodeDefaultEffort(bodyMap map[string]json.RawMessage, relayInfo *relaycommon.RelayInfo) bool {
	if bodyMap == nil || relayInfo == nil {
		return false
	}

	configured, ok := relayInfo.ChannelSetting["default_effort"].(string)
	if !ok {
		return false
	}
	effort := strings.ToLower(strings.TrimSpace(configured))
	switch effort {
	case "auto":
		return true
	case "low", "high", "max":
	default:
		return false
	}

	outputConfigRaw, exists := bodyMap["output_config"]
	if !exists || !hasClaudeRequestRawField(outputConfigRaw) {
		bodyMap["output_config"] = json.RawMessage(`{"effort":"` + effort + `"}`)
		return true
	}

	var outputConfig map[string]any
	if err := json.Unmarshal(outputConfigRaw, &outputConfig); err != nil || outputConfig == nil {
		return true
	}
	if _, exists := outputConfig["effort"]; exists {
		return true
	}
	outputConfig["effort"] = effort
	if patched, err := json.Marshal(outputConfig); err == nil {
		bodyMap["output_config"] = patched
	}
	return true
}

func ensureDeepSeekV4ThinkingOptions(bodyMap map[string]json.RawMessage, hasDefaultEffortSetting bool) {
	if bodyMap == nil {
		return
	}
	if !hasClaudeRequestField(bodyMap, "thinking") {
		bodyMap["thinking"] = json.RawMessage(`{"type":"enabled"}`)
	}
	if !hasDefaultEffortSetting {
		bodyMap["output_config"] = normalizeDeepSeekV4OutputConfig(bodyMap["output_config"])
	}
}

func applyDeepSeekV4ThinkingCompatibility(bodyMap map[string]json.RawMessage, hasDefaultEffortSetting bool) {
	if isClaudeThinkingDisabled(bodyMap["thinking"]) || hasIncompleteDeepSeekV4ThinkingHistory(bodyMap["messages"]) {
		disableDeepSeekV4Thinking(bodyMap, hasDefaultEffortSetting)
		return
	}
	ensureDeepSeekV4ThinkingOptions(bodyMap, hasDefaultEffortSetting)
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

func disableDeepSeekV4Thinking(bodyMap map[string]json.RawMessage, hasDefaultEffortSetting bool) {
	if bodyMap == nil {
		return
	}
	bodyMap["thinking"] = json.RawMessage(`{"type":"disabled"}`)
	if !hasDefaultEffortSetting {
		delete(bodyMap, "output_config")
	}
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

func normalizeDeepSeekV4OutputConfig(raw json.RawMessage) json.RawMessage {
	effort := "high"
	if hasClaudeRequestRawField(raw) {
		var outputConfig map[string]any
		if err := json.Unmarshal(raw, &outputConfig); err == nil && outputConfig != nil {
			if rawEffort, ok := outputConfig["effort"].(string); ok && strings.EqualFold(strings.TrimSpace(rawEffort), "max") {
				effort = "max"
			}
		}
	}
	return json.RawMessage(`{"effort":"` + effort + `"}`)
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
