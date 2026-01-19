package openai

import (
	relaycommon "one-api/relay/common"
	"testing"
)

func TestConvertResponsesRequest_ReasoningAndTokens(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{}

	req := map[string]any{
		"model":       "o1-high",
		"max_tokens":  128,
		"temperature": 0.2,
		"input": []any{
			map[string]any{
				"type": "message",
				"role": "system",
				"content": []any{
					map[string]any{"type": "input_text", "text": "你是一个助手"},
				},
			},
		},
	}

	outAny, err := a.ConvertResponsesRequest(nil, info, req)
	if err != nil {
		t.Fatalf("ConvertResponsesRequest error: %v", err)
	}
	out := outAny.(map[string]any)

	if out["model"] != "o1" {
		t.Fatalf("expected model o1, got %v", out["model"])
	}
	if _, ok := out["temperature"]; ok {
		t.Fatalf("expected temperature removed for o-series")
	}
	if out["max_output_tokens"] == nil {
		t.Fatalf("expected max_output_tokens set")
	}
	if _, ok := out["max_tokens"]; ok {
		t.Fatalf("expected max_tokens removed")
	}

	reasoning, _ := out["reasoning"].(map[string]any)
	if reasoning == nil || reasoning["effort"] != "high" {
		t.Fatalf("expected reasoning.effort=high, got %v", reasoning)
	}

	inputArr, _ := out["input"].([]any)
	first, _ := inputArr[0].(map[string]any)
	if first["role"] != "developer" {
		t.Fatalf("expected first role developer, got %v", first["role"])
	}
}

func TestConvertResponsesRequest_Gpt5KeepTemperature(t *testing.T) {
	a := &Adaptor{}
	info := &relaycommon.RelayInfo{}

	req := map[string]any{
		"model":       "gpt-5.2",
		"temperature": 0.7,
		"input":       "hi",
	}

	outAny, err := a.ConvertResponsesRequest(nil, info, req)
	if err != nil {
		t.Fatalf("ConvertResponsesRequest error: %v", err)
	}
	out := outAny.(map[string]any)
	if out["model"] != "gpt-5.2" {
		t.Fatalf("expected model gpt-5.2, got %v", out["model"])
	}
	if _, ok := out["temperature"]; !ok {
		t.Fatalf("expected temperature kept for gpt-5.2")
	}
}
