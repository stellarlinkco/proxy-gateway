package responses

import (
	"bytes"
	"io"
	"log"
	"strings"
	"testing"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/BenedictKing/claude-proxy/internal/types"
)

func TestEstimateResponsesOutputFromItems_CoversShapes(t *testing.T) {
	if got := estimateResponsesOutputFromItems(nil); got != 0 {
		t.Fatalf("nil output=%d, want 0", got)
	}

	out := []types.ResponsesItem{
		{Type: "message", Role: "assistant", Content: "hello"},
		{Type: "message", Role: "assistant", Content: []interface{}{
			map[string]interface{}{"text": "world"},
			123,
		}},
		{Type: "message", Role: "assistant", Content: []types.ContentBlock{
			{Type: "output_text", Text: "hi"},
		}},
		{Type: "message", Role: "assistant", Content: map[string]interface{}{"k": "v"}},
		{Type: "tool_call", ToolUse: &types.ToolUse{Name: "tool", Input: map[string]interface{}{"a": "b"}}},
		{Type: "function_call", Content: "args"},
	}

	if got := estimateResponsesOutputFromItems(out); got <= 0 {
		t.Fatalf("output tokens=%d, want >0", got)
	}
}

func TestExtractResponsesTextFromEvent_CollectsKnownDeltas(t *testing.T) {
	var buf bytes.Buffer
	event := strings.Join([]string{
		"ignore this",
		`data: {"type":"response.output_text.delta","delta":"a"}`,
		`data: {"type":"response.function_call_arguments.delta","delta":"b"}`,
		`data: {"type":"response.reasoning_summary_text.delta","text":"c"}`,
		`data: {"type":"response.output_json.delta","delta":"d"}`,
		`data: {"type":"response.content_part.delta","delta":"e"}`,
		`data: {"type":"response.content_part.delta","text":"f"}`,
		`data: {"type":"response.audio.delta","delta":"g"}`,
		`data: {"type":"response.audio_transcript.delta","delta":"h"}`,
		`data: {`, // invalid json, should be ignored
		"",
	}, "\n")

	extractResponsesTextFromEvent(event, &buf)
	if got := buf.String(); got != "abcdefgh" {
		t.Fatalf("buf=%q, want %q", got, "abcdefgh")
	}
}

func TestCheckResponsesEventUsage_DetectsAndDecidesPatch(t *testing.T) {
	t.Run("no usage returns false", func(t *testing.T) {
		hasUsage, needPatch, _ := checkResponsesEventUsage("data: {\"type\":\"response.output_text.delta\",\"delta\":\"x\"}\n", false)
		if hasUsage || needPatch {
			t.Fatalf("hasUsage=%v needPatch=%v, want false/false", hasUsage, needPatch)
		}
	})

	t.Run("usage needs patch when tokens small", func(t *testing.T) {
		event := "data:{\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":0,\"output_tokens\":0,\"total_tokens\":0}}}\n"
		hasUsage, needPatch, u := checkResponsesEventUsage(event, false)
		if !hasUsage || !needPatch {
			t.Fatalf("hasUsage=%v needPatch=%v, want true/true", hasUsage, needPatch)
		}
		if u.InputTokens != 0 || u.OutputTokens != 0 || u.TotalTokens != 0 {
			t.Fatalf("unexpected usage: %+v", u)
		}
	})

	t.Run("claude cache allows skipping input patch", func(t *testing.T) {
		event := "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":0,\"output_tokens\":2,\"total_tokens\":2,\"cache_creation_input_tokens\":1}}}\n"
		hasUsage, needPatch, u := checkResponsesEventUsage(event, false)
		if !hasUsage || needPatch {
			t.Fatalf("hasUsage=%v needPatch=%v, want true/false", hasUsage, needPatch)
		}
		if !u.HasClaudeCache {
			t.Fatalf("expected HasClaudeCache true")
		}
	})

	t.Run("total_tokens missing triggers patch", func(t *testing.T) {
		event := "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":2,\"output_tokens\":2,\"total_tokens\":0}}}\n"
		hasUsage, needPatch, _ := checkResponsesEventUsage(event, false)
		if !hasUsage || !needPatch {
			t.Fatalf("hasUsage=%v needPatch=%v, want true/true", hasUsage, needPatch)
		}
	})
}

func TestInjectResponsesUsageToCompletedEvent_FirstPassAndFallback(t *testing.T) {
	origLogOut := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(origLogOut) })

	envCfg := &config.EnvConfig{
		Env:                "development",
		EnableResponseLogs: true,
		LogLevel:           "debug",
	}

	t.Run("first pass injects when JSON is complete", func(t *testing.T) {
		event := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\"}}\n\n"
		patched, inTok, outTok := injectResponsesUsageToCompletedEvent(event, []byte(`{"input":"hi"}`), "hello", envCfg)
		if inTok <= 0 || outTok <= 0 {
			t.Fatalf("tokens in=%d out=%d, want >0", inTok, outTok)
		}
		if !strings.Contains(patched, "\"usage\"") || !strings.Contains(patched, "\"input_tokens\"") {
			t.Fatalf("missing injected usage: %s", patched)
		}
	})

	t.Run("fallback injects when JSON spans multiple data lines", func(t *testing.T) {
		event := "event: response.completed\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\"\n" +
			"data: }}\n\n"
		patched, _, _ := injectResponsesUsageToCompletedEvent(event, []byte(`{"input":"hi"}`), "hello", envCfg)
		if !strings.Contains(patched, "\"usage\"") {
			t.Fatalf("expected injected usage via fallback: %s", patched)
		}
	})

	t.Run("returns original when no completed event exists", func(t *testing.T) {
		event := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n"
		patched, _, _ := injectResponsesUsageToCompletedEvent(event, []byte(`{"input":"hi"}`), "hello", envCfg)
		if patched != event {
			t.Fatalf("patched differs, want original\npatched=%q\nevent=%q", patched, event)
		}
	})
}

func TestPatchResponsesUsage_CoversBranches(t *testing.T) {
	origLogOut := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(origLogOut) })

	envCfg := &config.EnvConfig{EnableResponseLogs: true, Env: "development", LogLevel: "debug"}
	longInput := strings.Repeat("a", 200)
	longOutput := strings.Repeat("b", 200)
	reqBody := []byte(`{"model":"gpt-4o","input":"` + longInput + `"}`)

	t.Run("empty usage gets fully estimated", func(t *testing.T) {
		resp := &types.ResponsesResponse{
			Output: []types.ResponsesItem{{Type: "message", Content: longOutput}},
			Usage:  types.ResponsesUsage{},
		}
		patchResponsesUsage(resp, reqBody, envCfg)
		if resp.Usage.TotalTokens <= 0 || resp.Usage.InputTokens <= 0 || resp.Usage.OutputTokens <= 0 {
			t.Fatalf("unexpected usage: %+v", resp.Usage)
		}
	})

	t.Run("fake values get patched (no claude cache)", func(t *testing.T) {
		resp := &types.ResponsesResponse{
			Output: []types.ResponsesItem{{Type: "message", Content: longOutput}},
			Usage:  types.ResponsesUsage{InputTokens: 1, OutputTokens: 1, TotalTokens: 0},
		}
		patchResponsesUsage(resp, reqBody, envCfg)
		if resp.Usage.InputTokens <= 1 || resp.Usage.OutputTokens <= 1 || resp.Usage.TotalTokens <= 0 {
			t.Fatalf("unexpected usage: %+v", resp.Usage)
		}
	})

	t.Run("claude cache skips input patch but may patch output", func(t *testing.T) {
		resp := &types.ResponsesResponse{
			Output: []types.ResponsesItem{{Type: "message", Content: longOutput}},
			Usage: types.ResponsesUsage{
				InputTokens:                1,
				OutputTokens:               1,
				TotalTokens:                0,
				CacheCreationInputTokens:   1,
				CacheReadInputTokens:       1,
				CacheCreation5mInputTokens: 1,
			},
		}
		patchResponsesUsage(resp, reqBody, envCfg)
		if resp.Usage.InputTokens != 1 {
			t.Fatalf("input tokens patched unexpectedly: %+v", resp.Usage)
		}
		if resp.Usage.OutputTokens <= 1 || resp.Usage.TotalTokens <= 0 {
			t.Fatalf("expected output/total patched: %+v", resp.Usage)
		}
	})
}
