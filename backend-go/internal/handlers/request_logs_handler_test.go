package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/BenedictKing/claude-proxy/internal/metrics"
	"github.com/gin-gonic/gin"
)

func TestRequestLogsHandler_NilOrMissingStore(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/messages/logs", nil)

	var h *RequestLogsHandler
	h.GetLogs(c)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestRequestLogsHandler_GetLogs_ParsesAPITypeAndPagination(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dbPath := filepath.Join(t.TempDir(), "metrics.db")
	store, err := metrics.NewSQLiteStore(&metrics.SQLiteStoreConfig{DBPath: dbPath, RetentionDays: 3})
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.AddRequestLog(metrics.RequestLogRecord{
		RequestID:    "req_1",
		ChannelIndex: 0,
		ChannelName:  "c0",
		KeyMask:      "***",
		Timestamp:    time.Now(),
		DurationMs:   10,
		StatusCode:   200,
		Success:      true,
		Model:        "m",
		APIType:      "messages",
	}); err != nil {
		t.Fatalf("AddRequestLog: %v", err)
	}

	h := NewRequestLogsHandler(store)
	r := gin.New()
	r.GET("/api/messages/logs", h.GetLogs)
	r.GET("/api/foo/logs", h.GetLogs)

	// valid
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/messages/logs?limit=1&offset=0", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}

		var resp metrics.RequestLogsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if resp.Total != 1 || len(resp.Logs) != 1 || resp.Limit != 1 || resp.Offset != 0 {
			t.Fatalf("unexpected resp: %+v", resp)
		}
	}

	// invalid apiType
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/foo/logs", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
}

func TestRequestLogsHandler_FallbackToRequestPathAndQueryError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dbPath := filepath.Join(t.TempDir(), "metrics.db")
	store, err := metrics.NewSQLiteStore(&metrics.SQLiteStoreConfig{DBPath: dbPath, RetentionDays: 3})
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}

	h := NewRequestLogsHandler(store)

	// FullPath() is empty when calling handler directly; it should fallback to Request.URL.Path.
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/messages/logs?limit=999&offset=-1", nil)
	h.GetLogs(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp metrics.RequestLogsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Limit != 200 || resp.Offset != 0 {
		t.Fatalf("limit/offset=%d/%d", resp.Limit, resp.Offset)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest(http.MethodGet, "/api/messages/logs", nil)
	h.GetLogs(c2)
	if w2.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", w2.Code, w2.Body.String())
	}
}

func TestRequestLogsHandler_ParseHelpers(t *testing.T) {
	if parseLimit("") != 50 {
		t.Fatalf("parseLimit default")
	}
	if parseLimit("0") != 50 {
		t.Fatalf("parseLimit non-positive")
	}
	if parseLimit("999") != 200 {
		t.Fatalf("parseLimit clamp")
	}

	if parseOffset("") != 0 {
		t.Fatalf("parseOffset default")
	}
	if parseOffset("-1") != 0 {
		t.Fatalf("parseOffset negative")
	}

	if apiTypeFromAdminLogsPath("") != "" {
		t.Fatalf("apiTypeFromAdminLogsPath empty")
	}
	if apiTypeFromAdminLogsPath("/api/messages/logs") != "messages" {
		t.Fatalf("apiTypeFromAdminLogsPath messages")
	}
	if apiTypeFromAdminLogsPath("/api/unknown/logs") != "" {
		t.Fatalf("apiTypeFromAdminLogsPath unknown")
	}
	if apiTypeFromAdminLogsPath("/bad/messages/logs") != "" {
		t.Fatalf("apiTypeFromAdminLogsPath invalid prefix")
	}
	if apiTypeFromAdminLogsPath("/api/messages/bad") != "" {
		t.Fatalf("apiTypeFromAdminLogsPath invalid suffix")
	}
}
