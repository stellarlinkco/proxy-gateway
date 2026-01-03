package responses

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/BenedictKing/claude-proxy/internal/session"
	"github.com/gin-gonic/gin"
)

func TestCompactHandler_MultiChannel_SkipsSuspendedKeyThenSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var calls atomic.Int64
	var usedSuspended atomic.Bool
	var usedGood atomic.Bool

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses/compact" {
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
		_, _ = w.Write([]byte(`{"compacted":true}`))
	}))
	defer upstream.Close()

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{},
		ResponsesUpstream: []config.UpstreamConfig{
			{
				Name:        "r0",
				BaseURL:     upstream.URL,
				APIKeys:     []string{"k-suspended", "k-good"},
				ServiceType: "responses",
				Status:      "active",
				Priority:    1,
			},
			{
				Name:        "unused",
				BaseURL:     upstream.URL,
				APIKeys:     []string{"k2"},
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

	sch, cleanupSch := createTestSchedulerWithMetricsConfig(t, cfgManager)
	defer cleanupSch()

	// 让 k-suspended 的失败率达到熔断阈值（>=0.5），同时保证渠道聚合失败率仍 < 0.5，
	// 避免调度器在选渠道阶段直接跳过整个渠道，确保覆盖 compact 的 ShouldSuspendKey 跳过分支。
	rm := sch.GetResponsesMetricsManager()
	rm.RecordFailure(upstream.URL, "k-suspended")
	rm.RecordFailure(upstream.URL, "k-suspended")
	rm.RecordSuccess(upstream.URL, "k-suspended")
	rm.RecordSuccess(upstream.URL, "k-good")
	rm.RecordSuccess(upstream.URL, "k-good")
	rm.RecordSuccess(upstream.URL, "k-good")
	if !rm.ShouldSuspendKey(upstream.URL, "k-suspended") {
		t.Fatalf("precondition failed: expected k-suspended in suspended state")
	}

	envCfg := &config.EnvConfig{ProxyAccessKey: "secret", MaxRequestBodySize: 1024 * 1024}
	r := gin.New()
	r.POST("/v1/responses/compact", CompactHandler(envCfg, cfgManager, session.NewSessionManager(time.Hour, 10, 1000), sch))

	req := httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewBufferString(`{"input":"hi"}`))
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
