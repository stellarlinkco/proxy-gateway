package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/gin-gonic/gin"
)

type fakePromotionConfigManager struct {
	lastIndex    int
	lastDuration time.Duration
	err          error
}

func (f *fakePromotionConfigManager) SetChannelPromotion(index int, duration time.Duration) error {
	f.lastIndex = index
	f.lastDuration = duration
	return f.err
}

type fakeResponsesPromotionConfigManager struct {
	lastIndex    int
	lastDuration time.Duration
	err          error
}

func (f *fakeResponsesPromotionConfigManager) SetResponsesChannelPromotion(index int, duration time.Duration) error {
	f.lastIndex = index
	f.lastDuration = duration
	return f.err
}

func TestChannelMetricsHandlers_CoreEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{Name: "m0", ServiceType: "claude", BaseURL: "https://m0.example.com", APIKeys: []string{"mkey0", "mkey1"}, Status: "active"},
			{Name: "m1", ServiceType: "claude", BaseURL: "https://m1.example.com", APIKeys: []string{"mkey2"}, Status: "active"},
		},
		ResponsesUpstream: []config.UpstreamConfig{
			{Name: "r0", ServiceType: "openai", BaseURL: "https://r0.example.com", APIKeys: []string{"rkey0"}, Status: "active"},
		},
		GeminiUpstream: []config.UpstreamConfig{
			{Name: "g0", ServiceType: "gemini", BaseURL: "https://g0.example.com", APIKeys: []string{"gkey0"}, Status: "active"},
		},
		LoadBalance:          "failover",
		ResponsesLoadBalance: "failover",
		GeminiLoadBalance:    "failover",
		FuzzyModeEnabled:     true,
	}

	cm, _ := newTestConfigManager(t, cfg)
	sch, cleanupSch := newTestScheduler(t, cm)
	t.Cleanup(cleanupSch)

	// Seed some metrics to populate lastSuccessAt/lastFailureAt/circuitBrokenAt branches.
	mm := sch.GetMessagesMetricsManager()
	mm.RecordSuccess("https://m0.example.com", "mkey0")
	mm.RecordFailure("https://m0.example.com", "mkey0")
	mm.RecordFailure("https://m0.example.com", "mkey0")
	mm.RecordFailure("https://m0.example.com", "mkey0") // trip circuit (window=3, threshold=0.5)
	mm.RecordSuccess("https://m1.example.com", "mkey2")
	if !mm.ShouldSuspendKey("https://m0.example.com", "mkey0") {
		t.Fatalf("expected mkey0 suspended")
	}

	rm := sch.GetResponsesMetricsManager()
	rm.RecordSuccess("https://r0.example.com", "rkey0")

	gm := sch.GetGeminiMetricsManager()
	gm.RecordSuccess("https://g0.example.com", "gkey0")
	gm.RecordFailure("https://g0.example.com", "gkey0")
	gm.RecordFailure("https://g0.example.com", "gkey0")
	gm.RecordFailure("https://g0.example.com", "gkey0") // trip circuit

	r := gin.New()
	r.GET("/m/metrics", GetChannelMetricsWithConfig(mm, cm, false))
	r.GET("/r/metrics", GetChannelMetricsWithConfig(rm, cm, true))
	r.GET("/r/deprecated", GetResponsesChannelMetrics(rm))
	r.GET("/keys", GetAllKeyMetrics(mm))
	r.GET("/deprecated", GetChannelMetrics(mm))
	r.POST("/resume/:id", ResumeChannel(sch, false))
	r.GET("/stats", GetSchedulerStats(sch))
	r.GET("/dash", GetChannelDashboard(cm, sch))
	r.GET("/m/history", GetChannelMetricsHistory(mm, cm, false))
	r.GET("/r/history", GetChannelMetricsHistory(rm, cm, true))
	r.GET("/m/key/history/:id", GetChannelKeyMetricsHistory(mm, cm, false))
	r.GET("/r/key/history/:id", GetChannelKeyMetricsHistory(rm, cm, true))
	r.GET("/g/history", GetGeminiChannelMetricsHistory(gm, cm))
	r.GET("/g/key/history/:id", GetGeminiChannelKeyMetricsHistory(gm, cm))
	r.GET("/g/metrics", GetGeminiChannelMetrics(gm, cm))

	// channel metrics
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/m/metrics", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("m/metrics status=%d body=%s", w.Code, w.Body.String())
		}
		var resp []map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(resp) != 2 {
			t.Fatalf("len=%d", len(resp))
		}
	}
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/r/metrics", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("r/metrics status=%d body=%s", w.Code, w.Body.String())
		}
	}
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/r/deprecated", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("r/deprecated status=%d body=%s", w.Code, w.Body.String())
		}
	}

	// all key metrics and deprecated handler
	{
		// Create a reset key so RequestCount==0 branch is exercised.
		mm.RecordSuccess("https://m0.example.com", "mkey1")
		mm.ResetKey("https://m0.example.com", "mkey1")

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/keys", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("keys status=%d body=%s", w.Code, w.Body.String())
		}
		w2 := httptest.NewRecorder()
		req2 := httptest.NewRequest(http.MethodGet, "/deprecated", nil)
		r.ServeHTTP(w2, req2)
		if w2.Code != http.StatusOK {
			t.Fatalf("deprecated status=%d body=%s", w2.Code, w2.Body.String())
		}
	}

	// resume channel
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/resume/bad", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("resume invalid status=%d body=%s", w.Code, w.Body.String())
		}
	}
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/resume/0", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("resume status=%d body=%s", w.Code, w.Body.String())
		}
	}

	// scheduler stats
	{
		sch.SetTraceAffinity("user-1", 0)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/stats", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("stats status=%d body=%s", w.Code, w.Body.String())
		}
		w2 := httptest.NewRecorder()
		req2 := httptest.NewRequest(http.MethodGet, "/stats?type=responses", nil)
		r.ServeHTTP(w2, req2)
		if w2.Code != http.StatusOK {
			t.Fatalf("stats responses status=%d body=%s", w2.Code, w2.Body.String())
		}
	}

	// dashboard
	{
		// ResumeChannel 会重置指标；这里重新写入失败数据以覆盖 dashboard 的 lastFailureAt/circuitBrokenAt 分支。
		mm.RecordFailure("https://m0.example.com", "mkey0")
		mm.RecordFailure("https://m0.example.com", "mkey0")
		mm.RecordFailure("https://m0.example.com", "mkey0")

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/dash?type=messages", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("dash status=%d body=%s", w.Code, w.Body.String())
		}

		var payload map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		metricsArr, ok := payload["metrics"].([]any)
		if !ok {
			t.Fatalf("missing metrics: %+v", payload)
		}
		var hasLastFailure, hasCircuitBroken bool
		for _, itemAny := range metricsArr {
			item, ok := itemAny.(map[string]any)
			if !ok {
				continue
			}
			if _, ok := item["lastFailureAt"]; ok {
				hasLastFailure = true
			}
			if _, ok := item["circuitBrokenAt"]; ok {
				hasCircuitBroken = true
			}
		}
		if !hasLastFailure || !hasCircuitBroken {
			t.Fatalf("expected lastFailureAt/circuitBrokenAt in dashboard metrics, got=%+v", metricsArr)
		}

		w2 := httptest.NewRecorder()
		req2 := httptest.NewRequest(http.MethodGet, "/dash?type=responses", nil)
		r.ServeHTTP(w2, req2)
		if w2.Code != http.StatusOK {
			t.Fatalf("dash responses status=%d body=%s", w2.Code, w2.Body.String())
		}
	}

	// metrics history (duration/interval validation and warning)
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/m/history?duration=bad", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("history invalid duration status=%d", w.Code)
		}
		w2 := httptest.NewRecorder()
		req2 := httptest.NewRequest(http.MethodGet, "/m/history?duration=1h&interval=bad", nil)
		r.ServeHTTP(w2, req2)
		if w2.Code != http.StatusBadRequest {
			t.Fatalf("history invalid interval status=%d", w2.Code)
		}
		// interval 小于 1m 会被强制抬升到 1m（避免生成过多 bucket）
		wClamp := httptest.NewRecorder()
		reqClamp := httptest.NewRequest(http.MethodGet, "/m/history?duration=1h&interval=30s", nil)
		r.ServeHTTP(wClamp, reqClamp)
		if wClamp.Code != http.StatusOK {
			t.Fatalf("history clamped interval status=%d body=%s", wClamp.Code, wClamp.Body.String())
		}
		w3 := httptest.NewRecorder()
		req3 := httptest.NewRequest(http.MethodGet, "/m/history?duration=30d", nil)
		r.ServeHTTP(w3, req3)
		if w3.Code != http.StatusOK {
			t.Fatalf("history 30d status=%d body=%s", w3.Code, w3.Body.String())
		}
		var hist []MetricsHistoryResponse
		if err := json.Unmarshal(w3.Body.Bytes(), &hist); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(hist) != 2 {
			t.Fatalf("len=%d", len(hist))
		}
		if hist[0].Warning == "" {
			t.Fatalf("expected warning for 30d without persistence")
		}

		for _, duration := range []string{"1h", "6h", "24h", "7d"} {
			wAuto := httptest.NewRecorder()
			reqAuto := httptest.NewRequest(http.MethodGet, "/m/history?duration="+duration, nil)
			r.ServeHTTP(wAuto, reqAuto)
			if wAuto.Code != http.StatusOK {
				t.Fatalf("m/history duration=%s status=%d body=%s", duration, wAuto.Code, wAuto.Body.String())
			}
		}

		wResp := httptest.NewRecorder()
		reqResp := httptest.NewRequest(http.MethodGet, "/r/history?duration=1h", nil)
		r.ServeHTTP(wResp, reqResp)
		if wResp.Code != http.StatusOK {
			t.Fatalf("r/history status=%d body=%s", wResp.Code, wResp.Body.String())
		}
	}

	// key metrics history
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/m/key/history/bad", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("keyhistory bad id status=%d", w.Code)
		}
		w2 := httptest.NewRecorder()
		req2 := httptest.NewRequest(http.MethodGet, "/m/key/history/0?duration=bad", nil)
		r.ServeHTTP(w2, req2)
		if w2.Code != http.StatusBadRequest {
			t.Fatalf("keyhistory bad duration status=%d", w2.Code)
		}
		w3 := httptest.NewRecorder()
		req3 := httptest.NewRequest(http.MethodGet, "/m/key/history/999", nil)
		r.ServeHTTP(w3, req3)
		if w3.Code != http.StatusBadRequest {
			t.Fatalf("keyhistory out of range status=%d", w3.Code)
		}
		w4 := httptest.NewRecorder()
		req4 := httptest.NewRequest(http.MethodGet, "/m/key/history/0?duration=30d&interval=30s", nil)
		r.ServeHTTP(w4, req4)
		if w4.Code != http.StatusOK {
			t.Fatalf("keyhistory status=%d body=%s", w4.Code, w4.Body.String())
		}
		var keyHist ChannelKeyMetricsHistoryResponse
		if err := json.Unmarshal(w4.Body.Bytes(), &keyHist); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if keyHist.ChannelIndex != 0 || keyHist.ChannelName == "" || len(keyHist.Keys) == 0 {
			t.Fatalf("unexpected keyHist: %+v", keyHist)
		}
		for _, k := range keyHist.Keys {
			if len(k.KeyMask) > 8 {
				t.Fatalf("expected truncated keyMask <= 8, got %q", k.KeyMask)
			}
		}

		for _, duration := range []string{"1h", "6h", "24h", "7d", "30d"} {
			wAuto := httptest.NewRecorder()
			reqAuto := httptest.NewRequest(http.MethodGet, "/m/key/history/0?duration="+duration, nil)
			r.ServeHTTP(wAuto, reqAuto)
			if wAuto.Code != http.StatusOK {
				t.Fatalf("m/key/history duration=%s status=%d body=%s", duration, wAuto.Code, wAuto.Body.String())
			}
		}

		wResp := httptest.NewRecorder()
		reqResp := httptest.NewRequest(http.MethodGet, "/r/key/history/0?duration=1h", nil)
		r.ServeHTTP(wResp, reqResp)
		if wResp.Code != http.StatusOK {
			t.Fatalf("r/key/history status=%d body=%s", wResp.Code, wResp.Body.String())
		}
	}

	// gemini history handlers
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/g/history?duration=bad", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("gemini history bad duration status=%d", w.Code)
		}

		wBadInterval := httptest.NewRecorder()
		reqBadInterval := httptest.NewRequest(http.MethodGet, "/g/history?duration=1h&interval=bad", nil)
		r.ServeHTTP(wBadInterval, reqBadInterval)
		if wBadInterval.Code != http.StatusBadRequest {
			t.Fatalf("gemini history bad interval status=%d", wBadInterval.Code)
		}

		wClamp := httptest.NewRecorder()
		reqClamp := httptest.NewRequest(http.MethodGet, "/g/history?duration=1h&interval=30s", nil)
		r.ServeHTTP(wClamp, reqClamp)
		if wClamp.Code != http.StatusOK {
			t.Fatalf("gemini history clamped interval status=%d body=%s", wClamp.Code, wClamp.Body.String())
		}

		wAuto1h := httptest.NewRecorder()
		reqAuto1h := httptest.NewRequest(http.MethodGet, "/g/history?duration=1h", nil)
		r.ServeHTTP(wAuto1h, reqAuto1h)
		if wAuto1h.Code != http.StatusOK {
			t.Fatalf("gemini history auto 1h status=%d body=%s", wAuto1h.Code, wAuto1h.Body.String())
		}

		wAuto6h := httptest.NewRecorder()
		reqAuto6h := httptest.NewRequest(http.MethodGet, "/g/history?duration=6h", nil)
		r.ServeHTTP(wAuto6h, reqAuto6h)
		if wAuto6h.Code != http.StatusOK {
			t.Fatalf("gemini history auto 6h status=%d body=%s", wAuto6h.Code, wAuto6h.Body.String())
		}

		w2 := httptest.NewRecorder()
		req2 := httptest.NewRequest(http.MethodGet, "/g/history?duration=48h", nil)
		r.ServeHTTP(w2, req2)
		if w2.Code != http.StatusOK {
			t.Fatalf("gemini history status=%d body=%s", w2.Code, w2.Body.String())
		}

		w3 := httptest.NewRecorder()
		req3 := httptest.NewRequest(http.MethodGet, "/g/key/history/0?duration=bad", nil)
		r.ServeHTTP(w3, req3)
		if w3.Code != http.StatusBadRequest {
			t.Fatalf("gemini key history bad duration status=%d", w3.Code)
		}

		w4 := httptest.NewRecorder()
		req4 := httptest.NewRequest(http.MethodGet, "/g/key/history/999", nil)
		r.ServeHTTP(w4, req4)
		if w4.Code != http.StatusBadRequest {
			t.Fatalf("gemini key history out of range status=%d", w4.Code)
		}

		w6 := httptest.NewRecorder()
		req6 := httptest.NewRequest(http.MethodGet, "/g/key/history/0?duration=48h&interval=30s", nil)
		r.ServeHTTP(w6, req6)
		if w6.Code != http.StatusOK {
			t.Fatalf("gemini key history status=%d body=%s", w6.Code, w6.Body.String())
		}

		wBadID := httptest.NewRecorder()
		reqBadID := httptest.NewRequest(http.MethodGet, "/g/key/history/bad?duration=1h", nil)
		r.ServeHTTP(wBadID, reqBadID)
		if wBadID.Code != http.StatusBadRequest {
			t.Fatalf("gemini key history bad id status=%d body=%s", wBadID.Code, wBadID.Body.String())
		}

		for _, duration := range []string{"1h", "6h", "48h"} {
			wAuto := httptest.NewRecorder()
			reqAuto := httptest.NewRequest(http.MethodGet, "/g/key/history/0?duration="+duration, nil)
			r.ServeHTTP(wAuto, reqAuto)
			if wAuto.Code != http.StatusOK {
				t.Fatalf("gemini key history duration=%s status=%d body=%s", duration, wAuto.Code, wAuto.Body.String())
			}
		}

		w5 := httptest.NewRecorder()
		req5 := httptest.NewRequest(http.MethodGet, "/g/metrics", nil)
		r.ServeHTTP(w5, req5)
		if w5.Code != http.StatusOK {
			t.Fatalf("gemini metrics status=%d body=%s", w5.Code, w5.Body.String())
		}
		var metricsPayload []map[string]any
		if err := json.Unmarshal(w5.Body.Bytes(), &metricsPayload); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(metricsPayload) == 0 {
			t.Fatalf("expected metrics payload")
		}
		if _, ok := metricsPayload[0]["lastFailureAt"]; !ok {
			t.Fatalf("expected lastFailureAt in gemini metrics: %+v", metricsPayload[0])
		}
		if _, ok := metricsPayload[0]["circuitBrokenAt"]; !ok {
			t.Fatalf("expected circuitBrokenAt in gemini metrics: %+v", metricsPayload[0])
		}
	}

	if truncateKeyMask("abc", 8) != "abc" {
		t.Fatalf("truncateKeyMask short")
	}
	if truncateKeyMask("abcdefghijk", 8) != "abcdefgh" {
		t.Fatalf("truncateKeyMask long")
	}
}

func TestChannelPromotionHandlers(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mgr := &fakePromotionConfigManager{}
	rmgr := &fakeResponsesPromotionConfigManager{}

	r := gin.New()
	r.POST("/promo/:id", SetChannelPromotion(mgr))
	r.POST("/promo-resp/:id", SetResponsesChannelPromotion(rmgr))

	// invalid id
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/promo/bad", bytes.NewBufferString(`{"duration":10}`))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/promo-resp/bad", bytes.NewBufferString(`{"duration":10}`))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}

	// invalid json
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/promo/0", bytes.NewBufferString("{"))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/promo-resp/0", bytes.NewBufferString("{"))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}

	// set duration
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/promo/1", bytes.NewBufferString(`{"duration":10}`))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		if mgr.lastIndex != 1 || mgr.lastDuration != 10*time.Second {
			t.Fatalf("index/duration=%d/%s", mgr.lastIndex, mgr.lastDuration)
		}
	}

	// clear duration
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/promo/1", bytes.NewBufferString(`{"duration":0}`))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}

	// error from cfg manager
	{
		mgr.err = errTest("boom")
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/promo/1", bytes.NewBufferString(`{"duration":10}`))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		mgr.err = nil
	}

	// responses set duration
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/promo-resp/2", bytes.NewBufferString(`{"duration":5}`))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		if rmgr.lastIndex != 2 || rmgr.lastDuration != 5*time.Second {
			t.Fatalf("index/duration=%d/%s", rmgr.lastIndex, rmgr.lastDuration)
		}
	}
	// responses clear duration
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/promo-resp/2", bytes.NewBufferString(`{"duration":0}`))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
	// responses error from cfg manager
	{
		rmgr.err = errTest("boom")
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/promo-resp/2", bytes.NewBufferString(`{"duration":5}`))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		rmgr.err = nil
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }

func TestChannelKeyMetricsHistory_IntervalValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{Name: "m0", ServiceType: "claude", BaseURL: "https://m0.example.com", APIKeys: []string{"mkey0"}, Status: "active"},
		},
		LoadBalance:          "failover",
		ResponsesLoadBalance: "failover",
		GeminiLoadBalance:    "failover",
	}

	cm, _ := newTestConfigManager(t, cfg)
	sch, cleanupSch := newTestScheduler(t, cm)
	t.Cleanup(cleanupSch)
	mm := sch.GetMessagesMetricsManager()
	mm.RecordSuccess("https://m0.example.com", "mkey0")

	r := gin.New()
	r.GET("/m/key/history/:id", GetChannelKeyMetricsHistory(mm, cm, false))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/m/key/history/0?duration=1h&interval=bad", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestGeminiChannelKeyMetricsHistory_IntervalValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		GeminiUpstream: []config.UpstreamConfig{
			{Name: "g0", ServiceType: "gemini", BaseURL: "https://g0.example.com", APIKeys: []string{"gkey0"}, Status: "active"},
		},
		LoadBalance:          "failover",
		ResponsesLoadBalance: "failover",
		GeminiLoadBalance:    "failover",
	}

	cm, _ := newTestConfigManager(t, cfg)
	sch, cleanupSch := newTestScheduler(t, cm)
	t.Cleanup(cleanupSch)
	gm := sch.GetGeminiMetricsManager()
	gm.RecordSuccess("https://g0.example.com", "gkey0")

	r := gin.New()
	r.GET("/g/key/history/:id", GetGeminiChannelKeyMetricsHistory(gm, cm))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/g/key/history/0?duration=1h&interval=bad", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestChannelKeyMetricsHistory_TodayDurationClamp(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{Name: "m0", ServiceType: "claude", BaseURL: "https://m0.example.com", APIKeys: []string{"mkey0"}, Status: "active"},
		},
		LoadBalance:          "failover",
		ResponsesLoadBalance: "failover",
		GeminiLoadBalance:    "failover",
	}

	cm, _ := newTestConfigManager(t, cfg)
	sch, cleanupSch := newTestScheduler(t, cm)
	t.Cleanup(cleanupSch)
	mm := sch.GetMessagesMetricsManager()
	mm.RecordSuccess("https://m0.example.com", "mkey0")

	r := gin.New()
	r.GET("/m/key/history/:id", GetChannelKeyMetricsHistory(mm, cm, false))

	oldLocal := time.Local
	t.Cleanup(func() { time.Local = oldLocal })
	utc := time.Now().UTC()
	secondsIntoDay := utc.Hour()*3600 + utc.Minute()*60 + utc.Second()
	time.Local = time.FixedZone("test-today", 10-secondsIntoDay)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/m/key/history/0?duration=today", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestGeminiChannelKeyMetricsHistory_TodayDurationClamp(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := config.Config{
		GeminiUpstream: []config.UpstreamConfig{
			{Name: "g0", ServiceType: "gemini", BaseURL: "https://g0.example.com", APIKeys: []string{"gkey0"}, Status: "active"},
		},
		LoadBalance:          "failover",
		ResponsesLoadBalance: "failover",
		GeminiLoadBalance:    "failover",
	}

	cm, _ := newTestConfigManager(t, cfg)
	sch, cleanupSch := newTestScheduler(t, cm)
	t.Cleanup(cleanupSch)
	gm := sch.GetGeminiMetricsManager()
	gm.RecordSuccess("https://g0.example.com", "gkey0")

	r := gin.New()
	r.GET("/g/key/history/:id", GetGeminiChannelKeyMetricsHistory(gm, cm))

	oldLocal := time.Local
	t.Cleanup(func() { time.Local = oldLocal })
	utc := time.Now().UTC()
	secondsIntoDay := utc.Hour()*3600 + utc.Minute()*60 + utc.Second()
	time.Local = time.FixedZone("test-today", 10-secondsIntoDay)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/g/key/history/0?duration=today", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
