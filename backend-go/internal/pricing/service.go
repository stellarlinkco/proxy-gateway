// Package pricing 提供 LiteLLM 价格表服务
package pricing

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"
)

const LiteLLMPricingURL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

// ModelPricing LiteLLM 模型价格信息
type ModelPricing struct {
	InputCostPerToken  float64 `json:"input_cost_per_token"`
	OutputCostPerToken float64 `json:"output_cost_per_token"`
	MaxTokens          int     `json:"max_tokens"`
	MaxInputTokens     int     `json:"max_input_tokens"`
	MaxOutputTokens    int     `json:"max_output_tokens"`
	LiteLLMProvider    string  `json:"litellm_provider"`
	Mode               string  `json:"mode"`
}

// Service 价格表服务
type Service struct {
	models         map[string]*ModelPricing
	mu             sync.RWMutex
	lastUpdated    time.Time
	updateInterval time.Duration
	stopCh         chan struct{}
}

// NewService 创建价格表服务
func NewService(updateInterval time.Duration) *Service {
	if updateInterval == 0 {
		updateInterval = 24 * time.Hour
	}
	svc := &Service{
		models:         make(map[string]*ModelPricing),
		updateInterval: updateInterval,
		stopCh:         make(chan struct{}),
	}
	// 启动时加载
	if err := svc.loadPricing(); err != nil {
		log.Printf("[Pricing] 警告: 初始加载失败: %v", err)
	}
	// 后台更新
	go svc.autoUpdate()
	return svc
}

// loadPricing 从 LiteLLM 加载价格表
func (s *Service) loadPricing() error {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(LiteLLMPricingURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var models map[string]*ModelPricing
	if err := json.NewDecoder(resp.Body).Decode(&models); err != nil {
		return err
	}

	s.mu.Lock()
	s.models = models
	s.lastUpdated = time.Now()
	s.mu.Unlock()

	log.Printf("[Pricing] 加载 %d 个模型价格", len(models))
	return nil
}

// autoUpdate 后台定时更新
func (s *Service) autoUpdate() {
	ticker := time.NewTicker(s.updateInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := s.loadPricing(); err != nil {
				log.Printf("[Pricing] 警告: 定时更新失败: %v", err)
			}
		case <-s.stopCh:
			return
		}
	}
}

// Stop 停止后台更新
func (s *Service) Stop() {
	close(s.stopCh)
}

// Calculate 计算成本 (返回 cents)
func (s *Service) Calculate(model string, inputTokens, outputTokens int) int64 {
	pricing := s.getOrFuzzyMatch(model)
	if pricing == nil {
		return s.calculateDefault(inputTokens, outputTokens)
	}

	// LiteLLM: USD per token → cents
	inputCostUSD := float64(inputTokens) * pricing.InputCostPerToken
	outputCostUSD := float64(outputTokens) * pricing.OutputCostPerToken
	return int64((inputCostUSD + outputCostUSD) * 100)
}

// getOrFuzzyMatch 精确匹配或模糊匹配模型
func (s *Service) getOrFuzzyMatch(model string) *ModelPricing {
	// 拒绝空 model，避免匹配到任意 key
	if model == "" {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// 精确匹配
	if p, ok := s.models[model]; ok {
		return p
	}

	// 尝试带 provider 前缀的匹配
	prefixes := []string{"anthropic/", "openai/", "google/", "gemini/"}
	for _, prefix := range prefixes {
		if p, ok := s.models[prefix+model]; ok {
			return p
		}
	}

	// 模糊匹配已移除：map 迭代顺序不确定，可能导致同一 model 匹配到不同价格
	// 如需模糊匹配，应使用排序后的 key 列表并选择最长匹配

	return nil
}

// calculateDefault 默认价格计算 (Claude 3.5 Sonnet 价格作为默认)
func (s *Service) calculateDefault(inputTokens, outputTokens int) int64 {
	// 默认使用 Claude 3.5 Sonnet 价格: $3/M input, $15/M output
	inputCostUSD := float64(inputTokens) * 3.0 / 1_000_000
	outputCostUSD := float64(outputTokens) * 15.0 / 1_000_000
	return int64((inputCostUSD + outputCostUSD) * 100)
}

// GetPricing 获取指定模型的价格信息
func (s *Service) GetPricing(model string) *ModelPricing {
	return s.getOrFuzzyMatch(model)
}

// LastUpdated 返回最后更新时间
func (s *Service) LastUpdated() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastUpdated
}

// ModelCount 返回已加载的模型数量
func (s *Service) ModelCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.models)
}
