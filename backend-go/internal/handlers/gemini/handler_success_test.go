package gemini

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/BenedictKing/claude-proxy/internal/types"
	"github.com/gin-gonic/gin"
)

type failingReadCloser struct{}

func (failingReadCloser) Read([]byte) (int, error) { return 0, errors.New("read failed") }
func (failingReadCloser) Close() error             { return nil }

func TestBuildProviderRequest_ServiceTypesAndAuthHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "http://client.example/v1/models/gemini-pro:generateContent", bytes.NewBufferString(`{}`))
	c.Request.Header.Set("User-Agent", "unit-test")
	c.Request.Header.Set("Authorization", "Bearer client-token")

	geminiReq := &types.GeminiRequest{}

	cases := []struct {
		name      string
		upType    string
		apiKey    string
		isStream  bool
		wantURL   string
		wantKeyH  string
		wantKeyV  string
		wantExtra map[string]string
	}{
		{
			name:     "gemini non-stream",
			upType:   "gemini",
			apiKey:   "gk",
			isStream: false,
			wantURL:  "https://up.example/v1beta/models/gemini-pro:generateContent",
			wantKeyH: "x-goog-api-key",
			wantKeyV: "gk",
		},
		{
			name:     "gemini stream",
			upType:   "gemini",
			apiKey:   "gk",
			isStream: true,
			wantURL:  "https://up.example/v1beta/models/gemini-pro:streamGenerateContent?alt=sse",
			wantKeyH: "x-goog-api-key",
			wantKeyV: "gk",
		},
		{
			name:     "claude",
			upType:   "claude",
			apiKey:   "sk-ant-test",
			isStream: false,
			wantURL:  "https://up.example/v1/messages",
			wantKeyH: "x-api-key",
			wantKeyV: "sk-ant-test",
			wantExtra: map[string]string{
				"anthropic-version": "2023-06-01",
			},
		},
		{
			name:     "openai",
			upType:   "openai",
			apiKey:   "ok",
			isStream: false,
			wantURL:  "https://up.example/v1/chat/completions",
			wantKeyH: "Authorization",
			wantKeyV: "Bearer ok",
		},
		{
			name:     "default treated as gemini",
			upType:   "unknown",
			apiKey:   "gk",
			isStream: false,
			wantURL:  "https://up.example/v1beta/models/gemini-pro:generateContent",
			wantKeyH: "x-goog-api-key",
			wantKeyV: "gk",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			up := &config.UpstreamConfig{ServiceType: tc.upType}

			req, err := buildProviderRequest(c, up, "https://up.example/", tc.apiKey, geminiReq, "gemini-pro", tc.isStream)
			if err != nil {
				t.Fatalf("buildProviderRequest: %v", err)
			}

			if req.Method != http.MethodPost {
				t.Fatalf("method=%q", req.Method)
			}
			if req.URL.String() != tc.wantURL {
				t.Fatalf("url=%q want %q", req.URL.String(), tc.wantURL)
			}
			if ct := req.Header.Get("Content-Type"); ct != "application/json" {
				t.Fatalf("content-type=%q", ct)
			}
			if got := req.Header.Get(tc.wantKeyH); got != tc.wantKeyV {
				t.Fatalf("%s=%q want %q", tc.wantKeyH, got, tc.wantKeyV)
			}
			for k, v := range tc.wantExtra {
				if got := req.Header.Get(k); got != v {
					t.Fatalf("%s=%q want %q", k, got, v)
				}
			}
		})
	}
}

func TestHandleSuccess_NonStream_ConvertsAndExtractsUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	envCfg := &config.EnvConfig{}
	start := time.Now()

	cases := []struct {
		name            string
		upstreamType    string
		body            string
		wantStatus      int
		wantUsage       *types.Usage
		wantBodyContain string
	}{
		{
			name:         "gemini",
			upstreamType: "gemini",
			body: `{
  "candidates":[{"content":{"parts":[{"text":"hi"}]}}],
  "usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2,"totalTokenCount":7,"cachedContentTokenCount":1}
}`,
			wantStatus: http.StatusOK,
			wantUsage: &types.Usage{
				InputTokens:  4,
				OutputTokens: 2,
			},
			wantBodyContain: "\"candidates\"",
		},
		{
			name:         "claude",
			upstreamType: "claude",
			body: `{
  "content":[{"type":"text","text":"hi"}],
  "usage":{"input_tokens":3,"output_tokens":2,"cache_read_input_tokens":1}
}`,
			wantStatus: http.StatusOK,
			wantUsage: &types.Usage{
				InputTokens:  3,
				OutputTokens: 2,
			},
			wantBodyContain: "\"candidates\"",
		},
		{
			name:         "openai",
			upstreamType: "openai",
			body: `{
  "choices":[{"message":{"content":"hi"},"finish_reason":"stop"}],
  "usage":{"prompt_tokens":3,"completion_tokens":2}
}`,
			wantStatus: http.StatusOK,
			wantUsage: &types.Usage{
				InputTokens:  3,
				OutputTokens: 2,
			},
			wantBodyContain: "\"candidates\"",
		},
		{
			name:            "default passthrough",
			upstreamType:    "whatever",
			body:            `{"raw":true}`,
			wantStatus:      http.StatusOK,
			wantUsage:       nil,
			wantBodyContain: `{"raw":true}`,
		},
		{
			name:            "invalid json passthrough",
			upstreamType:    "gemini",
			body:            "not-json",
			wantStatus:      http.StatusOK,
			wantUsage:       nil,
			wantBodyContain: "not-json",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/models/gemini-pro:generateContent", nil)

			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(tc.body)),
			}

			usage := handleSuccess(c, resp, tc.upstreamType, envCfg, start, &types.GeminiRequest{}, "gemini-pro", false)
			if w.Code != tc.wantStatus {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if tc.wantUsage == nil {
				if usage != nil {
					t.Fatalf("usage=%+v, want nil", usage)
				}
			} else {
				if usage == nil {
					t.Fatalf("usage=nil, want %+v", tc.wantUsage)
				}
				if usage.InputTokens != tc.wantUsage.InputTokens || usage.OutputTokens != tc.wantUsage.OutputTokens {
					t.Fatalf("usage=%+v want %+v", usage, tc.wantUsage)
				}
			}
			if !strings.Contains(w.Body.String(), tc.wantBodyContain) {
				t.Fatalf("unexpected body=%s", w.Body.String())
			}
		})
	}
}

func TestHandleSuccess_ReadBodyError_Returns500(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/models/gemini-pro:generateContent", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       failingReadCloser{},
	}

	usage := handleSuccess(c, resp, "gemini", &config.EnvConfig{}, time.Now(), &types.GeminiRequest{}, "gemini-pro", false)
	if usage != nil {
		t.Fatalf("usage=%+v, want nil", usage)
	}
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Failed to read response") {
		t.Fatalf("unexpected body=%s", w.Body.String())
	}
}
