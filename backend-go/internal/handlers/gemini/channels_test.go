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

func TestGeminiChannelsHandlers_CRUD(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		GeminiUpstream: []config.UpstreamConfig{
			{
				Name:        "g0",
				BaseURL:     "http://example.invalid",
				APIKeys:     []string{"k1", "k2"},
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
	r.GET("/channels", GetUpstreams(cfgManager))
	r.POST("/channels", AddUpstream(cfgManager))
	r.PUT("/channels/:id", UpdateUpstream(cfgManager, sch))
	r.DELETE("/channels/:id", DeleteUpstream(cfgManager))
	r.POST("/channels/:id/keys", AddApiKey(cfgManager))
	r.DELETE("/channels/:id/keys/:apiKey", DeleteApiKey(cfgManager))
	r.POST("/channels/:id/keys/:apiKey/top", MoveApiKeyToTop(cfgManager))
	r.POST("/channels/:id/keys/:apiKey/bottom", MoveApiKeyToBottom(cfgManager))
	r.POST("/channels/reorder", ReorderChannels(cfgManager))
	r.PATCH("/channels/:id/status", SetChannelStatus(cfgManager))

	t.Run("get upstreams returns 200", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/channels", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("add/update/reorder/delete upstream works", func(t *testing.T) {
		addBody := map[string]interface{}{
			"name":        "g1",
			"serviceType": "gemini",
			"baseUrl":     "http://example.invalid",
			"apiKeys":     []string{"k3"},
			"status":      "active",
			"priority":    2,
		}
		addJSON, _ := json.Marshal(addBody)
		reqAdd := httptest.NewRequest(http.MethodPost, "/channels", bytes.NewReader(addJSON))
		reqAdd.Header.Set("Content-Type", "application/json")
		wAdd := httptest.NewRecorder()
		r.ServeHTTP(wAdd, reqAdd)
		if wAdd.Code != http.StatusOK {
			t.Fatalf("add status = %d, want %d", wAdd.Code, http.StatusOK)
		}

		updateBody := map[string]interface{}{
			"name": "g0-updated",
		}
		updateJSON, _ := json.Marshal(updateBody)
		reqUpdate := httptest.NewRequest(http.MethodPut, "/channels/0", bytes.NewReader(updateJSON))
		reqUpdate.Header.Set("Content-Type", "application/json")
		wUpdate := httptest.NewRecorder()
		r.ServeHTTP(wUpdate, reqUpdate)
		if wUpdate.Code != http.StatusOK {
			t.Fatalf("update status = %d, want %d", wUpdate.Code, http.StatusOK)
		}

		reorderJSON, _ := json.Marshal(map[string]interface{}{"order": []int{1, 0}})
		reqReorder := httptest.NewRequest(http.MethodPost, "/channels/reorder", bytes.NewReader(reorderJSON))
		reqReorder.Header.Set("Content-Type", "application/json")
		wReorder := httptest.NewRecorder()
		r.ServeHTTP(wReorder, reqReorder)
		if wReorder.Code != http.StatusOK {
			t.Fatalf("reorder status = %d, want %d", wReorder.Code, http.StatusOK)
		}

		reqDelete := httptest.NewRequest(http.MethodDelete, "/channels/1", nil)
		wDelete := httptest.NewRecorder()
		r.ServeHTTP(wDelete, reqDelete)
		if wDelete.Code != http.StatusOK {
			t.Fatalf("delete status = %d, want %d", wDelete.Code, http.StatusOK)
		}
	})

	t.Run("api key operations work", func(t *testing.T) {
		addKeyJSON, _ := json.Marshal(map[string]string{"apiKey": "k-new"})
		reqAdd := httptest.NewRequest(http.MethodPost, "/channels/0/keys", bytes.NewReader(addKeyJSON))
		reqAdd.Header.Set("Content-Type", "application/json")
		wAdd := httptest.NewRecorder()
		r.ServeHTTP(wAdd, reqAdd)
		if wAdd.Code != http.StatusOK {
			t.Fatalf("add key status = %d, want %d", wAdd.Code, http.StatusOK)
		}

		reqTop := httptest.NewRequest(http.MethodPost, "/channels/0/keys/k-new/top", nil)
		wTop := httptest.NewRecorder()
		r.ServeHTTP(wTop, reqTop)
		if wTop.Code != http.StatusOK {
			t.Fatalf("move top status = %d, want %d", wTop.Code, http.StatusOK)
		}

		reqBottom := httptest.NewRequest(http.MethodPost, "/channels/0/keys/k-new/bottom", nil)
		wBottom := httptest.NewRecorder()
		r.ServeHTTP(wBottom, reqBottom)
		if wBottom.Code != http.StatusOK {
			t.Fatalf("move bottom status = %d, want %d", wBottom.Code, http.StatusOK)
		}

		reqDel := httptest.NewRequest(http.MethodDelete, "/channels/0/keys/k-new", nil)
		wDel := httptest.NewRecorder()
		r.ServeHTTP(wDel, reqDel)
		if wDel.Code != http.StatusOK {
			t.Fatalf("delete key status = %d, want %d", wDel.Code, http.StatusOK)
		}
	})
}

func TestGeminiChannelsHandlers_PingPromotionAndLoadBalance(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstreamOK := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstreamOK.Close()

	cfg := config.Config{
		GeminiUpstream: []config.UpstreamConfig{
			{Name: "ok", BaseURL: upstreamOK.URL, APIKeys: []string{"k1"}, ServiceType: "gemini", Status: "active", Priority: 1},
			{Name: "nourl", BaseURL: "", APIKeys: []string{"k2"}, ServiceType: "gemini", Status: "active", Priority: 2},
			{Name: "bad", BaseURL: "http://127.0.0.1:0", APIKeys: []string{"k3"}, ServiceType: "gemini", Status: "active", Priority: 3},
		},
		LoadBalance:          "failover",
		ResponsesLoadBalance: "failover",
		GeminiLoadBalance:    "failover",
		FuzzyModeEnabled:     true,
	}

	cfgManager, cleanupCfg := createTestConfigManager(t, cfg)
	defer cleanupCfg()

	r := gin.New()
	r.PATCH("/channels/:id/promotion", SetChannelPromotion(cfgManager))
	r.GET("/channels/ping/:id", PingChannel(cfgManager))
	r.GET("/channels/ping", PingAllChannels(cfgManager))
	r.PATCH("/loadbalance", UpdateLoadBalance(cfgManager))
	r.PATCH("/channels/:id/status", SetChannelStatus(cfgManager))

	// promotion invalid id
	{
		req := httptest.NewRequest(http.MethodPatch, "/channels/bad/promotion", bytes.NewBufferString(`{"duration":10}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
	// promotion invalid json
	{
		req := httptest.NewRequest(http.MethodPatch, "/channels/0/promotion", bytes.NewBufferString("{"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
	// promotion set and clear
	{
		req := httptest.NewRequest(http.MethodPatch, "/channels/0/promotion", bytes.NewBufferString(`{"duration":10}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		req2 := httptest.NewRequest(http.MethodPatch, "/channels/0/promotion", bytes.NewBufferString(`{"duration":0}`))
		req2.Header.Set("Content-Type", "application/json")
		w2 := httptest.NewRecorder()
		r.ServeHTTP(w2, req2)
		if w2.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w2.Code, w2.Body.String())
		}
	}

	// loadbalance invalid body
	{
		req := httptest.NewRequest(http.MethodPatch, "/loadbalance", bytes.NewBufferString("{"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
	// loadbalance invalid strategy
	{
		req := httptest.NewRequest(http.MethodPatch, "/loadbalance", bytes.NewBufferString(`{"strategy":"nope"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
	// loadbalance success
	{
		req := httptest.NewRequest(http.MethodPatch, "/loadbalance", bytes.NewBufferString(`{"strategy":"failover"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}

	// status invalid id / out of range
	{
		req := httptest.NewRequest(http.MethodPatch, "/channels/bad/status", bytes.NewBufferString(`{"status":"active"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
	{
		req := httptest.NewRequest(http.MethodPatch, "/channels/999/status", bytes.NewBufferString(`{"status":"active"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
	{
		req := httptest.NewRequest(http.MethodPatch, "/channels/0/status", bytes.NewBufferString(`{"status":"suspended"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}

	// ping invalid id
	{
		req := httptest.NewRequest(http.MethodGet, "/channels/ping/bad", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
	// ping channel not found
	{
		req := httptest.NewRequest(http.MethodGet, "/channels/ping/999", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
	// ping no baseURL configured
	{
		req := httptest.NewRequest(http.MethodGet, "/channels/ping/1", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
	// ping success path
	{
		req := httptest.NewRequest(http.MethodGet, "/channels/ping/0", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}

	// ping all channels covers success + no baseURL + error
	{
		req := httptest.NewRequest(http.MethodGet, "/channels/ping", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
}
