package gemini

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/gin-gonic/gin"
)

func TestGeminiChannelsHandlers_AdditionalBranches(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		GeminiUpstream: []config.UpstreamConfig{
			{Name: "ok", BaseURL: "http://example.invalid", APIKeys: []string{"k1"}, ServiceType: "gemini", Status: "active", Priority: 1},
			{Name: "bad", BaseURL: "http://127.0.0.1:0", APIKeys: []string{"k2"}, ServiceType: "gemini", Status: "active", Priority: 2},
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
	r.POST("/channels/:id/keys/:apiKey/top", MoveApiKeyToTop(cfgManager))
	r.POST("/channels/:id/keys/:apiKey/bottom", MoveApiKeyToBottom(cfgManager))
	r.PATCH("/channels/:id/status", SetChannelStatus(cfgManager))
	r.GET("/channels/ping/:id", PingChannel(cfgManager))
	r.DELETE("/channels/:id", DeleteUpstream(cfgManager))

	t.Run("reorder duplicate returns 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"order": []int{0, 0}})
		req := httptest.NewRequest(http.MethodPost, "/channels/reorder", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("move api key error returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/channels/999/keys/k1/top", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}

		req2 := httptest.NewRequest(http.MethodPost, "/channels/999/keys/k1/bottom", nil)
		w2 := httptest.NewRecorder()
		r.ServeHTTP(w2, req2)
		if w2.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w2.Code, w2.Body.String())
		}
	})

	t.Run("set status invalid value returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPatch, "/channels/0/status", bytes.NewBufferString(`{"status":"nope"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("ping error path returns 200 with success false", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/channels/ping/1", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "\"success\":false") {
			t.Fatalf("unexpected body=%s", w.Body.String())
		}
	})

	t.Run("delete upstream out of range returns 500", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/channels/999", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})
}

func TestGeminiChannels_AddUpstream_SaveErrorReturns500(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configFile, []byte(`{"geminiUpstream":[],"geminiLoadBalance":"failover"}`), 0644); err != nil {
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
		"name":        "g0",
		"serviceType": "gemini",
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

