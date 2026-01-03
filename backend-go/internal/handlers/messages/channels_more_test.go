package messages

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/BenedictKing/claude-proxy/internal/monitor"
	"github.com/gin-gonic/gin"
)

func TestMessagesChannelsHandlers_ReorderInvalidOrderReturns400(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{Name: "c0", BaseURL: "http://example.invalid", APIKeys: []string{"k1"}, ServiceType: "claude", Status: "active", Priority: 1},
			{Name: "c1", BaseURL: "http://example.invalid", APIKeys: []string{"k2"}, ServiceType: "claude", Status: "active", Priority: 2},
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

	// 重复索引触发 cfgManager.ReorderUpstreams 的错误分支
	body, _ := json.Marshal(map[string]any{"order": []int{0, 0}})
	req := httptest.NewRequest(http.MethodPost, "/channels/reorder", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestMessagesChannels_PingURL_SuccessAndDoError(t *testing.T) {
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer okSrv.Close()

	ok := pingURL(okSrv.URL, false)
	if ok["success"] != true || ok["status"] != "healthy" {
		t.Fatalf("unexpected ok result: %+v", ok)
	}

	// 端口 0：快速失败，覆盖 client.Do error 分支
	bad := pingURL("http://127.0.0.1:0", false)
	if bad["success"] != false || bad["status"] != "error" {
		t.Fatalf("unexpected bad result: %+v", bad)
	}
}

func TestMessagesChannels_AddUpstream_SaveErrorReturns500(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configFile, []byte(`{"upstream":[],"loadBalance":"failover"}`), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfgManager, err := config.NewConfigManager(configFile)
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}
	defer cfgManager.Close()

	// 使配置文件不可写，触发 saveConfigLocked 的 WriteFile 失败
	if err := os.Chmod(configFile, 0444); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	r := gin.New()
	r.POST("/channels", AddUpstream(cfgManager))

	body, _ := json.Marshal(map[string]any{
		"name":        "c0",
		"serviceType": "claude",
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

func TestMessagesRequestLogContext_UpdateLive_RecordsRequest(t *testing.T) {
	live := monitor.NewLiveRequestManager(10)
	start := time.Now()

	ctx := &requestLogContext{
		requestID:          "req-1",
		startTime:          start,
		apiType:            "messages",
		model:              "claude-3",
		isStreaming:        true,
		channelIndex:       1,
		channelName:        "c1",
		apiKey:             "sk-test",
		liveRequestManager: live,
	}
	ctx.updateLive()

	if live.Count() != 1 {
		t.Fatalf("count=%d, want 1", live.Count())
	}
	reqs := live.GetAllRequests()
	if len(reqs) != 1 || reqs[0].RequestID != "req-1" || reqs[0].APIType != "messages" || reqs[0].ChannelName != "c1" {
		t.Fatalf("unexpected live requests: %+v", reqs)
	}
}

