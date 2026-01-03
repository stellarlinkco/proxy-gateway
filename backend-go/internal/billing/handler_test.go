package billing

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BenedictKing/claude-proxy/internal/pricing"
	"github.com/BenedictKing/claude-proxy/internal/usage"
	"github.com/gin-gonic/gin"
)

func TestHandler_BeforeRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		billingEnabled bool
		apiKey         string
		serverStatus   int
		wantErr        bool
		wantNil        bool
	}{
		{
			name:           "billing disabled",
			billingEnabled: false,
			apiKey:         "test-key",
			wantNil:        true,
		},
		{
			name:           "no api key",
			billingEnabled: true,
			apiKey:         "",
			wantNil:        true,
		},
		{
			name:           "preauth success",
			billingEnabled: true,
			apiKey:         "test-key",
			serverStatus:   200,
			wantErr:        false,
		},
		{
			name:           "preauth insufficient balance",
			billingEnabled: true,
			apiKey:         "test-key",
			serverStatus:   402,
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.serverStatus)
			}))
			defer server.Close()

			client := NewClient(server.URL)
			pricingSvc := &pricing.Service{}
			usageStore := usage.NewStore(100)
			handler := NewHandler(client, pricingSvc, usageStore, 500)

			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set("billing_enabled", tt.billingEnabled)
			c.Set("api_key", tt.apiKey)

			ctx, err := handler.BeforeRequest(c)

			if tt.wantNil {
				if ctx != nil {
					t.Errorf("BeforeRequest() ctx = %v, want nil", ctx)
				}
				return
			}

			if (err != nil) != tt.wantErr {
				t.Errorf("BeforeRequest() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestHandler_AfterRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/billing/charge" {
			w.WriteHeader(200)
		}
	}))
	defer server.Close()

	// 创建带模型价格的 pricing service
	pricingSvc := &pricing.Service{}
	usageStore := usage.NewStore(100)
	client := NewClient(server.URL)
	handler := NewHandler(client, pricingSvc, usageStore, 500)

	ctx := &RequestContext{
		RequestID:    "req-123",
		APIKey:       "test-key",
		PreAuthCents: 500,
		Charged:      false,
	}

	handler.AfterRequest(ctx, "claude-3-5-sonnet-20241022", 1000, 500, 0, 0)

	if !ctx.Charged {
		t.Error("AfterRequest() should set Charged = true")
	}

	// 验证 usage 记录
	records := usageStore.GetRecent(10)
	if len(records) != 1 {
		t.Errorf("AfterRequest() should add 1 usage record, got %d", len(records))
	}
}

func TestHandler_AfterRequest_AlreadyCharged(t *testing.T) {
	handler := NewHandler(nil, nil, nil, 500)

	ctx := &RequestContext{
		Charged: true,
	}

	// 不应 panic
	handler.AfterRequest(ctx, "model", 100, 50, 0, 0)
}

func TestHandler_AfterRequest_NilContext(t *testing.T) {
	handler := NewHandler(nil, nil, nil, 500)

	// 不应 panic
	handler.AfterRequest(nil, "model", 100, 50, 0, 0)
}

func TestHandler_AfterRequest_NilDependencies(t *testing.T) {
	// 测试依赖项为 nil 时不 panic
	handler := NewHandler(nil, nil, nil, 500)

	ctx := &RequestContext{
		RequestID:    "req-123",
		APIKey:       "test-key",
		PreAuthCents: 500,
		Charged:      false,
	}

	// 不应 panic，应该安全返回
	handler.AfterRequest(ctx, "model", 100, 50, 0, 0)

	// Charged 应该保持 false（因为依赖项为 nil，无法扣费）
	if ctx.Charged {
		t.Error("Charged should remain false when dependencies are nil")
	}
}

func TestHandler_Release(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/billing/release" {
			var payload map[string]interface{}
			json.NewDecoder(r.Body).Decode(&payload)
			if payload["request_id"] != "req-123" {
				t.Errorf("Release() request_id = %v, want req-123", payload["request_id"])
			}
			w.WriteHeader(200)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL)
	handler := NewHandler(client, nil, nil, 500)

	ctx := &RequestContext{
		RequestID:    "req-123",
		APIKey:       "test-key",
		PreAuthCents: 500,
		Charged:      false,
	}

	handler.Release(ctx)
}

func TestHandler_Release_AlreadyCharged(t *testing.T) {
	// 创建一个会失败的 server（不应被调用）
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Release() should not call server when already charged")
	}))
	defer server.Close()

	client := NewClient(server.URL)
	handler := NewHandler(client, nil, nil, 500)

	ctx := &RequestContext{
		Charged: true,
	}

	handler.Release(ctx)
}

func TestHandler_Release_DoubleRelease(t *testing.T) {
	releaseCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/billing/release" {
			releaseCount++
			w.WriteHeader(200)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL)
	handler := NewHandler(client, nil, nil, 500)

	ctx := &RequestContext{
		RequestID:    "req-123",
		APIKey:       "test-key",
		PreAuthCents: 500,
		Charged:      false,
		Released:     false,
	}

	// 第一次释放
	handler.Release(ctx)
	if releaseCount != 1 {
		t.Errorf("First release should call server once, got %d", releaseCount)
	}
	if !ctx.Released {
		t.Error("Released should be true after first release")
	}

	// 第二次释放（不应调用服务器）
	handler.Release(ctx)
	if releaseCount != 1 {
		t.Errorf("Second release should not call server, got %d calls", releaseCount)
	}
}

func TestHandler_Release_NilClient(t *testing.T) {
	handler := NewHandler(nil, nil, nil, 500)

	ctx := &RequestContext{
		RequestID:    "req-123",
		APIKey:       "test-key",
		PreAuthCents: 500,
	}

	// 不应 panic
	handler.Release(ctx)
}

func TestHandler_IsEnabled(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    bool
	}{
		{"enabled", "http://example.com", true},
		{"disabled", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient(tt.baseURL)
			handler := NewHandler(client, nil, nil, 500)

			if got := handler.IsEnabled(); got != tt.want {
				t.Errorf("IsEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHandler_AfterRequest_ChargeFailed(t *testing.T) {
	chargeCallCount := 0
	releaseCallCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/billing/charge" {
			chargeCallCount++
			w.WriteHeader(500) // 扣费失败
		}
		if r.URL.Path == "/api/billing/release" {
			releaseCallCount++
			w.WriteHeader(200)
		}
	}))
	defer server.Close()

	pricingSvc := &pricing.Service{}
	usageStore := usage.NewStore(100)
	client := NewClient(server.URL)
	handler := NewHandler(client, pricingSvc, usageStore, 500)

	ctx := &RequestContext{
		RequestID:    "req-123",
		APIKey:       "test-key",
		PreAuthCents: 500,
		Charged:      false,
	}

	handler.AfterRequest(ctx, "claude-3-5-sonnet-20241022", 1000, 500, 0, 0)

	// 扣费失败时应该释放预授权
	if chargeCallCount != 1 {
		t.Errorf("Charge should be called once, got %d", chargeCallCount)
	}
	if releaseCallCount != 1 {
		t.Errorf("Release should be called once when charge fails, got %d", releaseCallCount)
	}
	if ctx.Charged {
		t.Error("Charged should remain false when charge fails")
	}

	// 不应记录 usage（Store.Add 是同步的，无需 sleep）
	records := usageStore.GetRecent(10)
	if len(records) != 0 {
		t.Errorf("Should not add usage record when charge fails, got %d", len(records))
	}
}
