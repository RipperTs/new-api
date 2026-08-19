package claudecode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

var requestFieldOrder = []string{"messages", "system", "tools", "metadata", "max_tokens", "thinking", "output_config", "stream"}

func reorderMessages(raw json.RawMessage) (json.RawMessage, error) {
	var messages []json.RawMessage
	if err := json.Unmarshal(raw, &messages); err != nil {
		return nil, fmt.Errorf("unmarshal Claude Code request messages failed: %w", err)
	}

	var buf bytes.Buffer
	buf.Grow(len(raw))
	buf.WriteByte('[')
	for i, messageRaw := range messages {
		var messageMap map[string]json.RawMessage
		if err := json.Unmarshal(messageRaw, &messageMap); err != nil {
			return nil, fmt.Errorf("unmarshal Claude Code request message %d failed: %w", i, err)
		}

		remainingKeys := make([]string, 0, len(messageMap))
		for key, value := range messageMap {
			if key == "role" || key == "content" || len(value) == 0 {
				continue
			}
			remainingKeys = append(remainingKeys, key)
		}
		sort.Strings(remainingKeys)

		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteByte('{')
		fieldWritten := false
		writeField := func(key string) {
			value, ok := messageMap[key]
			if !ok || len(value) == 0 {
				return
			}
			if fieldWritten {
				buf.WriteByte(',')
			}
			buf.WriteString(strconv.Quote(key))
			buf.WriteByte(':')
			buf.Write(value)
			fieldWritten = true
		}
		writeField("role")
		writeField("content")
		for _, key := range remainingKeys {
			writeField(key)
		}
		buf.WriteByte('}')
	}
	buf.WriteByte(']')
	return buf.Bytes(), nil
}

func MarshalRequestBody(bodyMap map[string]json.RawMessage, model string) ([]byte, error) {
	if bodyMap == nil {
		return nil, fmt.Errorf("bodyMap is nil")
	}

	messagesRaw := bodyMap["messages"]
	if len(messagesRaw) > 0 {
		var err error
		messagesRaw, err = reorderMessages(messagesRaw)
		if err != nil {
			return nil, err
		}
	}

	remainingKeys := make([]string, 0, len(bodyMap))
	for key, raw := range bodyMap {
		ordered := false
		for _, orderedKey := range requestFieldOrder {
			if key == orderedKey {
				ordered = true
				break
			}
		}
		if key == "model" || len(raw) == 0 || ordered {
			continue
		}
		remainingKeys = append(remainingKeys, key)
	}
	sort.Strings(remainingKeys)

	var buf bytes.Buffer
	estimatedSize := len(model) + 12
	for key, raw := range bodyMap {
		if key != "model" && len(raw) > 0 {
			estimatedSize += len(key) + len(raw) + 4
		}
	}
	buf.Grow(estimatedSize)
	buf.WriteByte('{')
	buf.WriteString(`"model":`)
	buf.WriteString(strconv.Quote(model))
	writeField := func(key string) {
		raw, ok := bodyMap[key]
		if key == "messages" && len(messagesRaw) > 0 {
			raw = messagesRaw
		}
		if !ok || len(raw) == 0 {
			return
		}
		buf.WriteByte(',')
		buf.WriteString(strconv.Quote(key))
		buf.WriteByte(':')
		buf.Write(raw)
	}
	for _, key := range requestFieldOrder {
		writeField(key)
	}
	for _, key := range remainingKeys {
		writeField(key)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func ReorderRequestBody(body []byte) ([]byte, error) {
	var bodyMap map[string]json.RawMessage
	if err := json.Unmarshal(body, &bodyMap); err != nil {
		return nil, fmt.Errorf("unmarshal Claude Code request body failed: %w", err)
	}

	var model string
	if err := json.Unmarshal(bodyMap["model"], &model); err != nil {
		return nil, fmt.Errorf("unmarshal Claude Code request model failed: %w", err)
	}
	return MarshalRequestBody(bodyMap, model)
}
