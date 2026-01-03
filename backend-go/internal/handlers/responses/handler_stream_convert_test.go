package responses

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/BenedictKing/claude-proxy/internal/session"
	"github.com/gin-gonic/gin"
)

func TestResponsesHandler_Stream_ConvertsOpenAIChatAndPatchesUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl_1\",\"choices\":[{\"delta\":{\"content\":\"h\"}}]}\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl_1\",\"choices\":[{\"delta\":{\"content\":\"i\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":0,\"completion_tokens\":0}}\n"))
		_, _ = w.Write([]byte("data: [DONE]\n"))
	}))
	defer upstream.Close()

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{},
		ResponsesUpstream: []config.UpstreamConfig{
			{
				Name:        "openai",
				BaseURL:     upstream.URL,
				APIKeys:     []string{"rk1"},
				ServiceType: "openai",
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

	sessionManager := session.NewSessionManager(time.Hour, 10, 1000)

	envCfg := &config.EnvConfig{
		ProxyAccessKey:     "secret",
		MaxRequestBodySize: 1024 * 1024,
		Env:                "development",
		EnableResponseLogs: false,
		LogLevel:           "debug",
	}

	r := gin.New()
	r.POST("/v1/responses", NewHandler(envCfg, cfgManager, sessionManager, sch, nil, nil, nil, nil))

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(`{"model":"gpt-4o","input":"hello","stream":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var completedJSON string
	for _, line := range strings.Split(w.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		if strings.Contains(line, "response.completed") {
			completedJSON = strings.TrimPrefix(line, "data: ")
			break
		}
	}
	if completedJSON == "" {
		t.Fatalf("missing response.completed event in body: %s", w.Body.String())
	}

	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(completedJSON), &obj); err != nil {
		t.Fatalf("unmarshal completed: %v", err)
	}
	resp, _ := obj["response"].(map[string]interface{})
	usage, _ := resp["usage"].(map[string]interface{})
	if usage == nil {
		t.Fatalf("missing usage in completed event: %s", completedJSON)
	}
	inTok, _ := usage["input_tokens"].(float64)
	outTok, _ := usage["output_tokens"].(float64)
	if inTok <= 0 || outTok <= 0 {
		t.Fatalf("expected patched usage tokens >0, got: %+v", usage)
	}
}
