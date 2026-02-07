package handlers

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BenedictKing/claude-proxy/internal/cache"
	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/BenedictKing/claude-proxy/internal/metrics"
	"github.com/BenedictKing/claude-proxy/internal/middleware"
	"github.com/gin-gonic/gin"
)

func TestGetCacheStats_ProtectedAndReturnsMetrics(t *testing.T) {
	gin.SetMode(gin.TestMode)

	envCfg := &config.EnvConfig{
		ProxyAccessKey: "secret-key",
		EnableWebUI:    true,
	}

	modelsMetrics := &metrics.CacheMetrics{}
	modelsCache := cache.NewHTTPResponseCache(10, time.Minute, modelsMetrics)

	modelsCache.Set("k1", cache.HTTPResponse{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{gin.MIMEJSON}},
		Body:       []byte(`{"ok":true}`),
	})
	_, _ = modelsCache.Get("k1")
	_, _ = modelsCache.Get("missing")

	r := gin.New()
	r.Use(middleware.WebAuthMiddleware(envCfg, nil))
	r.GET("/api/cache/stats", GetCacheStats(modelsCache, modelsMetrics))

	t.Run("missing key returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/cache/stats", nil)
		w := httptest.NewRecorder()

		r.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
		}
	})

	t.Run("correct key returns expected JSON", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/cache/stats", nil)
		req.Header.Set("x-api-key", envCfg.ProxyAccessKey)
		w := httptest.NewRecorder()

		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}

		var resp CacheStatsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		if resp.Models.ReadHit != 1 {
			t.Fatalf("readHit = %d, want %d", resp.Models.ReadHit, 1)
		}
		if resp.Models.ReadMiss != 1 {
			t.Fatalf("readMiss = %d, want %d", resp.Models.ReadMiss, 1)
		}
		if resp.Models.WriteSet != 1 {
			t.Fatalf("writeSet = %d, want %d", resp.Models.WriteSet, 1)
		}
		if resp.Models.WriteUpdate != 0 {
			t.Fatalf("writeUpdate = %d, want %d", resp.Models.WriteUpdate, 0)
		}
		if resp.Models.Entries != 1 {
			t.Fatalf("entries = %d, want %d", resp.Models.Entries, 1)
		}
		if resp.Models.Capacity != 10 {
			t.Fatalf("capacity = %d, want %d", resp.Models.Capacity, 10)
		}
		if math.Abs(resp.Models.HitRate-0.5) > 1e-9 {
			t.Fatalf("hitRate = %v, want %v", resp.Models.HitRate, 0.5)
		}
		if resp.Timestamp.IsZero() {
			t.Fatalf("timestamp should not be zero")
		}
	})
}
