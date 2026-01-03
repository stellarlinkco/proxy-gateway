package messages

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/BenedictKing/claude-proxy/internal/metrics"
	"github.com/BenedictKing/claude-proxy/internal/scheduler"
	"github.com/BenedictKing/claude-proxy/internal/session"
	"github.com/BenedictKing/claude-proxy/internal/warmup"
	"github.com/gin-gonic/gin"
)

func createTestSchedulerWithMetricsConfig(t *testing.T, cfgManager *config.ConfigManager) (*scheduler.ChannelScheduler, func()) {
	t.Helper()

	messagesMetrics := metrics.NewMetricsManagerWithConfig(3, 0.5)
	responsesMetrics := metrics.NewMetricsManagerWithConfig(3, 0.5)
	geminiMetrics := metrics.NewMetricsManagerWithConfig(3, 0.5)
	traceAffinity := session.NewTraceAffinityManagerWithTTL(2 * time.Minute)
	urlManager := warmup.NewURLManager(30*time.Second, 3)

	sch := scheduler.NewChannelScheduler(cfgManager, messagesMetrics, responsesMetrics, geminiMetrics, traceAffinity, urlManager)
	cleanup := func() {
		messagesMetrics.Stop()
		responsesMetrics.Stop()
		geminiMetrics.Stop()
		traceAffinity.Stop()
	}
	return sch, cleanup
}

func TestMessagesHandler_SingleChannel_SkipsSuspendedKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var calls atomic.Int64
	var usedSuspended atomic.Bool
	var usedGood atomic.Bool

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		calls.Add(1)
		auth := r.Header.Get("Authorization")
		if strings.Contains(auth, "k-suspended") {
			usedSuspended.Store(true)
		}
		if strings.Contains(auth, "k-good") {
			usedGood.Store(true)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "id":"msg_ok",
  "type":"message",
  "role":"assistant",
  "content":[{"type":"text","text":"ok"}],
  "usage":{"input_tokens":1,"output_tokens":1}
}`))
	}))
	defer upstream.Close()

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:        "c0",
				BaseURL:     upstream.URL,
				APIKeys:     []string{"k-suspended", "k-good"},
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

	sch, cleanupSch := createTestSchedulerWithMetricsConfig(t, cfgManager)
	defer cleanupSch()

	// 让第一个 key 进入熔断状态，触发 ShouldSuspendKey 跳过逻辑。
	mm := sch.GetMessagesMetricsManager()
	for i := 0; i < 3; i++ {
		mm.RecordFailure(upstream.URL, "k-suspended")
	}

	envCfg := &config.EnvConfig{
		ProxyAccessKey:     "secret",
		MaxRequestBodySize: 1024 * 1024,
	}

	r := gin.New()
	r.POST("/v1/messages", NewHandler(envCfg, cfgManager, sch, nil, nil, nil, nil))

	reqBody := `{"model":"claude-3","messages":[{"role":"user","content":"hi"}],"max_tokens":16}`
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
	if usedSuspended.Load() {
		t.Fatalf("expected suspended key skipped")
	}
	if !usedGood.Load() {
		t.Fatalf("expected good key used")
	}
}

func TestMessagesHandler_MultiChannel_BaseURLFailoverWithinChannel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var badCalls atomic.Int64
	upstreamBad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		badCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"quota exceeded"}}`))
	}))
	defer upstreamBad.Close()

	var goodCalls atomic.Int64
	upstreamGood := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		goodCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "id":"msg_ok",
  "type":"message",
  "role":"assistant",
  "content":[{"type":"text","text":"ok"}],
  "usage":{"input_tokens":1,"output_tokens":1}
}`))
	}))
	defer upstreamGood.Close()

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:        "c0",
				BaseURL:     upstreamBad.URL,
				BaseURLs:    []string{upstreamBad.URL, upstreamGood.URL},
				APIKeys:     []string{"k1"},
				ServiceType: "claude",
				Status:      "active",
				Priority:    1,
			},
			{
				Name:        "unused",
				BaseURL:     upstreamGood.URL,
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

	sch, cleanupSch := createTestSchedulerWithMetricsConfig(t, cfgManager)
	defer cleanupSch()

	envCfg := &config.EnvConfig{
		ProxyAccessKey:     "secret",
		MaxRequestBodySize: 1024 * 1024,
		Env:                "development",
		EnableRequestLogs:  true,
		EnableResponseLogs: true,
		RawLogOutput:       true,
	}

	r := gin.New()
	r.POST("/v1/messages", NewHandler(envCfg, cfgManager, sch, nil, nil, nil, nil))

	reqBody := `{"model":"claude-3","messages":[{"role":"user","content":"hi"}],"max_tokens":16}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if badCalls.Load() != 1 {
		t.Fatalf("badCalls=%d, want 1", badCalls.Load())
	}
	if goodCalls.Load() != 1 {
		t.Fatalf("goodCalls=%d, want 1", goodCalls.Load())
	}
	if !strings.Contains(w.Body.String(), `"id":"msg_ok"`) {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

