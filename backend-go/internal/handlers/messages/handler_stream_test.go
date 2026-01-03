package messages

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/gin-gonic/gin"
)

func TestMessagesHandler_MultiChannel_Stream_FailoverToNextChannel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var callsBad atomic.Int64
	upstreamBad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		callsBad.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
	}))
	defer upstreamBad.Close()

	var callsGood atomic.Int64
	upstreamGood := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		callsGood.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		sse := strings.Join([]string{
			"event: message_start",
			"data: {\"type\":\"message_start\",\"message\":{\"id\":\"\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"wrong-model\",\"content\":[]}}",
			"",
			"event: content_block_delta",
			"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}",
			"",
			"event: message_delta",
			"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"input_tokens\":null,\"output_tokens\":0}}",
			"",
			"event: message_stop",
			"data: {\"type\":\"message_stop\"}",
			"",
		}, "\n")
		_, _ = w.Write([]byte(sse))
	}))
	defer upstreamGood.Close()

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{Name: "bad", BaseURL: upstreamBad.URL, APIKeys: []string{"k1"}, ServiceType: "claude", Status: "active", Priority: 1},
			{Name: "good", BaseURL: upstreamGood.URL, APIKeys: []string{"k2"}, ServiceType: "claude", Status: "active", Priority: 2},
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

	envCfg := &config.EnvConfig{
		ProxyAccessKey:     "secret",
		MaxRequestBodySize: 1024 * 1024,
		Env:                "development",
		EnableResponseLogs: true,
		SSEDebugLevel:      "summary",
		LogLevel:           "debug",
	}

	h := NewHandler(envCfg, cfgManager, sch, nil, nil, nil, nil)
	r := gin.New()
	r.POST("/v1/messages", h)

	reqBody := `{"model":"claude-3","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if callsBad.Load() != 1 || callsGood.Load() != 1 {
		t.Fatalf("calls bad=%d good=%d, want 1/1", callsBad.Load(), callsGood.Load())
	}
	// message_start 应被修补为请求模型，而不是 wrong-model
	if !strings.Contains(w.Body.String(), `"model":"claude-3"`) {
		t.Fatalf("expected patched model in stream, got: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "wrong-model") {
		t.Fatalf("expected wrong-model removed, got: %s", w.Body.String())
	}
}
