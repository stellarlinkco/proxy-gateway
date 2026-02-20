package responses

import (
	"bytes"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/BenedictKing/claude-proxy/internal/types"
	"github.com/gin-gonic/gin"
)

func TestEstimateResponsesOutputFromItems_CoversShapes(t *testing.T) {
	if got := estimateResponsesOutputFromItems(nil); got != 0 {
		t.Fatalf("nil output=%d, want 0", got)
	}

	out := []types.ResponsesItem{
		{Type: "message", Role: "assistant", Content: "hello"},
		{Type: "message", Role: "assistant", Content: []interface{}{
			map[string]interface{}{"text": "world"},
			123,
		}},
		{Type: "message", Role: "assistant", Content: []types.ContentBlock{
			{Type: "output_text", Text: "hi"},
		}},
		{Type: "message", Role: "assistant", Content: map[string]interface{}{"k": "v"}},
		{Type: "tool_call", ToolUse: &types.ToolUse{Name: "tool", Input: map[string]interface{}{"a": "b"}}},
		{Type: "function_call", Content: "args"},
	}

	if got := estimateResponsesOutputFromItems(out); got <= 0 {
		t.Fatalf("output tokens=%d, want >0", got)
	}
}

func TestExtractResponsesTextFromEvent_CollectsKnownDeltas(t *testing.T) {
	var buf bytes.Buffer
	event := strings.Join([]string{
		"ignore this",
		`data: {"type":"response.output_text.delta","delta":"a"}`,
		`data: {"type":"response.function_call_arguments.delta","delta":"b"}`,
		`data: {"type":"response.reasoning_summary_text.delta","text":"c"}`,
		`data: {"type":"response.output_json.delta","delta":"d"}`,
		`data: {"type":"response.content_part.delta","delta":"e"}`,
		`data: {"type":"response.content_part.delta","text":"f"}`,
		`data: {"type":"response.audio.delta","delta":"g"}`,
		`data: {"type":"response.audio_transcript.delta","delta":"h"}`,
		`data: {`, // invalid json, should be ignored
		"",
	}, "\n")

	extractResponsesTextFromEvent(event, &buf)
	if got := buf.String(); got != "abcdefgh" {
		t.Fatalf("buf=%q, want %q", got, "abcdefgh")
	}
}

func TestCheckResponsesEventUsage_DetectsAndDecidesPatch(t *testing.T) {
	t.Run("no usage returns false", func(t *testing.T) {
		hasUsage, needPatch, _ := checkResponsesEventUsage("data: {\"type\":\"response.output_text.delta\",\"delta\":\"x\"}\n", false)
		if hasUsage || needPatch {
			t.Fatalf("hasUsage=%v needPatch=%v, want false/false", hasUsage, needPatch)
		}
	})

	t.Run("usage needs patch when tokens small", func(t *testing.T) {
		event := "data:{\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":0,\"output_tokens\":0,\"total_tokens\":0}}}\n"
		hasUsage, needPatch, u := checkResponsesEventUsage(event, false)
		if !hasUsage || !needPatch {
			t.Fatalf("hasUsage=%v needPatch=%v, want true/true", hasUsage, needPatch)
		}
		if u.InputTokens != 0 || u.OutputTokens != 0 || u.TotalTokens != 0 {
			t.Fatalf("unexpected usage: %+v", u)
		}
	})

	t.Run("claude cache allows skipping input patch", func(t *testing.T) {
		event := "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":0,\"output_tokens\":2,\"total_tokens\":2,\"cache_creation_input_tokens\":1}}}\n"
		hasUsage, needPatch, u := checkResponsesEventUsage(event, false)
		if !hasUsage || needPatch {
			t.Fatalf("hasUsage=%v needPatch=%v, want true/false", hasUsage, needPatch)
		}
		if !u.HasClaudeCache {
			t.Fatalf("expected HasClaudeCache true")
		}
	})

	t.Run("total_tokens missing triggers patch", func(t *testing.T) {
		event := "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":2,\"output_tokens\":2,\"total_tokens\":0}}}\n"
		hasUsage, needPatch, _ := checkResponsesEventUsage(event, false)
		if !hasUsage || !needPatch {
			t.Fatalf("hasUsage=%v needPatch=%v, want true/true", hasUsage, needPatch)
		}
	})
}

func TestInjectResponsesUsageToCompletedEvent_FirstPassAndFallback(t *testing.T) {
	origLogOut := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(origLogOut) })

	envCfg := &config.EnvConfig{
		Env:                "development",
		EnableResponseLogs: true,
		LogLevel:           "debug",
	}

	t.Run("first pass injects when JSON is complete", func(t *testing.T) {
		event := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\"}}\n\n"
		patched, inTok, outTok := injectResponsesUsageToCompletedEvent(event, []byte(`{"input":"hi"}`), "hello", envCfg)
		if inTok <= 0 || outTok <= 0 {
			t.Fatalf("tokens in=%d out=%d, want >0", inTok, outTok)
		}
		if !strings.Contains(patched, "\"usage\"") || !strings.Contains(patched, "\"input_tokens\"") {
			t.Fatalf("missing injected usage: %s", patched)
		}
	})

	t.Run("fallback injects when JSON spans multiple data lines", func(t *testing.T) {
		event := "event: response.completed\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\"\n" +
			"data: }}\n\n"
		patched, _, _ := injectResponsesUsageToCompletedEvent(event, []byte(`{"input":"hi"}`), "hello", envCfg)
		if !strings.Contains(patched, "\"usage\"") {
			t.Fatalf("expected injected usage via fallback: %s", patched)
		}
	})

	t.Run("returns original when no completed event exists", func(t *testing.T) {
		event := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n"
		patched, _, _ := injectResponsesUsageToCompletedEvent(event, []byte(`{"input":"hi"}`), "hello", envCfg)
		if patched != event {
			t.Fatalf("patched differs, want original\npatched=%q\nevent=%q", patched, event)
		}
	})
}

func TestPatchResponsesUsage_CoversBranches(t *testing.T) {
	origLogOut := log.Writer()
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(origLogOut) })

	envCfg := &config.EnvConfig{EnableResponseLogs: true, Env: "development", LogLevel: "debug"}
	longInput := strings.Repeat("a", 200)
	longOutput := strings.Repeat("b", 200)
	reqBody := []byte(`{"model":"gpt-4o","input":"` + longInput + `"}`)

	t.Run("empty usage gets fully estimated", func(t *testing.T) {
		resp := &types.ResponsesResponse{
			Output: []types.ResponsesItem{{Type: "message", Content: longOutput}},
			Usage:  types.ResponsesUsage{},
		}
		patchResponsesUsage(resp, reqBody, envCfg)
		if resp.Usage.TotalTokens <= 0 || resp.Usage.InputTokens <= 0 || resp.Usage.OutputTokens <= 0 {
			t.Fatalf("unexpected usage: %+v", resp.Usage)
		}
	})

	t.Run("fake values get patched (no claude cache)", func(t *testing.T) {
		resp := &types.ResponsesResponse{
			Output: []types.ResponsesItem{{Type: "message", Content: longOutput}},
			Usage:  types.ResponsesUsage{InputTokens: 1, OutputTokens: 1, TotalTokens: 0},
		}
		patchResponsesUsage(resp, reqBody, envCfg)
		if resp.Usage.InputTokens <= 1 || resp.Usage.OutputTokens <= 1 || resp.Usage.TotalTokens <= 0 {
			t.Fatalf("unexpected usage: %+v", resp.Usage)
		}
	})

	t.Run("claude cache skips input patch but may patch output", func(t *testing.T) {
		resp := &types.ResponsesResponse{
			Output: []types.ResponsesItem{{Type: "message", Content: longOutput}},
			Usage: types.ResponsesUsage{
				InputTokens:                1,
				OutputTokens:               1,
				TotalTokens:                0,
				CacheCreationInputTokens:   1,
				CacheReadInputTokens:       1,
				CacheCreation5mInputTokens: 1,
			},
		}
		patchResponsesUsage(resp, reqBody, envCfg)
		if resp.Usage.InputTokens != 1 {
			t.Fatalf("input tokens patched unexpectedly: %+v", resp.Usage)
		}
		if resp.Usage.OutputTokens <= 1 || resp.Usage.TotalTokens <= 0 {
			t.Fatalf("expected output/total patched: %+v", resp.Usage)
		}
	})
}

func TestHandleStreamSuccess_ReadsLargeLine(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	requestBody := []byte(`{"model":"gpt-5","input":"hello","stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(requestBody))

	largePayload := strings.Repeat("x", 2*1024*1024)
	streamBody := "data: " + largePayload + "\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\"}}\n\n"

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(streamBody)),
	}
	envCfg := &config.EnvConfig{Env: "production", LogLevel: "error"}
	originalReq := &types.ResponsesRequest{Model: "gpt-5", Stream: true}

	usage, err := handleStreamSuccess(c, resp, "responses", envCfg, time.Now(), originalReq, requestBody)
	if err != nil {
		t.Fatalf("expected no stream read error, got %v", err)
	}
	if usage == nil {
		t.Fatalf("expected usage result, got nil")
	}

	body := rec.Body.String()
	if !strings.Contains(body, "\"response.completed\"") {
		t.Fatalf("missing response.completed event")
	}
	if rec.Body.Len() <= len(largePayload) {
		t.Fatalf("response body too short: %d", rec.Body.Len())
	}
}

func TestHandleStreamSuccess_PropagatesReadError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	requestBody := []byte(`{"model":"gpt-5","input":"hello","stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(requestBody))

	reader := &eofToErrorReader{
		reader: strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n"),
		err:    errors.New("mock read failure"),
	}

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(reader),
	}
	envCfg := &config.EnvConfig{Env: "production", LogLevel: "error"}
	originalReq := &types.ResponsesRequest{Model: "gpt-5", Stream: true}

	_, err := handleStreamSuccess(c, resp, "responses", envCfg, time.Now(), originalReq, requestBody)
	if err == nil {
		t.Fatalf("expected stream read error, got nil")
	}
	if !strings.Contains(err.Error(), "mock read failure") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleStreamSuccess_IgnoresReadErrorAfterCompleted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	requestBody := []byte(`{"model":"gpt-5","input":"hello","stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(requestBody))

	reader := &eofToErrorReader{
		reader: strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\"}}\n\n"),
		err:    errors.New("unexpected EOF"),
	}

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(reader),
	}
	envCfg := &config.EnvConfig{Env: "production", LogLevel: "error"}
	originalReq := &types.ResponsesRequest{Model: "gpt-5", Stream: true}

	_, err := handleStreamSuccess(c, resp, "responses", envCfg, time.Now(), originalReq, requestBody)
	if err != nil {
		t.Fatalf("expected nil error after completed event, got %v", err)
	}
	if !strings.Contains(rec.Body.String(), "\"response.completed\"") {
		t.Fatalf("missing response.completed event")
	}
}

func TestHandleStreamSuccess_CompletedButNonIgnorableReadError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	requestBody := []byte(`{"model":"gpt-5","input":"hello","stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(requestBody))

	reader := &eofToErrorReader{
		reader: strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\"}}\n\n"),
		err:    errors.New("checksum mismatch"),
	}

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(reader),
	}
	envCfg := &config.EnvConfig{Env: "production", LogLevel: "error"}
	originalReq := &types.ResponsesRequest{Model: "gpt-5", Stream: true}

	_, err := handleStreamSuccess(c, resp, "responses", envCfg, time.Now(), originalReq, requestBody)
	if err == nil {
		t.Fatalf("expected non-ignorable read error, got nil")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleStreamSuccess_EOFLikeErrorBeforeCompletedStillFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	requestBody := []byte(`{"model":"gpt-5","input":"hello","stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(requestBody))

	reader := &eofToErrorReader{
		reader: strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n"),
		err:    errors.New("unexpected EOF"),
	}

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(reader),
	}
	envCfg := &config.EnvConfig{Env: "production", LogLevel: "error"}
	originalReq := &types.ResponsesRequest{Model: "gpt-5", Stream: true}

	_, err := handleStreamSuccess(c, resp, "responses", envCfg, time.Now(), originalReq, requestBody)
	if err == nil {
		t.Fatalf("expected error before completed event, got nil")
	}
}

func TestIsIgnorableStreamReadError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "unexpected EOF", err: errors.New("unexpected EOF"), want: true},
		{name: "connection reset", err: errors.New("read tcp: connection reset by peer"), want: true},
		{name: "broken pipe", err: errors.New("write tcp: broken pipe"), want: true},
		{name: "stream closed", err: errors.New("http2: stream closed"), want: true},
		{name: "non ignorable", err: errors.New("json checksum mismatch"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isIgnorableStreamReadError(tt.err)
			if got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHandleStreamSuccess_ConcurrentIgnoreTailReadError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const workers = 24
	requestBody := []byte(`{"model":"gpt-5","input":"hello","stream":true}`)
	envCfg := &config.EnvConfig{Env: "production", LogLevel: "error"}

	var wg sync.WaitGroup
	errCh := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(requestBody))

			reader := &eofToErrorReader{
				reader: strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\"}}\n\n"),
				err:    errors.New("unexpected EOF"),
			}

			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(reader),
			}
			originalReq := &types.ResponsesRequest{Model: "gpt-5", Stream: true}

			_, err := handleStreamSuccess(c, resp, "responses", envCfg, time.Now(), originalReq, requestBody)
			errCh <- err
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("expected nil error in concurrent completed streams, got %v", err)
		}
	}
}

type eofToErrorReader struct {
	reader io.Reader
	err    error
}

func (r *eofToErrorReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if err == io.EOF {
		return n, r.err
	}
	return n, err
}
