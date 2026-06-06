package relay

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"one-api/common"
	"one-api/constant"
	"one-api/dto"
	relaycommon "one-api/relay/common"
	"one-api/service"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type openAIChatToolBlockState struct {
	blockIndex int
	id         string
	name       string
}

func streamOpenAIChatToClaude(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *dto.OpenAIErrorWithStatusCode, error) {
	defer resp.Body.Close()

	usage := &dto.Usage{}
	var responseText strings.Builder
	messageID := "msg_" + common.GetUUID()
	modelName := info.UpstreamModelName
	started := false
	stopReason := "end_turn"
	hasToolCall := false
	nextBlockIndex := 0
	openBlockIndexes := make([]int, 0, 8)
	textBlockIndex := -1
	thinkingBlockIndex := -1
	thinkingHasDelta := false
	thinkingHasSignature := false
	toolBlocks := make(map[int]*openAIChatToolBlockState)
	sawAnyChunk := false

	startMessage := func() error {
		if started {
			return nil
		}
		started = true
		payload := map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":            messageID,
				"type":          "message",
				"role":          "assistant",
				"content":       make([]any, 0),
				"model":         modelName,
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage": map[string]any{
					"input_tokens":  usage.PromptTokens,
					"output_tokens": 0,
				},
			},
		}
		return writeClaudeStreamEvent(c, "message_start", payload)
	}
	writeTextDelta := func(text string) error {
		content := stripOpenAIChatVisibleControlTokens(text)
		if strings.TrimSpace(content) == "" {
			return nil
		}
		responseText.WriteString(content)
		if textBlockIndex < 0 {
			textBlockIndex = nextBlockIndex
			nextBlockIndex++
			openBlockIndexes = append(openBlockIndexes, textBlockIndex)
			startPayload := map[string]any{
				"type":  "content_block_start",
				"index": textBlockIndex,
				"content_block": map[string]any{
					"type": "text",
					"text": "",
				},
			}
			if err := writeClaudeStreamEvent(c, "content_block_start", startPayload); err != nil {
				return err
			}
		}
		deltaPayload := map[string]any{
			"type":  "content_block_delta",
			"index": textBlockIndex,
			"delta": map[string]any{
				"type": "text_delta",
				"text": content,
			},
		}
		return writeClaudeStreamEvent(c, "content_block_delta", deltaPayload)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	streamingTimeout := time.Duration(constant.StreamingTimeout) * time.Second
	if streamingTimeout <= 0 {
		streamingTimeout = 60 * time.Second
	}
	ticker := time.NewTicker(streamingTimeout)
	defer ticker.Stop()

	lineCh := make(chan string, 64)
	scanErrCh := make(chan error, 1)
	stopReaderCh := make(chan struct{})
	defer close(stopReaderCh)

	go func() {
		defer close(lineCh)
		for scanner.Scan() {
			line := scanner.Text()
			select {
			case lineCh <- line:
			case <-stopReaderCh:
				return
			}
		}
		scanErrCh <- scanner.Err()
	}()

streamReadLoop:
	for {
		select {
		case <-ticker.C:
			_ = resp.Body.Close()
			return nil, nil, errors.New("openai chat stream timeout")
		case line, ok := <-lineCh:
			if !ok {
				if err := <-scanErrCh; err != nil {
					return nil, nil, err
				}
				goto streamReadDone
			}
			info.SetFirstResponseTime()
			ticker.Reset(streamingTimeout)
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" {
				continue
			}
			if data == "[DONE]" {
				break streamReadLoop
			}
			sawAnyChunk = true

			var errorChunk map[string]any
			if err := json.Unmarshal([]byte(data), &errorChunk); err == nil {
				if errObj, ok := errorChunk["error"]; ok && errObj != nil {
					errType, errMsg := parseOpenAIStreamError(errObj)
					openaiErr := openAIChatClaudeStreamError(errType, errMsg)
					if !started {
						return nil, openaiErr, nil
					}
					payload := map[string]any{
						"type": "error",
						"error": map[string]any{
							"type":    errType,
							"message": errMsg,
						},
					}
					if writeErr := writeClaudeStreamEvent(c, "error", payload); writeErr != nil {
						return nil, nil, writeErr
					}
					return nil, nil, errors.New("openai chat upstream stream error: " + errMsg)
				}
			}

			var chunk dto.ChatCompletionsStreamResponse
			normalizedData := normalizeOpenAIChatStreamChunkData(data)
			refusalTexts := extractOpenAIChatRefusalTexts(normalizedData)
			if err := json.Unmarshal(normalizedData, &chunk); err != nil {
				if len(refusalTexts) > 0 {
					if err = startMessage(); err != nil {
						return nil, nil, err
					}
					for _, refusalText := range refusalTexts {
						if err = writeTextDelta(refusalText); err != nil {
							return nil, nil, err
						}
					}
				}
				continue
			}
			if strings.TrimSpace(chunk.Id) != "" {
				messageID = chunk.Id
			}
			if strings.TrimSpace(chunk.Model) != "" {
				modelName = chunk.Model
				info.UpstreamModelName = modelName
			}

			if chunk.Usage != nil {
				usage.PromptTokens = chunk.Usage.PromptTokens
				usage.CompletionTokens = chunk.Usage.CompletionTokens
				usage.TotalTokens = chunk.Usage.TotalTokens
			}

			for _, choice := range chunk.Choices {
				delta := choice.Delta

				if reasoningText := stripOpenAIChatVisibleControlTokens(extractReasoningFromDelta(&delta)); strings.TrimSpace(reasoningText) != "" {
					if err := startMessage(); err != nil {
						return nil, nil, err
					}
					if thinkingBlockIndex < 0 {
						thinkingBlockIndex = nextBlockIndex
						nextBlockIndex++
						openBlockIndexes = append(openBlockIndexes, thinkingBlockIndex)
						startPayload := map[string]any{
							"type":  "content_block_start",
							"index": thinkingBlockIndex,
							"content_block": map[string]any{
								"type":     "thinking",
								"thinking": "",
							},
						}
						if err := writeClaudeStreamEvent(c, "content_block_start", startPayload); err != nil {
							return nil, nil, err
						}
					}
					deltaPayload := map[string]any{
						"type":  "content_block_delta",
						"index": thinkingBlockIndex,
						"delta": map[string]any{
							"type":     "thinking_delta",
							"thinking": reasoningText,
						},
					}
					if err := writeClaudeStreamEvent(c, "content_block_delta", deltaPayload); err != nil {
						return nil, nil, err
					}
					thinkingHasDelta = true
				}

				if content := delta.GetContentString(); content != "" {
					if err := startMessage(); err != nil {
						return nil, nil, err
					}
					if err := writeTextDelta(content); err != nil {
						return nil, nil, err
					}
				}
				for _, refusalText := range refusalTexts {
					if err := startMessage(); err != nil {
						return nil, nil, err
					}
					if err := writeTextDelta(refusalText); err != nil {
						return nil, nil, err
					}
				}

				if len(delta.ToolCalls) > 0 {
					if err := startMessage(); err != nil {
						return nil, nil, err
					}
					hasToolCall = true
					for _, toolCall := range delta.ToolCalls {
						toolCallIndex := 0
						if toolCall.Index != nil {
							toolCallIndex = *toolCall.Index
						}
						state, ok := toolBlocks[toolCallIndex]
						if !ok {
							blockIndex := nextBlockIndex
							nextBlockIndex++
							openBlockIndexes = append(openBlockIndexes, blockIndex)
							toolID := strings.TrimSpace(toolCall.ID)
							if toolID == "" {
								toolID = fmt.Sprintf("call_%s_%d", common.GetUUID(), toolCallIndex)
							}
							toolName := strings.TrimSpace(toolCall.Function.Name)
							if toolName == "" {
								toolName = fmt.Sprintf("tool_%d", toolCallIndex)
							}
							state = &openAIChatToolBlockState{
								blockIndex: blockIndex,
								id:         toolID,
								name:       toolName,
							}
							toolBlocks[toolCallIndex] = state
							startPayload := map[string]any{
								"type":  "content_block_start",
								"index": state.blockIndex,
								"content_block": map[string]any{
									"type":  "tool_use",
									"id":    state.id,
									"name":  state.name,
									"input": map[string]any{},
								},
							}
							if err := writeClaudeStreamEvent(c, "content_block_start", startPayload); err != nil {
								return nil, nil, err
							}
						}
						if strings.TrimSpace(toolCall.Function.Name) != "" {
							state.name = toolCall.Function.Name
						}
						if strings.TrimSpace(toolCall.ID) != "" {
							state.id = toolCall.ID
						}
						if strings.TrimSpace(toolCall.Function.Arguments) != "" {
							responseText.WriteString(state.name)
							responseText.WriteString(toolCall.Function.Arguments)
							deltaPayload := map[string]any{
								"type":  "content_block_delta",
								"index": state.blockIndex,
								"delta": map[string]any{
									"type":         "input_json_delta",
									"partial_json": toolCall.Function.Arguments,
								},
							}
							if err := writeClaudeStreamEvent(c, "content_block_delta", deltaPayload); err != nil {
								return nil, nil, err
							}
						}
					}
				}

				if choice.FinishReason != nil {
					stopReason = mapOpenAIFinishReasonToClaude(*choice.FinishReason)
				}
			}
		}
	}

streamReadDone:
	if !sawAnyChunk {
		return nil, nil, io.ErrUnexpectedEOF
	}
	if !started {
		if err := startMessage(); err != nil {
			return nil, nil, err
		}
	}
	if stopReason == "end_turn" && hasToolCall {
		stopReason = "tool_use"
	}

	if thinkingBlockIndex >= 0 && thinkingHasDelta && !thinkingHasSignature {
		signaturePayload := map[string]any{
			"type":  "content_block_delta",
			"index": thinkingBlockIndex,
			"delta": map[string]any{
				"type":      "signature_delta",
				"signature": fmt.Sprintf("%d", common.GetTimestamp()),
			},
		}
		if err := writeClaudeStreamEvent(c, "content_block_delta", signaturePayload); err != nil {
			return nil, nil, err
		}
		thinkingHasSignature = true
	}

	for _, idx := range openBlockIndexes {
		stopPayload := map[string]any{
			"type":  "content_block_stop",
			"index": idx,
		}
		if err := writeClaudeStreamEvent(c, "content_block_stop", stopPayload); err != nil {
			return nil, nil, err
		}
	}

	if usage.PromptTokens == 0 {
		usage.PromptTokens = info.PromptTokens
	}
	if usage.CompletionTokens == 0 {
		u, _ := service.ResponseText2Usage(responseText.String(), info.UpstreamModelName, usage.PromptTokens)
		usage.CompletionTokens = u.CompletionTokens
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	} else if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}

	messageDelta := map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]any{
			"input_tokens":  usage.PromptTokens,
			"output_tokens": usage.CompletionTokens,
		},
	}
	if err := writeClaudeStreamEvent(c, "message_delta", messageDelta); err != nil {
		return nil, nil, err
	}

	messageStop := map[string]any{
		"type": "message_stop",
	}
	if err := writeClaudeStreamEvent(c, "message_stop", messageStop); err != nil {
		return nil, nil, err
	}
	return usage, nil, nil
}

func writeClaudeStreamEvent(c *gin.Context, event string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if !c.Writer.Written() {
		service.SetEventStreamHeaders(c)
		c.Writer.WriteHeader(http.StatusOK)
	}
	if event != "" {
		if _, err = c.Writer.Write([]byte("event: " + event + "\n")); err != nil {
			return err
		}
	}
	if _, err = c.Writer.Write([]byte("data: " + string(data) + "\n\n")); err != nil {
		return err
	}
	if f, ok := c.Writer.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}
