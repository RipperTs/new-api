package service

import (
	"one-api/common"
	relaycommon "one-api/relay/common"
	"testing"
)

func init() {
	InitTokenEncoders()
}

func TestCountTokenResponsesRequest(t *testing.T) {
	info := &relaycommon.RelayInfo{
		ChannelType: common.ChannelTypeOpenAI,
	}
	req := map[string]any{
		"model": "gpt-4o-mini",
		"input": []any{
			map[string]any{
				"type": "message",
				"role": "user",
				"content": []any{
					map[string]any{"type": "input_text", "text": "你好"},
					map[string]any{"type": "input_image", "image_url": "https://example.com/a.png", "detail": "low"},
				},
			},
		},
		"tools": []any{
			map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        "get_weather",
					"description": "get weather by city",
					"parameters": map[string]any{
						"type": "object",
					},
				},
			},
		},
	}

	tokens, err := CountTokenResponsesRequest(info, req)
	if err != nil {
		t.Fatalf("CountTokenResponsesRequest error: %v", err)
	}
	if tokens <= 0 {
		t.Fatalf("expected tokens > 0, got %d", tokens)
	}
}
