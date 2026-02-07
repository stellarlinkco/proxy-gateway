package gemini

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/gin-gonic/gin"
)

func TestHandleStreamSuccess_DefaultUpstreamTypeFallsBackToGemini(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/x:streamGenerateContent", nil)

	envCfg := &config.EnvConfig{EnableResponseLogs: true}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			"data: {\"usageMetadata\":{\"promptTokenCount\":2,\"candidatesTokenCount\":3,\"totalTokenCount\":5}}",
			"",
		}, "\n"))),
	}

	usage := handleStreamSuccess(c, resp, "unknown", envCfg, time.Now(), "gemini-pro")
	if usage == nil || usage.InputTokens == 0 || usage.OutputTokens == 0 {
		t.Fatalf("unexpected usage=%+v", usage)
	}
	if !strings.Contains(rec.Body.String(), "data:") {
		t.Fatalf("expected stream output, got=%s", rec.Body.String())
	}
}
