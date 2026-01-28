package codex

import (
	"bytes"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"one-api/dto"
	relaycommon "one-api/relay/common"
	"one-api/service"
	"strings"
)

// /v1/responses (Codex 渠道)：非流式尽量返回 JSON；若上游误返回 SSE，则提取 response.completed 的 response 并返回 JSON。
func codexResponsesPassthroughHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.OpenAIErrorWithStatusCode, *dto.Usage) {
	if resp == nil || resp.Body == nil {
		return service.OpenAIErrorWrapper(io.ErrUnexpectedEOF, "invalid_response", http.StatusInternalServerError), nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return service.OpenAIErrorWrapper(err, "read_response_failed", http.StatusInternalServerError), nil
	}

	// 如果是 SSE：尝试找到 response.completed 并提取 response 对象，作为非流式 JSON 返回
	if looksLikeSSEBody(body, resp) {
		var completedEvt map[string]any
		lines := bytes.Split(body, []byte("\n"))
		for _, line := range lines {
			line = bytes.TrimSpace(bytes.TrimSuffix(line, []byte("\r")))
			if !bytes.HasPrefix(line, []byte("data:")) {
				continue
			}
			payload := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
			if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
				continue
			}
			var evt map[string]any
			if json.Unmarshal(payload, &evt) != nil {
				continue
			}
			if typ, _ := evt["type"].(string); typ == "response.completed" {
				completedEvt = evt
			}
		}
		if completedEvt != nil {
			if respObj, ok := completedEvt["response"].(map[string]any); ok {
				b, _ := json.Marshal(respObj)
				c.Writer.Header().Set("Content-Type", "application/json")
				c.Writer.WriteHeader(http.StatusOK)
				_, _ = c.Writer.Write(b)
				return nil, parseUsageFromResponseCompleted(completedEvt)
			}
		}
		// 未能提取 completed：退化为直接返回上游原始内容（避免静默）
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.WriteHeader(http.StatusOK)
		_, _ = c.Writer.Write(body)
		return nil, tryParseUsageFromBody(body)
	}

	// 默认：JSON/其它内容，直接原样返回
	ct := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if ct == "" {
		ct = "application/json"
	}
	c.Writer.Header().Set("Content-Type", ct)
	c.Writer.WriteHeader(http.StatusOK)
	_, _ = c.Writer.Write(body)
	return nil, tryParseUsageFromBody(body)
}

func looksLikeSSEBody(body []byte, resp *http.Response) bool {
	if resp != nil {
		ct := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
		if strings.Contains(ct, "text/event-stream") {
			return true
		}
	}
	b := bytes.TrimSpace(body)
	if len(b) == 0 {
		return false
	}
	// JSON/HTML 一般以 { [ < 开头；SSE 常见为 event:/data:
	switch b[0] {
	case '{', '[', '<':
		return false
	default:
		return bytes.Contains(b, []byte("\ndata:")) || bytes.HasPrefix(b, []byte("data:")) || bytes.HasPrefix(b, []byte("event:"))
	}
}
