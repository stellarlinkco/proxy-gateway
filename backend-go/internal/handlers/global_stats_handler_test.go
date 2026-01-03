package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BenedictKing/claude-proxy/internal/metrics"
	"github.com/BenedictKing/claude-proxy/internal/types"
	"github.com/gin-gonic/gin"
)

func TestGetGlobalStatsHistory_ValidationsAndToday(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mm := metrics.NewMetricsManagerWithConfig(3, 0.5)
	t.Cleanup(mm.Stop)

	// Seed some token data so response isn't totally empty.
	mm.RecordSuccessWithUsage("https://example.com", "k1", &types.Usage{InputTokens: 10, OutputTokens: 10}, "m", 10)

	r := gin.New()
	r.GET("/stats", GetGlobalStatsHistory(mm))

	// invalid duration
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/stats?duration=bad", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}

	// invalid interval
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/stats?duration=1h&interval=bad", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}

	// today (and interval clamp)
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/stats?duration=today&interval=30s", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}

		var resp map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		summary, ok := resp["summary"].(map[string]any)
		if !ok {
			t.Fatalf("missing summary: %+v", resp)
		}
		if summary["duration"] != "today" {
			t.Fatalf("duration=%v", summary["duration"])
		}
	}

	// long duration hits auto interval branches
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/stats?duration=30d", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}

	for _, duration := range []string{"1h", "6h", "24h", "7d"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/stats?duration="+duration, nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("duration=%s status=%d body=%s", duration, w.Code, w.Body.String())
		}
	}
}

func TestGetGlobalStatsHistory_TodayDurationClamp(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mm := metrics.NewMetricsManagerWithConfig(3, 0.5)
	t.Cleanup(mm.Stop)

	r := gin.New()
	r.GET("/stats", GetGlobalStatsHistory(mm))

	oldLocal := time.Local
	t.Cleanup(func() { time.Local = oldLocal })

	utc := time.Now().UTC()
	secondsIntoDay := utc.Hour()*3600 + utc.Minute()*60 + utc.Second()
	time.Local = time.FixedZone("test-today", 10-secondsIntoDay)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/stats?duration=today", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	summary, ok := resp["summary"].(map[string]any)
	if !ok {
		t.Fatalf("missing summary: %+v", resp)
	}
	if summary["duration"] != "today" {
		t.Fatalf("duration=%v", summary["duration"])
	}
}
