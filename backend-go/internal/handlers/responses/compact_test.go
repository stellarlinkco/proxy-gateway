package responses

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/gin-gonic/gin"
)

func TestTryCompactWithKey_LongRunningResponseNotTimedOut(t *testing.T) {
	gin.SetMode(gin.TestMode)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	c, rec := newCompactTestContext()
	body := []byte(`{"input":"hello"}`)

	upstream := &config.UpstreamConfig{
		BaseURL: server.URL,
	}
	envCfg := &config.EnvConfig{
		RequestTimeout: 1,
	}

	success, compactErr := tryCompactWithKey(c, upstream, "test-key", body, envCfg, nil)
	if !success {
		t.Fatalf("expected success=true, got false, err=%+v", compactErr)
	}
	if compactErr != nil {
		t.Fatalf("expected compactErr=nil, got %+v", compactErr)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got=%d want=%d", rec.Code, http.StatusOK)
	}
	if strings.TrimSpace(rec.Body.String()) != `{"ok":true}` {
		t.Fatalf("unexpected body: %q", rec.Body.String())
	}
}

func TestReadCompactResponseBody_ReturnsErrorOnReadFailure(t *testing.T) {
	resp := &http.Response{
		Header: make(http.Header),
		Body: io.NopCloser(&readErrorAfterNReader{
			data: []byte(`{"ok":`),
			err:  errors.New("mock read failure"),
		}),
	}

	_, err := readCompactResponseBody(resp)
	if err == nil {
		t.Fatalf("expected read error, got nil")
	}
	if !strings.Contains(err.Error(), "mock read failure") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func newCompactTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(`{}`))
	return c, rec
}

type readErrorAfterNReader struct {
	data []byte
	err  error
	read bool
}

func (r *readErrorAfterNReader) Read(p []byte) (int, error) {
	if !r.read {
		r.read = true
		n := copy(p, r.data)
		return n, nil
	}
	return 0, r.err
}
