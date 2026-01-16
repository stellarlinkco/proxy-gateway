package metrics

import (
	"testing"
	"time"
)

func TestNewCircuitBreaker_NormalizesConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  CircuitBreakerConfig
		want CircuitBreakerConfig
	}{
		{
			name: "invalid_values_use_defaults",
			cfg: CircuitBreakerConfig{
				FailureThreshold:    -1,
				MinRequestThreshold: 0,
				OpenTimeout:         0,
				RecoveryThreshold:   2,
			},
			want: CircuitBreakerConfig{
				FailureThreshold:    0.5,
				MinRequestThreshold: 1,
				OpenTimeout:         15 * time.Minute,
				RecoveryThreshold:   0.8,
			},
		},
		{
			name: "valid_values_kept",
			cfg: CircuitBreakerConfig{
				FailureThreshold:    0.25,
				MinRequestThreshold: 7,
				OpenTimeout:         3 * time.Second,
				RecoveryThreshold:   0.9,
			},
			want: CircuitBreakerConfig{
				FailureThreshold:    0.25,
				MinRequestThreshold: 7,
				OpenTimeout:         3 * time.Second,
				RecoveryThreshold:   0.9,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cb := NewCircuitBreaker(tt.cfg)
			if cb.cfg != tt.want {
				t.Fatalf("cfg=%+v, want=%+v", cb.cfg, tt.want)
			}
			if cb.State() != CircuitClosed {
				t.Fatalf("state=%v, want=%v", cb.State(), CircuitClosed)
			}
			if cb.OpenedAt() != nil {
				t.Fatalf("openedAt=%v, want nil", cb.OpenedAt())
			}
		})
	}
}

func TestCircuitBreaker_ShouldAllow_OpenToHalfOpenAfterTimeout(t *testing.T) {
	cfg := CircuitBreakerConfig{
		FailureThreshold:    0.5,
		MinRequestThreshold: 1,
		OpenTimeout:         10 * time.Second,
		RecoveryThreshold:   0.8,
	}

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	tTimeout := t0.Add(cfg.OpenTimeout)

	t.Run("open_without_openedAt_initializes_timestamp", func(t *testing.T) {
		cb := NewCircuitBreaker(cfg)
		cb.state = CircuitOpen
		cb.openedAt = nil

		if cb.ShouldAllow(t0) {
			t.Fatalf("ShouldAllow()=true, want false")
		}
		if cb.State() != CircuitOpen {
			t.Fatalf("state=%v, want=%v", cb.State(), CircuitOpen)
		}
		if cb.OpenedAt() == nil || !cb.OpenedAt().Equal(t0) {
			t.Fatalf("openedAt=%v, want %v", cb.OpenedAt(), t0)
		}
	})

	t.Run("open_before_timeout_denies", func(t *testing.T) {
		cb := NewCircuitBreaker(cfg)
		cb.RecordFailure(t0, 1, 1)

		if cb.State() != CircuitOpen {
			t.Fatalf("state=%v, want=%v", cb.State(), CircuitOpen)
		}
		if cb.ShouldAllow(t0.Add(cfg.OpenTimeout - time.Nanosecond)) {
			t.Fatalf("ShouldAllow(before timeout)=true, want false")
		}
		if cb.State() != CircuitOpen {
			t.Fatalf("state(after)=%v, want=%v", cb.State(), CircuitOpen)
		}
	})

	t.Run("open_at_timeout_transitions_to_halfopen", func(t *testing.T) {
		cb := NewCircuitBreaker(cfg)
		cb.RecordFailure(t0, 1, 1)

		if !cb.ShouldAllow(tTimeout) {
			t.Fatalf("ShouldAllow(at timeout)=false, want true")
		}
		if cb.State() != CircuitHalfOpen {
			t.Fatalf("state=%v, want=%v", cb.State(), CircuitHalfOpen)
		}
	})
}

func TestCircuitBreaker_RecordFailure_StateTransitions(t *testing.T) {
	cfg := CircuitBreakerConfig{
		FailureThreshold:    0.5,
		MinRequestThreshold: 2,
		OpenTimeout:         10 * time.Second,
		RecoveryThreshold:   0.8,
	}

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(1 * time.Second)
	t2 := t0.Add(11 * time.Second)

	tests := []struct {
		name         string
		setup        func(*CircuitBreaker)
		now          time.Time
		failureRate  float64
		sampleCount  int
		wantState    CircuitState
		wantOpenedAt *time.Time
	}{
		{
			name: "closed_below_min_requests_does_not_open",
			setup: func(cb *CircuitBreaker) {
			},
			now:          t0,
			failureRate:  1,
			sampleCount:  1,
			wantState:    CircuitClosed,
			wantOpenedAt: nil,
		},
		{
			name: "closed_reaches_threshold_opens",
			setup: func(cb *CircuitBreaker) {
			},
			now:          t0,
			failureRate:  0.5,
			sampleCount:  2,
			wantState:    CircuitOpen,
			wantOpenedAt: &t0,
		},
		{
			name: "open_refreshes_openedAt",
			setup: func(cb *CircuitBreaker) {
				cb.RecordFailure(t0, 0.5, 2)
			},
			now:          t1,
			failureRate:  0,
			sampleCount:  0,
			wantState:    CircuitOpen,
			wantOpenedAt: &t1,
		},
		{
			name: "halfopen_failure_reopens",
			setup: func(cb *CircuitBreaker) {
				cb.RecordFailure(t0, 0.5, 2)
				cb.ShouldAllow(t0.Add(cfg.OpenTimeout))
			},
			now:          t2,
			failureRate:  0,
			sampleCount:  0,
			wantState:    CircuitOpen,
			wantOpenedAt: &t2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cb := NewCircuitBreaker(cfg)
			tt.setup(cb)

			cb.RecordFailure(tt.now, tt.failureRate, tt.sampleCount)
			if cb.State() != tt.wantState {
				t.Fatalf("state=%v, want=%v", cb.State(), tt.wantState)
			}

			gotOpenedAt := cb.OpenedAt()
			if tt.wantOpenedAt == nil {
				if gotOpenedAt != nil {
					t.Fatalf("openedAt=%v, want nil", gotOpenedAt)
				}
				return
			}
			if gotOpenedAt == nil || !gotOpenedAt.Equal(*tt.wantOpenedAt) {
				t.Fatalf("openedAt=%v, want %v", gotOpenedAt, *tt.wantOpenedAt)
			}
		})
	}
}

func TestCircuitBreaker_StateMachine_FullCycle(t *testing.T) {
	cfg := CircuitBreakerConfig{
		FailureThreshold:    0.5,
		MinRequestThreshold: 2,
		OpenTimeout:         10 * time.Second,
		RecoveryThreshold:   0.8,
	}

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	cb := NewCircuitBreaker(cfg)

	cb.RecordFailure(t0, 0.5, 2)
	if cb.State() != CircuitOpen {
		t.Fatalf("after failure: state=%v, want=%v", cb.State(), CircuitOpen)
	}

	if cb.ShouldAllow(t0.Add(cfg.OpenTimeout - time.Nanosecond)) {
		t.Fatalf("ShouldAllow(before timeout)=true, want false")
	}

	if !cb.ShouldAllow(t0.Add(cfg.OpenTimeout)) {
		t.Fatalf("ShouldAllow(at timeout)=false, want true")
	}
	if cb.State() != CircuitHalfOpen {
		t.Fatalf("after timeout: state=%v, want=%v", cb.State(), CircuitHalfOpen)
	}

	cb.RecordSuccess(t0.Add(cfg.OpenTimeout))
	if cb.State() != CircuitHalfOpen {
		t.Fatalf("after 1st probe success: state=%v, want=%v", cb.State(), CircuitHalfOpen)
	}

	cb.RecordSuccess(t0.Add(cfg.OpenTimeout))
	if cb.State() != CircuitClosed {
		t.Fatalf("after 2nd probe success: state=%v, want=%v", cb.State(), CircuitClosed)
	}
	if cb.OpenedAt() != nil {
		t.Fatalf("after close: openedAt=%v, want nil", cb.OpenedAt())
	}
}

func TestCircuitBreaker_RecordSuccess_WhileOpenForcesHalfOpen(t *testing.T) {
	cfg := CircuitBreakerConfig{
		FailureThreshold:    0.5,
		MinRequestThreshold: 2,
		OpenTimeout:         10 * time.Second,
		RecoveryThreshold:   0.8,
	}

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	cb := NewCircuitBreaker(cfg)
	cb.RecordFailure(t0, 1, 2)

	cb.RecordSuccess(t0.Add(1 * time.Second))
	if cb.State() != CircuitHalfOpen {
		t.Fatalf("state=%v, want=%v", cb.State(), CircuitHalfOpen)
	}

	// 还差一次探测成功才会关闭
	cb.RecordSuccess(t0.Add(2 * time.Second))
	if cb.State() != CircuitClosed {
		t.Fatalf("state=%v, want=%v", cb.State(), CircuitClosed)
	}
}
