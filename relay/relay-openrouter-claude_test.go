package relay

import (
	"encoding/json"
	"testing"

	"one-api/relay/channel/claudecode"
)

func TestConvertClaudeThinkingToReasoningDisabledWinsOverOutputConfig(t *testing.T) {
	bodyMap := map[string]json.RawMessage{
		"thinking":      json.RawMessage(`{"type":"disabled"}`),
		"output_config": json.RawMessage(`{"effort":"max"}`),
	}

	reasoning := convertClaudeThinkingToReasoning(bodyMap)

	if reasoning == nil {
		t.Fatal("reasoning is nil")
	}
	if reasoning["effort"] != "none" {
		t.Fatalf("expected disabled thinking to become effort none, got: %#v", reasoning)
	}
}

func TestFilterOpenRouterRedundantSystemKeepsUserSystemOnly(t *testing.T) {
	filtered := filterOpenRouterRedundantSystem([]claudecode.ClaudeContent{
		{Type: "text", Text: claudeCodeSystemBillingHeader},
		{Type: "text", Text: claudeCodeSystemCLIKeyword},
		{Type: "text", Text: "keep this"},
	})

	if len(filtered) != 1 {
		t.Fatalf("unexpected filtered system length: %d", len(filtered))
	}
	if filtered[0].Text != "keep this" {
		t.Fatalf("unexpected filtered system text: %q", filtered[0].Text)
	}
}
