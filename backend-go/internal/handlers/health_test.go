package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/gin-gonic/gin"
)

func TestHealthCheck_IncludesVersionAndConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)

	SetVersionInfo("v9.9.9", "build-time", "deadbeef")

	cm, _ := newTestConfigManager(t, config.Config{
		Upstream: []config.UpstreamConfig{
			{Name: "m0", ServiceType: "claude", BaseURL: "https://example.com", APIKeys: []string{"k1"}},
		},
		LoadBalance:          "failover",
		ResponsesLoadBalance: "failover",
	})

	envCfg := &config.EnvConfig{Env: "development"}

	r := gin.New()
	r.GET("/health", HealthCheck(envCfg, cm))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	version, ok := resp["version"].(map[string]any)
	if !ok {
		t.Fatalf("missing version: %+v", resp)
	}
	if version["version"] != "v9.9.9" {
		t.Fatalf("version=%v", version["version"])
	}
}

func TestSaveConfigHandler_SuccessAndError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cm, path := newTestConfigManager(t, config.Config{
		LoadBalance:          "failover",
		ResponsesLoadBalance: "failover",
		GeminiLoadBalance:    "failover",
		FuzzyModeEnabled:     true,
	})

	r := gin.New()
	r.POST("/api/save", SaveConfigHandler(cm))

	// success
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/save", bytes.NewBuffer(nil))
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("success status=%d body=%s", w.Code, w.Body.String())
		}
	}

	// make config file read-only to force SaveConfig failure
	if err := os.Chmod(path, 0400); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/save", bytes.NewBuffer(nil))
		r.ServeHTTP(w, req)
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("error status=%d body=%s", w.Code, w.Body.String())
		}
	}
}

func TestDevInfoHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cm, _ := newTestConfigManager(t, config.Config{
		LoadBalance:          "failover",
		ResponsesLoadBalance: "failover",
		GeminiLoadBalance:    "failover",
		FuzzyModeEnabled:     true,
	})
	envCfg := &config.EnvConfig{Env: "development"}

	r := gin.New()
	r.GET("/dev", DevInfo(envCfg, cm))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dev", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["status"] != "development" {
		t.Fatalf("status=%v", resp["status"])
	}
}
