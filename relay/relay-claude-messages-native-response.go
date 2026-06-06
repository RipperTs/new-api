package relay

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"one-api/dto"
	"one-api/relay/channel/claudecode"
	relaycommon "one-api/relay/common"
	"one-api/service"
	"strings"

	"github.com/gin-gonic/gin"
)

func patchMissingThinkingSignature(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		changed := false
		if typ, ok := t["type"].(string); ok && typ == "thinking" {
			signatureRaw, exists := t["signature"]
			if !exists {
				t["signature"] = "skip_thought_signature_validator"
				changed = true
			} else if signature, ok := signatureRaw.(string); ok && strings.TrimSpace(signature) == "" {
				t["signature"] = "skip_thought_signature_validator"
				changed = true
			}
		}
		for _, sub := range t {
			if patchMissingThinkingSignature(sub) {
				changed = true
			}
		}
		return changed
	case []any:
		changed := false
		for _, sub := range t {
			if patchMissingThinkingSignature(sub) {
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}

func patchEmptyClaudeCodeReadPages(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		changed := false
		if typ, _ := t["type"].(string); typ == "tool_use" {
			if name, _ := t["name"].(string); name == "Read" {
				if input, ok := t["input"].(map[string]any); ok {
					if pages, ok := input["pages"].(string); ok && pages == "" {
						delete(input, "pages")
						changed = true
					}
				}
			}
		}
		for _, sub := range t {
			if patchEmptyClaudeCodeReadPages(sub) {
				changed = true
			}
		}
		return changed
	case []any:
		changed := false
		for _, sub := range t {
			if patchEmptyClaudeCodeReadPages(sub) {
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}

func sanitizeClaudeCodeReadInput(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return raw
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		return raw
	}
	if pages, ok := input["pages"].(string); !ok || pages != "" {
		return raw
	}
	delete(input, "pages")
	patched, err := json.Marshal(input)
	if err != nil {
		return raw
	}
	return string(patched)
}

func patchClaudeCodeResponse(v any) bool {
	changed := patchMissingThinkingSignature(v)
	if patchEmptyClaudeCodeReadPages(v) {
		changed = true
	}
	return changed
}

func patchClaudeCodeResponseJSON(raw []byte) ([]byte, bool) {
	var parsed any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return raw, false
	}
	if !patchClaudeCodeResponse(parsed) {
		return raw, false
	}
	patched, err := json.Marshal(parsed)
	if err != nil {
		return raw, false
	}
	return patched, true
}

type claudeCodeStreamPatchState struct {
	readToolInputs map[int]*strings.Builder
}

func newClaudeCodeStreamPatchState() *claudeCodeStreamPatchState {
	return &claudeCodeStreamPatchState{
		readToolInputs: make(map[int]*strings.Builder),
	}
}

func (s *claudeCodeStreamPatchState) patchEvent(root map[string]any) (extraLines []string, suppress bool) {
	if s == nil {
		return nil, false
	}
	eventType, _ := root["type"].(string)
	idx, hasIndex := claudeCodeStreamEventIndex(root["index"])

	switch eventType {
	case "content_block_start":
		if !hasIndex {
			return nil, false
		}
		block, ok := root["content_block"].(map[string]any)
		if !ok {
			return nil, false
		}
		blockType, _ := block["type"].(string)
		name, _ := block["name"].(string)
		if blockType == "tool_use" && name == "Read" {
			s.readToolInputs[idx] = &strings.Builder{}
		}
	case "content_block_delta":
		if !hasIndex {
			return nil, false
		}
		input, ok := s.readToolInputs[idx]
		if !ok {
			return nil, false
		}
		delta, ok := root["delta"].(map[string]any)
		if !ok {
			return nil, false
		}
		deltaType, _ := delta["type"].(string)
		if deltaType != "input_json_delta" {
			return nil, false
		}
		if partialJSON, ok := delta["partial_json"].(string); ok {
			input.WriteString(partialJSON)
		}
		return nil, true
	case "content_block_stop":
		if !hasIndex {
			return nil, false
		}
		input, ok := s.readToolInputs[idx]
		if !ok {
			return nil, false
		}
		delete(s.readToolInputs, idx)
		raw := input.String()
		if raw == "" {
			return nil, false
		}
		event := map[string]any{
			"type":  "content_block_delta",
			"index": idx,
			"delta": map[string]any{
				"type":         "input_json_delta",
				"partial_json": sanitizeClaudeCodeReadInput(raw),
			},
		}
		data, err := json.Marshal(event)
		if err != nil {
			return nil, false
		}
		return []string{"event: content_block_delta", "data: " + string(data), ""}, false
	}
	return nil, false
}

func patchClaudeCodeStreamEventData(data string, state *claudeCodeStreamPatchState) (string, []string, bool, bool) {
	var parsed any
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		return data, nil, false, false
	}
	root, _ := parsed.(map[string]any)
	var extraLines []string
	if root != nil && state != nil {
		var suppress bool
		extraLines, suppress = state.patchEvent(root)
		if suppress {
			return data, nil, true, false
		}
	}
	if !patchClaudeCodeResponse(parsed) {
		return data, extraLines, false, false
	}
	patched, err := json.Marshal(parsed)
	if err != nil {
		return data, extraLines, false, false
	}
	return string(patched), extraLines, false, true
}

func claudeCodeStreamEventIndex(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	default:
		return 0, false
	}
}

func claudeCodeStreamError(resp *http.Response, claudeResp *claudecode.ClaudeResponse) *dto.OpenAIErrorWithStatusCode {
	if claudeResp == nil {
		return nil
	}
	errType := strings.TrimSpace(claudeResp.Error.Type)
	if errType == "" && strings.EqualFold(strings.TrimSpace(claudeResp.Type), "error") {
		errType = "upstream_error"
	}
	if errType == "" {
		return nil
	}
	message := strings.TrimSpace(claudeResp.Error.Message)
	if message == "" {
		message = "upstream error"
	}
	statusCode := claudeCodeStreamErrorStatusCode(resp, errType)
	return &dto.OpenAIErrorWithStatusCode{
		Error: dto.OpenAIError{
			Message: message,
			Type:    errType,
			Code:    errType,
		},
		StatusCode: statusCode,
		LocalError: false,
	}
}

func claudeCodeStreamErrorStatusCode(resp *http.Response, errType string) int {
	if resp != nil && resp.StatusCode >= http.StatusBadRequest {
		return resp.StatusCode
	}
	switch strings.ToLower(strings.TrimSpace(errType)) {
	case "rate_limit_error", "rate_limit_exceeded", "usage_limit_reached", "overloaded_error":
		return http.StatusTooManyRequests
	case "invalid_request_error":
		return http.StatusBadRequest
	case "authentication_error":
		return http.StatusUnauthorized
	case "permission_error":
		return http.StatusForbidden
	case "not_found_error":
		return http.StatusNotFound
	default:
		return http.StatusBadGateway
	}
}

func writeClaudeCodeStreamLine(c *gin.Context, line string) error {
	if !c.Writer.Written() {
		service.SetEventStreamHeaders(c)
	}
	if _, err := c.Writer.Write([]byte(line + "\n")); err != nil {
		return err
	}
	if flusher, ok := c.Writer.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

func streamClaudeCodePassthrough(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *dto.OpenAIErrorWithStatusCode, error) {
	defer resp.Body.Close()

	usage := &dto.Usage{}
	var responseText strings.Builder
	pendingLines := make([]string, 0, 2)
	streamStarted := false
	streamPatchState := newClaudeCodeStreamPatchState()
	pendingEventLine := ""

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		info.SetFirstResponseTime()
		outputLine := line
		parsedData := ""
		if strings.HasPrefix(line, "event:") {
			if pendingEventLine != "" {
				if !streamStarted {
					if len(pendingLines) < 4 {
						pendingLines = append(pendingLines, pendingEventLine)
					}
				} else if err := writeClaudeCodeStreamLine(c, pendingEventLine); err != nil {
					return nil, nil, err
				}
			}
			pendingEventLine = line
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			parsedData = data
			if data != "" && data != "[DONE]" {
				if patchedData, extraLines, suppress, changed := patchClaudeCodeStreamEventData(data, streamPatchState); suppress {
					pendingEventLine = ""
					continue
				} else {
					for _, extraLine := range extraLines {
						if !streamStarted {
							pendingLines = append(pendingLines, extraLine)
						} else {
							if err := writeClaudeCodeStreamLine(c, extraLine); err != nil {
								return nil, nil, err
							}
						}
					}
					if changed {
						if strings.HasPrefix(line, "data: ") {
							outputLine = "data: " + patchedData
						} else {
							outputLine = "data:" + patchedData
						}
						parsedData = patchedData
					}
				}
			}
		}
		if parsedData == "" || parsedData == "[DONE]" {
			if pendingEventLine != "" {
				if !streamStarted {
					if len(pendingLines) < 4 {
						pendingLines = append(pendingLines, pendingEventLine)
					}
				} else if err := writeClaudeCodeStreamLine(c, pendingEventLine); err != nil {
					return nil, nil, err
				}
				pendingEventLine = ""
			}
			if !streamStarted {
				if strings.TrimSpace(outputLine) != "" && len(pendingLines) < 4 {
					pendingLines = append(pendingLines, outputLine)
				}
				continue
			}
			if err := writeClaudeCodeStreamLine(c, outputLine); err != nil {
				return nil, nil, err
			}
			streamStarted = true
			continue
		}
		var claudeResp claudecode.ClaudeResponse
		if err := json.Unmarshal([]byte(parsedData), &claudeResp); err != nil {
			if !streamStarted {
				return nil, nil, errors.New("invalid claude stream event before message_start")
			}
			if pendingEventLine != "" {
				pendingLines = append(pendingLines, pendingEventLine)
				pendingEventLine = ""
			}
			for _, pendingLine := range pendingLines {
				if err := writeClaudeCodeStreamLine(c, pendingLine); err != nil {
					return nil, nil, err
				}
			}
			pendingLines = pendingLines[:0]
			if err := writeClaudeCodeStreamLine(c, outputLine); err != nil {
				return nil, nil, err
			}
			streamStarted = true
			continue
		}
		if upstreamErr := claudeCodeStreamError(resp, &claudeResp); upstreamErr != nil {
			if streamStarted {
				if err := writeClaudeCodeStreamLine(c, outputLine); err != nil {
					return nil, nil, err
				}
				return nil, nil, fmt.Errorf("%s", upstreamErr.Error.Message)
			}
			return nil, upstreamErr, nil
		}
		if !streamStarted && claudeResp.Type != "message_start" {
			pendingLines = pendingLines[:0]
			pendingEventLine = ""
			continue
		}
		if pendingEventLine != "" {
			pendingLines = append(pendingLines, pendingEventLine)
			pendingEventLine = ""
		}
		for _, pendingLine := range pendingLines {
			if err := writeClaudeCodeStreamLine(c, pendingLine); err != nil {
				return nil, nil, err
			}
		}
		pendingLines = pendingLines[:0]
		if err := writeClaudeCodeStreamLine(c, outputLine); err != nil {
			return nil, nil, err
		}
		streamStarted = true
		if claudeResp.Type == "message_start" && claudeResp.Message != nil {
			info.UpstreamModelName = claudeResp.Message.Model
			usage.PromptTokens = claudeResp.Message.Usage.InputTokens
		} else if claudeResp.Type == "content_block_delta" && claudeResp.Delta != nil {
			responseText.WriteString(claudeResp.Delta.Text)
		} else if claudeResp.Type == "message_delta" {
			usage.CompletionTokens = claudeResp.Usage.OutputTokens
			usage.TotalTokens = claudeResp.Usage.InputTokens + claudeResp.Usage.OutputTokens
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	// 流式响应结束但没有 message_start：保持未写响应状态，让 controller 统一重试。
	if !streamStarted {
		return nil, nil, io.ErrUnexpectedEOF
	}

	if usage.PromptTokens == 0 {
		usage.PromptTokens = info.PromptTokens
	}
	if usage.CompletionTokens == 0 {
		u, _ := service.ResponseText2Usage(responseText.String(), info.UpstreamModelName, usage.PromptTokens)
		usage.CompletionTokens = u.CompletionTokens
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	return usage, nil, nil
}

func nonStreamClaudeCodePassthrough(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if patched, changed := patchClaudeCodeResponseJSON(body); changed {
		body = patched
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	c.Writer.Header().Set("Content-Type", contentType)
	c.Writer.WriteHeader(resp.StatusCode)
	if _, err := c.Writer.Write(body); err != nil {
		return nil, err
	}

	var claudeResp claudecode.ClaudeResponse
	if err := json.Unmarshal(body, &claudeResp); err != nil {
		return nil, err
	}
	if claudeResp.Model != "" {
		info.UpstreamModelName = claudeResp.Model
	}
	usage := &dto.Usage{
		PromptTokens:     claudeResp.Usage.InputTokens,
		CompletionTokens: claudeResp.Usage.OutputTokens,
		TotalTokens:      claudeResp.Usage.InputTokens + claudeResp.Usage.OutputTokens,
	}
	if usage.PromptTokens == 0 {
		usage.PromptTokens = info.PromptTokens
	}
	return usage, nil
}
