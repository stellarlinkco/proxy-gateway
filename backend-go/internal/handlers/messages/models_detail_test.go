package messages

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/BenedictKing/claude-proxy/internal/metrics"
	"github.com/BenedictKing/claude-proxy/internal/scheduler"
	"github.com/BenedictKing/claude-proxy/internal/session"
	"github.com/BenedictKing/claude-proxy/internal/warmup"
	"github.com/gin-gonic/gin"
)

func TestModelsDetailHandler_EmptyModelReturns400(t *testing.T) {
	gin.SetMode(gin.TestMode)

	envCfg := &config.EnvConfig{ProxyAccessKey: "secret"}
	h := ModelsDetailHandler(envCfg, nil, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models/", nil)
	c.Request.Header.Set("x-api-key", envCfg.ProxyAccessKey)
	c.Params = gin.Params{{Key: "model", Value: ""}}

	h(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestModelsDetailHandler_MissingAPIKeyAborts(t *testing.T) {
	gin.SetMode(gin.TestMode)

	envCfg := &config.EnvConfig{ProxyAccessKey: "secret"}
	r := gin.New()
	r.GET("/v1/models/:model", ModelsDetailHandler(envCfg, nil, nil))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models/gpt", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestModelsDetailHandler_MessagesThenResponsesFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("messages success returns 200", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/models/gpt" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if strings.Contains(r.Header.Get("Authorization"), "msg") {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"gpt"}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer upstream.Close()

		cfg := config.Config{
			Upstream:             []config.UpstreamConfig{{Name: "m0", BaseURL: upstream.URL, APIKeys: []string{"msg"}, Status: "active"}},
			ResponsesUpstream:    []config.UpstreamConfig{{Name: "r0", BaseURL: upstream.URL, APIKeys: []string{"resp"}, Status: "active"}},
			LoadBalance:          "failover",
			ResponsesLoadBalance: "failover",
			GeminiLoadBalance:    "failover",
			FuzzyModeEnabled:     true,
		}

		cfgManager, cleanupCfg := createTestConfigManager(t, cfg)
		defer cleanupCfg()

		messagesMetrics := metrics.NewMetricsManager()
		responsesMetrics := metrics.NewMetricsManager()
		geminiMetrics := metrics.NewMetricsManager()
		defer messagesMetrics.Stop()
		defer responsesMetrics.Stop()
		defer geminiMetrics.Stop()

		traceAffinity := session.NewTraceAffinityManager()
		defer traceAffinity.Stop()
		urlManager := warmup.NewURLManager(30*time.Second, 3)
		sch := scheduler.NewChannelScheduler(cfgManager, messagesMetrics, responsesMetrics, geminiMetrics, traceAffinity, urlManager)

		envCfg := &config.EnvConfig{ProxyAccessKey: "secret"}
		r := gin.New()
		r.GET("/v1/models/:model", ModelsDetailHandler(envCfg, cfgManager, sch))

		req := httptest.NewRequest(http.MethodGet, "/v1/models/gpt", nil)
		req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("responses success after messages fail returns 200", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/models/gpt" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if strings.Contains(r.Header.Get("Authorization"), "resp") {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"gpt"}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer upstream.Close()

		cfg := config.Config{
			Upstream:             []config.UpstreamConfig{{Name: "m0", BaseURL: upstream.URL, APIKeys: []string{"msg"}, Status: "active"}},
			ResponsesUpstream:    []config.UpstreamConfig{{Name: "r0", BaseURL: upstream.URL, APIKeys: []string{"resp"}, Status: "active"}},
			LoadBalance:          "failover",
			ResponsesLoadBalance: "failover",
			GeminiLoadBalance:    "failover",
			FuzzyModeEnabled:     true,
		}

		cfgManager, cleanupCfg := createTestConfigManager(t, cfg)
		defer cleanupCfg()

		messagesMetrics := metrics.NewMetricsManager()
		responsesMetrics := metrics.NewMetricsManager()
		geminiMetrics := metrics.NewMetricsManager()
		defer messagesMetrics.Stop()
		defer responsesMetrics.Stop()
		defer geminiMetrics.Stop()

		traceAffinity := session.NewTraceAffinityManager()
		defer traceAffinity.Stop()
		urlManager := warmup.NewURLManager(30*time.Second, 3)
		sch := scheduler.NewChannelScheduler(cfgManager, messagesMetrics, responsesMetrics, geminiMetrics, traceAffinity, urlManager)

		envCfg := &config.EnvConfig{ProxyAccessKey: "secret"}
		r := gin.New()
		r.GET("/v1/models/:model", ModelsDetailHandler(envCfg, cfgManager, sch))

		req := httptest.NewRequest(http.MethodGet, "/v1/models/gpt", nil)
		req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("both fail returns 404", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer upstream.Close()

		cfg := config.Config{
			Upstream:             []config.UpstreamConfig{{Name: "m0", BaseURL: upstream.URL, APIKeys: []string{"msg"}, Status: "active"}},
			ResponsesUpstream:    []config.UpstreamConfig{{Name: "r0", BaseURL: upstream.URL, APIKeys: []string{"resp"}, Status: "active"}},
			LoadBalance:          "failover",
			ResponsesLoadBalance: "failover",
			GeminiLoadBalance:    "failover",
			FuzzyModeEnabled:     true,
		}

		cfgManager, cleanupCfg := createTestConfigManager(t, cfg)
		defer cleanupCfg()

		messagesMetrics := metrics.NewMetricsManager()
		responsesMetrics := metrics.NewMetricsManager()
		geminiMetrics := metrics.NewMetricsManager()
		defer messagesMetrics.Stop()
		defer responsesMetrics.Stop()
		defer geminiMetrics.Stop()

		traceAffinity := session.NewTraceAffinityManager()
		defer traceAffinity.Stop()
		urlManager := warmup.NewURLManager(30*time.Second, 3)
		sch := scheduler.NewChannelScheduler(cfgManager, messagesMetrics, responsesMetrics, geminiMetrics, traceAffinity, urlManager)

		envCfg := &config.EnvConfig{ProxyAccessKey: "secret"}
		r := gin.New()
		r.GET("/v1/models/:model", ModelsDetailHandler(envCfg, cfgManager, sch))

		req := httptest.NewRequest(http.MethodGet, "/v1/models/gpt", nil)
		req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})
}
