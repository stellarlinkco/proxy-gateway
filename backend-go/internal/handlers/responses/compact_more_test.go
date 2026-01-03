package responses

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/BenedictKing/claude-proxy/internal/session"
	"github.com/gin-gonic/gin"
)

func TestCompactHandler_MultiChannel_AllChannelsFail_FuzzyDisabledReturnsLastError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstreamFail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses/compact" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
	}))
	defer upstreamFail.Close()

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{},
		ResponsesUpstream: []config.UpstreamConfig{
			{Name: "r0", BaseURL: upstreamFail.URL, APIKeys: []string{"k1"}, ServiceType: "responses", Status: "active", Priority: 1},
			{Name: "r1", BaseURL: upstreamFail.URL, APIKeys: []string{"k2"}, ServiceType: "responses", Status: "active", Priority: 2},
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
	r := gin.New()
	r.POST("/v1/responses/compact", CompactHandler(envCfg, cfgManager, session.NewSessionManager(time.Hour, 10, 1000), sch))

	req := httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewBufferString(`{"input":"hi"}`))
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

func TestCompactHandler_MultiChannel_AllChannelsFail_FuzzyEnabledReturns503(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstreamFail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses/compact" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
	}))
	defer upstreamFail.Close()

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{},
		ResponsesUpstream: []config.UpstreamConfig{
			{Name: "r0", BaseURL: upstreamFail.URL, APIKeys: []string{"k1"}, ServiceType: "responses", Status: "active", Priority: 1},
			{Name: "r1", BaseURL: upstreamFail.URL, APIKeys: []string{"k2"}, ServiceType: "responses", Status: "active", Priority: 2},
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
	r := gin.New()
	r.POST("/v1/responses/compact", CompactHandler(envCfg, cfgManager, session.NewSessionManager(time.Hour, 10, 1000), sch))

	req := httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewBufferString(`{"input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

