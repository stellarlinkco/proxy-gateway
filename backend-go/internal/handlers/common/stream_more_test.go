package common

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/BenedictKing/claude-proxy/internal/metrics"
	"github.com/BenedictKing/claude-proxy/internal/providers"
	"github.com/BenedictKing/claude-proxy/internal/scheduler"
	"github.com/BenedictKing/claude-proxy/internal/session"
	"github.com/BenedictKing/claude-proxy/internal/types"
	"github.com/BenedictKing/claude-proxy/internal/warmup"
	"github.com/gin-gonic/gin"
)

type fakeStreamProvider struct {
	events []string
	err    error
}

func (p *fakeStreamProvider) ConvertToProviderRequest(*gin.Context, *config.UpstreamConfig, string) (*http.Request, []byte, error) {
	return nil, nil, errors.New("not used")
}

func (p *fakeStreamProvider) ConvertToClaudeResponse(*types.ProviderResponse) (*types.ClaudeResponse, error) {
	return nil, errors.New("not used")
}

func (p *fakeStreamProvider) HandleStreamResponse(body io.ReadCloser) (<-chan string, <-chan error, error) {
	eventChan := make(chan string, len(p.events))
	errChan := make(chan error, 1)
	go func() {
		defer close(eventChan)
		defer close(errChan)
		if body != nil {
			_ = body.Close()
		}
		for _, e := range p.events {
			eventChan <- e
		}
		if p.err != nil {
			errChan <- p.err
		}
	}()
	return eventChan, errChan, nil
}

func createTestSchedulerForStream(t *testing.T) (*scheduler.ChannelScheduler, func()) {
	t.Helper()

	messagesMetrics := metrics.NewMetricsManagerWithConfig(3, 0.5)
	responsesMetrics := metrics.NewMetricsManagerWithConfig(3, 0.5)
	geminiMetrics := metrics.NewMetricsManagerWithConfig(3, 0.5)
	traceAffinity := session.NewTraceAffinityManager()
	urlManager := warmup.NewURLManager(30*time.Second, 3)

	sch := scheduler.NewChannelScheduler(nil, messagesMetrics, responsesMetrics, geminiMetrics, traceAffinity, urlManager)
	cleanup := func() {
		messagesMetrics.Stop()
		responsesMetrics.Stop()
		geminiMetrics.Stop()
	}
	return sch, cleanup
}

func TestHandleStreamResponse_PatchesMessageStartAndUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	envCfg := &config.EnvConfig{
		Env:                "development",
		EnableResponseLogs: true,
		SSEDebugLevel:      "summary",
	}

	requestBody := []byte(`{"model":"claude-3","messages":[{"role":"assistant","content":[{"type":"text","text":"{"}]}]}`)

	sse := strings.Join([]string{
		"event: message_start",
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"wrong-model\",\"content\":[]}}",
		"",
		"event: content_block_delta",
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}",
		"",
		"event: message_delta",
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"input_tokens\":null,\"output_tokens\":0}}",
		"",
		"event: message_stop",
		"data: {\"type\":\"message_stop\"}",
		"",
	}, "\n")

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(sse)),
	}

	upstream := &config.UpstreamConfig{Name: "u", BaseURL: "https://example.com"}

	sch, cleanup := createTestSchedulerForStream(t)
	defer cleanup()

	usage, _, err := HandleStreamResponse(c, resp, &providers.ClaudeProvider{}, envCfg, time.Now(), upstream, requestBody, sch, "k1", nil, nil, "claude-3", "claude-3")
	if err != nil {
		t.Fatalf("HandleStreamResponse: %v", err)
	}
	_ = usage

	out := rec.Body.String()
	if !strings.Contains(out, "\"id\":\"msg_") {
		t.Fatalf("expected patched message id, got: %s", out)
	}
	if !strings.Contains(out, "\"model\":\"claude-3\"") {
		t.Fatalf("expected patched model, got: %s", out)
	}
	if strings.Contains(out, "\"input_tokens\":null") {
		t.Fatalf("expected patched input_tokens, got: %s", out)
	}
	if strings.Contains(out, "\"output_tokens\":0") {
		t.Fatalf("expected patched output_tokens, got: %s", out)
	}
}

func TestHandleStreamResponse_InjectsUsageWhenMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	envCfg := &config.EnvConfig{
		Env:                "development",
		EnableResponseLogs: true,
	}

	requestBody := []byte(`{"model":"claude-3","messages":[{"role":"user","content":"hi"}]}`)

	sse := strings.Join([]string{
		"event: content_block_delta",
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}",
		"",
		"event: message_stop",
		"data: {\"type\":\"message_stop\"}",
		"",
	}, "\n")

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(sse)),
	}

	upstream := &config.UpstreamConfig{Name: "u", BaseURL: "https://example.com"}

	sch, cleanup := createTestSchedulerForStream(t)
	defer cleanup()

	_, _, err := HandleStreamResponse(c, resp, &providers.ClaudeProvider{}, envCfg, time.Now(), upstream, requestBody, sch, "k1", nil, nil, "claude-3", "claude-3")
	if err != nil {
		t.Fatalf("HandleStreamResponse: %v", err)
	}

	out := rec.Body.String()
	if !strings.Contains(out, "event: message_delta") || !strings.Contains(out, "\"usage\"") {
		t.Fatalf("expected injected usage event, got: %s", out)
	}
}

func TestHandleStreamResponse_ErrorPathWritesErrorEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	envCfg := &config.EnvConfig{
		Env:                "development",
		EnableResponseLogs: true,
	}

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
	}

	upstream := &config.UpstreamConfig{Name: "u", BaseURL: "https://example.com"}
	sch, cleanup := createTestSchedulerForStream(t)
	defer cleanup()

	prov := &fakeStreamProvider{
		events: []string{"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{}}\n\n"},
		err:    errors.New("boom"),
	}
	_, _, err := HandleStreamResponse(c, resp, prov, envCfg, time.Now(), upstream, []byte(`{}`), sch, "k1", nil, nil, "claude-3", "claude-3")
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(rec.Body.String(), "event: error") {
		t.Fatalf("expected error event, got: %s", rec.Body.String())
	}
}

func TestStreamHelpers_CoverMisc(t *testing.T) {
	ctx := NewStreamContext(&config.EnvConfig{Env: "development", EnableResponseLogs: true})
	seedSynthesizerFromRequest(ctx, []byte(`{"messages":[{"role":"assistant","content":[{"type":"text","text":"{"}]}]}`))

	if !IsMessageStartEvent(`data: {"type":"message_start"}`) {
		t.Fatalf("expected IsMessageStartEvent true")
	}
	if !IsMessageStopEvent("event: message_stop\n") {
		t.Fatalf("expected IsMessageStopEvent true")
	}
	if !IsMessageDeltaEvent("event: message_delta\n") {
		t.Fatalf("expected IsMessageDeltaEvent true")
	}

	var buf bytes.Buffer
	ExtractTextFromEvent(`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}`, &buf)
	if buf.String() == "" {
		t.Fatalf("expected extracted text")
	}

	event := strings.Join([]string{
		"event: content_block_start",
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\"}}",
		"",
	}, "\n")
	eventType, blockIndex, blockType := extractSSEEventInfo(event)
	if eventType == "" || blockIndex != 0 || blockType != "text" {
		t.Fatalf("unexpected event info: %q %d %q", eventType, blockIndex, blockType)
	}

	if truncateForLog("abc", 2) != "ab..." {
		t.Fatalf("truncateForLog unexpected")
	}

	if !IsClientDisconnectError(errors.New("broken pipe")) {
		t.Fatalf("expected disconnect error")
	}
}

func TestPatchTokensInEventAndLowQuality(t *testing.T) {
	ev := "data: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}\n"
	patched := PatchTokensInEvent(ev, 10, 20, false, false, false)
	if !strings.Contains(patched, "\"input_tokens\":10") || !strings.Contains(patched, "\"output_tokens\":20") {
		t.Fatalf("unexpected patched event: %s", patched)
	}

	usage := map[string]interface{}{
		"input_tokens":  float64(10),
		"output_tokens": float64(10),
	}
	patchUsageFieldsWithLog(usage, 100, 200, false, false, "test", true)
	if usage["input_tokens"] != 100 || usage["output_tokens"] != 200 {
		t.Fatalf("unexpected lowQuality patch: %+v", usage)
	}
}

func TestSeedSynthesizerFromRequest_CoversBranches(t *testing.T) {
	t.Run("empty body returns early", func(t *testing.T) {
		ctx := NewStreamContext(&config.EnvConfig{Env: "development", EnableResponseLogs: true})
		seedSynthesizerFromRequest(ctx, nil)
		if ctx.LogPrefillText != "" {
			t.Fatalf("LogPrefillText=%q, want empty", ctx.LogPrefillText)
		}
	})

	t.Run("invalid json is ignored", func(t *testing.T) {
		ctx := NewStreamContext(&config.EnvConfig{Env: "development", EnableResponseLogs: true})
		seedSynthesizerFromRequest(ctx, []byte("{"))
		if ctx.LogPrefillText != "" {
			t.Fatalf("LogPrefillText=%q, want empty", ctx.LogPrefillText)
		}
	})

	t.Run("no assistant does not set prefill", func(t *testing.T) {
		ctx := NewStreamContext(&config.EnvConfig{Env: "development", EnableResponseLogs: true})
		seedSynthesizerFromRequest(ctx, []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`))
		if ctx.LogPrefillText != "" {
			t.Fatalf("LogPrefillText=%q, want empty", ctx.LogPrefillText)
		}
	})

	t.Run("assistant prefill over limit is ignored", func(t *testing.T) {
		ctx := NewStreamContext(&config.EnvConfig{Env: "development", EnableResponseLogs: true})
		longText := strings.Repeat("a", 257)
		seedSynthesizerFromRequest(ctx, []byte(`{"messages":[{"role":"assistant","content":[{"type":"text","text":"`+longText+`"}]}]}`))
		if ctx.LogPrefillText != "" {
			t.Fatalf("LogPrefillText=%q, want empty", ctx.LogPrefillText)
		}
	})

	t.Run("assistant text is captured", func(t *testing.T) {
		ctx := NewStreamContext(&config.EnvConfig{Env: "development", EnableResponseLogs: true})
		seedSynthesizerFromRequest(ctx, []byte(`{"messages":[{"role":"assistant","content":[{"type":"text","text":"{"},{"type":"tool_use","text":"ignored"}]}]}`))
		if ctx.LogPrefillText != "{" {
			t.Fatalf("LogPrefillText=%q, want %q", ctx.LogPrefillText, "{")
		}
	})

	if abs(-1) != 1 {
		t.Fatalf("abs(-1) unexpected")
	}
	if abs(1) != 1 {
		t.Fatalf("abs(1) unexpected")
	}
}
