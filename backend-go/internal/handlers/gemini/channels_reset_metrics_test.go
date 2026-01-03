package gemini

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/gin-gonic/gin"
)

func TestGeminiChannels_UpdateUpstream_SingleKeySwapResetsMetrics(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		GeminiUpstream: []config.UpstreamConfig{
			{Name: "g0", BaseURL: "http://example.invalid", APIKeys: []string{"k1"}, ServiceType: "gemini", Status: "suspended", Priority: 1},
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

	r := gin.New()
	r.PUT("/channels/:id", UpdateUpstream(cfgManager, sch))

	body, _ := json.Marshal(map[string]any{"apiKeys": []string{"k2"}})
	req := httptest.NewRequest(http.MethodPut, "/channels/0", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if cfgManager.GetConfig().GeminiUpstream[0].Status != "active" {
		t.Fatalf("expected status active")
	}
}

func TestGeminiChannels_DeleteApiKey_EmptyParamReturns400(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{
		{Key: "id", Value: "0"},
		{Key: "apiKey", Value: ""},
	}

	DeleteApiKey(nil)(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestGeminiChannels_UpdateUpstream_OutOfRangeReturns500(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		GeminiUpstream: []config.UpstreamConfig{
			{Name: "g0", BaseURL: "http://example.invalid", APIKeys: []string{"k1"}, ServiceType: "gemini", Status: "active", Priority: 1},
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

	r := gin.New()
	r.PUT("/channels/:id", UpdateUpstream(cfgManager, sch))

	req := httptest.NewRequest(http.MethodPut, "/channels/999", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestGeminiChannels_SetChannelStatus_OutOfRangeReturns404(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		GeminiUpstream: []config.UpstreamConfig{
			{Name: "g0", BaseURL: "http://example.invalid", APIKeys: []string{"k1"}, ServiceType: "gemini", Status: "active", Priority: 1},
		},
		LoadBalance:          "failover",
		ResponsesLoadBalance: "failover",
		GeminiLoadBalance:    "failover",
		FuzzyModeEnabled:     true,
	}

	cfgManager, cleanupCfg := createTestConfigManager(t, cfg)
	defer cleanupCfg()

	r := gin.New()
	r.PATCH("/channels/:id/status", SetChannelStatus(cfgManager))

	req := httptest.NewRequest(http.MethodPatch, "/channels/999/status", bytes.NewBufferString(`{"status":"active"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

