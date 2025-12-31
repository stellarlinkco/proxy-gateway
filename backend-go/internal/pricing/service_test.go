package pricing

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestService_Calculate(t *testing.T) {
	// 创建模拟服务器
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		models := map[string]*ModelPricing{
			"claude-3-5-sonnet-20241022": {
				InputCostPerToken:  0.000003,  // $3/M
				OutputCostPerToken: 0.000015, // $15/M
			},
			"gpt-4": {
				InputCostPerToken:  0.00003,  // $30/M
				OutputCostPerToken: 0.00006, // $60/M
			},
		}
		json.NewEncoder(w).Encode(models)
	}))
	defer server.Close()

	// 直接测试计算逻辑
	svc := &Service{
		models: map[string]*ModelPricing{
			"claude-3-5-sonnet-20241022": {
				InputCostPerToken:  0.000003,
				OutputCostPerToken: 0.000015,
			},
		},
		updateInterval: 24 * time.Hour,
		stopCh:         make(chan struct{}),
	}

	tests := []struct {
		name         string
		model        string
		inputTokens  int
		outputTokens int
		wantCents    int64
	}{
		{
			name:         "claude sonnet",
			model:        "claude-3-5-sonnet-20241022",
			inputTokens:  1000,
			outputTokens: 500,
			wantCents:    1, // (1000 * 0.000003 + 500 * 0.000015) * 100 = 1.05 -> 1
		},
		{
			name:         "unknown model uses default",
			model:        "unknown-model",
			inputTokens:  1000,
			outputTokens: 500,
			wantCents:    1, // 默认价格
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := svc.Calculate(tt.model, tt.inputTokens, tt.outputTokens)
			if got != tt.wantCents {
				t.Errorf("Calculate() = %v, want %v", got, tt.wantCents)
			}
		})
	}
}

func TestService_getOrFuzzyMatch(t *testing.T) {
	svc := &Service{
		models: map[string]*ModelPricing{
			"claude-3-5-sonnet-20241022": {InputCostPerToken: 0.000003},
			"anthropic/claude-3-opus":    {InputCostPerToken: 0.000015},
			"openai/gpt-4":               {InputCostPerToken: 0.00003},
		},
		stopCh: make(chan struct{}),
	}

	tests := []struct {
		name      string
		model     string
		wantFound bool
	}{
		{"exact match", "claude-3-5-sonnet-20241022", true},
		{"with provider prefix", "claude-3-opus", true},
		{"fuzzy match removed", "sonnet", false}, // 模糊匹配已移除，避免非确定性
		{"no match", "nonexistent-model-xyz", false},
		{"empty model", "", false}, // 空 model 应返回 nil
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := svc.getOrFuzzyMatch(tt.model)
			if (got != nil) != tt.wantFound {
				t.Errorf("getOrFuzzyMatch(%s) found = %v, want %v", tt.model, got != nil, tt.wantFound)
			}
		})
	}
}

func TestService_calculateDefault(t *testing.T) {
	svc := &Service{}

	// 默认使用 Claude 3.5 Sonnet 价格: $3/M input, $15/M output
	// 1000 input + 500 output = (1000 * 3 / 1M + 500 * 15 / 1M) * 100 cents
	// = (0.003 + 0.0075) * 100 = 1.05 cents -> 1
	got := svc.calculateDefault(1000, 500)
	if got != 1 {
		t.Errorf("calculateDefault() = %v, want 1", got)
	}
}

func TestService_ModelCount(t *testing.T) {
	svc := &Service{
		models: map[string]*ModelPricing{
			"model1": {},
			"model2": {},
			"model3": {},
		},
		stopCh: make(chan struct{}),
	}

	if got := svc.ModelCount(); got != 3 {
		t.Errorf("ModelCount() = %v, want 3", got)
	}
}
