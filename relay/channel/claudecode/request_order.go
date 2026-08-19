package claudecode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

var requestFieldOrder = []string{"messages", "system", "tools", "metadata", "max_tokens", "stream"}

func MarshalRequestBody(bodyMap map[string]json.RawMessage, model string) ([]byte, error) {
	if bodyMap == nil {
		return nil, fmt.Errorf("bodyMap is nil")
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
