package providers

import (
	"encoding/json"
	"testing"

	"github.com/BenedictKing/claude-proxy/internal/types"
)

func TestClaudeProviderConvertToClaudeResponse_PreservesThinkingObject(t *testing.T) {
	p := &ClaudeProvider{}

	body := []byte(`{
		"id":"msg_123",
		"type":"message",
		"role":"assistant",
		"content":[
			{"type":"thinking","thinking":{"thinking":"abc","signature":"sig1"}},
			{"type":"text","text":"hello"}
		],
		"usage":{"input_tokens":1,"output_tokens":2}
	}`)

	got, err := p.ConvertToClaudeResponse(&types.ProviderResponse{Body: body})
	if err != nil {
		t.Fatalf("ConvertToClaudeResponse error: %v", err)
	}

	if len(got.Content) != 2 {
		t.Fatalf("content length mismatch: got=%d want=2", len(got.Content))
	}
	if got.Content[0].Type != "thinking" {
		t.Fatalf("content[0].type mismatch: got=%q want=%q", got.Content[0].Type, "thinking")
	}

	thinkingMap, ok := got.Content[0].Thinking.(map[string]interface{})
	if !ok {
		t.Fatalf("content[0].thinking type mismatch: got=%T want=map[string]interface{}", got.Content[0].Thinking)
	}
	if thinkingMap["thinking"] != "abc" {
		t.Fatalf("content[0].thinking.thinking mismatch: got=%v want=%q", thinkingMap["thinking"], "abc")
	}
	if thinkingMap["signature"] != "sig1" {
		t.Fatalf("content[0].thinking.signature mismatch: got=%v want=%q", thinkingMap["signature"], "sig1")
	}

	// 确保重新序列化时字段不会丢失（避免前端/下一轮请求拿到残缺结构）
	reencoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(reencoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(reencoded) error: %v", err)
	}
	content, ok := decoded["content"].([]interface{})
	if !ok || len(content) != 2 {
		t.Fatalf("decoded.content mismatch: %T len=%d", decoded["content"], len(content))
	}
	block0, ok := content[0].(map[string]interface{})
	if !ok {
		t.Fatalf("decoded.content[0] type mismatch: %T", content[0])
	}
	if _, ok := block0["thinking"]; !ok {
		t.Fatalf("decoded.content[0].thinking missing")
	}
}

func TestClaudeProviderConvertToClaudeResponse_PreservesThinkingString(t *testing.T) {
	p := &ClaudeProvider{}

	body := []byte(`{
		"id":"msg_123",
		"type":"message",
		"role":"assistant",
		"content":[
			{"type":"thinking","thinking":"abc","signature":"sig1"},
			{"type":"text","text":"hello"}
		],
		"usage":{"input_tokens":1,"output_tokens":2}
	}`)

	got, err := p.ConvertToClaudeResponse(&types.ProviderResponse{Body: body})
	if err != nil {
		t.Fatalf("ConvertToClaudeResponse error: %v", err)
	}

	if len(got.Content) != 2 {
		t.Fatalf("content length mismatch: got=%d want=2", len(got.Content))
	}
	if got.Content[0].Type != "thinking" {
		t.Fatalf("content[0].type mismatch: got=%q want=%q", got.Content[0].Type, "thinking")
	}

	if got.Content[0].Thinking != "abc" {
		t.Fatalf("content[0].thinking mismatch: got=%v want=%q", got.Content[0].Thinking, "abc")
	}
	if got.Content[0].Signature != "sig1" {
		t.Fatalf("content[0].signature mismatch: got=%q want=%q", got.Content[0].Signature, "sig1")
	}
}
