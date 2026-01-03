package messages

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BenedictKing/claude-proxy/internal/cache"
	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/BenedictKing/claude-proxy/internal/metrics"
	"github.com/BenedictKing/claude-proxy/internal/scheduler"
	"github.com/BenedictKing/claude-proxy/internal/session"
	"github.com/BenedictKing/claude-proxy/internal/warmup"
	"github.com/gin-gonic/gin"
)

func TestModelsHandler_CacheHitReturnsCachedResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	envCfg := &config.EnvConfig{ProxyAccessKey: "secret"}
	m := &metrics.CacheMetrics{}
	respCache := cache.NewHTTPResponseCache(10, time.Minute, m)

	req := httptest.NewRequest(http.MethodGet, "/v1/models?a=1&b=2", nil)
	key := modelsCacheKey(req)

	cachedBody := []byte(`{"object":"list","data":[{"id":"m1","object":"model","created":1,"owned_by":"x"}]}`)
	respCache.Set(key, cache.HTTPResponse{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{modelsCacheContentType}},
		Body:       cachedBody,
	})

	r := gin.New()
	r.GET("/v1/models", ModelsHandler(envCfg, nil, nil, respCache))

	req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if w.Body.String() != string(cachedBody) {
		t.Fatalf("body = %q, want %q", w.Body.String(), string(cachedBody))
	}
}

func TestModelsHandler_MissingAPIKeyAborts(t *testing.T) {
	gin.SetMode(gin.TestMode)

	envCfg := &config.EnvConfig{ProxyAccessKey: "secret"}
	cacheMetrics := &metrics.CacheMetrics{}
	respCache := cache.NewHTTPResponseCache(10, time.Minute, cacheMetrics)

	r := gin.New()
	r.GET("/v1/models", ModelsHandler(envCfg, nil, nil, respCache))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestModelsHandler_CacheMissFetchesUpstreamAndCaches(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var upstreamCalls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		upstreamCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"m1","object":"model","created":1,"owned_by":"x"}]}`))
	}))
	defer upstream.Close()

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "messages",
				BaseURL:  upstream.URL,
				APIKeys:  []string{"k-msg"},
				Status:   "active",
				Priority: 1,
			},
		},
		LoadBalance: "failover",
		ResponsesUpstream: []config.UpstreamConfig{
			{
				Name:     "responses",
				BaseURL:  upstream.URL,
				APIKeys:  []string{"k-resp"},
				Status:   "active",
				Priority: 1,
			},
		},
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
	cacheMetrics := &metrics.CacheMetrics{}
	respCache := cache.NewHTTPResponseCache(10, time.Minute, cacheMetrics)

	r := gin.New()
	r.GET("/v1/models", ModelsHandler(envCfg, cfgManager, sch, respCache))

	req1 := httptest.NewRequest(http.MethodGet, "/v1/models?a=1&b=2", nil)
	req1.Header.Set("x-api-key", envCfg.ProxyAccessKey)
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)

	if w1.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w1.Code, http.StatusOK)
	}
	if upstreamCalls.Load() != 2 {
		t.Fatalf("upstream calls = %d, want %d", upstreamCalls.Load(), 2)
	}

	key1 := modelsCacheKey(req1)
	if _, ok := respCache.Get(key1); !ok {
		t.Fatalf("expected response cached for key %q", key1)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/v1/models?b=2&a=1", nil)
	req2.Header.Set("x-api-key", envCfg.ProxyAccessKey)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w2.Code, http.StatusOK)
	}
	if upstreamCalls.Load() != 2 {
		t.Fatalf("upstream calls = %d, want %d (cached hit should not call upstream)", upstreamCalls.Load(), 2)
	}
}

func TestModelsHandler_UpstreamFailureDoesNotCache(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "messages",
				BaseURL:  upstream.URL,
				APIKeys:  []string{"k-msg"},
				Status:   "active",
				Priority: 1,
			},
		},
		LoadBalance: "failover",
		ResponsesUpstream: []config.UpstreamConfig{
			{
				Name:     "responses",
				BaseURL:  upstream.URL,
				APIKeys:  []string{"k-resp"},
				Status:   "active",
				Priority: 1,
			},
		},
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
	urlManager := warmup.NewURLManager(30*time.Second, 3)
	sch := scheduler.NewChannelScheduler(cfgManager, messagesMetrics, responsesMetrics, geminiMetrics, traceAffinity, urlManager)

	envCfg := &config.EnvConfig{ProxyAccessKey: "secret"}
	cacheMetrics := &metrics.CacheMetrics{}
	respCache := cache.NewHTTPResponseCache(10, time.Minute, cacheMetrics)

	r := gin.New()
	r.GET("/v1/models", ModelsHandler(envCfg, cfgManager, sch, respCache))

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}

	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Error.Message == "" {
		t.Fatalf("expected error message in response")
	}

	if respCache.Len() != 0 {
		t.Fatalf("cache len = %d, want %d", respCache.Len(), 0)
	}
}

func TestModelsHandler_InvalidJSONFromUpstream_Returns404AndDoesNotCache(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var upstreamCalls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		upstreamCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not-json"))
	}))
	defer upstream.Close()

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{Name: "messages", BaseURL: upstream.URL, APIKeys: []string{"k-msg"}, Status: "active", Priority: 1},
		},
		LoadBalance: "failover",
		ResponsesUpstream: []config.UpstreamConfig{
			{Name: "responses", BaseURL: upstream.URL, APIKeys: []string{"k-resp"}, Status: "active", Priority: 1},
		},
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
	urlManager := warmup.NewURLManager(30*time.Second, 3)
	sch := scheduler.NewChannelScheduler(cfgManager, messagesMetrics, responsesMetrics, geminiMetrics, traceAffinity, urlManager)

	envCfg := &config.EnvConfig{ProxyAccessKey: "secret"}
	cacheMetrics := &metrics.CacheMetrics{}
	respCache := cache.NewHTTPResponseCache(10, time.Minute, cacheMetrics)

	r := gin.New()
	r.GET("/v1/models", ModelsHandler(envCfg, cfgManager, sch, respCache))

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
	if upstreamCalls.Load() != 2 {
		t.Fatalf("upstream calls = %d, want %d", upstreamCalls.Load(), 2)
	}
	if respCache.Len() != 0 {
		t.Fatalf("cache len = %d, want %d", respCache.Len(), 0)
	}
}

func TestMergeModels_DeduplicatesByID(t *testing.T) {
	in := []ModelEntry{
		{ID: "m1"},
		{ID: "m1"},
		{ID: "m2"},
	}
	out := []ModelEntry{
		{ID: "m2"},
		{ID: "m3"},
	}

	merged := mergeModels(in, out)
	if len(merged) != 3 {
		t.Fatalf("len=%d, want %d", len(merged), 3)
	}
}

func TestBuildModelsURL_VersionAndSkipPrefix(t *testing.T) {
	cases := map[string]string{
		"http://x":         "http://x/v1/models",
		"http://x/":        "http://x/v1/models",
		"http://x/v1":      "http://x/v1/models",
		"http://x/v2beta":  "http://x/v2beta/models",
		"http://x#":        "http://x/models",
		"http://x/v1#":     "http://x/v1/models",
		"http://x/v2beta#": "http://x/v2beta/models",
	}

	for baseURL, want := range cases {
		if got := buildModelsURL(baseURL); got != want {
			t.Fatalf("buildModelsURL(%q)=%q, want %q", baseURL, got, want)
		}
	}
}

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

	cleanup := func() {
		cfgManager.Close()
	}
	return cfgManager, cleanup
}
