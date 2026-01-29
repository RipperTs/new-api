package dto

import (
	"encoding/json"
)

type ResponseFormat struct {
	Type       string            `json:"type,omitempty"`
	JsonSchema *FormatJsonSchema `json:"json_schema,omitempty"`
}

type FormatJsonSchema struct {
	Description string `json:"description,omitempty"`
	Name        string `json:"name"`
	Schema      any    `json:"schema,omitempty"`
	Strict      any    `json:"strict,omitempty"`
}

type GeneralOpenAIRequest struct {
	Model               string          `json:"model,omitempty"`
	Messages            []Message       `json:"messages,omitempty"`
	Prompt              any             `json:"prompt,omitempty"`
	Stream              bool            `json:"stream,omitempty"`
	StreamOptions       *StreamOptions  `json:"stream_options,omitempty"`
	MaxTokens           uint            `json:"max_tokens,omitempty"`
	MaxCompletionTokens uint            `json:"max_completion_tokens,omitempty"`
	ReasoningEffort     string          `json:"reasoning_effort,omitempty"`
	Temperature         *float64        `json:"temperature,omitempty"`
	TopP                float64         `json:"top_p,omitempty"`
	TopK                int             `json:"top_k,omitempty"`
	Stop                any             `json:"stop,omitempty"`
	N                   int             `json:"n,omitempty"`
	Input               any             `json:"input,omitempty"`
	Instruction         string          `json:"instruction,omitempty"`
	Size                string          `json:"size,omitempty"`
	Functions           any             `json:"functions,omitempty"`
	FrequencyPenalty    float64         `json:"frequency_penalty,omitempty"`
	PresencePenalty     float64         `json:"presence_penalty,omitempty"`
	ResponseFormat      *ResponseFormat `json:"response_format,omitempty"`
	EncodingFormat      any             `json:"encoding_format,omitempty"`
	Seed                float64         `json:"seed,omitempty"`
	Tools               []ToolCall      `json:"tools,omitempty"`
	ToolChoice          any             `json:"tool_choice,omitempty"`
	User                string          `json:"user,omitempty"`
	LogProbs            bool            `json:"logprobs,omitempty"`
	TopLogProbs         int             `json:"top_logprobs,omitempty"`
	Dimensions          int             `json:"dimensions,omitempty"`
	Modalities          any             `json:"modalities,omitempty"`
	Audio               any             `json:"audio,omitempty"`
	Provider            any             `json:"provider,omitempty"`
}

type OpenAITools struct {
	Type     string         `json:"type"`
	Function OpenAIFunction `json:"function"`
}

type OpenAIFunction struct {
	Description string `json:"description,omitempty"`
	Name        string `json:"name"`
	Parameters  any    `json:"parameters,omitempty"`
}

type StreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

func (r GeneralOpenAIRequest) GetMaxTokens() int {
	return int(r.MaxTokens)
}

func (r GeneralOpenAIRequest) ParseInput() []string {
	if r.Input == nil {
		return nil
	}
	var input []string
	switch r.Input.(type) {
	case string:
		input = []string{r.Input.(string)}
	case []any:
		input = make([]string, 0, len(r.Input.([]any)))
		for _, item := range r.Input.([]any) {
			if str, ok := item.(string); ok {
				input = append(input, str)
			}
		}
	}
	return input
}

type Message struct {
	Role             string           `json:"role"`
	Content          json.RawMessage  `json:"content"`
	ReasoningContent *json.RawMessage `json:"reasoning_content,omitempty"`
	Reasoning        *json.RawMessage `json:"reasoning,omitempty"`
	Name             *string          `json:"name,omitempty"`
	ToolCalls        json.RawMessage  `json:"tool_calls,omitempty"`
	ToolCallId       string           `json:"tool_call_id,omitempty"`
}

type MediaContent struct {
	Type       string `json:"type"`
	Text       string `json:"text"`
	ImageUrl   any    `json:"image_url,omitempty"`
	InputAudio any    `json:"input_audio,omitempty"`
	FileData   any    `json:"file_data,omitempty"`
}

type MessageImageUrl struct {
	Url    string `json:"url"`
	Detail string `json:"detail"`
}

type MessageInputAudio struct {
	Data   string `json:"data"` //base64
	Format string `json:"format"`
}

// MessageFileData 用于兼容 Gemini 的 file_data/file_uri 输入（例如 YouTube URL）。
// 注意：该字段并非 OpenAI 官方 Chat Completions 的标准字段，仅用于本项目的可选扩展。
type MessageFileData struct {
	FileUri  string `json:"file_uri"`
	MimeType string `json:"mime_type,omitempty"`
}

const (
	ContentTypeText       = "text"
	ContentTypeImageURL   = "image_url"
	ContentTypeInputAudio = "input_audio"
	ContentTypeFileData   = "file_data"
)

func (m *Message) ParseToolCalls() []ToolCall {
	if m.ToolCalls == nil {
		return nil
	}
	var toolCalls []ToolCall
	if err := json.Unmarshal(m.ToolCalls, &toolCalls); err == nil {
		return toolCalls
	}
	return toolCalls
}

func (m *Message) SetToolCalls(toolCalls any) {
	toolCallsJson, _ := json.Marshal(toolCalls)
	m.ToolCalls = toolCallsJson
}

func (m *Message) StringContent() string {
	var stringContent string
	if err := json.Unmarshal(m.Content, &stringContent); err == nil {
		return stringContent
	}
	return string(m.Content)
}

func (m *Message) SetStringContent(content string) {
	jsonContent, _ := json.Marshal(content)
	m.Content = jsonContent
}

func (m *Message) IsStringContent() bool {
	var stringContent string
	if err := json.Unmarshal(m.Content, &stringContent); err == nil {
		return true
	}
	return false
}

// EnsureReasoningCompatibility 确保 reasoning 和 reasoning_content 的兼容性
func (m *Message) EnsureReasoningCompatibility() {
	// 如果 reasoning 有值但 reasoning_content 没有值，复制 reasoning 到 reasoning_content
	if m.Reasoning != nil && m.ReasoningContent == nil {
		m.ReasoningContent = m.Reasoning
	}
	// 如果 reasoning_content 有值但 reasoning 没有值，复制 reasoning_content 到 reasoning
	if m.ReasoningContent != nil && m.Reasoning == nil {
		m.Reasoning = m.ReasoningContent
	}
}

func (m *Message) ParseContent() []MediaContent {
	var contentList []MediaContent
	var stringContent string
	if err := json.Unmarshal(m.Content, &stringContent); err == nil {
		contentList = append(contentList, MediaContent{
			Type: ContentTypeText,
			Text: stringContent,
		})
		return contentList
	}
	var arrayContent []json.RawMessage
	if err := json.Unmarshal(m.Content, &arrayContent); err == nil {
		for _, contentItem := range arrayContent {
			var contentMap map[string]any
			if err := json.Unmarshal(contentItem, &contentMap); err != nil {
				continue
			}
			switch contentMap["type"] {
			case ContentTypeText:
				if subStr, ok := contentMap["text"].(string); ok {
					contentList = append(contentList, MediaContent{
						Type: ContentTypeText,
						Text: subStr,
					})
				}
			case ContentTypeImageURL:
				if subObj, ok := contentMap["image_url"].(map[string]any); ok {
					detail, ok := subObj["detail"]
					if ok {
						subObj["detail"] = detail.(string)
					} else {
						subObj["detail"] = "high"
					}
					contentList = append(contentList, MediaContent{
						Type: ContentTypeImageURL,
						ImageUrl: MessageImageUrl{
							Url:    subObj["url"].(string),
							Detail: subObj["detail"].(string),
						},
					})
				} else if url, ok := contentMap["image_url"].(string); ok {
					contentList = append(contentList, MediaContent{
						Type: ContentTypeImageURL,
						ImageUrl: MessageImageUrl{
							Url:    url,
							Detail: "high",
						},
					})
				}
			case ContentTypeInputAudio:
				if subObj, ok := contentMap["input_audio"].(map[string]any); ok {
					contentList = append(contentList, MediaContent{
						Type: ContentTypeInputAudio,
						InputAudio: MessageInputAudio{
							Data:   subObj["data"].(string),
							Format: subObj["format"].(string),
						},
					})
				}
			case ContentTypeFileData:
				// 兼容两种写法：
				// 1) {"type":"file_data","file_data":{"file_uri":"...","mime_type":"..."}}
				// 2) {"type":"file_data","file_data":"..."} 或 {"type":"file_data","file_uri":"..."}
				fd := MessageFileData{}
				if subObj, ok := contentMap["file_data"].(map[string]any); ok {
					if s, ok := subObj["file_uri"].(string); ok {
						fd.FileUri = s
					} else if s, ok := subObj["fileUri"].(string); ok {
						fd.FileUri = s
					}
					if s, ok := subObj["mime_type"].(string); ok {
						fd.MimeType = s
					} else if s, ok := subObj["mimeType"].(string); ok {
						fd.MimeType = s
					}
				} else if s, ok := contentMap["file_data"].(string); ok {
					fd.FileUri = s
				} else if s, ok := contentMap["file_uri"].(string); ok {
					fd.FileUri = s
				} else if s, ok := contentMap["fileUri"].(string); ok {
					fd.FileUri = s
				}
				if fd.FileUri != "" {
					contentList = append(contentList, MediaContent{
						Type:     ContentTypeFileData,
						FileData: fd,
					})
				}
			}
		}
		return contentList
	}
	return nil
}
