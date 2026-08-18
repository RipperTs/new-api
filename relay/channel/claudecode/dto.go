package claudecode

import (
	"encoding/json"
	"fmt"
	"strings"
)

type ClaudeMetadata struct {
	UserId string `json:"user_id"`
}

type ClaudeMediaMessage struct {
	Type        string               `json:"type"`
	Text        string               `json:"text,omitempty"`
	Source      *ClaudeMessageSource `json:"source,omitempty"`
	Usage       *ClaudeUsage         `json:"usage,omitempty"`
	StopReason  *string              `json:"stop_reason,omitempty"`
	PartialJson string               `json:"partial_json,omitempty"`
	// tool_calls
	Id        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Input     any    `json:"input,omitempty"`
	Content   any    `json:"content,omitempty"`
	ToolUseId string `json:"tool_use_id,omitempty"`
}

type ClaudeMessageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type ClaudeMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

type InputSchema struct {
	Type       string `json:"type"`
	Properties any    `json:"properties,omitempty"`
	Required   any    `json:"required,omitempty"`
}

type ClaudeRequest struct {
	Model             string                 `json:"model"`
	Messages          []ClaudeMessage        `json:"messages,omitempty"`
	System            []ClaudeContent        `json:"system,omitempty"`
	Tools             []Tool                 `json:"tools"`
	Metadata          map[string]interface{} `json:"metadata,omitempty"`
	MaxTokens         uint                   `json:"max_tokens,omitempty"`
	Stream            bool                   `json:"stream,omitempty"`
	Prompt            string                 `json:"prompt,omitempty"`
	MaxTokensToSample uint                   `json:"max_tokens_to_sample,omitempty"`
	StopSequences     []string               `json:"stop_sequences,omitempty"`
	Temperature       *float64               `json:"temperature,omitempty"`
	TopP              float64                `json:"top_p,omitempty"`
	TopK              int                    `json:"top_k,omitempty"`
	ToolChoice        any                    `json:"tool_choice,omitempty"`
}

func (r *ClaudeRequest) UnmarshalJSON(data []byte) error {
	type claudeRequestRaw struct {
		Model             string                 `json:"model"`
		Prompt            string                 `json:"prompt,omitempty"`
		System            json.RawMessage        `json:"system,omitempty"`
		Messages          []ClaudeMessage        `json:"messages,omitempty"`
		MaxTokens         uint                   `json:"max_tokens,omitempty"`
		MaxTokensToSample uint                   `json:"max_tokens_to_sample,omitempty"`
		StopSequences     []string               `json:"stop_sequences,omitempty"`
		Temperature       *float64               `json:"temperature,omitempty"`
		TopP              float64                `json:"top_p,omitempty"`
		TopK              int                    `json:"top_k,omitempty"`
		Metadata          map[string]interface{} `json:"metadata,omitempty"`
		Stream            bool                   `json:"stream,omitempty"`
		Tools             []Tool                 `json:"tools,omitempty"`
		ToolChoice        any                    `json:"tool_choice,omitempty"`
	}

	var raw claudeRequestRaw
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	system, err := parseClaudeSystemField(raw.System)
	if err != nil {
		return err
	}

	*r = ClaudeRequest{
		Model:             raw.Model,
		Prompt:            raw.Prompt,
		System:            system,
		Messages:          raw.Messages,
		MaxTokens:         raw.MaxTokens,
		MaxTokensToSample: raw.MaxTokensToSample,
		StopSequences:     raw.StopSequences,
		Temperature:       raw.Temperature,
		TopP:              raw.TopP,
		TopK:              raw.TopK,
		Metadata:          raw.Metadata,
		Stream:            raw.Stream,
		Tools:             raw.Tools,
		ToolChoice:        raw.ToolChoice,
	}
	return nil
}

func parseClaudeSystemField(raw json.RawMessage) ([]ClaudeContent, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}

	switch trimmed[0] {
	case '"':
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, err
		}
		return []ClaudeContent{{Type: "text", Text: text}}, nil
	case '{':
		block, err := parseClaudeSystemBlock(raw)
		if err != nil {
			return nil, err
		}
		return []ClaudeContent{block}, nil
	case '[':
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, err
		}
		blocks := make([]ClaudeContent, 0, len(items))
		for _, item := range items {
			itemTrim := strings.TrimSpace(string(item))
			if itemTrim == "" || itemTrim == "null" {
				continue
			}
			block, err := parseClaudeSystemBlock(item)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, block)
		}
		return blocks, nil
	default:
		return nil, fmt.Errorf("invalid system field: expected string, object, or array")
	}
}

func parseClaudeSystemBlock(raw json.RawMessage) (ClaudeContent, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ClaudeContent{}, nil
	}

	switch trimmed[0] {
	case '"':
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return ClaudeContent{}, err
		}
		return ClaudeContent{
			Type: "text",
			Text: text,
		}, nil
	case '{':
		var block ClaudeContent
		if err := json.Unmarshal(raw, &block); err != nil {
			return ClaudeContent{}, err
		}
		if strings.TrimSpace(block.Type) == "" && block.Text != "" {
			block.Type = "text"
		}
		return block, nil
	default:
		return ClaudeContent{}, fmt.Errorf("invalid system block: expected string or object")
	}
}

type ClaudeContent struct {
	Type         string        `json:"type"`
	Text         string        `json:"text,omitempty"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

type CacheControl struct {
	Type string `json:"type"`
}

type ClaudeError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type ClaudeResponse struct {
	Id           string               `json:"id"`
	Type         string               `json:"type"`
	Content      []ClaudeMediaMessage `json:"content"`
	Completion   string               `json:"completion"`
	StopReason   string               `json:"stop_reason"`
	Model        string               `json:"model"`
	Error        ClaudeError          `json:"error"`
	Usage        ClaudeUsage          `json:"usage"`
	Index        int                  `json:"index"` // stream only
	ContentBlock *ClaudeMediaMessage  `json:"content_block"`
	Delta        *ClaudeMediaMessage  `json:"delta"`   // stream only
	Message      *ClaudeResponse      `json:"message"` // stream only: message_start
}

type ClaudeUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}
