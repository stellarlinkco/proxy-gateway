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

func TestResponsesChannels_UpdateUpstream_SingleKeySwapResetsMetrics(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{},
		ResponsesUpstream: []config.UpstreamConfig{
			{Name: "r0", BaseURL: "http://example.invalid", APIKeys: []string{"k1"}, ServiceType: "responses", Status: "suspended", Priority: 1},
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

	updated := cfgManager.GetConfig().ResponsesUpstream[0]
	if updated.Status != "active" {
		t.Fatalf("status=%q, want %q", updated.Status, "active")
	}
}

