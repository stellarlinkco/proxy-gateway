package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BenedictKing/claude-proxy/internal/monitor"
	"github.com/gin-gonic/gin"
)

func TestLiveRequestsHandler_GetLiveRequests_FiltersByAPIType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	m := monitor.NewLiveRequestManager(50)
	h := NewLiveRequestsHandler(m)

	api := r.Group("/api")
	messagesAPI := api.Group("/messages")
	responsesAPI := api.Group("/responses")
	geminiAPI := api.Group("/gemini")

	messagesAPI.GET("/live", h.GetLiveRequests)
	responsesAPI.GET("/live", h.GetLiveRequests)
	geminiAPI.GET("/live", h.GetLiveRequests)

	base := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	m.StartRequest(&monitor.LiveRequest{RequestID: "m1", APIType: "messages", StartTime: base.Add(1 * time.Second)})
	m.StartRequest(&monitor.LiveRequest{RequestID: "m2", APIType: "messages", StartTime: base.Add(3 * time.Second), IsStreaming: true})
	m.StartRequest(&monitor.LiveRequest{RequestID: "r1", APIType: "responses", StartTime: base.Add(2 * time.Second)})

	t.Run("messages returns only messages sorted desc", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/messages/live", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}

		var resp monitor.LiveRequestsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("json.Unmarshal err = %v", err)
		}
		if resp.Count != 2 || len(resp.Requests) != 2 {
			t.Fatalf("count/len = %d/%d, want 2/2", resp.Count, len(resp.Requests))
		}
		if resp.Requests[0].RequestID != "m2" || resp.Requests[1].RequestID != "m1" {
			t.Fatalf("order = [%s, %s], want [m2, m1]", resp.Requests[0].RequestID, resp.Requests[1].RequestID)
		}
		if !resp.Requests[0].IsStreaming {
			t.Fatalf("IsStreaming = false, want true")
		}
	})

	t.Run("responses returns only responses", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/responses/live", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}

		var resp monitor.LiveRequestsResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("json.Unmarshal err = %v", err)
		}
		if resp.Count != 1 || len(resp.Requests) != 1 || resp.Requests[0].RequestID != "r1" {
			t.Fatalf("resp = %+v, want 1 item r1", resp)
		}
	})
}

func TestLiveRequestsHandler_GetLiveRequests_ManagerNilReturns503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	h := NewLiveRequestsHandler(nil)
	r.GET("/api/messages/live", h.GetLiveRequests)

	req := httptest.NewRequest(http.MethodGet, "/api/messages/live", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestLiveRequestsHandler_GetLiveRequests_FallbackToRequestPathWhenFullPathEmpty(t *testing.T) {
	gin.SetMode(gin.TestMode)

	m := monitor.NewLiveRequestManager(50)
	h := NewLiveRequestsHandler(m)

	base := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	m.StartRequest(&monitor.LiveRequest{RequestID: "m1", APIType: "messages", StartTime: base.Add(1 * time.Second)})
	m.StartRequest(&monitor.LiveRequest{RequestID: "r1", APIType: "responses", StartTime: base.Add(2 * time.Second)})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/messages/live", nil)
	h.GetLiveRequests(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var resp monitor.LiveRequestsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal err = %v", err)
	}
	if resp.Count != 1 || len(resp.Requests) != 1 || resp.Requests[0].RequestID != "m1" {
		t.Fatalf("resp=%+v, want 1 item m1", resp)
	}
}

func TestLiveRequestsHandler_GetLiveRequests_EmptyAPITypeReturnsAll(t *testing.T) {
	gin.SetMode(gin.TestMode)

	m := monitor.NewLiveRequestManager(50)
	h := NewLiveRequestsHandler(m)

	base := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	m.StartRequest(&monitor.LiveRequest{RequestID: "m1", APIType: "messages", StartTime: base.Add(1 * time.Second)})
	m.StartRequest(&monitor.LiveRequest{RequestID: "r1", APIType: "responses", StartTime: base.Add(2 * time.Second)})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/live", nil)
	h.GetLiveRequests(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var resp monitor.LiveRequestsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal err = %v", err)
	}
	if resp.Count != 2 || len(resp.Requests) != 2 {
		t.Fatalf("count/len=%d/%d, want 2/2", resp.Count, len(resp.Requests))
	}
}

func TestApiTypeFromAdminLivePath_CoversInvalidCases(t *testing.T) {
	cases := map[string]string{
		"":                 "",
		"/api/messages":    "",
		"/api/live":        "",
		"/bad/x/live":      "",
		"/api/x/bad":       "",
		"/api/x/live":      "",
		"/api/gemini/live": "gemini",
	}

	for path, want := range cases {
		if got := apiTypeFromAdminLivePath(path); got != want {
			t.Fatalf("apiTypeFromAdminLivePath(%q)=%q, want %q", path, got, want)
		}
	}
}
