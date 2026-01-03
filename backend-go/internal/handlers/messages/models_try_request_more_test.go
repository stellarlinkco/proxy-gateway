package messages

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/gin-gonic/gin"
)

func TestTryModelsRequest_ErrorBranches_ReturnsFalse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("no_channels", func(t *testing.T) {
		cfg := config.Config{
			Upstream:             nil,
			ResponsesUpstream:    nil,
			LoadBalance:          "failover",
			ResponsesLoadBalance: "failover",
			GeminiLoadBalance:    "failover",
			FuzzyModeEnabled:     true,
		}
		cfgManager, cleanupCfg := createTestConfigManager(t, cfg)
		defer cleanupCfg()

		sch, cleanupSch := createTestScheduler(t, cfgManager)
		defer cleanupSch()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)

		body, ok := tryModelsRequest(c, cfgManager, sch, http.MethodGet, "", false)
		if ok || body != nil {
			t.Fatalf("ok=%v body=%v, want false nil", ok, body)
		}
	})

	t.Run("channel_has_no_keys", func(t *testing.T) {
		cfg := config.Config{
			Upstream: []config.UpstreamConfig{
				{Name: "c0", BaseURL: "http://example.invalid", APIKeys: nil, Status: "active", Priority: 1},
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

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)

		body, ok := tryModelsRequest(c, cfgManager, sch, http.MethodGet, "", false)
		if ok || body != nil {
			t.Fatalf("ok=%v body=%v, want false nil", ok, body)
		}
	})

	t.Run("invalid_url_causes_new_request_error", func(t *testing.T) {
		cfg := config.Config{
			Upstream: []config.UpstreamConfig{
				{Name: "c0", BaseURL: "http://example.invalid/\n", APIKeys: []string{"k1"}, Status: "active", Priority: 1},
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

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)

		body, ok := tryModelsRequest(c, cfgManager, sch, http.MethodGet, "", false)
		if ok || body != nil {
			t.Fatalf("ok=%v body=%v, want false nil", ok, body)
		}
	})

	t.Run("read_body_error", func(t *testing.T) {
		badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/models" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Fatalf("ResponseWriter does not implement Hijacker")
			}
			conn, buf, err := hj.Hijack()
			if err != nil {
				t.Fatalf("Hijack: %v", err)
			}
			defer conn.Close()
			_, _ = buf.WriteString("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{\"object\":\"list\"")
			_ = buf.Flush()
		}))
		defer badSrv.Close()

		cfg := config.Config{
			Upstream: []config.UpstreamConfig{
				{Name: "c0", BaseURL: badSrv.URL, APIKeys: []string{"k1"}, Status: "active", Priority: 1},
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

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)

		body, ok := tryModelsRequest(c, cfgManager, sch, http.MethodGet, "", false)
		if ok || body != nil {
			t.Fatalf("ok=%v body=%v, want false nil", ok, body)
		}
	})

	t.Run("client_do_error", func(t *testing.T) {
		cfg := config.Config{
			Upstream: []config.UpstreamConfig{
				{Name: "c0", BaseURL: "http://127.0.0.1:0", APIKeys: []string{"k1"}, Status: "active", Priority: 1},
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

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)

		body, ok := tryModelsRequest(c, cfgManager, sch, http.MethodGet, "", false)
		if ok || body != nil {
			t.Fatalf("ok=%v body=%v, want false nil", ok, body)
		}
	})

	t.Run("non_200_skips_channel_then_succeeds", func(t *testing.T) {
		var badCalls atomic.Int64
		badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/models" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			badCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
		}))
		defer badSrv.Close()

		var goodCalls atomic.Int64
		goodSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/models" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			goodCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"m1","object":"model","created":1,"owned_by":"x"}]}`))
		}))
		defer goodSrv.Close()

		cfg := config.Config{
			Upstream: []config.UpstreamConfig{
				{Name: "bad", BaseURL: badSrv.URL, APIKeys: []string{"k1"}, Status: "active", Priority: 1},
				{Name: "good", BaseURL: goodSrv.URL, APIKeys: []string{"k2"}, Status: "active", Priority: 2},
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

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)

		body, ok := tryModelsRequest(c, cfgManager, sch, http.MethodGet, "", false)
		if !ok || body == nil {
			t.Fatalf("ok=%v body=%v, want true non-nil", ok, body)
		}
		if badCalls.Load() != 1 || goodCalls.Load() != 1 {
			t.Fatalf("calls bad=%d good=%d, want 1/1", badCalls.Load(), goodCalls.Load())
		}
	})
}
