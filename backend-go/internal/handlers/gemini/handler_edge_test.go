package gemini

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
	"github.com/BenedictKing/claude-proxy/internal/handlers/common"
	"github.com/BenedictKing/claude-proxy/internal/types"
	"github.com/gin-gonic/gin"
)

type errReadCloser struct{}

func (errReadCloser) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (errReadCloser) Close() error             { return nil }

func TestGeminiHandler_BypassAuthWithXGoogAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/gemini-pro:generateContent" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}`))
	}))
	defer upstream.Close()

	cfg := config.Config{
		GeminiUpstream: []config.UpstreamConfig{
			{Name: "g0", BaseURL: upstream.URL, APIKeys: []string{"k1"}, ServiceType: "gemini", Status: "active", Priority: 1},
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

	envCfg := &config.EnvConfig{ProxyAccessKey: "secret", MaxRequestBodySize: 1024 * 1024}
	h := NewHandler(envCfg, cfgManager, sch, nil, nil)

	r := gin.New()
	r.POST("/v1beta/models/*modelAction", h)

	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-pro:generateContent", bytes.NewBufferString(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", "client-key") // 触发 bypass
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestGeminiHandler_InvalidJSONReturns400(t *testing.T) {
	gin.SetMode(gin.TestMode)

	envCfg := &config.EnvConfig{ProxyAccessKey: "secret", MaxRequestBodySize: 1024 * 1024}

	cfg := config.Config{
		GeminiUpstream: []config.UpstreamConfig{
			{Name: "g0", BaseURL: "http://example.invalid", APIKeys: []string{"k1"}, ServiceType: "gemini", Status: "active", Priority: 1},
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

	h := NewHandler(envCfg, cfgManager, sch, nil, nil)

	r := gin.New()
	r.POST("/v1beta/models/*modelAction", h)

	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-pro:generateContent", bytes.NewBufferString("{"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", "client-key")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestGeminiHandler_MissingModelInURLReturns400(t *testing.T) {
	gin.SetMode(gin.TestMode)

	envCfg := &config.EnvConfig{ProxyAccessKey: "secret", MaxRequestBodySize: 1024 * 1024}

	cfg := config.Config{
		GeminiUpstream: []config.UpstreamConfig{
			{Name: "g0", BaseURL: "http://example.invalid", APIKeys: []string{"k1"}, ServiceType: "gemini", Status: "active", Priority: 1},
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

	h := NewHandler(envCfg, cfgManager, sch, nil, nil)

	r := gin.New()
	r.POST("/v1beta/models/*modelAction", h)

	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/", bytes.NewBufferString(`{"contents":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", "client-key")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestBuildProviderRequest_SetsURLAndAuthHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-pro:generateContent", bytes.NewBufferString(`{}`))
	c.Request.Header.Set("X-Test", "1")

	reqBody := &types.GeminiRequest{
		Contents: []types.GeminiContent{{Role: "user", Parts: []types.GeminiPart{{Text: "hi"}}}},
	}

	t.Run("gemini non-stream", func(t *testing.T) {
		up := &config.UpstreamConfig{ServiceType: "gemini"}
		req, err := buildProviderRequest(c, up, "http://example.com", "k", reqBody, "gemini-pro", false)
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		if !strings.Contains(req.URL.String(), "/v1beta/models/gemini-pro:generateContent") {
			t.Fatalf("url=%q", req.URL.String())
		}
		if req.Header.Get("x-goog-api-key") != "k" {
			t.Fatalf("x-goog-api-key=%q", req.Header.Get("x-goog-api-key"))
		}
	})

	t.Run("gemini stream includes alt=sse", func(t *testing.T) {
		up := &config.UpstreamConfig{ServiceType: "gemini"}
		req, err := buildProviderRequest(c, up, "http://example.com", "k", reqBody, "gemini-pro", true)
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		if !strings.Contains(req.URL.String(), "streamGenerateContent") || !strings.Contains(req.URL.String(), "alt=sse") {
			t.Fatalf("url=%q", req.URL.String())
		}
	})

	t.Run("claude sets anthropic version and bearer", func(t *testing.T) {
		up := &config.UpstreamConfig{ServiceType: "claude"}
		req, err := buildProviderRequest(c, up, "http://example.com", "k", reqBody, "claude-3", false)
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		if req.Header.Get("anthropic-version") == "" {
			t.Fatalf("missing anthropic-version")
		}
		if !strings.HasPrefix(req.Header.Get("Authorization"), "Bearer ") {
			t.Fatalf("auth=%q", req.Header.Get("Authorization"))
		}
	})

	t.Run("openai uses bearer", func(t *testing.T) {
		up := &config.UpstreamConfig{ServiceType: "openai"}
		req, err := buildProviderRequest(c, up, "http://example.com", "k", reqBody, "gpt-4o", false)
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		if !strings.Contains(req.URL.String(), "/v1/chat/completions") {
			t.Fatalf("url=%q", req.URL.String())
		}
		if !strings.HasPrefix(req.Header.Get("Authorization"), "Bearer ") {
			t.Fatalf("auth=%q", req.Header.Get("Authorization"))
		}
	})
}

func TestHandleSuccess_ConvertsClaudeAndOpenAIResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)

	envCfg := &config.EnvConfig{Env: "development", EnableResponseLogs: false}
	reqBody := &types.GeminiRequest{Contents: []types.GeminiContent{{Role: "user", Parts: []types.GeminiPart{{Text: "hi"}}}}}

	t.Run("read error returns 500", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: errReadCloser{}}
		if usage := handleSuccess(c, resp, "gemini", envCfg, time.Now(), reqBody, "gemini-pro", false); usage != nil {
			t.Fatalf("usage=%+v, want nil", usage)
		}
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("claude converts to gemini", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		body := `{"content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":3,"cache_read_input_tokens":1}}`
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
		usage := handleSuccess(c, resp, "claude", envCfg, time.Now(), reqBody, "claude-3", false)
		if usage == nil || usage.InputTokens <= 0 || usage.OutputTokens <= 0 {
			t.Fatalf("usage=%+v", usage)
		}
		if !strings.Contains(w.Body.String(), "\"candidates\"") {
			t.Fatalf("unexpected body: %s", w.Body.String())
		}
	})

	t.Run("openai converts to gemini", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		body := `{"choices":[{"message":{"content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3}}`
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
		usage := handleSuccess(c, resp, "openai", envCfg, time.Now(), reqBody, "gpt-4o", false)
		if usage == nil || usage.InputTokens <= 0 || usage.OutputTokens <= 0 {
			t.Fatalf("usage=%+v", usage)
		}
		if !strings.Contains(w.Body.String(), "\"candidates\"") {
			t.Fatalf("unexpected body: %s", w.Body.String())
		}
	})

	t.Run("default passthrough returns nil usage", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		body := `{"ok":true}`
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
		if usage := handleSuccess(c, resp, "unknown", envCfg, time.Now(), reqBody, "x", false); usage != nil {
			t.Fatalf("usage=%+v, want nil", usage)
		}
		if w.Body.String() != body {
			t.Fatalf("body=%q, want %q", w.Body.String(), body)
		}
	})
}

func TestHandleAllFailed_BothPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("channels failed uses failoverErr when present", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		handleAllChannelsFailed(c, &common.FailoverError{Status: 418, Body: []byte("x")}, nil)
		if w.Code != 418 {
			t.Fatalf("status=%d", w.Code)
		}
	})

	t.Run("keys failed uses lastError when no failoverErr", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		handleAllKeysFailed(c, nil, errors.New("boom"))
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "boom") {
			t.Fatalf("unexpected body: %s", w.Body.String())
		}
	})
}

func TestOpenAIFinishReasonToGemini_AllCases(t *testing.T) {
	cases := map[string]string{
		"stop":           "STOP",
		"length":         "MAX_TOKENS",
		"tool_calls":     "STOP",
		"content_filter": "SAFETY",
		"whatever":       "STOP",
	}
	for in, want := range cases {
		if got := openaiFinishReasonToGemini(in); got != want {
			t.Fatalf("openaiFinishReasonToGemini(%q)=%q, want %q", in, got, want)
		}
	}
}
