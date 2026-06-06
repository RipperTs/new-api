package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "one-api/relay/common"

	"github.com/gin-gonic/gin"
)

func TestHandleClaudeCodeNativeChannelTestResponseWritesNativeClaudeMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	info := &relaycommon.RelayInfo{
		PromptTokens:      3,
		UpstreamModelName: "deepseek-chat",
		IsStream:          false,
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(`{
			"id":"msg_1",
			"type":"message",
			"role":"assistant",
			"model":"deepseek-chat",
			"content":[{"type":"text","text":"hello"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":3,"output_tokens":2}
		}`)),
	}

	usage, openaiErr, err := HandleClaudeCodeNativeChannelTestResponse(c, resp, info)
	if err != nil {
		t.Fatalf("HandleClaudeCodeNativeChannelTestResponse error: %v", err)
	}
	if openaiErr != nil {
		t.Fatalf("HandleClaudeCodeNativeChannelTestResponse upstream error: %+v", openaiErr)
	}
	if usage == nil {
		t.Fatal("usage is nil")
	}
	if usage.PromptTokens != 3 || usage.CompletionTokens != 2 || usage.TotalTokens != 5 {
		t.Fatalf("unexpected usage: %+v", usage)
	}

	body := recorder.Body.String()
	if !strings.Contains(body, `"type":"message"`) {
		t.Fatalf("expected native Claude message response, got: %s", body)
	}
	if strings.Contains(body, `"choices"`) {
		t.Fatalf("expected no OpenAI choices in native Claude response, got: %s", body)
	}
	if info.UpstreamModelName != "deepseek-chat" {
		t.Fatalf("unexpected upstream model name: %s", info.UpstreamModelName)
	}
}
