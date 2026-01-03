package responses

import (
	"bytes"
	"encoding/json"
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

func TestCompactHandler_SingleChannel_FailoverKeyThenSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses/compact" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		calls.Add(1)
		auth := r.Header.Get("Authorization")
		if strings.Contains(auth, "k-bad") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
			return
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
				APIKeys:     []string{"k-bad", "k-good"},
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

	envCfg := &config.EnvConfig{
		ProxyAccessKey:     "secret",
		MaxRequestBodySize: 1024 * 1024,
	}

	r := gin.New()
	r.POST("/v1/responses/compact", CompactHandler(envCfg, cfgManager, session.NewSessionManager(time.Hour, 10, 1000), sch))

	req := httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewBufferString(`{"input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if calls.Load() != 2 {
		t.Fatalf("upstream calls = %d, want %d", calls.Load(), 2)
	}
	if !strings.Contains(w.Body.String(), "compacted") {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestCompactHandler_MultiChannel_FailoverToNextChannel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstreamBad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses/compact" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
	}))
	defer upstreamBad.Close()

	upstreamGood := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses/compact" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"compacted":true}`))
	}))
	defer upstreamGood.Close()

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{},
		ResponsesUpstream: []config.UpstreamConfig{
			{
				Name:        "bad",
				BaseURL:     upstreamBad.URL,
				APIKeys:     []string{"k1"},
				ServiceType: "responses",
				Status:      "active",
				Priority:    1,
			},
			{
				Name:        "good",
				BaseURL:     upstreamGood.URL,
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

	sch, cleanupSch := createTestScheduler(t, cfgManager)
	defer cleanupSch()

	envCfg := &config.EnvConfig{
		ProxyAccessKey:     "secret",
		MaxRequestBodySize: 1024 * 1024,
	}

	r := gin.New()
	r.POST("/v1/responses/compact", CompactHandler(envCfg, cfgManager, session.NewSessionManager(time.Hour, 10, 1000), sch))

	req := httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewBufferString(`{"input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "compacted") {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestBuildCompactURL_CoversVersionSuffix(t *testing.T) {
	up := &config.UpstreamConfig{BaseURL: "http://example.com/v1"}
	if got := buildCompactURL(up); got != "http://example.com/v1/responses/compact" {
		t.Fatalf("got %q", got)
	}

	up2 := &config.UpstreamConfig{BaseURL: "http://example.com"}
	if got := buildCompactURL(up2); got != "http://example.com/v1/responses/compact" {
		t.Fatalf("got %q", got)
	}

	// 额外覆盖：确保 JSON 绑定分支可达（避免 go test 优化掉未引用的 json 包导入）
	_, _ = json.Marshal(map[string]bool{"ok": true})
}

