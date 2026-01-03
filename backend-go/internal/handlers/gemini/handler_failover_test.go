package gemini

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/gin-gonic/gin"
)

func TestGeminiHandler_NoUpstreamConfiguredReturns503(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		GeminiUpstream:       []config.UpstreamConfig{},
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
	req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestGeminiHandler_NoAPIKeysReturns503(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		GeminiUpstream: []config.UpstreamConfig{
			{Name: "g0", BaseURL: "http://example.invalid", APIKeys: nil, ServiceType: "gemini", Status: "active", Priority: 1},
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
	req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestGeminiHandler_NonFailover400ReturnsUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad request"}}`))
	}))
	defer upstream.Close()

	cfg := config.Config{
		GeminiUpstream: []config.UpstreamConfig{
			{Name: "g0", BaseURL: upstream.URL, APIKeys: []string{"k1"}, ServiceType: "gemini", Status: "active", Priority: 1},
		},
		LoadBalance:          "failover",
		ResponsesLoadBalance: "failover",
		GeminiLoadBalance:    "failover",
		FuzzyModeEnabled:     false,
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
	req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "bad request") {
		t.Fatalf("unexpected body=%s", w.Body.String())
	}
}

func TestGeminiHandler_SingleChannel_FailoverKeyThenSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1beta/models/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		calls.Add(1)

		if strings.Contains(r.Header.Get("x-goog-api-key"), "bad") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"quota exceeded"}}`))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}`))
	}))
	defer upstream.Close()

	cfg := config.Config{
		GeminiUpstream: []config.UpstreamConfig{
			{Name: "g0", BaseURL: upstream.URL, APIKeys: []string{"bad", "good"}, ServiceType: "gemini", Status: "active", Priority: 1},
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
	req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if calls.Load() != 2 {
		t.Fatalf("calls=%d, want 2", calls.Load())
	}
}

func TestGeminiHandler_AllKeysFail_ReturnsFailoverBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
	}))
	defer upstream.Close()

	cfg := config.Config{
		GeminiUpstream: []config.UpstreamConfig{
			{Name: "g0", BaseURL: upstream.URL, APIKeys: []string{"k1", "k2"}, ServiceType: "gemini", Status: "active", Priority: 1},
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
	req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "boom") {
		t.Fatalf("unexpected body=%s", w.Body.String())
	}
}
