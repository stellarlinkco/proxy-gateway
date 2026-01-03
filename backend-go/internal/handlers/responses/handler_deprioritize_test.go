package responses

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/BenedictKing/claude-proxy/internal/session"
	"github.com/BenedictKing/claude-proxy/internal/types"
	"github.com/gin-gonic/gin"
)

func TestHandleSingleChannel_BaseURLFailover_DeprioritizeSaveErrorIsLogged(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var base1Calls atomic.Int64
	base1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		base1Calls.Add(1)

		w.Header().Set("Content-Type", "application/json")
		auth := r.Header.Get("Authorization")
		if strings.Contains(auth, "r-quota") {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"quota exceeded"}}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
	}))
	defer base1.Close()

	var base2Calls atomic.Int64
	base2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		base2Calls.Add(1)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "id":"resp_1",
  "model":"gpt-4o",
  "status":"completed",
  "output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],
  "usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}
}`))
	}))
	defer base2.Close()

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{},
		ResponsesUpstream: []config.UpstreamConfig{
			{
				Name:        "r0",
				BaseURL:     base1.URL,
				BaseURLs:    []string{base1.URL, base2.URL},
				APIKeys:     []string{"r-quota", "r-good"},
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

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(configFile, data, 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfgManager, err := config.NewConfigManager(configFile)
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}
	defer cfgManager.Close()

	if err := os.Chmod(configFile, 0444); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	sch, cleanupSch := createTestScheduler(t, cfgManager)
	defer cleanupSch()

	sessionManager := session.NewSessionManager(time.Hour, 100, 100000)

	envCfg := &config.EnvConfig{
		MaxRequestBodySize: 1024 * 1024,
		Env:                "development",
		EnableResponseLogs: true,
		RawLogOutput:       false,
	}

	responsesReq := types.ResponsesRequest{
		Model: "gpt-4o",
		Input: "hi",
	}
	bodyBytes, err := json.Marshal(responsesReq)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(bodyBytes))
	c.Request.Header.Set("Content-Type", "application/json")

	var logBuf bytes.Buffer
	oldOutput := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(oldOutput) })

	handleSingleChannel(
		c,
		envCfg,
		cfgManager,
		sch,
		sessionManager,
		bodyBytes,
		responsesReq,
		time.Now(),
		nil,
		nil,
		nil,
	)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"id":"resp_1"`) {
		t.Fatalf("unexpected body=%s", rec.Body.String())
	}
	if base1Calls.Load() != 2 || base2Calls.Load() != 1 {
		t.Fatalf("calls base1=%d base2=%d, want 2/1", base1Calls.Load(), base2Calls.Load())
	}
	if !strings.Contains(logBuf.String(), "密钥降级失败") {
		t.Fatalf("expected deprioritize error log, got=%s", logBuf.String())
	}
}
