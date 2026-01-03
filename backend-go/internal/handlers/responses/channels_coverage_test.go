package responses

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/gin-gonic/gin"
)

func TestResponsesChannels_UpdateUpstream_OutOfRangeReturns500(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{},
		ResponsesUpstream: []config.UpstreamConfig{
			{Name: "r0", BaseURL: "http://example.invalid", APIKeys: []string{"k1"}, ServiceType: "responses", Status: "active", Priority: 1},
		},
		LoadBalance:          "failover",
		ResponsesLoadBalance: "failover",
		GeminiLoadBalance:    "failover",
		FuzzyModeEnabled:     true,
	}
	cfgManager, cleanupCfg := createTestConfigManager(t, cfg)
	defer cleanupCfg()

	r := gin.New()
	r.PUT("/channels/:id", UpdateUpstream(cfgManager, nil))

	req := httptest.NewRequest(http.MethodPut, "/channels/999", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestResponsesChannels_AddApiKey_OutOfRangeReturns404(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{},
		ResponsesUpstream: []config.UpstreamConfig{
			{Name: "r0", BaseURL: "http://example.invalid", APIKeys: []string{"k1"}, ServiceType: "responses", Status: "active", Priority: 1},
		},
		LoadBalance:          "failover",
		ResponsesLoadBalance: "failover",
		GeminiLoadBalance:    "failover",
		FuzzyModeEnabled:     true,
	}
	cfgManager, cleanupCfg := createTestConfigManager(t, cfg)
	defer cleanupCfg()

	r := gin.New()
	r.POST("/channels/:id/keys", AddApiKey(cfgManager))

	body, _ := json.Marshal(map[string]string{"apiKey": "k2"})
	req := httptest.NewRequest(http.MethodPost, "/channels/999/keys", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestResponsesChannels_AddApiKey_SaveErrorReturns500(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{},
		ResponsesUpstream: []config.UpstreamConfig{
			{Name: "r0", BaseURL: "http://example.invalid", APIKeys: []string{"k1"}, ServiceType: "responses", Status: "active", Priority: 1},
		},
		LoadBalance:          "failover",
		ResponsesLoadBalance: "failover",
		GeminiLoadBalance:    "failover",
		FuzzyModeEnabled:     true,
	}
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
	defer cfgManager.Close()

	if err := os.Chmod(configFile, 0444); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	r := gin.New()
	r.POST("/channels/:id/keys", AddApiKey(cfgManager))

	body, _ := json.Marshal(map[string]string{"apiKey": "k2"})
	req := httptest.NewRequest(http.MethodPost, "/channels/0/keys", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestResponsesChannels_DeleteApiKey_EmptyParamReturns400(t *testing.T) {
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

func TestResponsesChannels_UpdateLoadBalance_SaveErrorReturns500(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configFile, []byte(`{"upstream":[],"loadBalance":"failover","responsesUpstream":[],"responsesLoadBalance":"failover","geminiUpstream":[],"geminiLoadBalance":"failover","fuzzyModeEnabled":true}`), 0644); err != nil {
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
	r.PUT("/load-balance", UpdateLoadBalance(cfgManager))

	req := httptest.NewRequest(http.MethodPut, "/load-balance", bytes.NewBufferString(`{"strategy":"random"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

