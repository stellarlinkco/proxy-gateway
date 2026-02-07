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

func TestStreamOpenAIToGemini_CoversMoreBranches(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/x:streamGenerateContent", nil)

	sse := strings.Join([]string{
		"event: ignored",
		"data: not-json",
		"data: {\"choices\":[1]}",
		"data: {\"choices\":[{\"finish_reason\":\"stop\"}]}",
		"data: {\"choices\":[{\"delta\":\"not-map\"}]}",
		"data: {\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3}}",
		"data: [DONE]",
		"",
	}, "\n")

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(sse)),
	}
	defer resp.Body.Close()

	flusher := c.Writer.(http.Flusher)
	usage := streamOpenAIToGemini(c, resp, flusher, &config.EnvConfig{Env: "development"}, "gpt-4o")
	if usage == nil || usage.InputTokens != 2 || usage.OutputTokens != 3 {
		t.Fatalf("usage=%+v, want input=2 output=3", usage)
	}
	if !strings.Contains(w.Body.String(), "data:") {
		t.Fatalf("expected output contains data:, got=%s", w.Body.String())
	}
}
