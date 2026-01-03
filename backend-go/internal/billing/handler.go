// Package billing 提供 swe-agent 计费服务客户端
package billing

import (
	"log"

	"github.com/BenedictKing/claude-proxy/internal/pricing"
	"github.com/BenedictKing/claude-proxy/internal/usage"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Handler 计费处理器
type Handler struct {
	client         *Client
	pricingService *pricing.Service
	usageStore     *usage.Store
	preAuthCents   int64
}

// NewHandler 创建计费处理器
func NewHandler(client *Client, pricingService *pricing.Service, usageStore *usage.Store, preAuthCents int64) *Handler {
	return &Handler{
		client:         client,
		pricingService: pricingService,
		usageStore:     usageStore,
		preAuthCents:   preAuthCents,
	}
}

// RequestContext 请求计费上下文
type RequestContext struct {
	RequestID    string
	APIKey       string
	PreAuthCents int64
	Charged      bool
	Released     bool // 防止双重释放
}

// BeforeRequest 请求前处理：预授权
// 返回 RequestContext 用于后续扣费/释放
func (h *Handler) BeforeRequest(c *gin.Context) (*RequestContext, error) {
	billingEnabled, _ := c.Get("billing_enabled")
	if billingEnabled != true || h.client == nil {
		return nil, nil // 非计费模式，跳过
	}

	apiKey, _ := c.Get("api_key")
	apiKeyStr, ok := apiKey.(string)
	if !ok || apiKeyStr == "" {
		return nil, nil
	}

	requestID := uuid.New().String()
	ctx := &RequestContext{
		RequestID:    requestID,
		APIKey:       apiKeyStr,
		PreAuthCents: h.preAuthCents,
	}

	if err := h.client.PreAuthorize(apiKeyStr, requestID, h.preAuthCents); err != nil {
		return ctx, err
	}

	return ctx, nil
}

// AfterRequest 请求后处理：扣费
func (h *Handler) AfterRequest(ctx *RequestContext, model string, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens int) {
	if ctx == nil || ctx.Charged {
		return
	}

	// 防御性检查：确保依赖项已初始化
	if h.pricingService == nil || h.usageStore == nil || h.client == nil {
		log.Printf("[Billing-Error] 计费处理器依赖项未初始化")
		return
	}

	// 计算实际成本
	actualCents := h.pricingService.Calculate(model, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens)

	// 扣费
	description := model + " API call"
	if err := h.client.Charge(ctx.APIKey, ctx.RequestID, ctx.PreAuthCents, actualCents, description); err != nil {
		log.Printf("[Billing-Error] 扣费失败: %v", err)
		// 扣费失败时释放预授权
		h.Release(ctx)
		return
	}
	ctx.Charged = true

	// 记录使用量
	h.usageStore.Add(usage.Record{
		ID:           uuid.New().String(),
		RequestID:    ctx.RequestID,
		APIKey:       ctx.APIKey,
		Model:        model,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		CostCents:    actualCents,
	})
}

// Release 释放预授权（请求失败时调用）
func (h *Handler) Release(ctx *RequestContext) {
	if ctx == nil || ctx.Charged || ctx.Released {
		return
	}
	if h.client == nil {
		return
	}
	if err := h.client.Release(ctx.APIKey, ctx.RequestID, ctx.PreAuthCents); err != nil {
		log.Printf("[Billing-Error] 释放预授权失败: %v", err)
	}
	ctx.Released = true // 标记已释放，防止双重释放
}

// IsEnabled 检查计费是否启用
func (h *Handler) IsEnabled() bool {
	return h.client != nil && h.client.IsEnabled()
}

// CalculateCost 计算成本（美分）
func (h *Handler) CalculateCost(model string, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens int) int64 {
	if h.pricingService == nil {
		return 0
	}
	return h.pricingService.Calculate(model, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens)
}
