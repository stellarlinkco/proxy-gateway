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

func TestFuzzyModeHandlers_GetAndSet(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cm, _ := newTestConfigManager(t, config.Config{
		LoadBalance:          "failover",
		ResponsesLoadBalance: "failover",
		GeminiLoadBalance:    "failover",
		FuzzyModeEnabled:     false,
	})

	r := gin.New()
	r.GET("/api/settings/fuzzy", GetFuzzyMode(cm))
	r.POST("/api/settings/fuzzy", SetFuzzyMode(cm))

	// GET initial
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/settings/fuzzy", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GET status=%d", w.Code)
		}
		var resp struct {
			Enabled bool `json:"fuzzyModeEnabled"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if resp.Enabled {
			t.Fatalf("expected fuzzyModeEnabled=false")
		}
	}

	// POST invalid body
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/settings/fuzzy", bytes.NewBufferString("{"))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("POST invalid status=%d", w.Code)
		}
	}

	// POST enable
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/settings/fuzzy", bytes.NewBufferString(`{"enabled":true}`))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("POST status=%d body=%s", w.Code, w.Body.String())
		}
	}

	// GET updated
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/settings/fuzzy", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GET2 status=%d", w.Code)
		}
		var resp struct {
			Enabled bool `json:"fuzzyModeEnabled"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !resp.Enabled {
			t.Fatalf("expected fuzzyModeEnabled=true")
		}
	}
}

func TestSetFuzzyMode_SaveFailReturns500(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cm, path := newTestConfigManager(t, config.Config{
		LoadBalance:          "failover",
		ResponsesLoadBalance: "failover",
		GeminiLoadBalance:    "failover",
		FuzzyModeEnabled:     false,
	})

	if err := os.Chmod(path, 0400); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	r := gin.New()
	r.POST("/api/settings/fuzzy", SetFuzzyMode(cm))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/settings/fuzzy", bytes.NewBufferString(`{"enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
