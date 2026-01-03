package common

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/gin-gonic/gin"
)

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestReadRequestBody_ReadErrorReturns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/x", nil)
	c.Request.Body = io.NopCloser(failingReader{})

	_, err := ReadRequestBody(c, 10)
	if err == nil {
		t.Fatalf("expected error")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestStreamHelpers_MoreBranches(t *testing.T) {
	// IsMessageDeltaEvent: JSON branch (no "event: message_delta")
	if !IsMessageDeltaEvent("data: {\"type\":\"message_delta\"}\n\n") {
		t.Fatalf("expected IsMessageDeltaEvent true for data-only event")
	}
	if IsMessageDeltaEvent("data: {bad}\n\n") {
		t.Fatalf("expected IsMessageDeltaEvent false for invalid JSON")
	}

	// truncateForLog: non-truncate branch
	if truncateForLog("abc", 10) != "abc" {
		t.Fatalf("truncateForLog non-truncate")
	}

	// abs: negative branch
	if abs(-1) != 1 {
		t.Fatalf("abs negative")
	}

	// logUsageDetection: just cover log path (no assertions)
	logUsageDetection("test", map[string]interface{}{"input_tokens": float64(1), "output_tokens": float64(2)}, true)

	// updateCollectedUsage covers max and overwrite paths
	var collected CollectedUsageData
	updateCollectedUsage(&collected, CollectedUsageData{InputTokens: 10, OutputTokens: 20})
	updateCollectedUsage(&collected, CollectedUsageData{InputTokens: 5, OutputTokens: 30})
	updateCollectedUsage(&collected, CollectedUsageData{
		CacheCreationInputTokens:   1,
		CacheReadInputTokens:       2,
		CacheCreation5mInputTokens: 3,
		CacheCreation1hInputTokens: 4,
		CacheTTL:                   "mixed",
	})
	if collected.InputTokens != 10 || collected.OutputTokens != 30 || collected.CacheTTL != "mixed" {
		t.Fatalf("unexpected collected: %+v", collected)
	}

	// ExtractUserID/ExtractConversationID: invalid JSON falls back to ""
	if got := ExtractUserID([]byte("{")); got != "" {
		t.Fatalf("ExtractUserID invalid json got %q", got)
	}
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBuffer(nil))
	if got := ExtractConversationID(c, []byte("{")); got != "" {
		t.Fatalf("ExtractConversationID invalid json got %q", got)
	}

	// PatchMessageStartEvent: non message_start stays unchanged
	ev := "data: {\"type\":\"not_message_start\"}\n"
	if got := PatchMessageStartEvent(ev, "m", false); got != ev {
		t.Fatalf("PatchMessageStartEvent should not change non-message_start")
	}

	// HasEventWithUsage: message.usage branch
	if !HasEventWithUsage("data: {\"message\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n") {
		t.Fatalf("expected HasEventWithUsage true for message.usage")
	}
}

func TestPatchMessageStartEvent_ParsesAndPatches(t *testing.T) {
	ev := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"\",\"model\":\"wrong\"}}\n\n"
	got := PatchMessageStartEvent(ev, "right", false)
	if got == ev {
		t.Fatalf("expected patched event")
	}
	if !bytes.Contains([]byte(got), []byte("\"model\":\"right\"")) {
		t.Fatalf("expected patched model, got: %s", got)
	}
}

func TestCheckEventUsageStatus_MessageUsageBranch(t *testing.T) {
	ev := "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":10}}}\n\n"
	hasUsage, needPatch, u := CheckEventUsageStatus(ev, true)
	if !hasUsage || needPatch {
		t.Fatalf("hasUsage=%v needPatch=%v usage=%+v", hasUsage, needPatch, u)
	}

	// Top-level usage: include TTL mixed
	ev2 := "data: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"cache_creation_5m_input_tokens\":1,\"cache_creation_1h_input_tokens\":1}}\n\n"
	hasUsage2, _, u2 := CheckEventUsageStatus(ev2, true)
	if !hasUsage2 || u2.CacheTTL != "mixed" {
		t.Fatalf("hasUsage=%v usage=%+v", hasUsage2, u2)
	}
}

func TestIsClientDisconnectError_NonDisconnect(t *testing.T) {
	if IsClientDisconnectError(errors.New("some other error")) {
		t.Fatalf("expected false")
	}
}

func TestNewStreamContext_LoggingDisabled(t *testing.T) {
	ctx := NewStreamContext(&config.EnvConfig{Env: "production", EnableResponseLogs: true})
	if ctx.LoggingEnabled {
		t.Fatalf("expected logging disabled outside development")
	}
	if ctx.Synthesizer != nil {
		t.Fatalf("expected nil synthesizer when logging disabled")
	}
}

func TestExtractTextFromEvent_PartialJSONAndContentBlock(t *testing.T) {
	var buf bytes.Buffer
	ev := "data: {\"delta\":{\"partial_json\":\"{\"}}\n" +
		"data: {\"content_block\":{\"text\":\"hi\"}}\n"
	ExtractTextFromEvent(ev, &buf)
	if got := buf.String(); got != "{hi" {
		t.Fatalf("got=%q, want %q", got, "{hi")
	}
}

func TestPatchTokensInEvent_PatchesMessageUsage(t *testing.T) {
	ev := strings.Join([]string{
		"event: message_delta",
		"data: {\"type\":\"message_delta\",\"message\":{\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}}",
		"",
	}, "\n")

	patched := PatchTokensInEvent(ev, 10, 20, false, false, false)
	if !strings.Contains(patched, "\"input_tokens\":10") || !strings.Contains(patched, "\"output_tokens\":20") {
		t.Fatalf("unexpected patched=%s", patched)
	}
	if !strings.Contains(patched, "event: message_delta") {
		t.Fatalf("expected non-data lines preserved, got=%s", patched)
	}
}

func TestPatchMessageStartEvent_InvalidJSONOrMissingMessage(t *testing.T) {
	evBadJSON := "event: message_start\ndata: {bad}\n\n"
	gotBad := PatchMessageStartEvent(evBadJSON, "m", false)
	if !strings.Contains(gotBad, "data: {bad}") {
		t.Fatalf("unexpected=%s", gotBad)
	}

	evMissingMsg := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":\"not-object\"}\n\n"
	got := PatchMessageStartEvent(evMissingMsg, "m", false)
	if strings.Contains(got, "\"id\":\"msg_") {
		t.Fatalf("expected no id patch, got=%s", got)
	}
}

func TestPatchUsageFieldsWithLog_LowQualityDeviationAndNilBranches(t *testing.T) {
	usage := map[string]interface{}{
		"input_tokens":  float64(100),
		"output_tokens": float64(100),
	}
	patchUsageFieldsWithLog(usage, 50, 50, false, false, "t", true)
	if usage["input_tokens"] != 50 || usage["output_tokens"] != 50 {
		t.Fatalf("unexpected patched usage: %+v", usage)
	}

	usage2 := map[string]interface{}{
		"input_tokens":  float64(50),
		"output_tokens": float64(50),
	}
	patchUsageFieldsWithLog(usage2, 49, 49, false, false, "t", true)
	if got, ok := usage2["input_tokens"].(float64); !ok || got != 50 {
		t.Fatalf("input_tokens=%v, want 50", usage2["input_tokens"])
	}
	if got, ok := usage2["output_tokens"].(float64); !ok || got != 50 {
		t.Fatalf("output_tokens=%v, want 50", usage2["output_tokens"])
	}

	usage3 := map[string]interface{}{
		"input_tokens":  nil,
		"output_tokens": float64(0),
	}
	patchUsageFieldsWithLog(usage3, 10, 20, false, false, "t", false)
	if usage3["input_tokens"] != 10 || usage3["output_tokens"] != 20 {
		t.Fatalf("unexpected nil-patched usage: %+v", usage3)
	}
}

func TestPatchUsageFieldsWithLog_LoggingBranches(t *testing.T) {
	usage := map[string]interface{}{
		"input_tokens":  float64(0),
		"output_tokens": float64(0),
	}
	patchUsageFieldsWithLog(usage, 10, 20, false, true, "t", false)

	usage2 := map[string]interface{}{
		"input_tokens":  float64(100),
		"output_tokens": float64(100),
	}
	patchUsageFieldsWithLog(usage2, 10, 20, true, true, "t", false)
}
