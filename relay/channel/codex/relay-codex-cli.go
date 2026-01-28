package codex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"one-api/common"
	"one-api/dto"
	relaycommon "one-api/relay/common"
	"one-api/service"
	"os"
	"strings"
)

// /v1/codex-cli：保持 SSE 原样透传，同时尽量解析 usage 以保证计费正常。
func codexCLIPassthroughStreamHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.OpenAIErrorWithStatusCode, *dto.Usage) {
	if resp == nil || resp.Body == nil {
		return service.OpenAIErrorWrapper(io.ErrUnexpectedEOF, "invalid_response", http.StatusInternalServerError), nil
	}
	defer resp.Body.Close()

	contentType := resp.Header.Get("Content-Type")
	codexCLILogLine(c, info, fmt.Sprintf("upstream_status=%d content_type=%s", resp.StatusCode, strings.TrimSpace(contentType)))

	// 某些上游（尤其是第三方/镜像）即便请求带 stream=true，也可能返回 JSON（非 SSE）。
	// Codex CLI 只认 SSE：这里提前探测 body，如果不是 SSE，则走非流式包装输出，避免 CLI “无输出/断流”。
	reader := bufio.NewReaderSize(resp.Body, 1024*1024)
	if !looksLikeSSE(resp, reader) {
		raw, _ := io.ReadAll(reader)
		codexCLILogLine(c, info, fmt.Sprintf("fallback_non_sse body=%s", truncateForLog(string(raw), 2000)))
		fakeResp := &http.Response{Body: io.NopCloser(bytes.NewReader(raw))}
		return codexCLIPassthroughHandler(c, fakeResp, info)
	}

	// Codex CLI 需要 SSE；无论上游如何，统一对外输出 text/event-stream
	service.SetEventStreamHeaders(c)
	c.Writer.WriteHeader(http.StatusOK)

	var outputText strings.Builder
	var usage *dto.Usage
	var sawResponseCompleted bool
	var sawFailedOrError bool
	var sawDone bool
	var responseID string
	var terminalErr map[string]any
	isFirst := true
	evtLogN := 0
	var readErr error

	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if isFirst {
				isFirst = false
				info.SetFirstResponseTime()
			}
			trimmed := bytes.TrimSpace(bytes.TrimSuffix(line, []byte("\r")))
			if bytes.HasPrefix(trimmed, []byte("data:")) {
				payload := bytes.TrimSpace(bytes.TrimPrefix(trimmed, []byte("data:")))
				if bytes.Equal(payload, []byte("[DONE]")) {
					sawDone = true
					if !sawResponseCompleted {
						// 某些镜像站仅发送 [DONE] 不发送 response.completed，Codex CLI 会报 “stream closed before response.completed”
						codexCLILogLine(c, info, "inject_completed=done_without_completed")
						status := "completed"
						if sawFailedOrError {
							status = "failed"
						}
						_ = writeSyntheticResponseCompletedWithStatus(c, responseID, info, usage, outputText.String(), status, terminalErr)
						sawResponseCompleted = true
					}
					// 再把上游的 [DONE] 原样写回
					_, _ = c.Writer.Write(line)
					if f, ok := c.Writer.(http.Flusher); ok {
						f.Flush()
					}
					continue
				}
				if len(payload) > 0 && !bytes.Equal(payload, []byte("[DONE]")) {
					var evt map[string]any
					if json.Unmarshal(payload, &evt) == nil {
						if r, ok := evt["response"].(map[string]any); ok {
							if id, ok := r["id"].(string); ok && id != "" {
								responseID = id
							}
						}
						typ, _ := evt["type"].(string)
						if evtLogN < 30 && codexCLIDebugEnabled(info) {
							evtLogN++
							switch typ {
							case "response.output_text.delta":
								delta, _ := evt["delta"].(string)
								codexCLILogLine(c, info, fmt.Sprintf("evt#%d type=%s delta_len=%d delta=%s", evtLogN, typ, len(delta), truncateForLog(delta, 200)))
							default:
								codexCLILogLine(c, info, fmt.Sprintf("evt#%d type=%s payload=%s", evtLogN, typ, truncateForLog(string(payload), 1000)))
							}
						}
						switch typ {
						case "response.output_text.delta":
							if delta, ok := evt["delta"].(string); ok && delta != "" {
								outputText.WriteString(delta)
							}
						case "response.completed":
							sawResponseCompleted = true
							if usage == nil {
								usage = parseUsageFromResponseCompleted(evt)
							}
						case "response.failed":
							// 注意：上游失败时通常不会再发送 response.completed。
							// 这里标记失败，避免 EOF 时注入“成功 completed”覆盖错误，导致客户端静默。
							sawFailedOrError = true
							if e := extractErrorObjectFromEvent(evt); e != nil {
								terminalErr = e
							}
						case "error":
							// OpenAI Responses SSE 可能直接下发 type=error
							sawFailedOrError = true
							if e, ok := evt["error"].(map[string]any); ok && len(e) > 0 {
								terminalErr = e
							}
						}
					}
				}
			}

			// 默认：原样透传该行
			_, _ = c.Writer.Write(line)
			if f, ok := c.Writer.(http.Flusher); ok {
				f.Flush()
			}
		}
		if err != nil {
			readErr = err
			break
		}
	}

	if readErr != nil {
		codexCLILogLine(c, info, fmt.Sprintf("upstream_read_error=%v", readErr))
	}
	if !sawResponseCompleted {
		codexCLILogLine(c, info, "inject_completed=eof_without_completed")
		status := "completed"
		if sawFailedOrError {
			status = "failed"
		}
		_ = writeSyntheticResponseCompletedWithStatus(c, responseID, info, usage, outputText.String(), status, terminalErr)
		sawResponseCompleted = true
	}
	if !sawDone {
		codexCLILogLine(c, info, "inject_done=eof_without_done")
		_, _ = c.Writer.Write([]byte("data: [DONE]\n\n"))
		if f, ok := c.Writer.(http.Flusher); ok {
			f.Flush()
		}
	}

	if usage == nil {
		usage, _ = service.ResponseText2Usage(outputText.String(), info.UpstreamModelName, info.PromptTokens)
	}
	return nil, usage
}

// 非流式：尽量原样返回（如果上游仍是 SSE，则作为文本返回）。
func codexCLIPassthroughHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.OpenAIErrorWithStatusCode, *dto.Usage) {
	if resp == nil || resp.Body == nil {
		return service.OpenAIErrorWrapper(io.ErrUnexpectedEOF, "invalid_response", http.StatusInternalServerError), nil
	}
	defer resp.Body.Close()

	// 对外仍以 SSE 形式输出，确保 CLI 不会因为“非 SSE/提前断流”报 stream closed
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return service.OpenAIErrorWrapper(err, "read_response_failed", http.StatusInternalServerError), nil
	}
	codexCLILogLine(c, info, fmt.Sprintf("upstream_body=%s", truncateForLog(string(body), 2000)))

	service.SetEventStreamHeaders(c)
	c.Writer.WriteHeader(http.StatusOK)

	// 若上游返回的是错误 JSON（常见于 4xx/5xx），转换为 SSE 错误事件，避免 Codex CLI “无输出/静默中断”。
	if openaiErr := tryParseOpenAIErrorFromBody(body, resp); openaiErr != nil {
		if errObj := openaiErrToErrorObject(openaiErr); errObj != nil {
			evt := map[string]any{
				"type":  "error",
				"error": errObj,
			}
			b, _ := json.Marshal(evt)
			_, _ = c.Writer.Write([]byte("data: " + string(b) + "\n\n"))
			if f, ok := c.Writer.(http.Flusher); ok {
				f.Flush()
			}
			failed := map[string]any{
				"type": "response.failed",
				"response": map[string]any{
					"id":     "resp_" + common.GetUUID(),
					"status": "failed",
					"error":  errObj,
				},
			}
			b2, _ := json.Marshal(failed)
			_, _ = c.Writer.Write([]byte("data: " + string(b2) + "\n\n"))
			if f, ok := c.Writer.(http.Flusher); ok {
				f.Flush()
			}
			_ = writeSyntheticResponseCompletedWithStatus(c, "", info, &dto.Usage{PromptTokens: 0, CompletionTokens: 0, TotalTokens: 0}, "", "failed", errObj)
			_, _ = c.Writer.Write([]byte("data: [DONE]\n\n"))
			if f, ok := c.Writer.(http.Flusher); ok {
				f.Flush()
			}
			return nil, &dto.Usage{PromptTokens: 0, CompletionTokens: 0, TotalTokens: 0}
		}
	}

	// 尝试把上游 body 解析成可用文本（兼容 chat.completions / responses / SSE 聚合）
	text := ""
	if s := extractTextFromBody(body); s != "" {
		text = s
	}
	if text != "" {
		evt := map[string]any{
			"type":  "response.output_text.delta",
			"delta": text,
		}
		b, _ := json.Marshal(evt)
		_, _ = c.Writer.Write([]byte("data: " + string(b) + "\n\n"))
		if f, ok := c.Writer.(http.Flusher); ok {
			f.Flush()
		}
	}

	u := tryParseUsageFromBody(body)
	if u == nil {
		u, _ = service.ResponseText2Usage(text, info.UpstreamModelName, info.PromptTokens)
	}
	_ = writeSyntheticResponseCompletedWithStatus(c, "", info, u, text, "completed", nil)
	_, _ = c.Writer.Write([]byte("data: [DONE]\n\n"))
	if f, ok := c.Writer.(http.Flusher); ok {
		f.Flush()
	}
	return nil, u
}

func parseUsageFromResponseCompleted(evt map[string]any) *dto.Usage {
	respObj, ok := evt["response"].(map[string]any)
	if !ok {
		return nil
	}
	usageObj, ok := respObj["usage"].(map[string]any)
	if !ok {
		return nil
	}
	p := intFromAny(usageObj["input_tokens"])
	c := intFromAny(usageObj["output_tokens"])
	t := intFromAny(usageObj["total_tokens"])
	if t == 0 {
		t = p + c
	}
	if p == 0 && c == 0 && t == 0 {
		return nil
	}
	return &dto.Usage{PromptTokens: p, CompletionTokens: c, TotalTokens: t}
}

func tryParseUsageFromBody(body []byte) *dto.Usage {
	// SSE：扫描 response.completed
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
			return parseUsageFromResponseCompleted(evt)
		}
	}
	// JSON：直接看 usage
	var obj map[string]any
	if json.Unmarshal(body, &obj) != nil {
		return nil
	}
	if usageObj, ok := obj["usage"].(map[string]any); ok {
		p := intFromAny(usageObj["input_tokens"])
		c := intFromAny(usageObj["output_tokens"])
		t := intFromAny(usageObj["total_tokens"])
		if t == 0 {
			t = p + c
		}
		if p == 0 && c == 0 && t == 0 {
			return nil
		}
		return &dto.Usage{PromptTokens: p, CompletionTokens: c, TotalTokens: t}
	}
	return nil
}

func intFromAny(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

func writeSyntheticResponseCompleted(c *gin.Context, responseID string, info *relaycommon.RelayInfo, usage *dto.Usage, outputText string) error {
	return writeSyntheticResponseCompletedWithStatus(c, responseID, info, usage, outputText, "completed", nil)
}

func writeSyntheticResponseCompletedWithStatus(c *gin.Context, responseID string, info *relaycommon.RelayInfo, usage *dto.Usage, outputText string, status string, responseErr map[string]any) error {
	if c == nil {
		return nil
	}
	if responseID == "" {
		responseID = "resp_" + common.GetUUID()
	}
	if usage == nil {
		usage, _ = service.ResponseText2Usage(outputText, info.UpstreamModelName, info.PromptTokens)
	}
	st := strings.TrimSpace(status)
	if st == "" {
		st = "completed"
	}
	payload := map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     responseID,
			"status": st,
			"usage": map[string]any{
				"input_tokens":  usage.PromptTokens,
				"output_tokens": usage.CompletionTokens,
				"total_tokens":  usage.TotalTokens,
			},
		},
	}
	if responseErr != nil && len(responseErr) > 0 {
		if respObj, ok := payload["response"].(map[string]any); ok {
			respObj["error"] = responseErr
		}
	}
	b, _ := json.Marshal(payload)
	_, err := c.Writer.Write([]byte("data: " + string(b) + "\n\n"))
	if f, ok := c.Writer.(http.Flusher); ok {
		f.Flush()
	}
	return err
}

func codexCLIDebugEnabled(info *relaycommon.RelayInfo) bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("CODEX_CLI_DEBUG")), "true") || common.DebugEnabled {
		return true
	}
	if info != nil && info.ChannelSetting != nil {
		if v, ok := info.ChannelSetting["codex_cli_debug"]; ok {
			switch vv := v.(type) {
			case bool:
				return vv
			case string:
				return strings.EqualFold(strings.TrimSpace(vv), "true")
			}
		}
	}
	return false
}

func codexCLILogLine(c *gin.Context, info *relaycommon.RelayInfo, line string) {
	if !codexCLIDebugEnabled(info) {
		return
	}
	reqID := ""
	if c != nil {
		reqID = c.GetString(common.RequestIdKey)
	}
	chID := 0
	if info != nil {
		chID = info.ChannelId
	}
	common.SysLog(fmt.Sprintf("[codex-cli] reqid=%s channel_id=%d %s", reqID, chID, truncateForLog(line, 4000)))
}

func truncateForLog(s string, max int) string {
	ss := strings.TrimSpace(s)
	if max <= 0 || len(ss) <= max {
		return ss
	}
	return ss[:max] + "…"
}

func looksLikeSSE(resp *http.Response, reader *bufio.Reader) bool {
	if resp == nil || reader == nil {
		return false
	}
	// 先看 Content-Type
	ct := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	ctSSE := strings.Contains(ct, "text/event-stream")
	// 再看 body 首个非空白字符：即便 Content-Type 写了 event-stream，也可能返回 JSON/HTML
	b, err := reader.Peek(256)
	if err == nil && len(b) > 0 {
		for _, ch := range b {
			if ch == ' ' || ch == '\n' || ch == '\r' || ch == '\t' {
				continue
			}
			if ch == '{' || ch == '[' || ch == '<' {
				return false
			}
			// SSE 常见字段：data/event/id/retry/注释行
			if ch == 'd' || ch == 'e' || ch == 'i' || ch == 'r' || ch == ':' {
				return true
			}
			break
		}
	}
	// 无法探测时，若 Content-Type 明确是 SSE，则认为是 SSE
	return ctSSE
}

func extractTextFromBody(body []byte) string {
	b := bytes.TrimSpace(body)
	if len(b) == 0 {
		return ""
	}
	// chat.completions
	var chatResp struct {
		Choices []struct {
			Message struct {
				Content any `json:"content"`
			} `json:"message"`
			Delta struct {
				Content any `json:"content"`
			} `json:"delta"`
			Text string `json:"text"`
		} `json:"choices"`
	}
	if json.Unmarshal(b, &chatResp) == nil && len(chatResp.Choices) > 0 {
		if s, ok := chatResp.Choices[0].Message.Content.(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
		if s, ok := chatResp.Choices[0].Delta.Content.(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
		if strings.TrimSpace(chatResp.Choices[0].Text) != "" {
			return chatResp.Choices[0].Text
		}
	}
	// responses json
	var obj map[string]any
	if json.Unmarshal(b, &obj) == nil {
		if out, ok := obj["output"].([]any); ok {
			for _, item := range out {
				m, ok := item.(map[string]any)
				if !ok {
					continue
				}
				if content, ok := m["content"].([]any); ok {
					for _, c := range content {
						cm, ok := c.(map[string]any)
						if !ok {
							continue
						}
						if t, _ := cm["type"].(string); t == "output_text" {
							if s, _ := cm["text"].(string); strings.TrimSpace(s) != "" {
								return s
							}
						}
					}
				}
			}
		}
	}
	// fallback raw (truncate)
	s := strings.TrimSpace(string(b))
	if len(s) > 500 {
		s = s[:500]
	}
	return s
}

func extractErrorObjectFromEvent(evt map[string]any) map[string]any {
	if evt == nil {
		return nil
	}
	// responses: response.failed 的 error 往往在 response.error
	if r, ok := evt["response"].(map[string]any); ok {
		if e, ok := r["error"].(map[string]any); ok && len(e) > 0 {
			return e
		}
	}
	// 有些实现把 error 放在顶层
	if e, ok := evt["error"].(map[string]any); ok && len(e) > 0 {
		return e
	}
	return nil
}

func tryParseOpenAIErrorFromBody(body []byte, resp *http.Response) *dto.OpenAIErrorWithStatusCode {
	b := bytes.TrimSpace(body)
	if len(b) == 0 {
		return nil
	}
	// 标准：{"error": {...}}
	var gen dto.GeneralErrorResponse
	if json.Unmarshal(b, &gen) != nil {
		return nil
	}
	msg := strings.TrimSpace(gen.ToMessage())
	if msg == "" && strings.TrimSpace(gen.Error.Type) == "" && gen.Error.Code == nil && strings.TrimSpace(gen.Error.Param) == "" {
		return nil
	}
	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	if status <= 0 {
		status = http.StatusBadGateway
	}
	e := gen.Error
	if strings.TrimSpace(e.Message) == "" && msg != "" {
		e.Message = msg
	}
	if strings.TrimSpace(e.Type) == "" {
		e.Type = "upstream_error"
	}
	return &dto.OpenAIErrorWithStatusCode{
		Error:      e,
		StatusCode: status,
		LocalError: false,
	}
}

func openaiErrToErrorObject(e *dto.OpenAIErrorWithStatusCode) map[string]any {
	if e == nil {
		return nil
	}
	msg := strings.TrimSpace(e.Error.Message)
	typ := strings.TrimSpace(e.Error.Type)
	if typ == "" {
		typ = "upstream_error"
	}
	obj := map[string]any{
		"message": msg,
		"type":    typ,
	}
	if strings.TrimSpace(e.Error.Param) != "" {
		obj["param"] = e.Error.Param
	}
	if e.Error.Code != nil {
		obj["code"] = e.Error.Code
	}
	return obj
}
