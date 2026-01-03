package messages

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/gin-gonic/gin"
)

func TestMessagesChannelsHandlers_ErrorPaths(t *testing.T) {
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

	sch, cleanupSch := createTestScheduler(t, cfgManager)
	defer cleanupSch()

	r := gin.New()
	r.POST("/channels", AddUpstream(cfgManager))
	r.PUT("/channels/:id", UpdateUpstream(cfgManager, sch))
	r.DELETE("/channels/:id", DeleteUpstream(cfgManager))
	r.POST("/channels/:id/keys", AddApiKey(cfgManager))
	r.DELETE("/channels/:id/keys/:apiKey", DeleteApiKey(cfgManager))
	r.POST("/channels/:id/keys/:apiKey/top", MoveApiKeyToTop(cfgManager))
	r.POST("/channels/:id/keys/:apiKey/bottom", MoveApiKeyToBottom(cfgManager))
	r.PUT("/loadbalance", UpdateLoadBalance(cfgManager))
	r.POST("/channels/reorder", ReorderChannels(cfgManager))
	r.PATCH("/channels/:id/status", SetChannelStatus(cfgManager))
	r.POST("/channels/:id/promotion", SetChannelPromotion(cfgManager))
	r.GET("/ping/:id", PingChannel(cfgManager))

	t.Run("add upstream invalid json -> 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/channels", bytes.NewBufferString("{"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("update upstream invalid id -> 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/channels/bad", bytes.NewBufferString(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("update upstream invalid json -> 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/channels/0", bytes.NewBufferString("{"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("update upstream out of range -> 404", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"name": "x"})
		req := httptest.NewRequest(http.MethodPut, "/channels/999", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("delete upstream invalid id -> 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/channels/bad", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("delete upstream out of range -> 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/channels/999", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("add api key invalid json -> 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/channels/0/keys", bytes.NewBufferString("{"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("add api key invalid id -> 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"apiKey": "k-new"})
		req := httptest.NewRequest(http.MethodPost, "/channels/bad/keys", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("add api key duplicate -> 400", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"apiKey": "k1"})
		req := httptest.NewRequest(http.MethodPost, "/channels/0/keys", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("delete api key not found -> 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/channels/0/keys/not-exist", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("delete api key invalid id -> 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/channels/bad/keys/k1", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("delete api key upstream not found -> 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/channels/999/keys/k1", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("move api key invalid id -> 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/channels/bad/keys/k1/top", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("move api key out of range -> 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/channels/999/keys/k1/top", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("move api key bottom invalid id -> 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/channels/bad/keys/k1/bottom", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("move api key bottom out of range -> 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/channels/999/keys/k1/bottom", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("loadbalance invalid json -> 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/loadbalance", bytes.NewBufferString("{"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("loadbalance invalid strategy -> 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/loadbalance", bytes.NewBufferString(`{"strategy":"nope"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("reorder invalid json -> 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/channels/reorder", bytes.NewBufferString("{"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("status invalid id -> 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPatch, "/channels/bad/status", bytes.NewBufferString(`{"status":"active"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("status invalid json -> 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPatch, "/channels/0/status", bytes.NewBufferString("{"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("status invalid value -> 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPatch, "/channels/0/status", bytes.NewBufferString(`{"status":"nope"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("promotion invalid json -> 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/channels/0/promotion", bytes.NewBufferString("{"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("promotion invalid id -> 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/channels/bad/promotion", bytes.NewBufferString(`{"duration":10}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("promotion out of range -> 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/channels/999/promotion", bytes.NewBufferString(`{"duration":10}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("ping invalid id -> 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/ping/bad", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})
	t.Run("ping out of range -> 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/ping/999", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	})
}
