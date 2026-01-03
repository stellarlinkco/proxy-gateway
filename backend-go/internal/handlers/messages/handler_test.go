package messages

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func createTestScheduler(t *testing.T, cfgManager *config.ConfigManager) (*scheduler.ChannelScheduler, func()) {
	t.Helper()

	messagesMetrics := metrics.NewMetricsManager()
	responsesMetrics := metrics.NewMetricsManager()
	geminiMetrics := metrics.NewMetricsManager()
	traceAffinity := session.NewTraceAffinityManager()
	urlManager := warmup.NewURLManager(30*time.Second, 3)

	sch := scheduler.NewChannelScheduler(cfgManager, messagesMetrics, responsesMetrics, geminiMetrics, traceAffinity, urlManager)
	cleanup := func() {
		messagesMetrics.Stop()
		responsesMetrics.Stop()
		geminiMetrics.Stop()
	}
	return sch, cleanup
}

func TestCountTokensHandler_ReturnsTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)

	envCfg := &config.EnvConfig{
		ProxyAccessKey:     "secret",
		MaxRequestBodySize: 1024 * 1024,
	}

	r := gin.New()
	r.POST("/v1/messages/count_tokens", CountTokensHandler(envCfg, nil, nil))

	t.Run("invalid json returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", bytes.NewBufferString("{"))
		req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
		w := httptest.NewRecorder()

		r.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})

	t.Run("valid json returns input_tokens", func(t *testing.T) {
		body := `{"model":"claude-3","messages":[{"role":"user","content":"hi"}]}`
		req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", bytes.NewBufferString(body))
		req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
		w := httptest.NewRecorder()

		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}

		var resp struct {
			InputTokens int `json:"input_tokens"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if resp.InputTokens <= 0 {
			t.Fatalf("input_tokens = %d, want > 0", resp.InputTokens)
		}
	})
}

func TestMessagesHandler_SingleChannel_FailoverKeyThenSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var upstreamCalls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		upstreamCalls.Add(1)
		auth := r.Header.Get("Authorization")
		if strings.Contains(auth, "k-bad") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"quota exceeded"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "id":"msg_1",
  "type":"message",
  "role":"assistant",
  "content":[{"type":"text","text":"hello"}],
  "usage":{"input_tokens":1,"output_tokens":1}
}`))
	}))
	defer upstream.Close()

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:        "ch0",
				BaseURL:     upstream.URL,
				APIKeys:     []string{"k-bad", "k-good"},
				ServiceType: "claude",
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

	// 传入非 nil billingHandler 覆盖计费分支，但使用 nil client 以避免外部依赖。
	billingHandler := billing.NewHandler(nil, nil, nil, 0)
	h := NewHandler(envCfg, cfgManager, sch, nil, billingHandler, nil, sqliteStore)

	r := gin.New()
	r.POST("/v1/messages", h)

	reqBody := `{"model":"claude-3","messages":[{"role":"user","content":"hi"}],"max_tokens":16}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(reqBody))
	req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if upstreamCalls.Load() != 2 {
		t.Fatalf("upstream calls = %d, want %d (failover 1 key + success 1 key)", upstreamCalls.Load(), 2)
	}
	if !strings.Contains(w.Body.String(), `"id":"msg_1"`) {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestMessagesHandler_MultiChannel_FailoverToNextChannel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var calls1 atomic.Int64
	upstream1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
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
		if r.URL.Path != "/v1/messages" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		calls2.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "id":"msg_2",
  "type":"message",
  "role":"assistant",
  "content":[{"type":"text","text":"ok"}],
  "usage":{"input_tokens":2,"output_tokens":2}
}`))
	}))
	defer upstream2.Close()

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:        "bad",
				BaseURL:     upstream1.URL,
				APIKeys:     []string{"k1"},
				ServiceType: "claude",
				Status:      "active",
				Priority:    1,
			},
			{
				Name:        "good",
				BaseURL:     upstream2.URL,
				APIKeys:     []string{"k2"},
				ServiceType: "claude",
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
	h := NewHandler(envCfg, cfgManager, sch, nil, nil, nil, nil)

	r := gin.New()
	r.POST("/v1/messages", h)

	reqBody := `{"model":"claude-3","messages":[{"role":"user","content":"hi"}],"max_tokens":16}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(reqBody))
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
	if !strings.Contains(w.Body.String(), `"id":"msg_2"`) {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestTruncateErrorMessage_TruncatesToMaxLen(t *testing.T) {
	long := strings.Repeat("x", 2048)
	got := truncateErrorMessage(long)
	if len(got) != 1027 {
		t.Fatalf("len = %d, want %d", len(got), 1027)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected suffix \"...\"")
	}
}
