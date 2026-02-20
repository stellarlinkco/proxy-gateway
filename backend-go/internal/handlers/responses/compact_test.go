package responses

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

	_, decodedGzip, err := readCompactResponseBody(resp)
	if err == nil {
		t.Fatalf("expected read error, got nil")
	}
	if decodedGzip {
		t.Fatalf("decodedGzip=%v, want false", decodedGzip)
	}
	if !strings.Contains(err.Error(), "mock read failure") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReadCompactResponseBody_ReturnsErrorOnInvalidGzipBody(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{"Content-Encoding": []string{"gzip"}},
		Body:   io.NopCloser(strings.NewReader("not-a-valid-gzip-payload")),
	}

	_, decodedGzip, err := readCompactResponseBody(resp)
	if err == nil {
		t.Fatalf("expected gzip decode error, got nil")
	}
	if decodedGzip {
		t.Fatalf("decodedGzip=%v, want false", decodedGzip)
	}
}

func TestTryCompactWithKey_ReturnsFailoverOnUnexpectedEOF(t *testing.T) {
	gin.SetMode(gin.TestMode)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijacker unsupported", http.StatusInternalServerError)
			return
		}
		conn, rw, err := hijacker.Hijack()
		if err != nil {
			return
		}
		_, _ = rw.WriteString("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 64\r\n\r\n{\"ok\":")
		_ = rw.Flush()
		_ = conn.Close()
	}))
	defer server.Close()

	c, _ := newCompactTestContext()
	upstream := &config.UpstreamConfig{BaseURL: server.URL}
	envCfg := &config.EnvConfig{RequestTimeout: 1000}

	success, compactErr := tryCompactWithKey(c, upstream, "test-key", []byte(`{"input":"hi"}`), envCfg, nil)
	if success {
		t.Fatalf("expected success=false on truncated upstream response")
	}
	if compactErr == nil {
		t.Fatalf("expected compactErr, got nil")
	}
	if compactErr.status != http.StatusBadGateway {
		t.Fatalf("status=%d, want=%d", compactErr.status, http.StatusBadGateway)
	}
	if !compactErr.shouldFailover {
		t.Fatalf("expected shouldFailover=true")
	}
	if !strings.Contains(string(compactErr.body), "读取上游响应失败") {
		t.Fatalf("unexpected error body: %s", string(compactErr.body))
	}
}

func TestTryCompactWithKey_ReturnsFailoverOnInvalidGzipBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write([]byte("not-a-valid-gzip-payload"))
	}))
	defer server.Close()

	c, _ := newCompactTestContext()
	upstream := &config.UpstreamConfig{BaseURL: server.URL}
	envCfg := &config.EnvConfig{RequestTimeout: 1000}

	success, compactErr := tryCompactWithKey(c, upstream, "test-key", []byte(`{"input":"hi"}`), envCfg, nil)
	if success {
		t.Fatalf("expected success=false on invalid gzip response")
	}
	if compactErr == nil {
		t.Fatalf("expected compactErr, got nil")
	}
	if compactErr.status != http.StatusBadGateway {
		t.Fatalf("status=%d, want=%d", compactErr.status, http.StatusBadGateway)
	}
	if !compactErr.shouldFailover {
		t.Fatalf("expected shouldFailover=true")
	}
	if !strings.Contains(string(compactErr.body), "读取上游响应失败") {
		t.Fatalf("unexpected error body: %s", string(compactErr.body))
	}
}

func TestTryCompactWithKey_DecodesValidGzipBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var gzipBuf bytes.Buffer
	gzipWriter := gzip.NewWriter(&gzipBuf)
	_, _ = gzipWriter.Write([]byte(`{"ok":true}`))
	_ = gzipWriter.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(gzipBuf.Bytes())
	}))
	defer server.Close()

	c, rec := newCompactTestContext()
	upstream := &config.UpstreamConfig{BaseURL: server.URL}
	envCfg := &config.EnvConfig{RequestTimeout: 1000}

	success, compactErr := tryCompactWithKey(c, upstream, "test-key", []byte(`{"input":"hi"}`), envCfg, nil)
	if !success || compactErr != nil {
		t.Fatalf("expected success on valid gzip response, got success=%v err=%+v", success, compactErr)
	}
	if strings.TrimSpace(rec.Body.String()) != `{"ok":true}` {
		t.Fatalf("unexpected decoded body: %q", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding=%q, want empty", got)
	}
}

func TestTryCompactWithKey_ForwardsNonGzipContentEncoding(t *testing.T) {
	gin.SetMode(gin.TestMode)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "br")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	c, rec := newCompactTestContext()
	upstream := &config.UpstreamConfig{BaseURL: server.URL}
	envCfg := &config.EnvConfig{RequestTimeout: 1000}

	success, compactErr := tryCompactWithKey(c, upstream, "test-key", []byte(`{"input":"hi"}`), envCfg, nil)
	if !success || compactErr != nil {
		t.Fatalf("expected success with non-gzip content-encoding, got success=%v err=%+v", success, compactErr)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "br" {
		t.Fatalf("Content-Encoding=%q, want %q", got, "br")
	}
}

func TestTryCompactWithKey_ConcurrentLongRunningRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	upstream := &config.UpstreamConfig{BaseURL: server.URL}
	envCfg := &config.EnvConfig{RequestTimeout: 1}
	const workers = 20

	var wg sync.WaitGroup
	errCh := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, rec := newCompactTestContext()
			success, compactErr := tryCompactWithKey(c, upstream, "test-key", []byte(`{"input":"hello"}`), envCfg, nil)
			if !success || compactErr != nil {
				errCh <- errors.New("compact request failed unexpectedly")
				return
			}
			if rec.Code != http.StatusOK {
				errCh <- errors.New("unexpected status code")
				return
			}
			if strings.TrimSpace(rec.Body.String()) != `{"ok":true}` {
				errCh <- errors.New("unexpected response body")
				return
			}
			errCh <- nil
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent compact failed: %v", err)
		}
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
