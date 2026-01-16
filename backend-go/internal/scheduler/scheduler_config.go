package scheduler

import (
	"math"
	"time"
)

// LoadBalanceStrategy 调度策略（渠道级别）
type LoadBalanceStrategy string

const (
	// LoadBalancePriority 按优先级选择（同优先级时按顺序）
	LoadBalancePriority LoadBalanceStrategy = "priority"
	// LoadBalanceWeightedRandom 同优先级组内按权重随机
	LoadBalanceWeightedRandom LoadBalanceStrategy = "weighted_random"
	// LoadBalanceRoundRobin 同优先级组内轮询
	LoadBalanceRoundRobin LoadBalanceStrategy = "round_robin"
)

// PromotionConfig 促销期策略
type PromotionConfig struct {
	Enabled           bool
	BypassHealthCheck bool
	// MaxFailureRate 促销期仍允许选择的最大失败率（0~1）。
	// 用于避免“促销期完全绕过健康检查”导致长时间黑洞。
	MaxFailureRate float64
}

// AffinityConfig Trace 亲和性策略
type AffinityConfig struct {
	Enabled bool
	// OnlyWithinSamePriority 表示仅在“当前可用的最高优先级组”内使用亲和性。
	// 目的：避免 Trace 亲和性把请求长期锁死在低优先级渠道。
	OnlyWithinSamePriority bool
	TTL                    time.Duration
}

// CircuitBreakerConfig 熔断配置（Key 级别）
type CircuitBreakerConfig struct {
	FailureThreshold    float64
	MinRequestThreshold int
	OpenTimeout         time.Duration
	RecoveryThreshold   float64
}

// FallbackConfig 降级策略
type FallbackConfig struct {
	PriorityFirst bool
	MaxRetries    int
}

// SchedulerConfig 调度器配置
type SchedulerConfig struct {
	LoadBalanceStrategy LoadBalanceStrategy
	Promotion           PromotionConfig
	Affinity            AffinityConfig
	CircuitBreaker      CircuitBreakerConfig
	Fallback            FallbackConfig
}

// DefaultSchedulerConfig 返回默认调度配置
func DefaultSchedulerConfig() SchedulerConfig {
	return SchedulerConfig{
		LoadBalanceStrategy: LoadBalancePriority,
		Promotion: PromotionConfig{
			Enabled:           true,
			BypassHealthCheck: true,
			MaxFailureRate:    0.9,
		},
		Affinity: AffinityConfig{
			Enabled:                true,
			OnlyWithinSamePriority: true,
			TTL:                    30 * time.Minute,
		},
		CircuitBreaker: CircuitBreakerConfig{
			FailureThreshold:    0.5,
			MinRequestThreshold: 0, // 0 表示由指标窗口大小推导
			OpenTimeout:         15 * time.Minute,
			RecoveryThreshold:   0.8,
		},
		Fallback: FallbackConfig{
			PriorityFirst: true,
			MaxRetries:    2,
		},
	}
}

// ValidateSchedulerConfig 验证调度器配置边界，并对非法值回退到默认值。
func ValidateSchedulerConfig(cfg *SchedulerConfig) {
	if cfg == nil {
		return
	}

	defaults := DefaultSchedulerConfig()

	if cfg.Promotion.MaxFailureRate <= 0 || cfg.Promotion.MaxFailureRate > 1 || math.IsNaN(cfg.Promotion.MaxFailureRate) {
		cfg.Promotion.MaxFailureRate = defaults.Promotion.MaxFailureRate
	}
	if cfg.Affinity.TTL <= 0 || cfg.Affinity.TTL > 24*time.Hour {
		cfg.Affinity.TTL = defaults.Affinity.TTL
	}
	if cfg.CircuitBreaker.FailureThreshold <= 0 || cfg.CircuitBreaker.FailureThreshold > 1 || math.IsNaN(cfg.CircuitBreaker.FailureThreshold) {
		cfg.CircuitBreaker.FailureThreshold = defaults.CircuitBreaker.FailureThreshold
	}
	if cfg.CircuitBreaker.RecoveryThreshold <= 0 || cfg.CircuitBreaker.RecoveryThreshold > 1 || math.IsNaN(cfg.CircuitBreaker.RecoveryThreshold) {
		cfg.CircuitBreaker.RecoveryThreshold = defaults.CircuitBreaker.RecoveryThreshold
	}
	if cfg.Fallback.MaxRetries < 0 || cfg.Fallback.MaxRetries > 10 {
		cfg.Fallback.MaxRetries = defaults.Fallback.MaxRetries
	}
}
