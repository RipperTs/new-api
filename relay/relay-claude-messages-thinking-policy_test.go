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

func TestDeepSeekV4CompatibilityDisablesThinkingAndRemovesConflictingFields(t *testing.T) {
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

	applyDeepSeekV4ThinkingCompatibility(bodyMap)

	if string(bodyMap["thinking"]) != `{"type":"disabled"}` {
		t.Fatalf("unexpected thinking: %s", bodyMap["thinking"])
	}
	for _, key := range []string{"output_config", "reasoning_effort", "context_management"} {
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

func jsonContains(raw, needle string) bool {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return false
	}
	compact, _ := json.Marshal(v)
	return strings.Contains(string(compact), needle)
}
