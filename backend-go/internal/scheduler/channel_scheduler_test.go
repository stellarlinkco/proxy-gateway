package scheduler

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/BenedictKing/claude-proxy/internal/metrics"
	"github.com/BenedictKing/claude-proxy/internal/session"
	"github.com/BenedictKing/claude-proxy/internal/warmup"
)

// createTestConfigManager 创建测试用配置管理器
func createTestConfigManager(t *testing.T, cfg config.Config) (*config.ConfigManager, func()) {
	t.Helper()

	// 创建临时目录
	tmpDir, err := os.MkdirTemp("", "scheduler-test-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}

	// 创建临时配置文件
	configFile := filepath.Join(tmpDir, "config.json")
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("序列化配置失败: %v", err)
	}

	if err := os.WriteFile(configFile, data, 0644); err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("写入配置文件失败: %v", err)
	}

	// 创建配置管理器
	cfgManager, err := config.NewConfigManager(configFile)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("创建配置管理器失败: %v", err)
	}

	cleanup := func() {
		cfgManager.Close()
		os.RemoveAll(tmpDir)
	}

	return cfgManager, cleanup
}

// createTestScheduler 创建测试用调度器
func createTestScheduler(t *testing.T, cfg config.Config) (*ChannelScheduler, func()) {
	t.Helper()

	cfgManager, cleanup := createTestConfigManager(t, cfg)
	messagesMetrics := metrics.NewMetricsManager()
	responsesMetrics := metrics.NewMetricsManager()
	geminiMetrics := metrics.NewMetricsManager()
	traceAffinity := session.NewTraceAffinityManager()
	urlManager := warmup.NewURLManager(30*time.Second, 3)

	scheduler := NewChannelScheduler(cfgManager, messagesMetrics, responsesMetrics, geminiMetrics, traceAffinity, urlManager)

	return scheduler, func() {
		messagesMetrics.Stop()
		responsesMetrics.Stop()
		geminiMetrics.Stop()
		cleanup()
	}
}

// TestPromotedChannelBypassesHealthCheckWithMaxFailureRate 测试促销渠道在 maxFailureRate 范围内绕过健康检查
func TestPromotedChannelBypassesHealthCheckWithMaxFailureRate(t *testing.T) {
	// 设置促销截止时间为 5 分钟后
	promotionUntil := time.Now().Add(5 * time.Minute)

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "normal-channel",
				BaseURL:  "https://normal.example.com",
				APIKeys:  []string{"sk-normal-key"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:           "promoted-channel",
				BaseURL:        "https://promoted.example.com",
				APIKeys:        []string{"sk-promoted-key"},
				Status:         "active",
				Priority:       2,
				PromotionUntil: &promotionUntil,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	// 模拟促销渠道之前有高失败率（使其不健康）
	metricsManager := scheduler.messagesMetricsManager
	for i := 0; i < 6; i++ {
		metricsManager.RecordFailure("https://promoted.example.com", "sk-promoted-key")
	}
	for i := 0; i < 4; i++ {
		metricsManager.RecordSuccess("https://promoted.example.com", "sk-promoted-key")
	}

	// 验证促销渠道确实不健康
	isHealthy := metricsManager.IsChannelHealthyWithKeys("https://promoted.example.com", []string{"sk-promoted-key"})
	if isHealthy {
		t.Fatal("促销渠道应该被标记为不健康")
	}

	// 选择渠道 - 促销渠道应该被选中，即使它不健康
	result, err := scheduler.SelectChannel(context.Background(), "test-user", make(map[int]bool), false)
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}

	if result.ChannelIndex != 1 {
		t.Errorf("期望选择促销渠道 (index=1)，实际选择了 index=%d", result.ChannelIndex)
	}

	if result.Reason != "promotion_priority" {
		t.Errorf("期望选择原因为 promotion_priority，实际为 %s", result.Reason)
	}

	if result.Upstream.Name != "promoted-channel" {
		t.Errorf("期望选择 promoted-channel，实际选择了 %s", result.Upstream.Name)
	}
}

// TestPromotedChannelSkippedWhenTooUnhealthy 测试促销渠道失败率超过 maxFailureRate 时被跳过
func TestPromotedChannelSkippedWhenTooUnhealthy(t *testing.T) {
	promotionUntil := time.Now().Add(5 * time.Minute)

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "normal-channel",
				BaseURL:  "https://normal.example.com",
				APIKeys:  []string{"sk-normal-key"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:           "promoted-channel",
				BaseURL:        "https://promoted.example.com",
				APIKeys:        []string{"sk-promoted-key"},
				Status:         "active",
				Priority:       2,
				PromotionUntil: &promotionUntil,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	metricsManager := scheduler.messagesMetricsManager
	for i := 0; i < 10; i++ {
		metricsManager.RecordFailure("https://promoted.example.com", "sk-promoted-key")
	}

	result, err := scheduler.SelectChannel(context.Background(), "test-user", make(map[int]bool), false)
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}

	if result.ChannelIndex != 0 {
		t.Errorf("期望跳过过度不健康的促销渠道并选择 normal-channel (index=0)，实际选择了 index=%d", result.ChannelIndex)
	}
	if result.Upstream.Name != "normal-channel" {
		t.Errorf("期望选择 normal-channel，实际选择了 %s", result.Upstream.Name)
	}
}

// TestPromotedChannelSkippedAfterFailure 测试促销渠道在本次请求失败后被跳过
func TestPromotedChannelSkippedAfterFailure(t *testing.T) {
	promotionUntil := time.Now().Add(5 * time.Minute)

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "normal-channel",
				BaseURL:  "https://normal.example.com",
				APIKeys:  []string{"sk-normal-key"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:           "promoted-channel",
				BaseURL:        "https://promoted.example.com",
				APIKeys:        []string{"sk-promoted-key"},
				Status:         "active",
				Priority:       2,
				PromotionUntil: &promotionUntil,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	// 模拟促销渠道在本次请求中已经失败
	failedChannels := map[int]bool{
		1: true, // 促销渠道已失败
	}

	// 选择渠道 - 应该跳过促销渠道，选择正常渠道
	result, err := scheduler.SelectChannel(context.Background(), "test-user", failedChannels, false)
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}

	if result.ChannelIndex != 0 {
		t.Errorf("期望选择正常渠道 (index=0)，实际选择了 index=%d", result.ChannelIndex)
	}

	if result.Upstream.Name != "normal-channel" {
		t.Errorf("期望选择 normal-channel，实际选择了 %s", result.Upstream.Name)
	}
}

// TestNonPromotedChannelStillChecksHealth 测试非促销渠道仍然检查健康状态
func TestNonPromotedChannelStillChecksHealth(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "unhealthy-channel",
				BaseURL:  "https://unhealthy.example.com",
				APIKeys:  []string{"sk-unhealthy-key"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:     "healthy-channel",
				BaseURL:  "https://healthy.example.com",
				APIKeys:  []string{"sk-healthy-key"},
				Status:   "active",
				Priority: 2,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	// 模拟第一个渠道不健康
	metricsManager := scheduler.messagesMetricsManager
	for i := 0; i < 10; i++ {
		metricsManager.RecordFailure("https://unhealthy.example.com", "sk-unhealthy-key")
	}

	// 选择渠道 - 应该跳过不健康的渠道，选择健康的渠道
	result, err := scheduler.SelectChannel(context.Background(), "test-user", make(map[int]bool), false)
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}

	if result.ChannelIndex != 1 {
		t.Errorf("期望选择健康渠道 (index=1)，实际选择了 index=%d", result.ChannelIndex)
	}

	if result.Upstream.Name != "healthy-channel" {
		t.Errorf("期望选择 healthy-channel，实际选择了 %s", result.Upstream.Name)
	}
}

// TestExpiredPromotionNotBypassHealthCheck 测试过期的促销不绕过健康检查
func TestExpiredPromotionNotBypassHealthCheck(t *testing.T) {
	// 设置促销截止时间为过去
	promotionUntil := time.Now().Add(-5 * time.Minute)

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "healthy-channel",
				BaseURL:  "https://healthy.example.com",
				APIKeys:  []string{"sk-healthy-key"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:           "expired-promoted-channel",
				BaseURL:        "https://expired.example.com",
				APIKeys:        []string{"sk-expired-key"},
				Status:         "active",
				Priority:       2,
				PromotionUntil: &promotionUntil, // 已过期
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	// 模拟过期促销渠道不健康
	metricsManager := scheduler.messagesMetricsManager
	for i := 0; i < 10; i++ {
		metricsManager.RecordFailure("https://expired.example.com", "sk-expired-key")
	}

	// 选择渠道 - 过期促销渠道不应该被优先选择，应该选择健康的渠道
	result, err := scheduler.SelectChannel(context.Background(), "test-user", make(map[int]bool), false)
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}

	if result.ChannelIndex != 0 {
		t.Errorf("期望选择健康渠道 (index=0)，实际选择了 index=%d", result.ChannelIndex)
	}

	if result.Upstream.Name != "healthy-channel" {
		t.Errorf("期望选择 healthy-channel，实际选择了 %s", result.Upstream.Name)
	}
}

func TestTraceAffinityOnlyWithinSamePriority_SkipsLowerPriority(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "p1",
				BaseURL:  "https://p1.example.com",
				APIKeys:  []string{"k1"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:     "p2",
				BaseURL:  "https://p2.example.com",
				APIKeys:  []string{"k2"},
				Status:   "active",
				Priority: 2,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	scheduler.SetTraceAffinity("u", 1)

	result, err := scheduler.SelectChannel(context.Background(), "u", make(map[int]bool), false)
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}
	if result.ChannelIndex != 0 {
		t.Errorf("期望跳过低优先级亲和并选择 index=0，实际选择了 index=%d", result.ChannelIndex)
	}
	if result.Reason != "priority_order" {
		t.Errorf("期望选择原因为 priority_order，实际为 %s", result.Reason)
	}
}

func TestTraceAffinityOnlyWithinSamePriority_AllowsSamePriority(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "p1-a",
				BaseURL:  "https://p1a.example.com",
				APIKeys:  []string{"k1"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:     "p1-b",
				BaseURL:  "https://p1b.example.com",
				APIKeys:  []string{"k2"},
				Status:   "active",
				Priority: 1,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	scheduler.SetTraceAffinity("u", 1)

	result, err := scheduler.SelectChannel(context.Background(), "u", make(map[int]bool), false)
	if err != nil {
		t.Fatalf("选择渠道失败: %v", err)
	}
	if result.ChannelIndex != 1 {
		t.Errorf("期望使用同优先级亲和选择 index=1，实际选择了 index=%d", result.ChannelIndex)
	}
	if result.Reason != "trace_affinity" {
		t.Errorf("期望选择原因为 trace_affinity，实际为 %s", result.Reason)
	}
}

func TestChannelScheduler_SelectChannel_WeightedRandomStrategy(t *testing.T) {
	tests := []struct {
		name        string
		isResponses bool
		cfg         config.Config
	}{
		{
			name:        "messages",
			isResponses: false,
			cfg: config.Config{
				Upstream: []config.UpstreamConfig{
					{
						Name:     "c1",
						BaseURL:  "https://c1.example.com",
						APIKeys:  []string{"k1"},
						Status:   "active",
						Priority: 1,
						Weight:   0, // 覆盖 weight<=0 分支
					},
					{
						Name:     "c2",
						BaseURL:  "https://c2.example.com",
						APIKeys:  []string{"k2"},
						Status:   "active",
						Priority: 1,
						Weight:   3,
					},
				},
			},
		},
		{
			name:        "responses",
			isResponses: true,
			cfg: config.Config{
				ResponsesUpstream: []config.UpstreamConfig{
					{
						Name:     "c1",
						BaseURL:  "https://c1.example.com",
						APIKeys:  []string{"k1"},
						Status:   "active",
						Priority: 1,
						Weight:   0, // 覆盖 weight<=0 分支
					},
					{
						Name:     "c2",
						BaseURL:  "https://c2.example.com",
						APIKeys:  []string{"k2"},
						Status:   "active",
						Priority: 1,
						Weight:   3,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheduler, cleanup := createTestScheduler(t, tt.cfg)
			defer cleanup()

			scheduler.schedulerConfig.Promotion.Enabled = false
			scheduler.schedulerConfig.Affinity.Enabled = false
			scheduler.schedulerConfig.LoadBalanceStrategy = LoadBalanceWeightedRandom

			result, err := scheduler.SelectChannel(context.Background(), "", make(map[int]bool), tt.isResponses)
			if err != nil {
				t.Fatalf("选择渠道失败: %v", err)
			}
			if result.Reason != "weighted_random" {
				t.Fatalf("reason=%s, want weighted_random", result.Reason)
			}
			if result.ChannelIndex != 0 && result.ChannelIndex != 1 {
				t.Fatalf("ChannelIndex=%d, want 0 or 1", result.ChannelIndex)
			}
		})
	}
}

func TestChannelScheduler_SelectChannel_FallbackSorting(t *testing.T) {
	tests := []struct {
		name          string
		priorityFirst bool
		wantIndex     int
	}{
		{
			name:          "priority_first_over_failure_rate",
			priorityFirst: true,
			wantIndex:     0,
		},
		{
			name:          "failure_rate_first_when_configured",
			priorityFirst: false,
			wantIndex:     1,
		},
	}

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "p1-high-failure",
				BaseURL:  "https://p1.example.com",
				APIKeys:  []string{"k1"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:     "p2-low-failure",
				BaseURL:  "https://p2.example.com",
				APIKeys:  []string{"k2"},
				Status:   "active",
				Priority: 2,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheduler, cleanup := createTestScheduler(t, cfg)
			defer cleanup()

			scheduler.schedulerConfig.Promotion.Enabled = false
			scheduler.schedulerConfig.Affinity.Enabled = false
			scheduler.schedulerConfig.Fallback.PriorityFirst = tt.priorityFirst

			// 触发 fallback：让两个渠道都不健康（>=minRequests 且 failureRate>=0.5）。
			for i := 0; i < 5; i++ {
				scheduler.RecordFailure("https://p1.example.com", "k1", false)
			}
			for i := 0; i < 3; i++ {
				scheduler.RecordFailure("https://p2.example.com", "k2", false)
			}
			for i := 0; i < 2; i++ {
				scheduler.RecordSuccess("https://p2.example.com", "k2", false)
			}

			result, err := scheduler.SelectChannel(context.Background(), "", make(map[int]bool), false)
			if err != nil {
				t.Fatalf("选择渠道失败: %v", err)
			}
			if result.Reason != "fallback" {
				t.Fatalf("reason=%s, want fallback", result.Reason)
			}
			if result.ChannelIndex != tt.wantIndex {
				t.Fatalf("ChannelIndex=%d, want %d", result.ChannelIndex, tt.wantIndex)
			}
		})
	}
}

func TestChannelScheduler_getActiveChannels_ExcludesDisabled(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "active-0",
				BaseURL:  "https://active0.example.com",
				APIKeys:  []string{"k0"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:     "disabled-1",
				BaseURL:  "https://disabled1.example.com",
				APIKeys:  []string{"k1"},
				Status:   "disabled",
				Priority: 2,
			},
			{
				Name:     "suspended-2",
				BaseURL:  "https://suspended2.example.com",
				APIKeys:  []string{"k2"},
				Status:   "suspended",
				Priority: 3,
			},
			{
				Name:     "unknown-3",
				BaseURL:  "https://unknown3.example.com",
				APIKeys:  []string{"k3"},
				Status:   "unknown",
				Priority: 4,
			},
			{
				Name:     "empty-status-4",
				BaseURL:  "https://empty4.example.com",
				APIKeys:  []string{"k4"},
				Status:   "",
				Priority: 5,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	got := scheduler.getActiveChannels(false)
	if len(got) != 2 {
		t.Fatalf("期望只返回 2 个 active 渠道，实际返回 %d 个", len(got))
	}
	if got[0].Index != 0 || got[1].Index != 4 {
		t.Fatalf("期望返回 index=[0,4]，实际返回 index=[%d,%d]", got[0].Index, got[1].Index)
	}
	for _, ch := range got {
		if ch.Status != "active" {
			t.Fatalf("期望返回渠道状态均为 active，但发现 index=%d status=%q", ch.Index, ch.Status)
		}
	}
}

func TestChannelScheduler_SelectChannel_SkipsDisabledWhenAllActiveFailed(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "active",
				BaseURL:  "https://active.example.com",
				APIKeys:  []string{"k-active"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:     "disabled",
				BaseURL:  "https://disabled.example.com",
				APIKeys:  []string{"k-disabled"},
				Status:   "disabled",
				Priority: 0,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	scheduler.schedulerConfig.Promotion.Enabled = false
	scheduler.schedulerConfig.Affinity.Enabled = false

	result, err := scheduler.SelectChannel(context.Background(), "", map[int]bool{0: true}, false)
	if err == nil {
		t.Fatalf("期望 active 渠道失败后返回错误（不允许选择 disabled），但得到了 result=%+v", result)
	}
	if result != nil {
		t.Fatalf("期望 result=nil，但得到了 result=%+v (err=%v)", result, err)
	}
}

func TestChannelScheduler_getActiveGeminiChannels_ExcludesDisabled(t *testing.T) {
	cfg := config.Config{
		GeminiUpstream: []config.UpstreamConfig{
			{
				Name:     "active-0",
				BaseURL:  "https://gemini-active0.example.com",
				APIKeys:  []string{"k0"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:     "disabled-1",
				BaseURL:  "https://gemini-disabled1.example.com",
				APIKeys:  []string{"k1"},
				Status:   "disabled",
				Priority: 2,
			},
			{
				Name:     "empty-status-2",
				BaseURL:  "https://gemini-empty2.example.com",
				APIKeys:  []string{"k2"},
				Status:   "",
				Priority: 3,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	got := scheduler.getActiveGeminiChannels()
	if len(got) != 2 {
		t.Fatalf("期望只返回 2 个 active Gemini 渠道，实际返回 %d 个", len(got))
	}
	if got[0].Index != 0 || got[1].Index != 2 {
		t.Fatalf("期望返回 index=[0,2]，实际返回 index=[%d,%d]", got[0].Index, got[1].Index)
	}
	for _, ch := range got {
		if ch.Status != "active" {
			t.Fatalf("期望返回渠道状态均为 active，但发现 index=%d status=%q", ch.Index, ch.Status)
		}
	}
}

func TestChannelScheduler_SelectGeminiChannel_SkipsDisabledWhenAllActiveFailed(t *testing.T) {
	cfg := config.Config{
		GeminiUpstream: []config.UpstreamConfig{
			{
				Name:     "active",
				BaseURL:  "https://gemini-active.example.com",
				APIKeys:  []string{"k-active"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:     "disabled",
				BaseURL:  "https://gemini-disabled.example.com",
				APIKeys:  []string{"k-disabled"},
				Status:   "disabled",
				Priority: 0,
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	scheduler.schedulerConfig.Promotion.Enabled = false
	scheduler.schedulerConfig.Affinity.Enabled = false

	result, err := scheduler.SelectGeminiChannel(context.Background(), "", map[int]bool{0: true})
	if err == nil {
		t.Fatalf("期望 active Gemini 渠道失败后返回错误（不允许选择 disabled），但得到了 result=%+v", result)
	}
	if result != nil {
		t.Fatalf("期望 result=nil，但得到了 result=%+v (err=%v)", result, err)
	}
}
