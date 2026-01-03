package messages

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BenedictKing/claude-proxy/internal/cache"
	"github.com/gin-gonic/gin"
)

func TestModelsCacheKey_NilSafe(t *testing.T) {
	if got := modelsCacheKey(nil); got != "" {
		t.Fatalf("modelsCacheKey(nil)=%q, want empty", got)
	}
	if got := modelsCacheKey(&http.Request{}); got != "" {
		t.Fatalf("modelsCacheKey(req with nil URL)=%q, want empty", got)
	}
}

func TestWriteCachedHTTPResponse_DefaultContentType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	writeCachedHTTPResponse(c, cache.HTTPResponse{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       []byte(`{"ok":true}`),
	})

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); ct != gin.MIMEJSON {
		t.Fatalf("Content-Type=%q, want %q", ct, gin.MIMEJSON)
	}
	if got := w.Body.String(); got != `{"ok":true}` {
		t.Fatalf("body=%q, want %q", got, `{"ok":true}`)
	}
}

func TestWriteCachedHTTPResponse_NilContextDoesNotPanic(t *testing.T) {
	writeCachedHTTPResponse(nil, cache.HTTPResponse{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       []byte(`{"ok":true}`),
	})
}
