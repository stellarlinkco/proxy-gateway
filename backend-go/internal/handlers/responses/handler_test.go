package responses

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BenedictKing/claude-proxy/internal/billing"
	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/BenedictKing/claude-proxy/internal/metrics"
	"github.com/BenedictKing/claude-proxy/internal/scheduler"
	"github.com/BenedictKing/claude-proxy/internal/session"
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
	}
}

func TestResponsesHandler_SingleChannel_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "id":"resp_1",
  "model":"gpt-4o",
  "status":"completed",
  "output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],
  "usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}
}`))
	}))
	defer upstream.Close()

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{},
		ResponsesUpstream: []config.UpstreamConfig{
			{
				Name:        "r0",
				BaseURL:     upstream.URL,
				APIKeys:     []string{"rk1"},
				ServiceType: "responses",
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

	sessionManager := session.NewSessionManager(time.Hour, 100, 100000)

	dbPath := filepath.Join(t.TempDir(), "metrics.db")
	sqliteStore, err := metrics.NewSQLiteStore(&metrics.SQLiteStoreConfig{
		DBPath:        dbPath,
		RetentionDays: 3,
	})
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer sqliteStore.Close()

	envCfg := &config.EnvConfig{
		ProxyAccessKey:     "secret",
		MaxRequestBodySize: 1024 * 1024,
		Env:                "development",
		EnableResponseLogs: true,
	}
	billingHandler := billing.NewHandler(nil, nil, nil, 0)
	h := NewHandler(envCfg, cfgManager, sessionManager, sch, nil, billingHandler, nil, sqliteStore)

	r := gin.New()
	r.POST("/v1/responses", h)

	reqBody := `{"model":"gpt-4o","input":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), `"id":"resp_1"`) {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestResponsesHandler_MultiChannel_FailoverToNextChannel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var calls1 atomic.Int64
	upstream1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		calls1.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
	}))
	defer upstream1.Close()

	var calls2 atomic.Int64
	upstream2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		calls2.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "id":"resp_2",
  "model":"gpt-4o",
  "status":"completed",
  "output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],
  "usage":{"input_tokens":2,"output_tokens":2,"total_tokens":4}
}`))
	}))
	defer upstream2.Close()

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{},
		ResponsesUpstream: []config.UpstreamConfig{
			{
				Name:        "bad",
				BaseURL:     upstream1.URL,
				APIKeys:     []string{"rk1"},
				ServiceType: "responses",
				Status:      "active",
				Priority:    1,
			},
			{
				Name:        "good",
				BaseURL:     upstream2.URL,
				APIKeys:     []string{"rk2"},
				ServiceType: "responses",
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

	sessionManager := session.NewSessionManager(time.Hour, 100, 100000)

	envCfg := &config.EnvConfig{
		ProxyAccessKey:     "secret",
		MaxRequestBodySize: 1024 * 1024,
	}
	h := NewHandler(envCfg, cfgManager, sessionManager, sch, nil, nil, nil, nil)

	r := gin.New()
	r.POST("/v1/responses", h)

	reqBody := `{"model":"gpt-4o","input":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(reqBody))
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
	if !strings.Contains(w.Body.String(), `"id":"resp_2"`) {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestResponsesHandler_Stream_InsertsUsageWhenMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_s\",\"model\":\"gpt-4o\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"hi\"}]}]}}\n"))
	}))
	defer upstream.Close()

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{},
		ResponsesUpstream: []config.UpstreamConfig{
			{
				Name:        "r0",
				BaseURL:     upstream.URL,
				APIKeys:     []string{"rk1"},
				ServiceType: "responses",
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

	sessionManager := session.NewSessionManager(time.Hour, 100, 100000)

	envCfg := &config.EnvConfig{
		ProxyAccessKey:     "secret",
		MaxRequestBodySize: 1024 * 1024,
		Env:                "development",
		EnableResponseLogs: true,
		LogLevel:           "debug",
	}

	h := NewHandler(envCfg, cfgManager, sessionManager, sch, nil, nil, nil, nil)

	r := gin.New()
	r.POST("/v1/responses", h)

	reqBody := `{"model":"gpt-4o","input":"hello","stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "\"usage\"") {
		t.Fatalf("expected injected usage, got body: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "\"input_tokens\"") {
		t.Fatalf("expected injected input_tokens, got body: %s", w.Body.String())
	}
}

func TestParseInputToItems(t *testing.T) {
	t.Run("string input", func(t *testing.T) {
		items, err := parseInputToItems("hello")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(items) != 1 {
			t.Fatalf("len = %d, want %d", len(items), 1)
		}
	})

	t.Run("invalid input type", func(t *testing.T) {
		_, err := parseInputToItems(123)
		if err == nil {
			t.Fatalf("expected error")
		}
	})
}

