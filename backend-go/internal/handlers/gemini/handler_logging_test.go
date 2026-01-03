package gemini

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/BenedictKing/claude-proxy/internal/metrics"
	"github.com/BenedictKing/claude-proxy/internal/monitor"
	"github.com/gin-gonic/gin"
)

func TestGeminiHandler_WithSQLiteStore_WritesRequestLog_NonStream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/gemini-pro:generateContent" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}],
  "usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":3,"totalTokenCount":5}
}`))
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

	dbPath := filepath.Join(t.TempDir(), "metrics.db")
	store, err := metrics.NewSQLiteStore(&metrics.SQLiteStoreConfig{
		DBPath:        dbPath,
		RetentionDays: 3,
	})
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	live := monitor.NewLiveRequestManager(10)

	envCfg := &config.EnvConfig{
		ProxyAccessKey:     "secret",
		MaxRequestBodySize: 1024 * 1024,
	}
	h := NewHandler(envCfg, cfgManager, sch, live, store)

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
	if !strings.Contains(w.Body.String(), "\"candidates\"") {
		t.Fatalf("unexpected body=%s", w.Body.String())
	}

	logs, total, err := store.QueryRequestLogs("gemini", 10, 0)
	if err != nil {
		t.Fatalf("QueryRequestLogs: %v", err)
	}
	if total != 1 || len(logs) != 1 {
		t.Fatalf("logs total=%d len=%d, want 1", total, len(logs))
	}
	if logs[0].RequestID == "" || logs[0].ChannelName == "" || logs[0].KeyMask == "" {
		t.Fatalf("unexpected log: %+v", logs[0])
	}
	if logs[0].StatusCode != http.StatusOK || !logs[0].Success {
		t.Fatalf("unexpected log status=%d success=%v err=%q", logs[0].StatusCode, logs[0].Success, logs[0].ErrorMessage)
	}

	if live.Count() != 0 {
		t.Fatalf("live requests not cleaned up, count=%d", live.Count())
	}
}

func TestGeminiHandler_WithSQLiteStore_WritesRequestLog_Stream(t *testing.T) {
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
		_, _ = w.Write([]byte("\n"))
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

	dbPath := filepath.Join(t.TempDir(), "metrics.db")
	store, err := metrics.NewSQLiteStore(&metrics.SQLiteStoreConfig{
		DBPath:        dbPath,
		RetentionDays: 3,
	})
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	live := monitor.NewLiveRequestManager(10)

	envCfg := &config.EnvConfig{
		ProxyAccessKey:     "secret",
		MaxRequestBodySize: 1024 * 1024,
	}
	h := NewHandler(envCfg, cfgManager, sch, live, store)

	r := gin.New()
	r.POST("/v1beta/models/*modelAction", h)

	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-pro:streamGenerateContent", bytes.NewBufferString(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "data:") {
		t.Fatalf("expected stream output, got=%s", w.Body.String())
	}

	logs, total, err := store.QueryRequestLogs("gemini", 10, 0)
	if err != nil {
		t.Fatalf("QueryRequestLogs: %v", err)
	}
	if total != 1 || len(logs) != 1 {
		t.Fatalf("logs total=%d len=%d, want 1", total, len(logs))
	}
	if logs[0].StatusCode != http.StatusOK || !logs[0].Success {
		t.Fatalf("unexpected log status=%d success=%v err=%q", logs[0].StatusCode, logs[0].Success, logs[0].ErrorMessage)
	}

	if live.Count() != 0 {
		t.Fatalf("live requests not cleaned up, count=%d", live.Count())
	}
}

