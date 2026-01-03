package messages

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

func TestMessagesChannels_DeleteApiKey_EmptyAPIKeyReturns400(t *testing.T) {
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

func TestMessagesChannels_MoveAPIKey_EmptyAPIKeyReturns400(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("top", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Params = gin.Params{
			{Key: "id", Value: "0"},
			{Key: "apiKey", Value: ""},
		}
		MoveApiKeyToTop(nil)(c)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("bottom", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Params = gin.Params{
			{Key: "id", Value: "0"},
			{Key: "apiKey", Value: ""},
		}
		MoveApiKeyToBottom(nil)(c)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})
}

func TestMessagesChannels_UpdateUpstream_OutOfRangeReturns404(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{Name: "c0", BaseURL: "http://example.invalid", APIKeys: []string{"k1"}, ServiceType: "claude", Status: "active", Priority: 1},
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

	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestMessagesChannels_AddApiKey_DuplicateReturns400(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{Name: "c0", BaseURL: "http://example.invalid", APIKeys: []string{"k1"}, ServiceType: "claude", Status: "active", Priority: 1},
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

	body, _ := json.Marshal(map[string]string{"apiKey": "k1"})
	req := httptest.NewRequest(http.MethodPost, "/channels/0/keys", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestMessagesChannels_UpdateLoadBalance_SaveErrorReturns500(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodPut, "/load-balance", bytes.NewBufferString(`{"strategy":"failover"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestPingChannelURLs_NoBaseURLReturnsError(t *testing.T) {
	got := pingChannelURLs(&config.UpstreamConfig{})
	if got["success"] != false || got["error"] != "no_base_url" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestMessagesChannels_AddApiKey_OutOfRangeReturns404(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{Name: "c0", BaseURL: "http://example.invalid", APIKeys: []string{"k1"}, ServiceType: "claude", Status: "active", Priority: 1},
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

func TestMessagesChannels_AddApiKey_SaveErrorReturns500(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{Name: "c0", BaseURL: "http://example.invalid", APIKeys: []string{"k1"}, ServiceType: "claude", Status: "active", Priority: 1},
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

func TestMessagesChannels_DeleteUpstream_OutOfRangeReturns404(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{Name: "c0", BaseURL: "http://example.invalid", APIKeys: []string{"k1"}, ServiceType: "claude", Status: "active", Priority: 1},
		},
		LoadBalance:          "failover",
		ResponsesLoadBalance: "failover",
		GeminiLoadBalance:    "failover",
		FuzzyModeEnabled:     true,
	}
	cfgManager, cleanupCfg := createTestConfigManager(t, cfg)
	defer cleanupCfg()

	r := gin.New()
	r.DELETE("/channels/:id", DeleteUpstream(cfgManager))

	req := httptest.NewRequest(http.MethodDelete, "/channels/999", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestMessagesChannels_UpdateUpstream_SingleKeySwapResetsMetrics(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{Name: "c0", BaseURL: "http://example.invalid", APIKeys: []string{"k1"}, ServiceType: "claude", Status: "suspended", Priority: 1},
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
	if cfgManager.GetConfig().Upstream[0].Status != "active" {
		t.Fatalf("expected status active")
	}
}

func TestPingURL_InvalidURLReturnsReqCreationFailed(t *testing.T) {
	got := pingURL("http://example.invalid/\n", false)
	if got["success"] != false || got["error"] != "req_creation_failed" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestPingChannelURLs_MultiURL_PrefersSuccessAndCoversFailureBranches(t *testing.T) {
	gin.SetMode(gin.TestMode)

	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer okSrv.Close()

	{
		got := pingChannelURLs(&config.UpstreamConfig{BaseURLs: []string{"http://example.invalid/\n", okSrv.URL}})
		if got["success"] != true || got["status"] != "healthy" {
			t.Fatalf("unexpected result: %+v", got)
		}
	}

	{
		got := pingChannelURLs(&config.UpstreamConfig{BaseURLs: []string{"http://example.invalid/\n1", "http://example.invalid/\r2"}})
		if got["success"] != false || got["status"] != "error" {
			t.Fatalf("unexpected result: %+v", got)
		}
	}
}

func TestPingChannelURLs_SingleURL_UsesPingURL(t *testing.T) {
	gin.SetMode(gin.TestMode)

	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer okSrv.Close()

	got := pingChannelURLs(&config.UpstreamConfig{BaseURLs: []string{okSrv.URL}})
	if got["success"] != true || got["status"] != "healthy" {
		t.Fatalf("unexpected result: %+v", got)
	}
}
