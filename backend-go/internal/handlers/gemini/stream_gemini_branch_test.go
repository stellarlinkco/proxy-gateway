package gemini

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/gin-gonic/gin"
)

func TestStreamGeminiToGemini_ForwardsNonDataLinesAndInvalidJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	resp := &http.Response{
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			"event: ping",
			"data: {bad}",
			"",
		}, "\n"))),
	}

	usage := streamGeminiToGemini(c, resp, nil, &config.EnvConfig{})
	if usage != nil {
		t.Fatalf("usage=%+v, want nil", usage)
	}
	if !strings.Contains(rec.Body.String(), "event: ping") {
		t.Fatalf("expected forwarded event line, got=%s", rec.Body.String())
	}
}
