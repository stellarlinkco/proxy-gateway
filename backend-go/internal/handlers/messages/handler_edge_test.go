package messages

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/gin-gonic/gin"
)

func TestMessagesHandler_SingleChannel_NoUpstreamConfigured(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		Upstream:                 []config.UpstreamConfig{},
		ResponsesUpstream:        []config.UpstreamConfig{},
		GeminiUpstream:           []config.UpstreamConfig{},
		LoadBalance:              "failover",
		ResponsesLoadBalance:     "failover",
		GeminiLoadBalance:        "failover",
		FuzzyModeEnabled:         true,
		CurrentUpstream:          0,
		CurrentResponsesUpstream: 0,
	}

	cfgManager, cleanupCfg := createTestConfigManager(t, cfg)
	defer cleanupCfg()

	// scheduler 仅用于判断单/多渠道模式
	sch, cleanupSch := createTestScheduler(t, cfgManager)
	defer cleanupSch()

	envCfg := &config.EnvConfig{ProxyAccessKey: "secret", MaxRequestBodySize: 1024 * 1024}
	h := NewHandler(envCfg, cfgManager, sch, nil, nil, nil, nil)

	r := gin.New()
	r.POST("/v1/messages", h)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(`{"model":"claude-3","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "NO_UPSTREAM") {
		t.Fatalf("expected NO_UPSTREAM, got %s", w.Body.String())
	}
}

func TestMessagesHandler_SingleChannel_NoAPIKeysReturns503(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{Name: "c0", BaseURL: "http://example.invalid", APIKeys: nil, ServiceType: "claude", Status: "active", Priority: 1},
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
	h := NewHandler(envCfg, cfgManager, sch, nil, nil, nil, nil)

	r := gin.New()
	r.POST("/v1/messages", h)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(`{"model":"claude-3","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "NO_API_KEYS") {
		t.Fatalf("expected NO_API_KEYS, got %s", w.Body.String())
	}
}

func TestMessagesHandler_SingleChannel_UnsupportedServiceTypeReturns400(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{Name: "c0", BaseURL: "http://example.invalid", APIKeys: []string{"k1"}, ServiceType: "nope", Status: "active", Priority: 1},
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
	h := NewHandler(envCfg, cfgManager, sch, nil, nil, nil, nil)

	r := gin.New()
	r.POST("/v1/messages", h)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(`{"model":"claude-3","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Unsupported service type") {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestMessagesHandler_SingleChannel_NonFailover400ReturnsUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad request"}}`))
	}))
	defer upstream.Close()

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{Name: "c0", BaseURL: upstream.URL, APIKeys: []string{"k1"}, ServiceType: "claude", Status: "active", Priority: 1},
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
	h := NewHandler(envCfg, cfgManager, sch, nil, nil, nil, nil)

	r := gin.New()
	r.POST("/v1/messages", h)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(`{"model":"claude-3","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "bad request") {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestMessagesHandler_SingleChannel_MissingUsagePatchesTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "id":"msg_x",
  "type":"message",
  "role":"assistant",
  "content":[{"type":"text","text":"hello world"}]
}`))
	}))
	defer upstream.Close()

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{Name: "c0", BaseURL: upstream.URL, APIKeys: []string{"k1"}, ServiceType: "claude", Status: "active", Priority: 1},
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
	h := NewHandler(envCfg, cfgManager, sch, nil, nil, nil, nil)

	r := gin.New()
	r.POST("/v1/messages", h)

	reqBody := `{"model":"claude-3","messages":[{"role":"user","content":"` + strings.Repeat("a", 200) + `"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("calls=%d, want 1", calls.Load())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	usage, _ := resp["usage"].(map[string]interface{})
	if usage == nil {
		t.Fatalf("expected patched usage, got: %s", w.Body.String())
	}
	inTok, _ := usage["input_tokens"].(float64)
	outTok, _ := usage["output_tokens"].(float64)
	if inTok <= 0 || outTok <= 0 {
		t.Fatalf("unexpected tokens: %+v", usage)
	}
}
