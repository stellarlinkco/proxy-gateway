package responses

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/BenedictKing/claude-proxy/internal/config"
)

func TestExtractResponsesUsageFromMap_ClaudeCacheAndTTL(t *testing.T) {
	u := extractResponsesUsageFromMap(map[string]interface{}{
		"input_tokens":                   float64(10),
		"output_tokens":                  float64(20),
		"total_tokens":                   float64(30),
		"cache_creation_input_tokens":    float64(1),
		"cache_read_input_tokens":        float64(2),
		"cache_creation_5m_input_tokens": float64(3),
		"cache_creation_1h_input_tokens": float64(4),
	})
	if u.InputTokens != 10 || u.OutputTokens != 20 || u.TotalTokens != 30 {
		t.Fatalf("unexpected tokens: %+v", u)
	}
	if !u.HasClaudeCache {
		t.Fatalf("expected HasClaudeCache true")
	}
	if u.CacheTTL != "mixed" {
		t.Fatalf("CacheTTL=%q", u.CacheTTL)
	}
}

func TestExtractResponsesUsageFromMap_OpenAICachedTokens(t *testing.T) {
	u := extractResponsesUsageFromMap(map[string]interface{}{
		"input_tokens":  float64(0),
		"output_tokens": float64(0),
		"input_tokens_details": map[string]interface{}{
			"cached_tokens": float64(42),
		},
	})
	if u.CacheReadInputTokens != 42 {
		t.Fatalf("CacheReadInputTokens=%d", u.CacheReadInputTokens)
	}
	if u.HasClaudeCache {
		t.Fatalf("expected HasClaudeCache false for OpenAI cached_tokens")
	}
}

func TestUpdateResponsesStreamUsage(t *testing.T) {
	collected := &responsesStreamUsage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3}
	updateResponsesStreamUsage(collected, responsesStreamUsage{
		InputTokens:                10,
		OutputTokens:               20,
		TotalTokens:                30,
		CacheCreationInputTokens:   1,
		CacheReadInputTokens:       2,
		CacheCreation5mInputTokens: 3,
		CacheCreation1hInputTokens: 4,
		CacheTTL:                   "mixed",
		HasClaudeCache:             true,
	})
	if collected.InputTokens != 10 || collected.OutputTokens != 20 || collected.TotalTokens != 30 {
		t.Fatalf("unexpected collected tokens: %+v", collected)
	}
	if collected.CacheTTL != "mixed" || !collected.HasClaudeCache {
		t.Fatalf("unexpected collected cache: %+v", collected)
	}
}

func TestIsClientDisconnectError(t *testing.T) {
	if !isClientDisconnectError(errors.New("broken pipe")) {
		t.Fatalf("expected true")
	}
	if isClientDisconnectError(errors.New("some other error")) {
		t.Fatalf("expected false")
	}
}

func TestPatchResponsesCompletedEventUsage_PatchesFields(t *testing.T) {
	envCfg := &config.EnvConfig{Env: "development"}
	collected := &responsesStreamUsage{}
	event := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":0,\"output_tokens\":0,\"total_tokens\":0}}}\n\n"

	patched := patchResponsesCompletedEventUsage(event, []byte(`{"input":"hi"}`), "hello", collected, envCfg)

	lines := strings.Split(patched, "\n")
	var jsonLine string
	for _, line := range lines {
		if strings.HasPrefix(line, "data: ") {
			jsonLine = strings.TrimPrefix(line, "data: ")
			break
		}
	}
	if jsonLine == "" {
		t.Fatalf("missing data line: %s", patched)
	}

	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(jsonLine), &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	resp, _ := obj["response"].(map[string]interface{})
	usage, _ := resp["usage"].(map[string]interface{})
	if usage == nil {
		t.Fatalf("missing usage: %+v", obj)
	}
	in, _ := usage["input_tokens"].(float64)
	out, _ := usage["output_tokens"].(float64)
	if in <= 0 || out <= 0 {
		t.Fatalf("expected patched tokens, got: %+v", usage)
	}
}

func TestParseInputToItems_ArrayBranch(t *testing.T) {
	items, err := parseInputToItems([]interface{}{
		map[string]interface{}{"type": "output_text", "content": "hi"},
		"not-a-map",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(items) != 1 || items[0].Type != "output_text" {
		t.Fatalf("items=%+v", items)
	}
}
