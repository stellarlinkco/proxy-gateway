package common

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/BenedictKing/claude-proxy/internal/metrics"
	"github.com/gin-gonic/gin"
)

func TestReadRequestBody_SuccessRestoresBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/x", bytes.NewBufferString("hello"))

	body, err := ReadRequestBody(c, 10)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if string(body) != "hello" {
		t.Fatalf("body = %q", string(body))
	}

	again, _ := io.ReadAll(c.Request.Body)
	if string(again) != "hello" {
		t.Fatalf("restored body = %q", string(again))
	}
}

func TestReadRequestBody_TooLargeReturns413(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/x", bytes.NewBufferString("123456"))

	_, err := ReadRequestBody(c, 5)
	if err == nil {
		t.Fatalf("expected error")
	}
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestRestoreRequestBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/x", bytes.NewBufferString("x"))

	RestoreRequestBody(c, []byte("abc"))
	got, _ := io.ReadAll(c.Request.Body)
	if string(got) != "abc" {
		t.Fatalf("got %q", string(got))
	}
}

func TestSendRequest_StandardAndStream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	upstream := &config.UpstreamConfig{InsecureSkipVerify: true}

	t.Run("standard client", func(t *testing.T) {
		envCfg := &config.EnvConfig{
			Env:               "development",
			RequestTimeout:    1000,
			EnableRequestLogs: true,
			RawLogOutput:      false,
		}

		req, err := http.NewRequest(http.MethodPost, srv.URL, bytes.NewBufferString(`{"a":1}`))
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := SendRequest(req, upstream, envCfg, false)
		if err != nil {
			t.Fatalf("SendRequest: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
	})

	t.Run("stream client", func(t *testing.T) {
		envCfg := &config.EnvConfig{
			Env:               "development",
			RequestTimeout:    1000,
			EnableRequestLogs: true,
			RawLogOutput:      true,
		}

		req, err := http.NewRequest(http.MethodPost, srv.URL, bytes.NewBufferString(`{"a":1}`))
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := SendRequest(req, upstream, envCfg, true)
		if err != nil {
			t.Fatalf("SendRequest: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
	})
}

func TestLogOriginalRequest_CoversBranches(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(`{"a":1}`))
	c.Request.Header.Set("Authorization", "Bearer secret")

	LogOriginalRequest(c, []byte(`{"a":1}`), &config.EnvConfig{EnableRequestLogs: false}, "Messages")
	LogOriginalRequest(c, []byte(`{"a":1}`), &config.EnvConfig{EnableRequestLogs: true, Env: "development"}, "Messages")
	LogOriginalRequest(c, []byte(`{"a":1}`), &config.EnvConfig{EnableRequestLogs: true, Env: "development", RawLogOutput: true}, "Messages")
}

func TestAreAllKeysSuspended(t *testing.T) {
	m := metrics.NewMetricsManagerWithConfig(3, 0.5)
	defer m.Stop()

	baseURL := "https://example.com"
	keys := []string{"k1", "k2"}

	for i := 0; i < 3; i++ {
		m.RecordFailure(baseURL, "k1")
		m.RecordFailure(baseURL, "k2")
	}

	if !AreAllKeysSuspended(m, baseURL, keys) {
		t.Fatalf("expected all keys suspended")
	}
	if AreAllKeysSuspended(m, baseURL, nil) {
		t.Fatalf("expected false for empty keys")
	}

	m2 := metrics.NewMetricsManagerWithConfig(3, 0.5)
	defer m2.Stop()
	for i := 0; i < 3; i++ {
		m2.RecordFailure(baseURL, "k1")
	}
	if AreAllKeysSuspended(m2, baseURL, keys) {
		t.Fatalf("expected false when one key is not suspended")
	}
}

func TestExtractUserIDAndConversationID(t *testing.T) {
	body := []byte(`{"metadata":{"user_id":"u1"},"prompt_cache_key":"pc"}`)
	if got := ExtractUserID(body); got != "u1" {
		t.Fatalf("user_id = %q", got)
	}

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("Conversation_id", "c1")
	if got := ExtractConversationID(c, body); got != "c1" {
		t.Fatalf("conversation_id = %q", got)
	}

	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	c2.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c2.Request.Header.Set("Session_id", "s1")
	if got := ExtractConversationID(c2, body); got != "s1" {
		t.Fatalf("session_id = %q", got)
	}

	c3, _ := gin.CreateTestContext(httptest.NewRecorder())
	c3.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	if got := ExtractConversationID(c3, body); got != "pc" {
		t.Fatalf("prompt_cache_key = %q", got)
	}

	body2 := []byte(`{"metadata":{"user_id":"u2"}}`)
	c4, _ := gin.CreateTestContext(httptest.NewRecorder())
	c4.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	if got := ExtractConversationID(c4, body2); got != "u2" {
		t.Fatalf("metadata.user_id = %q", got)
	}
}
