package codex

import (
	"bufio"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"one-api/common"
	"one-api/constant"
	"one-api/dto"
	relaycommon "one-api/relay/common"
	"one-api/service"
	"strings"
)

// 处理 Codex SSE -> OpenAI SSE
func codexStreamHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.OpenAIErrorWithStatusCode, *dto.Usage) {
	if resp == nil || resp.Body == nil {
		common.LogError(c, "invalid response or response body")
		return service.OpenAIErrorWrapper(io.ErrUnexpectedEOF, "invalid_response", http.StatusInternalServerError), nil
	}

	created := common.GetTimestamp()
	responseID := "chatcmpl-" + common.GetUUID()
	model := info.UpstreamModelName
	var responseText strings.Builder
	var usage *dto.Usage

	// 某些上游（尤其是网关/代理）可能返回 JSON（非 SSE），或不正确标注 Content-Type。
	// 如果不是 SSE，则将整体结果包装成 SSE（至少保证客户端能按 stream=true 消费）。
	reader := bufio.NewReaderSize(resp.Body, 1024*1024)
	if !looksLikeSSE(resp, reader) {
		raw, err := io.ReadAll(reader)
		_ = resp.Body.Close()
		if err != nil {
			return service.OpenAIErrorWrapper(err, "read_response_failed", http.StatusInternalServerError), nil
		}

		service.SetEventStreamHeaders(c)
		info.SetFirstResponseTime()

		text := extractTextFromBody(raw)
		if strings.TrimSpace(text) != "" {
			responseText.WriteString(text)
			chunk := dto.ChatCompletionsStreamResponse{
				Id:      responseID,
				Object:  "chat.completion.chunk",
				Created: created,
				Model:   model,
				Choices: []dto.ChatCompletionsStreamResponseChoice{{
					Index: 0,
					Delta: func() dto.ChatCompletionsStreamResponseChoiceDelta {
						d := dto.ChatCompletionsStreamResponseChoiceDelta{Role: "assistant"}
						d.SetContentString(text)
						return d
					}(),
				}},
			}
			js, _ := json.Marshal(chunk)
			_ = service.StringData(c, string(js))
		}

		usage, _ = service.ResponseText2Usage(responseText.String(), model, info.PromptTokens)
		final := dto.ChatCompletionsStreamResponse{
			Id:      responseID,
			Object:  "chat.completion.chunk",
			Created: created,
			Model:   model,
			Choices: []dto.ChatCompletionsStreamResponseChoice{{
				Index:        0,
				Delta:        dto.ChatCompletionsStreamResponseChoiceDelta{},
				FinishReason: &constant.FinishReasonStop,
			}},
		}
		finalJS, _ := json.Marshal(final)
		_ = service.StringData(c, string(finalJS))
		if info.ShouldIncludeUsage && usage != nil {
			_ = service.ObjectData(c, service.GenerateFinalUsageResponse(responseID, created, model, *usage))
		}
		service.Done(c)
		return nil, usage
	}

	scanner := bufio.NewScanner(reader)
	// Codex 的 SSE 事件可能包含较长字段（如 reasoning.encrypted_content），提升扫描缓冲上限
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 8*1024*1024)
	scanner.Split(bufio.ScanLines)
	dataChan := make(chan string)
	stopChan := make(chan bool)
	go func() {
		for scanner.Scan() {
			line := scanner.Text()
			dataChan <- line
		}
		stopChan <- true
	}()

	service.SetEventStreamHeaders(c)
	isFirst := true

	c.Stream(func(w io.Writer) bool {
		select {
		case raw := <-dataChan:
			if strings.TrimSpace(raw) == "" {
				return true
			}
			if isFirst {
				isFirst = false
				info.SetFirstResponseTime()
			}
			line := strings.TrimSuffix(raw, "\r")
			if !strings.HasPrefix(line, "data:") {
				return true
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "" {
				return true
			}
			if payload == "[DONE]" {
				if usage == nil {
					usage, _ = service.ResponseText2Usage(responseText.String(), model, info.PromptTokens)
				}
				// 最后一块：finish
				final := dto.ChatCompletionsStreamResponse{
					Id:      responseID,
					Object:  "chat.completion.chunk",
					Created: created,
					Model:   model,
					Choices: []dto.ChatCompletionsStreamResponseChoice{{
						Index:        0,
						Delta:        dto.ChatCompletionsStreamResponseChoiceDelta{},
						FinishReason: &constant.FinishReasonStop,
					}},
				}
				js, _ := json.Marshal(final)
				_ = service.StringData(c, string(js))
				if info.ShouldIncludeUsage && usage != nil {
					_ = service.ObjectData(c, service.GenerateFinalUsageResponse(responseID, created, model, *usage))
				}
				service.Done(c)
				return false
			}

			// 解析事件
			var evt map[string]any
			if err := json.Unmarshal(common.StringToByteSlice(payload), &evt); err != nil {
				return true
			}

			if respObj, ok := evt["response"].(map[string]any); ok {
				if id, ok := respObj["id"].(string); ok && id != "" {
					responseID = id
				}
			}

			kind, _ := evt["type"].(string)
			switch kind {
			case "response.output_text.delta":
				delta, _ := evt["delta"].(string)
				if delta == "" {
					return true
				}
				responseText.WriteString(delta)
				chunk := dto.ChatCompletionsStreamResponse{
					Id:      responseID,
					Object:  "chat.completion.chunk",
					Created: created,
					Model:   model,
					Choices: []dto.ChatCompletionsStreamResponseChoice{{
						Index: 0,
						Delta: func() dto.ChatCompletionsStreamResponseChoiceDelta {
							d := dto.ChatCompletionsStreamResponseChoiceDelta{Role: "assistant"}
							d.SetContentString(delta)
							return d
						}(),
					}},
				}
				js, _ := json.Marshal(chunk)
				_ = service.StringData(c, string(js))
				return true

			case "response.output_item.done":
				// function_call 完整结果
				if item, ok := evt["item"].(map[string]any); ok {
					if t, ok := item["type"].(string); ok && t == "function_call" {
						callID := ""
						if s, ok := item["call_id"].(string); ok && s != "" {
							callID = s
						} else if s, ok := item["id"].(string); ok {
							callID = s
						}
						name, _ := item["name"].(string)
						args, _ := item["arguments"].(string)
						if name != "" {
							tool := dto.ToolCall{ID: callID, Type: "function", Function: dto.FunctionCall{Name: name, Arguments: args}}
							tool.SetIndex(0)
							chunk := dto.ChatCompletionsStreamResponse{
								Id:      responseID,
								Object:  "chat.completion.chunk",
								Created: created,
								Model:   model,
								Choices: []dto.ChatCompletionsStreamResponseChoice{{
									Index: 0,
									Delta: dto.ChatCompletionsStreamResponseChoiceDelta{ToolCalls: []dto.ToolCall{tool}},
								}},
							}
							js, _ := json.Marshal(chunk)
							_ = service.StringData(c, string(js))
						}
					}
				}
				return true

			case "response.failed":
				// 上游错误，转发一个错误片段（保持兼容，不中断 SSE，最终由 [DONE] 结束）
				// 这里仅日志记录，不额外注入错误 chunk，避免客户端误解析
				return true

			case "response.completed":
				final := dto.ChatCompletionsStreamResponse{
					Id:      responseID,
					Object:  "chat.completion.chunk",
					Created: created,
					Model:   model,
					Choices: []dto.ChatCompletionsStreamResponseChoice{{
						Index:        0,
						Delta:        dto.ChatCompletionsStreamResponseChoiceDelta{},
						FinishReason: &constant.FinishReasonStop,
					}},
				}
				js, _ := json.Marshal(final)
				_ = service.StringData(c, string(js))
				// 不直接结束，等待 [DONE]
				return true
			}
			return true
		case <-stopChan:
			if usage == nil {
				usage, _ = service.ResponseText2Usage(responseText.String(), model, info.PromptTokens)
			}
			if info.ShouldIncludeUsage && usage != nil {
				_ = service.ObjectData(c, service.GenerateFinalUsageResponse(responseID, created, model, *usage))
			}
			service.Done(c)
			return false
		}
	})

	_ = resp.Body.Close()

	// 计算 usage
	if usage == nil {
		usage, _ = service.ResponseText2Usage(responseText.String(), model, info.PromptTokens)
	}
	return nil, usage
}

// 非流式：聚合 SSE 并回放为一次性 OpenAI 响应
func codexHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.OpenAIErrorWithStatusCode, *dto.Usage) {
	if resp == nil || resp.Body == nil {
		return service.OpenAIErrorWrapper(io.ErrUnexpectedEOF, "invalid_response", http.StatusInternalServerError), nil
	}
	defer resp.Body.Close()

	created := common.GetTimestamp()
	responseID := "chatcmpl-" + common.GetUUID()
	model := info.UpstreamModelName
	var sb strings.Builder
	var toolCalls []dto.ToolCall

	scanner := bufio.NewScanner(resp.Body)
	// 提升缓冲，避免 Codex 大字段导致单行过长
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 8*1024*1024)
	scanner.Split(bufio.ScanLines)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			if payload == "[DONE]" {
				break
			}
			continue
		}
		var evt map[string]any
		if err := json.Unmarshal(common.StringToByteSlice(payload), &evt); err != nil {
			continue
		}
		if respObj, ok := evt["response"].(map[string]any); ok {
			if id, ok := respObj["id"].(string); ok && id != "" {
				responseID = id
			}
		}
		kind, _ := evt["type"].(string)
		switch kind {
		case "response.output_text.delta":
			if delta, ok := evt["delta"].(string); ok && delta != "" {
				sb.WriteString(delta)
			}
		case "response.output_item.done":
			if item, ok := evt["item"].(map[string]any); ok {
				if t, _ := item["type"].(string); t == "function_call" {
					callID := ""
					if s, ok := item["call_id"].(string); ok && s != "" {
						callID = s
					} else if s, ok := item["id"].(string); ok {
						callID = s
					}
					name, _ := item["name"].(string)
					args, _ := item["arguments"].(string)
					if name != "" {
						tc := dto.ToolCall{ID: callID, Type: "function", Function: dto.FunctionCall{Name: name, Arguments: args}}
						toolCalls = append(toolCalls, tc)
					}
				}
			}
		case "response.completed":
			// 结束
		}
	}

	usage, _ := service.ResponseText2Usage(sb.String(), model, info.PromptTokens)

	// 组织 OpenAI 标准响应
	var msg dto.Message
	msg.Role = "assistant"
	msg.SetStringContent(sb.String())
	if len(toolCalls) > 0 {
		msg.SetToolCalls(toolCalls)
	}

	openaiResp := dto.TextResponse{
		Id:      responseID,
		Object:  "chat.completion",
		Created: created,
		Model:   model,
		Choices: []dto.OpenAITextResponseChoice{{
			Index:        0,
			Message:      msg,
			FinishReason: "stop",
		}},
		Usage: *usage,
	}

	js, err := json.Marshal(openaiResp)
	if err != nil {
		return service.OpenAIErrorWrapper(err, "marshal_response_body_failed", http.StatusInternalServerError), nil
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.WriteHeader(http.StatusOK)
	_, _ = c.Writer.Write(js)
	return nil, usage
}
