package gemini

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/BenedictKing/claude-proxy/internal/handlers/common"
	"github.com/BenedictKing/claude-proxy/internal/metrics"
	"github.com/BenedictKing/claude-proxy/internal/scheduler"
	"github.com/BenedictKing/claude-proxy/internal/session"
	"github.com/BenedictKing/claude-proxy/internal/types"
	"github.com/BenedictKing/claude-proxy/internal/warmup"
	"github.com/gin-gonic/gin"
)

func createTestConfigManager(t *testing.T, cfg config.Config) (*config.ConfigManager, func()) {
	t.Helper()

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(configFile, data, 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfgManager, err := config.NewConfigManager(configFile)
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}
	return cfgManager, func() { cfgManager.Close() }
}

func createTestScheduler(t *testing.T, cfgManager *config.ConfigManager) (*scheduler.ChannelScheduler, func()) {
	t.Helper()

	messagesMetrics := metrics.NewMetricsManager()
	responsesMetrics := metrics.NewMetricsManager()
	geminiMetrics := metrics.NewMetricsManager()
	traceAffinity := session.NewTraceAffinityManager()
	urlManager := warmup.NewURLManager(30*time.Second, 3)

	sch := scheduler.NewChannelScheduler(cfgManager, messagesMetrics, responsesMetrics, geminiMetrics, traceAffinity, urlManager)
	return sch, func() {
		messagesMetrics.Stop()
		responsesMetrics.Stop()
		geminiMetrics.Stop()
		traceAffinity.Stop()
	}
}

func TestExtractGeminiAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/x:generateContent?key=q", nil)
	if got := extractGeminiAPIKey(c); got != "q" {
		t.Fatalf("got %q", got)
	}

	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/x:generateContent", nil)
	c.Request.Header.Set("x-goog-api-key", "h")
	if got := extractGeminiAPIKey(c); got != "h" {
		t.Fatalf("got %q", got)
	}
}

func TestExtractModelName(t *testing.T) {
	if got := extractModelName("gemini-pro:generateContent"); got != "gemini-pro" {
		t.Fatalf("got %q", got)
	}
	if got := extractModelName("gemini-pro"); got != "gemini-pro" {
		t.Fatalf("got %q", got)
	}
	if got := extractModelName(""); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestTruncateErrorMessage(t *testing.T) {
	short := truncateErrorMessage("x")
	if short != "x" {
		t.Fatalf("short=%q", short)
	}

	long := strings.Repeat("a", 2000)
	out := truncateErrorMessage(long)
	if len(out) <= 1024 || !strings.HasSuffix(out, "...") {
		t.Fatalf("len(out)=%d suffix=%v", len(out), strings.HasSuffix(out, "..."))
	}
}

func TestGeminiHandler_SingleChannel_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/gemini-pro:generateContent" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]}}],
  "usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":3,"totalTokenCount":5}
}`))
	}))
	defer upstream.Close()

	cfg := config.Config{
		GeminiUpstream: []config.UpstreamConfig{
			{
				Name:        "g0",
				BaseURL:     upstream.URL,
				APIKeys:     []string{"gk1"},
				ServiceType: "gemini",
				Status:      "active",
				Priority:    1,
			},
		},
		LoadBalance:          "failover",
		ResponsesLoadBalance: "failover",
		GeminiLoadBalance:    "failover",
		FuzzyModeEnabled:     true,
	}

	cfgManager, cleanupCfg := createTestConfigManager(t, cfg)
	defer cleanupCfg()

	sch, cleanupSch := createTestScheduler(t, cfgManager)
	defer cleanupSch()

	envCfg := &config.EnvConfig{
		ProxyAccessKey:     "secret",
		MaxRequestBodySize: 1024 * 1024,
	}

	h := NewHandler(envCfg, cfgManager, sch, nil, nil)
	r := gin.New()
	r.POST("/v1beta/models/*modelAction", h)

	reqBody := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-pro:generateContent", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "\"candidates\"") {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestGeminiHandler_MultiChannel_FailoverToNextChannel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var calls1 atomic.Int64
	upstream1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1beta/models/") {
			calls1.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer upstream1.Close()

	var calls2 atomic.Int64
	upstream2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1beta/models/") {
			calls2.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
  "candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}],
  "usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}
}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer upstream2.Close()

	cfg := config.Config{
		GeminiUpstream: []config.UpstreamConfig{
			{
				Name:        "bad",
				BaseURL:     upstream1.URL,
				APIKeys:     []string{"gk1"},
				ServiceType: "gemini",
				Status:      "active",
				Priority:    1,
			},
			{
				Name:        "good",
				BaseURL:     upstream2.URL,
				APIKeys:     []string{"gk2"},
				ServiceType: "gemini",
				Status:      "active",
				Priority:    2,
			},
		},
		LoadBalance:          "failover",
		ResponsesLoadBalance: "failover",
		GeminiLoadBalance:    "failover",
		FuzzyModeEnabled:     true,
	}

	cfgManager, cleanupCfg := createTestConfigManager(t, cfg)
	defer cleanupCfg()

	sch, cleanupSch := createTestScheduler(t, cfgManager)
	defer cleanupSch()

	envCfg := &config.EnvConfig{
		ProxyAccessKey:     "secret",
		MaxRequestBodySize: 1024 * 1024,
	}

	h := NewHandler(envCfg, cfgManager, sch, nil, nil)
	r := gin.New()
	r.POST("/v1beta/models/*modelAction", h)

	reqBody := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-pro:generateContent", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if calls1.Load() != 1 {
		t.Fatalf("upstream1 calls = %d, want %d", calls1.Load(), 1)
	}
	if calls2.Load() != 1 {
		t.Fatalf("upstream2 calls = %d, want %d", calls2.Load(), 1)
	}
}

func TestGeminiHandler_Stream_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/gemini-pro:streamGenerateContent" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"h\"}]}}]}\n"))
		_, _ = w.Write([]byte("data: {\"usageMetadata\":{\"promptTokenCount\":2,\"candidatesTokenCount\":3,\"totalTokenCount\":5}}\n"))
	}))
	defer upstream.Close()

	cfg := config.Config{
		GeminiUpstream: []config.UpstreamConfig{
			{
				Name:        "g0",
				BaseURL:     upstream.URL,
				APIKeys:     []string{"gk1"},
				ServiceType: "gemini",
				Status:      "active",
				Priority:    1,
			},
		},
		LoadBalance:          "failover",
		ResponsesLoadBalance: "failover",
		GeminiLoadBalance:    "failover",
		FuzzyModeEnabled:     true,
	}

	cfgManager, cleanupCfg := createTestConfigManager(t, cfg)
	defer cleanupCfg()

	sch, cleanupSch := createTestScheduler(t, cfgManager)
	defer cleanupSch()

	envCfg := &config.EnvConfig{
		ProxyAccessKey:     "secret",
		MaxRequestBodySize: 1024 * 1024,
		Env:                "development",
		EnableResponseLogs: true,
	}

	h := NewHandler(envCfg, cfgManager, sch, nil, nil)
	r := gin.New()
	r.POST("/v1beta/models/*modelAction", h)

	reqBody := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-pro:streamGenerateContent", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "data:") {
		t.Fatalf("expected stream output, got: %s", w.Body.String())
	}
}

func TestStreamConversionHelpers(t *testing.T) {
	gin.SetMode(gin.TestMode)

	envCfg := &config.EnvConfig{EnableResponseLogs: true, Env: "development"}
	ctx, w := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/x:streamGenerateContent", nil)

	claudeBody := strings.Join([]string{
		"data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}",
		"data: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":2,\"output_tokens\":3}}",
		"",
	}, "\n")
	respClaude := &http.Response{Body: io.NopCloser(strings.NewReader(claudeBody)), Header: make(http.Header), StatusCode: http.StatusOK}
	usageClaude := handleStreamSuccess(ctx, respClaude, "claude", envCfg, time.Now(), "gemini-pro")
	if usageClaude == nil || usageClaude.InputTokens != 2 || usageClaude.OutputTokens != 3 {
		t.Fatalf("unexpected usage: %+v", usageClaude)
	}
	_ = w

	openaiBody := strings.Join([]string{
		"data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}",
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}",
		"data: {\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3}}",
		"data: [DONE]",
		"",
	}, "\n")
	ctx2, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx2.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/x:streamGenerateContent", nil)
	respOpenAI := &http.Response{Body: io.NopCloser(strings.NewReader(openaiBody)), Header: make(http.Header), StatusCode: http.StatusOK}
	usageOpenAI := handleStreamSuccess(ctx2, respOpenAI, "openai", envCfg, time.Now(), "gemini-pro")
	if usageOpenAI == nil || usageOpenAI.InputTokens != 2 || usageOpenAI.OutputTokens != 3 {
		t.Fatalf("unexpected usage: %+v", usageOpenAI)
	}
}

func TestOpenAIFinishReasonToGemini(t *testing.T) {
	if got := openaiFinishReasonToGemini("length"); got != "MAX_TOKENS" {
		t.Fatalf("got %q", got)
	}
	if got := openaiFinishReasonToGemini("content_filter"); got != "SAFETY" {
		t.Fatalf("got %q", got)
	}
}

func TestHandleAllFailedHelpers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	handleAllChannelsFailed(c, nil, nil)
	handleAllKeysFailed(c, nil, nil)
}

func TestHandleAllChannelsFailed_WithFailoverErrorReturnsBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	handleAllChannelsFailed(c, &common.FailoverError{Status: 418, Body: []byte(`{"x":1}`)}, nil)
	if w.Code != 418 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if w.Body.String() != `{"x":1}` {
		t.Fatalf("body=%q", w.Body.String())
	}
}

func TestBuildProviderRequest_CoversServiceTypes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-pro:generateContent", nil)

	req := &types.GeminiRequest{
		Contents: []types.GeminiContent{
			{Role: "user", Parts: []types.GeminiPart{{Text: "hi"}}},
		},
	}

	up := &config.UpstreamConfig{Name: "u", BaseURL: "http://example.com", ServiceType: "claude"}
	if _, err := buildProviderRequest(ctx, up, "http://example.com", "k", req, "gemini-pro", false); err != nil {
		t.Fatalf("claude buildProviderRequest: %v", err)
	}

	up2 := &config.UpstreamConfig{Name: "u", BaseURL: "http://example.com", ServiceType: "openai"}
	if _, err := buildProviderRequest(ctx, up2, "http://example.com", "k", req, "gemini-pro", false); err != nil {
		t.Fatalf("openai buildProviderRequest: %v", err)
	}
}
