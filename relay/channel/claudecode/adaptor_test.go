package claudecode

import (
	"encoding/json"
	"testing"
)

func TestSanitizeEmptyClaudeTextBlocks(t *testing.T) {
	body := []byte(`{
		"messages":[
			{
				"role":"user",
				"content":[
					{"type":"text","text":""},
					{"type":"text","text":"   "},
					{"type":"text","text":"keep"},
					{"type":"image","source":{"type":"base64","media_type":"image/png","data":"abc"}}
				]
			},
			{
				"role":"assistant",
				"content":[
					{"type":"thinking","thinking":"","signature":"sig"},
					{"type":"tool_use","id":"tool_1","name":"read","input":{}},
					{"type":"text","text":"\n\t"}
				]
			},
			{
				"role":"user",
				"content":[
					{"type":"tool_result","tool_use_id":"tool_1","content":[
						{"type":"text","text":""},
						{"type":"text","text":"result"}
					]},
					{"type":"tool_result","tool_use_id":"tool_2","content":[
						{"type":"text","text":""}
					]},
					{"type":"tool_result","tool_use_id":"tool_3","content":"  "}
				]
			},
			{
				"role":"user",
				"content":[
					{"type":"tool_result","tool_use_id":"tool_4","content":[]}
				]
			},
			{
				"role":"assistant",
				"content":[{"type":"text","text":"  "}]
			}
		]
	}`)

	patched := SanitizeEmptyClaudeTextBlocks(body)

	if string(patched) == string(body) {
		t.Fatal("request body should be patched")
	}
	var root map[string]any
	if err := json.Unmarshal(patched, &root); err != nil {
		t.Fatalf("unmarshal patched body: %v", err)
	}
	messages := root["messages"].([]any)

	firstContent := messages[0].(map[string]any)["content"].([]any)
	if len(firstContent) != 2 {
		t.Fatalf("expected empty text blocks removed from first message, got %#v", firstContent)
	}
	if firstContent[0].(map[string]any)["text"] != "keep" {
		t.Fatalf("expected non-empty text block to be kept, got %#v", firstContent)
	}
	if firstContent[1].(map[string]any)["type"] != "image" {
		t.Fatalf("expected image block to be kept, got %#v", firstContent)
	}

	secondContent := messages[1].(map[string]any)["content"].([]any)
	if len(secondContent) != 2 {
		t.Fatalf("expected thinking and tool_use blocks kept, got %#v", secondContent)
	}
	if secondContent[0].(map[string]any)["type"] != "thinking" || secondContent[1].(map[string]any)["type"] != "tool_use" {
		t.Fatalf("expected thinking and tool_use blocks kept, got %#v", secondContent)
	}

	thirdContent := messages[2].(map[string]any)["content"].([]any)
	toolResult := thirdContent[0].(map[string]any)
	toolResultContent := toolResult["content"].([]any)
	if len(toolResultContent) != 1 || toolResultContent[0].(map[string]any)["text"] != "result" {
		t.Fatalf("expected empty tool_result text removed, got %#v", toolResultContent)
	}
	emptyToolResultContent := thirdContent[1].(map[string]any)["content"].([]any)
	if len(emptyToolResultContent) != 1 || emptyToolResultContent[0].(map[string]any)["text"] != "..." {
		t.Fatalf("expected empty tool_result fallback, got %#v", emptyToolResultContent)
	}
	if thirdContent[2].(map[string]any)["content"] != "..." {
		t.Fatalf("expected empty string tool_result fallback, got %#v", thirdContent[2])
	}

	emptyArrayToolResultContent := messages[3].(map[string]any)["content"].([]any)[0].(map[string]any)["content"].([]any)
	if len(emptyArrayToolResultContent) != 1 || emptyArrayToolResultContent[0].(map[string]any)["text"] != "..." {
		t.Fatalf("expected empty array tool_result fallback, got %#v", emptyArrayToolResultContent)
	}

	fallbackContent := messages[4].(map[string]any)["content"].([]any)
	if len(fallbackContent) != 1 || fallbackContent[0].(map[string]any)["text"] != "..." {
		t.Fatalf("expected empty message content fallback, got %#v", fallbackContent)
	}
}

func TestSanitizeEmptyClaudeTextBlocksPatchesBlankStringContent(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":" \n\t "}]}`)

	patched := SanitizeEmptyClaudeTextBlocks(body)

	var root map[string]any
	if err := json.Unmarshal(patched, &root); err != nil {
		t.Fatalf("unmarshal patched body: %v", err)
	}
	messages := root["messages"].([]any)
	content := messages[0].(map[string]any)["content"]
	if content != "..." {
		t.Fatalf("expected blank string content fallback, got %#v", content)
	}
}

func TestSanitizeEmptyClaudeTextBlocksKeepsNonBlankStringContent(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"hello"}]}`)

	patched := SanitizeEmptyClaudeTextBlocks(body)

	if string(patched) != string(body) {
		t.Fatalf("non-blank string content should be left unchanged, got %s", patched)
	}
}
