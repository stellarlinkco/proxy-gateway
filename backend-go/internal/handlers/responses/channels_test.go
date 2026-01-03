package responses

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/gin-gonic/gin"
)

func TestResponsesChannelsHandlers_CRUD(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{},
		ResponsesUpstream: []config.UpstreamConfig{
			{
				Name:        "r0",
				BaseURL:     "http://example.invalid",
				APIKeys:     []string{"k1", "k2"},
				ServiceType: "responses",
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
	r.PUT("/loadbalance", UpdateLoadBalance(cfgManager))
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
			"name":        "r1",
			"serviceType": "responses",
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
			"name": "r0-updated",
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

	t.Run("set load balance/status works", func(t *testing.T) {
		lbJSON, _ := json.Marshal(map[string]string{"strategy": "random"})
		reqLB := httptest.NewRequest(http.MethodPut, "/loadbalance", bytes.NewReader(lbJSON))
		reqLB.Header.Set("Content-Type", "application/json")
		wLB := httptest.NewRecorder()
		r.ServeHTTP(wLB, reqLB)
		if wLB.Code != http.StatusOK {
			t.Fatalf("loadbalance = %d, want %d", wLB.Code, http.StatusOK)
		}

		statusJSON, _ := json.Marshal(map[string]string{"status": "suspended"})
		reqStatus := httptest.NewRequest(http.MethodPatch, "/channels/0/status", bytes.NewReader(statusJSON))
		reqStatus.Header.Set("Content-Type", "application/json")
		wStatus := httptest.NewRecorder()
		r.ServeHTTP(wStatus, reqStatus)
		if wStatus.Code != http.StatusOK {
			t.Fatalf("set status = %d, want %d", wStatus.Code, http.StatusOK)
		}
	})
}

