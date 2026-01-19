package dto

// OpenAI Responses API usage schema (input/output tokens).
// 参考：https://www.openresponses.org/reference （Usage）
type ResponsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`

	InputTokensDetails  ResponsesInputTokensDetails  `json:"input_tokens_details"`
	OutputTokensDetails ResponsesOutputTokensDetails `json:"output_tokens_details"`
}

type ResponsesInputTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type ResponsesOutputTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

type ResponsesResponseResourceLite struct {
	Model string          `json:"model"`
	Usage *ResponsesUsage `json:"usage"`
}

type ResponsesStreamEventLite struct {
	Type     string                         `json:"type"`
	Response *ResponsesResponseResourceLite `json:"response,omitempty"`
}

func ResponsesUsageToUsage(u *ResponsesUsage) *Usage {
	if u == nil {
		return nil
	}
	return &Usage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      u.TotalTokens,
		PromptTokensDetails: InputTokenDetails{
			CachedTokens: u.InputTokensDetails.CachedTokens,
		},
	}
}
