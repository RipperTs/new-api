package relay

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "one-api/relay/common"

	"github.com/gin-gonic/gin"
)

func TestNonStreamOpenAIChatToClaudeWritesClaudeMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	info := &relaycommon.RelayInfo{PromptTokens: 7, UpstreamModelName: "openai/gpt-test"}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(`{
			"id":"chatcmpl_1",
			"model":"openai/gpt-test",
			"choices":[{"index":0,"message":{
				"role":"assistant",
				"content":"hello",
				"reasoning_content":"thought"
			},"finish_reason":"tool_calls"}],
			"usage":{"prompt_tokens":7,"completion_tokens":5,"total_tokens":12}
		}`)),
	}

	usage, err := nonStreamOpenAIChatToClaude(c, resp, info)
	if err != nil {
		t.Fatalf("nonStreamOpenAIChatToClaude error: %v", err)
	}
	if usage.PromptTokens != 7 || usage.CompletionTokens != 5 || usage.TotalTokens != 12 {
		t.Fatalf("unexpected usage: %+v", usage)
	}

	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid response json: %v", err)
	}
	if body["type"] != "message" {
		t.Fatalf("expected Claude message type, got: %#v", body["type"])
	}
	if body["stop_reason"] != "tool_use" {
		t.Fatalf("expected tool_use stop reason, got: %#v", body["stop_reason"])
	}
	content, ok := body["content"].([]any)
	if !ok || len(content) != 2 {
		t.Fatalf("expected text and thinking content blocks, got: %#v", body["content"])
	}
}

func TestStreamOpenAIChatToClaudeWritesClaudeEvents(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	info := &relaycommon.RelayInfo{PromptTokens: 4, UpstreamModelName: "openai/gpt-test", IsStream: true}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"id":"chatcmpl_1","model":"openai/gpt-test","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
			`data: {"id":"chatcmpl_1","model":"openai/gpt-test","choices":[{"index":0,"delta":{"content":"hello"}}]}`,
			`data: {"id":"chatcmpl_1","model":"openai/gpt-test","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":3,"total_tokens":7}}`,
			`data: [DONE]`,
			``,
		}, "\n"))),
	}

	usage, upstreamErr, err := streamOpenAIChatToClaude(c, resp, info)
	if err != nil {
		t.Fatalf("streamOpenAIChatToClaude error: %v", err)
	}
	if upstreamErr != nil {
		t.Fatalf("streamOpenAIChatToClaude upstream error: %+v", upstreamErr)
	}
	if usage.PromptTokens != 4 || usage.CompletionTokens != 3 || usage.TotalTokens != 7 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
	body := recorder.Body.String()
	for _, event := range []string{
		"event: message_start",
		"event: content_block_start",
		"event: content_block_delta",
		"event: message_delta",
		"event: message_stop",
	} {
		if !strings.Contains(body, event) {
			t.Fatalf("expected %s in stream body: %s", event, body)
		}
	}
}
