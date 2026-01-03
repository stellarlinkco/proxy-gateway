package responses

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/BenedictKing/claude-proxy/internal/monitor"
	"github.com/BenedictKing/claude-proxy/internal/session"
	"github.com/gin-gonic/gin"
)

func TestResponsesRequestLogContext_UpdateLive_RecordsRequest(t *testing.T) {
	live := monitor.NewLiveRequestManager(10)
	start := time.Now()

	ctx := &requestLogContext{
		requestID:          "req-1",
		startTime:          start,
		apiType:            "responses",
		model:              "gpt-4o",
		isStreaming:        false,
		channelIndex:       0,
		channelName:        "r0",
		apiKey:             "rk",
		liveRequestManager: live,
	}
	ctx.updateLive()

	if live.Count() != 1 {
		t.Fatalf("count=%d, want 1", live.Count())
	}
	reqs := live.GetAllRequests()
	if len(reqs) != 1 || reqs[0].RequestID != "req-1" || reqs[0].APIType != "responses" || reqs[0].ChannelName != "r0" {
		t.Fatalf("unexpected live requests: %+v", reqs)
	}
}

func TestResponsesChannels_AddUpstream_SaveErrorReturns500(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configFile, []byte(`{"responsesUpstream":[],"responsesLoadBalance":"failover"}`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfgManager, err := config.NewConfigManager(configFile)
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}
	defer cfgManager.Close()

	if err := os.Chmod(configFile, 0444); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	r := gin.New()
	r.POST("/channels", AddUpstream(cfgManager))

	body, _ := json.Marshal(map[string]any{
		"name":        "r0",
		"serviceType": "responses",
		"baseUrl":     "http://example.invalid",
		"apiKeys":     []string{"k1"},
		"status":      "active",
		"priority":    1,
	})
	req := httptest.NewRequest(http.MethodPost, "/channels", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestResponsesChannels_ReorderInvalidOrderReturns400(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		ResponsesUpstream: []config.UpstreamConfig{
			{Name: "r0", BaseURL: "http://example.invalid", APIKeys: []string{"k1"}, ServiceType: "responses", Status: "active", Priority: 1},
			{Name: "r1", BaseURL: "http://example.invalid", APIKeys: []string{"k2"}, ServiceType: "responses", Status: "active", Priority: 2},
		},
		LoadBalance:          "failover",
		ResponsesLoadBalance: "failover",
		GeminiLoadBalance:    "failover",
		FuzzyModeEnabled:     true,
	}
	cfgManager, cleanupCfg := createTestConfigManager(t, cfg)
	defer cleanupCfg()

	r := gin.New()
	r.POST("/channels/reorder", ReorderChannels(cfgManager))

	body, _ := json.Marshal(map[string]any{"order": []int{0, 0}})
	req := httptest.NewRequest(http.MethodPost, "/channels/reorder", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestCompactHandler_MultiChannel_NonFailoverErrorStopsWithoutFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var badCalls int
	upstreamBad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses/compact" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		badCalls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad request"}}`))
	}))
	defer upstreamBad.Close()

	var goodCalls int
	upstreamGood := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses/compact" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		goodCalls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"compacted":true}`))
	}))
	defer upstreamGood.Close()

	cfg := config.Config{
		ResponsesUpstream: []config.UpstreamConfig{
			{Name: "bad", BaseURL: upstreamBad.URL, APIKeys: []string{"k1"}, ServiceType: "responses", Status: "active", Priority: 1},
			{Name: "good", BaseURL: upstreamGood.URL, APIKeys: []string{"k2"}, ServiceType: "responses", Status: "active", Priority: 2},
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

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "bad request") {
		t.Fatalf("unexpected body=%s", w.Body.String())
	}
	if badCalls != 1 || goodCalls != 0 {
		t.Fatalf("badCalls=%d goodCalls=%d, want 1/0", badCalls, goodCalls)
	}
}

