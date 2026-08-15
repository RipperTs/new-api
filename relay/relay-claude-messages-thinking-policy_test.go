package relay

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"one-api/relay/channel/claudecode"
	relaycommon "one-api/relay/common"

	"github.com/gin-gonic/gin"
)

func TestDeepSeekV4CompatibilityDisablesThinkingAndPreservesOutputConfig(t *testing.T) {
	bodyMap := map[string]json.RawMessage{
		"thinking":           json.RawMessage(`{"type":"disabled"}`),
		"output_config":      json.RawMessage(`{"effort":"max"}`),
		"reasoning_effort":   json.RawMessage(`"high"`),
		"context_management": json.RawMessage(`{"edits":true}`),
		"messages": json.RawMessage(`[
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"secret","signature":"sig"},
				{"type":"text","text":"visible"}
			]}
		]`),
	}

	applyDeepSeekV4ThinkingCompatibility(bodyMap, true)

	if string(bodyMap["thinking"]) != `{"type":"disabled"}` {
		t.Fatalf("unexpected thinking: %s", bodyMap["thinking"])
	}
	if string(bodyMap["output_config"]) != `{"effort":"max"}` {
		t.Fatalf("output_config should be preserved: %s", bodyMap["output_config"])
	}
	for _, key := range []string{"reasoning_effort", "context_management"} {
		if _, ok := bodyMap[key]; ok {
			t.Fatalf("expected %s to be removed", key)
		}
	}
	if raw := string(bodyMap["messages"]); raw == "" || jsonContains(raw, `"type":"thinking"`) {
		t.Fatalf("thinking block should be stripped from messages: %s", raw)
	}
}

func TestPrepareClaudeCodeMessagesRequestSyncsDeepSeekMessagesToParsedRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = &http.Request{Header: make(http.Header)}
	bodyMap := map[string]json.RawMessage{
		"model":      json.RawMessage(`"deepseek-chat"`),
		"thinking":   json.RawMessage(`{"type":"disabled"}`),
		"max_tokens": json.RawMessage(`10`),
		"messages": json.RawMessage(`[
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"secret","signature":"sig"},
				{"type":"text","text":"visible"}
			]}
		]`),
	}
	var req claudecode.ClaudeRequest
	if err := json.Unmarshal([]byte(`{
		"model":"deepseek-chat",
		"thinking":{"type":"disabled"},
		"max_tokens":10,
		"messages":[{"role":"assistant","content":[
			{"type":"thinking","thinking":"secret","signature":"sig"},
			{"type":"text","text":"visible"}
		]}]
	}`), &req); err != nil {
		t.Fatal(err)
	}
	info := &relaycommon.RelayInfo{
		ChannelSetting: map[string]any{"compatibility_mode": claudeCodeDeepSeekV4CompatibilityMode},
	}

	if err := PrepareClaudeCodeMessagesRequest(c, info, &req, bodyMap); err != nil {
		t.Fatalf("PrepareClaudeCodeMessagesRequest error: %v", err)
	}

	if raw := string(bodyMap["messages"]); jsonContains(raw, `"type":"thinking"`) {
		t.Fatalf("bodyMap messages still contain thinking block: %s", raw)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("unexpected message count: %d", len(req.Messages))
	}
	content, ok := req.Messages[0].Content.([]any)
	if !ok {
		t.Fatalf("unexpected parsed message content: %#v", req.Messages[0].Content)
	}
	for _, block := range content {
		m, _ := block.(map[string]any)
		if m["type"] == "thinking" {
			t.Fatalf("parsed request messages still contain thinking block: %#v", req.Messages[0].Content)
		}
	}
}

func TestPrepareClaudeCodeMessagesRequestRemovesContextManagementByDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = &http.Request{Header: make(http.Header)}
	bodyMap := map[string]json.RawMessage{
		"model":              json.RawMessage(`"claude-sonnet-4-20250514"`),
		"max_tokens":         json.RawMessage(`10`),
		"context_management": json.RawMessage(`{"enabled":true,"type":"context","edits":[{"type":"clear_tool_uses_20250919","trigger":{"type":"input_tokens","value":20000},"keep":{"type":"tool_uses","value":3}}]}`),
		"messages":           json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"hi"}]}]`),
	}
	var req claudecode.ClaudeRequest
	if err := json.Unmarshal([]byte(`{
		"model":"claude-sonnet-4-20250514",
		"max_tokens":10,
		"context_management":{"enabled":true,"type":"context","edits":[{"type":"clear_tool_uses_20250919","trigger":{"type":"input_tokens","value":20000},"keep":{"type":"tool_uses","value":3}}]},
		"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]
	}`), &req); err != nil {
		t.Fatal(err)
	}
	info := &relaycommon.RelayInfo{
		ApiKey:         "test-key",
		ChannelSetting: map[string]any{},
	}

	if err := PrepareClaudeCodeMessagesRequest(c, info, &req, bodyMap); err != nil {
		t.Fatalf("PrepareClaudeCodeMessagesRequest error: %v", err)
	}

	if _, ok := bodyMap["context_management"]; ok {
		t.Fatalf("context_management should be removed by default: %s", bodyMap["context_management"])
	}
}

func TestPrepareClaudeCodeMessagesRequestRemovesToolInputExamples(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = &http.Request{Header: make(http.Header)}
	bodyMap := map[string]json.RawMessage{
		"model":      json.RawMessage(`"claude-sonnet-4-20250514"`),
		"max_tokens": json.RawMessage(`10`),
		"messages":   json.RawMessage(`[{"role":"user","content":[{"type":"text","text":"hi"}]}]`),
		"tools": json.RawMessage(`[
			{
				"name":"Read",
				"description":"Read a file",
				"input_schema":{"type":"object","properties":{"file_path":{"type":"string"}}},
				"input_examples":[{"file_path":"/tmp/a.txt"}]
			}
		]`),
	}
	var req claudecode.ClaudeRequest
	if err := json.Unmarshal([]byte(`{
		"model":"claude-sonnet-4-20250514",
		"max_tokens":10,
		"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}],
		"tools":[{
			"name":"Read",
			"description":"Read a file",
			"input_schema":{"type":"object","properties":{"file_path":{"type":"string"}}},
			"input_examples":[{"file_path":"/tmp/a.txt"}]
		}]
	}`), &req); err != nil {
		t.Fatal(err)
	}
	info := &relaycommon.RelayInfo{
		ApiKey:         "test-key",
		ChannelSetting: map[string]any{},
	}

	if err := PrepareClaudeCodeMessagesRequest(c, info, &req, bodyMap); err != nil {
		t.Fatalf("PrepareClaudeCodeMessagesRequest error: %v", err)
	}

	var tools []map[string]any
	if err := json.Unmarshal(bodyMap["tools"], &tools); err != nil {
		t.Fatalf("unmarshal tools: %v", err)
	}
	if _, ok := tools[0]["input_examples"]; ok {
		t.Fatalf("input_examples should be removed: %s", bodyMap["tools"])
	}
	if tools[0]["name"] != "Read" {
		t.Fatalf("tool fields should be preserved: %#v", tools[0])
	}
}

func TestPrepareClaudeCodeMessagesRequestSanitizesEmptyTextBlocks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = &http.Request{Header: make(http.Header)}
	bodyMap := map[string]json.RawMessage{
		"model":      json.RawMessage(`"claude-sonnet-4-20250514"`),
		"max_tokens": json.RawMessage(`10`),
		"messages": json.RawMessage(`[
			{"role":"user","content":[
				{"type":"text","text":"  "},
				{"type":"tool_result","tool_use_id":"tool_1","content":" "}
			]}
		]`),
	}
	var req claudecode.ClaudeRequest
	if err := json.Unmarshal([]byte(`{
		"model":"claude-sonnet-4-20250514",
		"max_tokens":10,
		"messages":[{"role":"user","content":[
			{"type":"text","text":"  "},
			{"type":"tool_result","tool_use_id":"tool_1","content":" "}
		]}]
	}`), &req); err != nil {
		t.Fatal(err)
	}
	info := &relaycommon.RelayInfo{
		ApiKey:         "test-key",
		ChannelSetting: map[string]any{},
	}

	if err := PrepareClaudeCodeMessagesRequest(c, info, &req, bodyMap); err != nil {
		t.Fatalf("PrepareClaudeCodeMessagesRequest error: %v", err)
	}

	var messages []map[string]any
	if err := json.Unmarshal(bodyMap["messages"], &messages); err != nil {
		t.Fatalf("unmarshal messages: %v", err)
	}
	content := messages[0]["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("empty text block should be removed, got %#v", content)
	}
	toolResult := content[0].(map[string]any)
	if toolResult["type"] != "tool_result" || toolResult["content"] != "..." {
		t.Fatalf("tool_result content should be patched, got %#v", toolResult)
	}

	parsedContent := req.Messages[0].Content.([]any)
	parsedToolResult := parsedContent[0].(map[string]any)
	if parsedToolResult["content"] != "..." {
		t.Fatalf("parsed request messages should be synced, got %#v", req.Messages[0].Content)
	}
}

func jsonContains(raw, needle string) bool {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return false
	}
	compact, _ := json.Marshal(v)
	return strings.Contains(string(compact), needle)
}
