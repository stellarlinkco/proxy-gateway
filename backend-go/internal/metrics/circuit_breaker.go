package metrics

import "time"

// CircuitState 熔断器状态
type CircuitState int

const (
	CircuitClosed CircuitState = iota
	CircuitOpen
	CircuitHalfOpen
)

// CircuitBreakerConfig 熔断器配置
type CircuitBreakerConfig struct {
	// FailureThreshold 失败率阈值（0~1），达到后从 Closed -> Open
	FailureThreshold float64
	// MinRequestThreshold 最小样本数保护：样本不足时不触发熔断
	MinRequestThreshold int
	// OpenTimeout Open 状态持续时间，超过后进入 HalfOpen
	OpenTimeout time.Duration
	// RecoveryThreshold HalfOpen 状态下的成功率阈值（0~1），达到后从 HalfOpen -> Closed
	RecoveryThreshold float64
}

// CircuitBreaker 三态熔断器（Closed/Open/HalfOpen）
//
// 约束：
// - 不做内部加锁：由调用方（MetricsManager）保证并发安全。
// - Closed->Open 的触发条件由调用方提供 failureRate/sampleCount。
type CircuitBreaker struct {
	cfg CircuitBreakerConfig

	state CircuitState

	openedAt *time.Time

	halfOpenRequests  int
	halfOpenSuccesses int
}

func NewCircuitBreaker(cfg CircuitBreakerConfig) *CircuitBreaker {
	if cfg.FailureThreshold <= 0 || cfg.FailureThreshold > 1 {
		cfg.FailureThreshold = 0.5
	}
	if cfg.MinRequestThreshold < 1 {
		cfg.MinRequestThreshold = 1
	}
	if cfg.OpenTimeout <= 0 {
		cfg.OpenTimeout = 15 * time.Minute
	}
	if cfg.RecoveryThreshold <= 0 || cfg.RecoveryThreshold > 1 {
		cfg.RecoveryThreshold = 0.8
	}

	return &CircuitBreaker{
		cfg:   cfg,
		state: CircuitClosed,
	}
}

func (c *CircuitBreaker) State() CircuitState {
	return c.state
}

func (c *CircuitBreaker) OpenedAt() *time.Time {
	return c.openedAt
}

// ShouldAllow 判断是否允许请求通过。必要时执行状态推进（Open->HalfOpen）。
func (c *CircuitBreaker) ShouldAllow(now time.Time) bool {
	switch c.state {
	case CircuitClosed:
		return true
	case CircuitHalfOpen:
		return true
	case CircuitOpen:
		if c.openedAt == nil {
			t := now
			c.openedAt = &t
		}
		if now.Sub(*c.openedAt) >= c.cfg.OpenTimeout {
			c.toHalfOpen()
			return true
		}
		return false
	default:
		return true
	}
}

// RecordSuccess 记录一次成功，用于 HalfOpen 的恢复判断。
func (c *CircuitBreaker) RecordSuccess(now time.Time) {
	switch c.state {
	case CircuitClosed:
		return
	case CircuitOpen:
		// 强制探测场景：即使仍处于 Open，也允许用成功结果推动恢复。
		c.toHalfOpen()
	case CircuitHalfOpen:
	}

	c.halfOpenRequests++
	c.halfOpenSuccesses++
	c.maybeCloseOrReopen(now)
}

// RecordFailure 记录一次失败。Closed 状态下需要 caller 提供 failureRate/sampleCount。
func (c *CircuitBreaker) RecordFailure(now time.Time, failureRate float64, sampleCount int) {
	switch c.state {
	case CircuitClosed:
		if sampleCount >= c.cfg.MinRequestThreshold && failureRate >= c.cfg.FailureThreshold {
			c.toOpen(now)
		}
		return
	case CircuitOpen:
		// 保持 Open，并刷新 openedAt，避免频繁探测导致抖动。
		c.toOpen(now)
		return
	case CircuitHalfOpen:
		// HalfOpen 一旦失败，直接重新打开。
		c.toOpen(now)
		return
	default:
		return
	}
}

func (c *CircuitBreaker) toOpen(now time.Time) {
	t := now
	c.openedAt = &t
	c.state = CircuitOpen
	c.halfOpenRequests = 0
	c.halfOpenSuccesses = 0
}

func (c *CircuitBreaker) toHalfOpen() {
	c.state = CircuitHalfOpen
	c.halfOpenRequests = 0
	c.halfOpenSuccesses = 0
}

func (c *CircuitBreaker) toClosed() {
	c.state = CircuitClosed
	c.openedAt = nil
	c.halfOpenRequests = 0
	c.halfOpenSuccesses = 0
}

func (c *CircuitBreaker) maybeCloseOrReopen(now time.Time) {
	if c.halfOpenRequests < c.cfg.MinRequestThreshold {
		return
	}
	successRate := float64(c.halfOpenSuccesses) / float64(c.halfOpenRequests)
	if successRate >= c.cfg.RecoveryThreshold {
		c.toClosed()
		return
	}
	c.toOpen(now)
}
