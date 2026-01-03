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

func TestGeminiChannelsHandlers_EdgeCases(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		GeminiUpstream: []config.UpstreamConfig{
			{
				Name:        "g0",
				BaseURL:     "http://example.invalid",
				APIKeys:     []string{"k1"},
				ServiceType: "gemini",
				Status:      "active",
				Priority:    1,
			},
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
	r.POST("/channels", AddUpstream(cfgManager))
	r.PUT("/channels/:id", UpdateUpstream(cfgManager, sch))
	r.DELETE("/channels/:id", DeleteUpstream(cfgManager))
	r.POST("/channels/:id/keys", AddApiKey(cfgManager))
	r.DELETE("/channels/:id/keys/:apiKey", DeleteApiKey(cfgManager))
	r.POST("/channels/reorder", ReorderChannels(cfgManager))

	t.Run("add upstream invalid json returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/channels", bytes.NewBufferString("{"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("update upstream invalid id returns 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"name": "x"})
		req := httptest.NewRequest(http.MethodPut, "/channels/bad", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("update upstream invalid json returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/channels/0", bytes.NewBufferString("{"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("delete upstream invalid id returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/channels/bad", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("add api key invalid id returns 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"apiKey": "k-new"})
		req := httptest.NewRequest(http.MethodPost, "/channels/bad/keys", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("add api key invalid json returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/channels/0/keys", bytes.NewBufferString("{"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("add api key duplicate returns 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"apiKey": "k1"})
		req := httptest.NewRequest(http.MethodPost, "/channels/0/keys", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("add api key upstream not found returns 404", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"apiKey": "k-new"})
		req := httptest.NewRequest(http.MethodPost, "/channels/999/keys", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("delete api key invalid id returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/channels/bad/keys/k1", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("delete api key upstream not found returns 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/channels/999/keys/k1", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("delete api key not found returns 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/channels/0/keys/nope", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("reorder invalid json returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/channels/reorder", bytes.NewBufferString("{"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})
}

