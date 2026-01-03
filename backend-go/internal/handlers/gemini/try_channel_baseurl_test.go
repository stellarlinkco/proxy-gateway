package gemini

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/BenedictKing/claude-proxy/internal/types"
	"github.com/gin-gonic/gin"
)

func TestTryChannelWithAllKeys_BaseURLFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var base1Calls atomic.Int64
	base1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/gemini-pro:generateContent" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		base1Calls.Add(1)

		w.Header().Set("Content-Type", "application/json")
		key := r.Header.Get("x-goog-api-key")
		if strings.Contains(key, "quota") {
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
		if r.URL.Path != "/v1beta/models/gemini-pro:generateContent" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		base2Calls.Add(1)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}],
  "usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":3,"totalTokenCount":5}
}`))
	}))
	defer base2.Close()

	cfg := config.Config{
		GeminiUpstream: []config.UpstreamConfig{
			{
				Name:        "g0",
				BaseURL:     base1.URL,
				BaseURLs:    []string{base1.URL, base2.URL},
				APIKeys:     []string{"quota", "bad"},
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

	envCfg := &config.EnvConfig{ProxyAccessKey: "secret", MaxRequestBodySize: 1024 * 1024}

	geminiReq := &types.GeminiRequest{
		Contents: []types.GeminiContent{
			{Role: "user", Parts: []types.GeminiPart{{Text: "hi"}}},
		},
	}
	bodyBytes, err := json.Marshal(geminiReq)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-pro:generateContent", bytes.NewReader(bodyBytes))
	c.Request.Header.Set("Content-Type", "application/json")

	ok, successKey, successBaseURLIdx, failoverErr, usage := tryChannelWithAllKeys(
		c,
		envCfg,
		cfgManager,
		sch,
		&cfg.GeminiUpstream[0],
		0,
		bodyBytes,
		geminiReq,
		"gemini-pro",
		false,
		time.Now(),
		nil,
	)

	if !ok || failoverErr != nil {
		t.Fatalf("ok=%v failoverErr=%+v", ok, failoverErr)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "\"candidates\"") {
		t.Fatalf("unexpected body=%s", rec.Body.String())
	}
	if base1Calls.Load() != 2 || base2Calls.Load() != 1 {
		t.Fatalf("calls base1=%d base2=%d, want 2/1", base1Calls.Load(), base2Calls.Load())
	}
	if successKey == "" {
		t.Fatalf("expected successKey")
	}
	if successBaseURLIdx != 1 {
		t.Fatalf("successBaseURLIdx=%d, want 1", successBaseURLIdx)
	}
	if usage == nil || usage.InputTokens == 0 || usage.OutputTokens == 0 {
		t.Fatalf("unexpected usage=%+v", usage)
	}
}
