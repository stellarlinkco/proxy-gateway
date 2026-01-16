package scheduler

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/BenedictKing/claude-proxy/internal/metrics"
	"github.com/BenedictKing/claude-proxy/internal/session"
	"github.com/BenedictKing/claude-proxy/internal/types"
	"github.com/BenedictKing/claude-proxy/internal/warmup"
)

const (
	maxWeight      int64 = 10000
	maxTotalWeight int64 = 1000000
)

// ChannelScheduler 多渠道调度器
type ChannelScheduler struct {
	mu                      sync.RWMutex
	configManager           *config.ConfigManager
	messagesMetricsManager  *metrics.MetricsManager // Messages 渠道指标
	responsesMetricsManager *metrics.MetricsManager // Responses 渠道指标
	geminiMetricsManager    *metrics.MetricsManager // Gemini 渠道指标
	traceAffinity           *session.TraceAffinityManager
	urlManager              *warmup.URLManager // URL 管理器（非阻塞，动态排序）

	schedulerConfig SchedulerConfig

	rrLastMessages  atomic.Int64
	rrLastResponses atomic.Int64
	rrLastGemini    atomic.Int64
}

// NewChannelScheduler 创建多渠道调度器
func NewChannelScheduler(
	cfgManager *config.ConfigManager,
	messagesMetrics *metrics.MetricsManager,
	responsesMetrics *metrics.MetricsManager,
	geminiMetrics *metrics.MetricsManager,
	traceAffinity *session.TraceAffinityManager,
	urlMgr *warmup.URLManager,
) *ChannelScheduler {
	scheduler := &ChannelScheduler{
		configManager:           cfgManager,
		messagesMetricsManager:  messagesMetrics,
		responsesMetricsManager: responsesMetrics,
		geminiMetricsManager:    geminiMetrics,
		traceAffinity:           traceAffinity,
		urlManager:              urlMgr,
		schedulerConfig:         DefaultSchedulerConfig(),
	}
	scheduler.rrLastMessages.Store(-1)
	scheduler.rrLastResponses.Store(-1)
	scheduler.rrLastGemini.Store(-1)
	return scheduler
}

// getMetricsManager 根据类型获取对应的指标管理器
func (s *ChannelScheduler) getMetricsManager(isResponses bool) *metrics.MetricsManager {
	if isResponses {
		return s.responsesMetricsManager
	}
	return s.messagesMetricsManager
}

// SelectionResult 渠道选择结果
type SelectionResult struct {
	Upstream     *config.UpstreamConfig
	ChannelIndex int
	Reason       string // 选择原因（用于日志）
}

// SelectChannel 选择最佳渠道
// 优先级: 促销期渠道 > Trace 亲和（可配置） > 同优先级组内策略选择
func (s *ChannelScheduler) SelectChannel(
	ctx context.Context,
	userID string,
	failedChannels map[int]bool,
	isResponses bool,
) (*SelectionResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 获取活跃渠道列表
	activeChannels := s.getActiveChannels(isResponses)
	if len(activeChannels) == 0 {
		return nil, fmt.Errorf("没有可用的活跃渠道")
	}

	// 获取对应类型的指标管理器
	metricsManager := s.getMetricsManager(isResponses)
	cfg := s.schedulerConfig
	ValidateSchedulerConfig(&cfg)

	// 0. 检查促销期渠道（最高优先级，绕过健康检查）
	if cfg.Promotion.Enabled {
		promotedChannel := s.findPromotedChannel(activeChannels, isResponses)
		if promotedChannel != nil && !failedChannels[promotedChannel.Index] {
			upstream := s.getUpstreamByIndex(promotedChannel.Index, isResponses)
			if upstream != nil && len(upstream.APIKeys) > 0 {
				failureRate := metricsManager.CalculateChannelFailureRate(upstream.BaseURL, upstream.APIKeys)

				maxFailureRate := cfg.Promotion.MaxFailureRate

				if cfg.Promotion.BypassHealthCheck {
					if failureRate <= maxFailureRate {
						log.Printf("[Scheduler-Promotion] 促销期优先选择渠道: [%d] %s (失败率: %.1f%%, maxFailureRate: %.1f%%, 绕过健康检查)",
							promotedChannel.Index, upstream.Name, failureRate*100, maxFailureRate*100)
						return &SelectionResult{
							Upstream:     upstream,
							ChannelIndex: promotedChannel.Index,
							Reason:       "promotion_priority",
						}, nil
					}
					log.Printf("[Scheduler-Promotion] 警告: 促销渠道 [%d] %s 失败率过高，跳过 (失败率: %.1f%%, maxFailureRate: %.1f%%)",
						promotedChannel.Index, upstream.Name, failureRate*100, maxFailureRate*100)
				} else {
					if metricsManager.IsChannelHealthyWithKeys(upstream.BaseURL, upstream.APIKeys) {
						log.Printf("[Scheduler-Promotion] 促销期优先选择渠道: [%d] %s (失败率: %.1f%%)",
							promotedChannel.Index, upstream.Name, failureRate*100)
						return &SelectionResult{
							Upstream:     upstream,
							ChannelIndex: promotedChannel.Index,
							Reason:       "promotion_priority",
						}, nil
					}
					log.Printf("[Scheduler-Promotion] 警告: 促销渠道 [%d] %s 不健康，跳过 (失败率: %.1f%%)",
						promotedChannel.Index, upstream.Name, failureRate*100)
				}
			} else if upstream != nil {
				log.Printf("[Scheduler-Promotion] 警告: 促销渠道 [%d] %s 无可用密钥，跳过", promotedChannel.Index, upstream.Name)
			}
		} else if promotedChannel != nil {
			log.Printf("[Scheduler-Promotion] 警告: 促销渠道 [%d] %s 已在本次请求中失败，跳过", promotedChannel.Index, promotedChannel.Name)
		}
	}

	// 1. 检查 Trace 亲和性（促销渠道失败时或无促销渠道时）
	if cfg.Affinity.Enabled && userID != "" && s.traceAffinity != nil {
		if preferredIdx, ok := s.traceAffinity.GetPreferredChannel(userID); ok && !failedChannels[preferredIdx] {
			var preferredCh *ChannelInfo
			for i := range activeChannels {
				if activeChannels[i].Index == preferredIdx {
					preferredCh = &activeChannels[i]
					break
				}
			}

			if preferredCh != nil {
				if preferredCh.Status != "active" {
					log.Printf("[Scheduler-Affinity] 跳过亲和渠道 [%d] %s: 状态为 %s (user: %s)", preferredIdx, preferredCh.Name, preferredCh.Status, maskUserID(userID))
				} else {
					allowAffinity := true
					if cfg.Affinity.OnlyWithinSamePriority {
						bestHealthyPriority, hasHealthy := s.getBestHealthyPriority(activeChannels, failedChannels, isResponses, metricsManager)
						if hasHealthy && preferredCh.Priority != bestHealthyPriority {
							log.Printf("[Scheduler-Affinity] 跳过亲和渠道 [%d] %s: 优先级不匹配 (preferred=%d, best=%d, user: %s)",
								preferredIdx, preferredCh.Name, preferredCh.Priority, bestHealthyPriority, maskUserID(userID))
							allowAffinity = false
						}
					}

					if allowAffinity {
						upstream := s.getUpstreamByIndex(preferredIdx, isResponses)
						if upstream != nil && len(upstream.APIKeys) > 0 && metricsManager.IsChannelHealthyWithKeys(upstream.BaseURL, upstream.APIKeys) {
							log.Printf("[Scheduler-Affinity] Trace亲和选择渠道: [%d] %s (user: %s)", preferredIdx, upstream.Name, maskUserID(userID))
							return &SelectionResult{
								Upstream:     upstream,
								ChannelIndex: preferredIdx,
								Reason:       "trace_affinity",
							}, nil
						}
					}
				}
			}
		}
	}

	// 2. 选择健康渠道：先筛出“最高优先级组”，再按策略选择
	healthyCandidates := make([]ChannelInfo, 0, len(activeChannels))
	for _, ch := range activeChannels {
		if failedChannels[ch.Index] {
			continue
		}
		if ch.Status != "active" {
			continue
		}

		upstream := s.getUpstreamByIndex(ch.Index, isResponses)
		if upstream == nil || len(upstream.APIKeys) == 0 {
			continue
		}

		if !metricsManager.IsChannelHealthyWithKeys(upstream.BaseURL, upstream.APIKeys) {
			failureRate := metricsManager.CalculateChannelFailureRate(upstream.BaseURL, upstream.APIKeys)
			log.Printf("[Scheduler-Channel] 警告: 跳过不健康渠道: [%d] %s (失败率: %.1f%%)", ch.Index, ch.Name, failureRate*100)
			continue
		}
		healthyCandidates = append(healthyCandidates, ch)
	}

	if len(healthyCandidates) > 0 {
		topPriority := healthyCandidates[0].Priority
		topCandidates := make([]ChannelInfo, 0, len(healthyCandidates))
		for _, ch := range healthyCandidates {
			if ch.Priority != topPriority {
				break
			}
			topCandidates = append(topCandidates, ch)
		}

		selected, reason := s.pickFromTopCandidates(topCandidates, isResponses)
		upstream := s.getUpstreamByIndex(selected.Index, isResponses)
		if upstream != nil {
			log.Printf("[Scheduler-Channel] 选择渠道: [%d] %s (优先级: %d, 策略: %s)", selected.Index, upstream.Name, selected.Priority, reason)
			return &SelectionResult{
				Upstream:     upstream,
				ChannelIndex: selected.Index,
				Reason:       reason,
			}, nil
		}
	}

	// 3. 所有健康渠道都失败，选择失败率最低的作为降级
	return s.selectFallbackChannel(activeChannels, failedChannels, isResponses)
}

func (s *ChannelScheduler) getBestHealthyPriority(
	channels []ChannelInfo,
	failedChannels map[int]bool,
	isResponses bool,
	metricsManager *metrics.MetricsManager,
) (int, bool) {
	bestPriority := 0
	hasBest := false

	for _, ch := range channels {
		if failedChannels[ch.Index] {
			continue
		}
		if ch.Status != "active" {
			continue
		}
		upstream := s.getUpstreamByIndex(ch.Index, isResponses)
		if upstream == nil || len(upstream.APIKeys) == 0 {
			continue
		}
		if !metricsManager.IsChannelHealthyWithKeys(upstream.BaseURL, upstream.APIKeys) {
			continue
		}

		if !hasBest || ch.Priority < bestPriority {
			bestPriority = ch.Priority
			hasBest = true
		}
	}

	return bestPriority, hasBest
}

func (s *ChannelScheduler) pickFromTopCandidates(candidates []ChannelInfo, isResponses bool) (ChannelInfo, string) {
	if len(candidates) == 0 {
		return ChannelInfo{}, "no_candidates"
	}

	switch s.schedulerConfig.LoadBalanceStrategy {
	case LoadBalanceWeightedRandom:
		return pickWeightedRandom(candidates), "weighted_random"
	case LoadBalanceRoundRobin:
		if isResponses {
			return pickRoundRobin(candidates, &s.rrLastResponses), "round_robin"
		}
		return pickRoundRobin(candidates, &s.rrLastMessages), "round_robin"
	default:
		return candidates[0], "priority_order"
	}
}

func pickWeightedRandom(candidates []ChannelInfo) ChannelInfo {
	if len(candidates) == 0 {
		return ChannelInfo{}
	}
	if len(candidates) == 1 {
		return candidates[0]
	}

	var total int64
	for _, ch := range candidates {
		w := int64(ch.Weight)
		if w <= 0 {
			w = 1
		}
		if w > maxWeight {
			w = maxWeight
		}
		if total >= maxTotalWeight {
			total = maxTotalWeight
			break
		}
		if w > maxTotalWeight-total {
			total = maxTotalWeight
			break
		}
		total += w
	}
	if total <= 0 {
		return candidates[0]
	}

	r := rand.Int64N(total)
	for _, ch := range candidates {
		w := int64(ch.Weight)
		if w <= 0 {
			w = 1
		}
		if w > maxWeight {
			w = maxWeight
		}
		if r < w {
			return ch
		}
		r -= w
	}
	return candidates[len(candidates)-1]
}

func pickRoundRobin(candidates []ChannelInfo, lastPicked *atomic.Int64) ChannelInfo {
	if len(candidates) == 0 {
		return ChannelInfo{}
	}
	if len(candidates) == 1 {
		if lastPicked != nil {
			lastPicked.Store(int64(candidates[0].Index))
		}
		return candidates[0]
	}

	last := int64(-1)
	if lastPicked != nil {
		last = lastPicked.Load()
	}

	start := 0
	if last >= 0 {
		for i, ch := range candidates {
			if int64(ch.Index) == last {
				start = (i + 1) % len(candidates)
				break
			}
		}
	}

	selected := candidates[start]
	if lastPicked != nil {
		lastPicked.Store(int64(selected.Index))
	}
	return selected
}

// findPromotedChannel 查找处于促销期的渠道
func (s *ChannelScheduler) findPromotedChannel(activeChannels []ChannelInfo, isResponses bool) *ChannelInfo {
	for i := range activeChannels {
		ch := &activeChannels[i]
		if ch.Status != "active" {
			continue
		}
		upstream := s.getUpstreamByIndex(ch.Index, isResponses)
		if upstream != nil {
			if config.IsChannelInPromotion(upstream) {
				log.Printf("[Scheduler-Promotion] 找到促销渠道: [%d] %s (promotionUntil: %v)", ch.Index, upstream.Name, upstream.PromotionUntil)
				return ch
			}
		}
	}
	return nil
}

// selectFallbackChannel 选择降级渠道（失败率最低的）
func (s *ChannelScheduler) selectFallbackChannel(
	activeChannels []ChannelInfo,
	failedChannels map[int]bool,
	isResponses bool,
) (*SelectionResult, error) {
	metricsManager := s.getMetricsManager(isResponses)
	cfg := s.schedulerConfig.Fallback

	type candidate struct {
		ch          ChannelInfo
		upstream    *config.UpstreamConfig
		failureRate float64
	}

	candidates := make([]candidate, 0, len(activeChannels))

	for i := range activeChannels {
		ch := &activeChannels[i]
		if failedChannels[ch.Index] {
			continue
		}
		if ch.Status != "active" {
			continue
		}

		upstream := s.getUpstreamByIndex(ch.Index, isResponses)
		if upstream == nil || len(upstream.APIKeys) == 0 {
			continue
		}

		failureRate := metricsManager.CalculateChannelFailureRate(upstream.BaseURL, upstream.APIKeys)
		candidates = append(candidates, candidate{
			ch:          *ch,
			upstream:    upstream,
			failureRate: failureRate,
		})
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("所有渠道都不可用")
	}

	sort.Slice(candidates, func(i, j int) bool {
		if cfg.PriorityFirst {
			if candidates[i].ch.Priority != candidates[j].ch.Priority {
				return candidates[i].ch.Priority < candidates[j].ch.Priority
			}
			if candidates[i].failureRate != candidates[j].failureRate {
				return candidates[i].failureRate < candidates[j].failureRate
			}
			return candidates[i].ch.Index < candidates[j].ch.Index
		}

		if candidates[i].failureRate != candidates[j].failureRate {
			return candidates[i].failureRate < candidates[j].failureRate
		}
		if candidates[i].ch.Priority != candidates[j].ch.Priority {
			return candidates[i].ch.Priority < candidates[j].ch.Priority
		}
		return candidates[i].ch.Index < candidates[j].ch.Index
	})

	best := candidates[0]
	if best.upstream != nil {
		log.Printf("[Scheduler-Fallback] 警告: 降级选择渠道: [%d] %s (状态: %s, 优先级: %d, 失败率: %.1f%%)",
			best.ch.Index, best.upstream.Name, best.ch.Status, best.ch.Priority, best.failureRate*100)
		return &SelectionResult{
			Upstream:     best.upstream,
			ChannelIndex: best.ch.Index,
			Reason:       "fallback",
		}, nil
	}

	return nil, fmt.Errorf("所有渠道都不可用")
}

// ChannelInfo 渠道信息（用于排序）
type ChannelInfo struct {
	Index    int
	Name     string
	Priority int
	Weight   int
	Status   string
}

// getActiveChannels 获取可调度渠道列表（仅 active；空 status 视为 active）
func (s *ChannelScheduler) getActiveChannels(isResponses bool) []ChannelInfo {
	cfg := s.configManager.GetConfig()

	var upstreams []config.UpstreamConfig
	if isResponses {
		upstreams = cfg.ResponsesUpstream
	} else {
		upstreams = cfg.Upstream
	}

	// 筛选活跃渠道
	var activeChannels []ChannelInfo
	for i, upstream := range upstreams {
		status := upstream.Status
		if status == "" {
			status = "active" // 默认为活跃
		}

		// 仅 active 参与调度；disabled/suspended/unknown 都不参与。
		if status != "active" {
			continue
		}

		priority := upstream.Priority
		if priority == 0 {
			priority = i // 默认优先级为索引
		}

		activeChannels = append(activeChannels, ChannelInfo{
			Index:    i,
			Name:     upstream.Name,
			Priority: priority,
			Weight:   upstream.Weight,
			Status:   status,
		})
	}

	// 按优先级排序（数字越小优先级越高），最后按 index 保序
	sort.Slice(activeChannels, func(i, j int) bool {
		if activeChannels[i].Priority != activeChannels[j].Priority {
			return activeChannels[i].Priority < activeChannels[j].Priority
		}
		return activeChannels[i].Index < activeChannels[j].Index
	})

	return activeChannels
}

// getUpstreamByIndex 根据索引获取上游配置
// 注意：返回的是副本，避免指向 slice 元素的指针在 slice 重分配后失效
func (s *ChannelScheduler) getUpstreamByIndex(index int, isResponses bool) *config.UpstreamConfig {
	cfg := s.configManager.GetConfig()

	var upstreams []config.UpstreamConfig
	if isResponses {
		upstreams = cfg.ResponsesUpstream
	} else {
		upstreams = cfg.Upstream
	}

	if index >= 0 && index < len(upstreams) {
		// 返回副本，避免返回指向 slice 元素的指针
		upstream := upstreams[index]
		return &upstream
	}
	return nil
}

// RecordSuccess 记录渠道成功（使用 baseURL + apiKey）
func (s *ChannelScheduler) RecordSuccess(baseURL, apiKey string, isResponses bool) {
	s.getMetricsManager(isResponses).RecordSuccess(baseURL, apiKey)
}

// RecordSuccessWithUsage 记录渠道成功（带 Usage 数据）
func (s *ChannelScheduler) RecordSuccessWithUsage(baseURL, apiKey string, usage *types.Usage, isResponses bool, model string, costCents int64) {
	s.getMetricsManager(isResponses).RecordSuccessWithUsage(baseURL, apiKey, usage, model, costCents)
}

// RecordFailure 记录渠道失败（使用 baseURL + apiKey）
func (s *ChannelScheduler) RecordFailure(baseURL, apiKey string, isResponses bool) {
	s.getMetricsManager(isResponses).RecordFailure(baseURL, apiKey)
}

// SetTraceAffinity 设置 Trace 亲和
func (s *ChannelScheduler) SetTraceAffinity(userID string, channelIndex int) {
	if userID != "" {
		s.traceAffinity.SetPreferredChannel(userID, channelIndex)
	}
}

// UpdateTraceAffinity 更新 Trace 亲和时间（续期）
func (s *ChannelScheduler) UpdateTraceAffinity(userID string) {
	if userID != "" {
		s.traceAffinity.UpdateLastUsed(userID)
	}
}

// GetMessagesMetricsManager 获取 Messages 渠道指标管理器
func (s *ChannelScheduler) GetMessagesMetricsManager() *metrics.MetricsManager {
	return s.messagesMetricsManager
}

// GetResponsesMetricsManager 获取 Responses 渠道指标管理器
func (s *ChannelScheduler) GetResponsesMetricsManager() *metrics.MetricsManager {
	return s.responsesMetricsManager
}

// GetTraceAffinityManager 获取 Trace 亲和性管理器
func (s *ChannelScheduler) GetTraceAffinityManager() *session.TraceAffinityManager {
	return s.traceAffinity
}

// ResetChannelMetrics 重置渠道所有 Key 的指标（用于恢复熔断）
func (s *ChannelScheduler) ResetChannelMetrics(channelIndex int, isResponses bool) {
	upstream := s.getUpstreamByIndex(channelIndex, isResponses)
	if upstream == nil {
		return
	}
	metricsManager := s.getMetricsManager(isResponses)
	for _, apiKey := range upstream.APIKeys {
		metricsManager.ResetKey(upstream.BaseURL, apiKey)
	}
	log.Printf("[Scheduler-Reset] 渠道 [%d] %s 的所有 Key 指标已重置", channelIndex, upstream.Name)
}

// ResetKeyMetrics 重置单个 Key 的指标
func (s *ChannelScheduler) ResetKeyMetrics(baseURL, apiKey string, isResponses bool) {
	s.getMetricsManager(isResponses).ResetKey(baseURL, apiKey)
}

// GetActiveChannelCount 获取活跃渠道数量
func (s *ChannelScheduler) GetActiveChannelCount(isResponses bool) int {
	return len(s.getActiveChannels(isResponses))
}

// IsMultiChannelMode 判断是否为多渠道模式
func (s *ChannelScheduler) IsMultiChannelMode(isResponses bool) bool {
	return s.GetActiveChannelCount(isResponses) > 1
}

// maskUserID 掩码 user_id（保护隐私）
func maskUserID(userID string) string {
	if len(userID) <= 16 {
		return "***"
	}
	return userID[:8] + "***" + userID[len(userID)-4:]
}

// GetSortedURLsForChannel 获取渠道排序后的 URL 列表（非阻塞，立即返回）
// 返回按动态排序的 URL 结果列表，包含原始索引用于指标记录
func (s *ChannelScheduler) GetSortedURLsForChannel(
	channelIndex int,
	urls []string,
) []warmup.URLLatencyResult {
	if s.urlManager == nil || len(urls) <= 1 {
		// 无 URL 管理器或单 URL，返回默认结果
		results := make([]warmup.URLLatencyResult, len(urls))
		for i, url := range urls {
			results[i] = warmup.URLLatencyResult{
				URL:         url,
				OriginalIdx: i,
				Success:     true,
			}
		}
		return results
	}
	return s.urlManager.GetSortedURLs(channelIndex, urls)
}

// MarkURLSuccess 标记 URL 成功
func (s *ChannelScheduler) MarkURLSuccess(channelIndex int, url string) {
	if s.urlManager != nil {
		s.urlManager.MarkSuccess(channelIndex, url)
	}
}

// MarkURLFailure 标记 URL 失败，触发动态排序
func (s *ChannelScheduler) MarkURLFailure(channelIndex int, url string) {
	if s.urlManager != nil {
		s.urlManager.MarkFailure(channelIndex, url)
	}
}

// InvalidateURLCache 使渠道 URL 状态失效
func (s *ChannelScheduler) InvalidateURLCache(channelIndex int) {
	if s.urlManager != nil {
		s.urlManager.InvalidateChannel(channelIndex)
	}
}

// GetURLManagerStats 获取 URL 管理器统计
func (s *ChannelScheduler) GetURLManagerStats() map[string]interface{} {
	if s.urlManager != nil {
		return s.urlManager.GetStats()
	}
	return nil
}

// ============== Gemini 渠道相关方法 ==============

// SelectGeminiChannel 选择最佳 Gemini 渠道
// 优先级: 促销期渠道 > Trace 亲和（可配置） > 同优先级组内策略选择
func (s *ChannelScheduler) SelectGeminiChannel(
	ctx context.Context,
	userID string,
	failedChannels map[int]bool,
) (*SelectionResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 获取活跃渠道列表
	activeChannels := s.getActiveGeminiChannels()
	if len(activeChannels) == 0 {
		return nil, fmt.Errorf("没有可用的活跃 Gemini 渠道")
	}

	// 获取指标管理器
	metricsManager := s.geminiMetricsManager
	cfg := s.schedulerConfig
	ValidateSchedulerConfig(&cfg)

	// 0. 检查促销期渠道（最高优先级，绕过健康检查）
	if cfg.Promotion.Enabled {
		promotedChannel := s.findPromotedGeminiChannel(activeChannels)
		if promotedChannel != nil && !failedChannels[promotedChannel.Index] {
			upstream := s.getGeminiUpstreamByIndex(promotedChannel.Index)
			if upstream != nil && len(upstream.APIKeys) > 0 {
				failureRate := metricsManager.CalculateChannelFailureRate(upstream.BaseURL, upstream.APIKeys)

				maxFailureRate := cfg.Promotion.MaxFailureRate

				if cfg.Promotion.BypassHealthCheck {
					if failureRate <= maxFailureRate {
						log.Printf("[Scheduler-Gemini-Promotion] 促销期优先选择渠道: [%d] %s (失败率: %.1f%%, maxFailureRate: %.1f%%, 绕过健康检查)",
							promotedChannel.Index, upstream.Name, failureRate*100, maxFailureRate*100)
						return &SelectionResult{
							Upstream:     upstream,
							ChannelIndex: promotedChannel.Index,
							Reason:       "promotion_priority",
						}, nil
					}
					log.Printf("[Scheduler-Gemini-Promotion] 警告: 促销渠道 [%d] %s 失败率过高，跳过 (失败率: %.1f%%, maxFailureRate: %.1f%%)",
						promotedChannel.Index, upstream.Name, failureRate*100, maxFailureRate*100)
				} else {
					if metricsManager.IsChannelHealthyWithKeys(upstream.BaseURL, upstream.APIKeys) {
						log.Printf("[Scheduler-Gemini-Promotion] 促销期优先选择渠道: [%d] %s (失败率: %.1f%%)",
							promotedChannel.Index, upstream.Name, failureRate*100)
						return &SelectionResult{
							Upstream:     upstream,
							ChannelIndex: promotedChannel.Index,
							Reason:       "promotion_priority",
						}, nil
					}
					log.Printf("[Scheduler-Gemini-Promotion] 警告: 促销渠道 [%d] %s 不健康，跳过 (失败率: %.1f%%)",
						promotedChannel.Index, upstream.Name, failureRate*100)
				}
			} else if upstream != nil {
				log.Printf("[Scheduler-Gemini-Promotion] 警告: 促销渠道 [%d] %s 无可用密钥，跳过", promotedChannel.Index, upstream.Name)
			}
		} else if promotedChannel != nil {
			log.Printf("[Scheduler-Gemini-Promotion] 警告: 促销渠道 [%d] %s 已在本次请求中失败，跳过", promotedChannel.Index, promotedChannel.Name)
		}
	}

	// 1. 检查 Trace 亲和性
	if cfg.Affinity.Enabled && userID != "" && s.traceAffinity != nil {
		if preferredIdx, ok := s.traceAffinity.GetPreferredChannel(userID); ok && !failedChannels[preferredIdx] {
			var preferredCh *ChannelInfo
			for i := range activeChannels {
				if activeChannels[i].Index == preferredIdx {
					preferredCh = &activeChannels[i]
					break
				}
			}

			if preferredCh != nil {
				if preferredCh.Status != "active" {
					log.Printf("[Scheduler-Gemini-Affinity] 跳过亲和渠道 [%d] %s: 状态为 %s (user: %s)", preferredIdx, preferredCh.Name, preferredCh.Status, maskUserID(userID))
				} else {
					allowAffinity := true
					if cfg.Affinity.OnlyWithinSamePriority {
						bestHealthyPriority, hasHealthy := s.getBestHealthyGeminiPriority(activeChannels, failedChannels, metricsManager)
						if hasHealthy && preferredCh.Priority != bestHealthyPriority {
							log.Printf("[Scheduler-Gemini-Affinity] 跳过亲和渠道 [%d] %s: 优先级不匹配 (preferred=%d, best=%d, user: %s)",
								preferredIdx, preferredCh.Name, preferredCh.Priority, bestHealthyPriority, maskUserID(userID))
							allowAffinity = false
						}
					}

					if allowAffinity {
						upstream := s.getGeminiUpstreamByIndex(preferredIdx)
						if upstream != nil && len(upstream.APIKeys) > 0 && metricsManager.IsChannelHealthyWithKeys(upstream.BaseURL, upstream.APIKeys) {
							log.Printf("[Scheduler-Gemini-Affinity] Trace亲和选择渠道: [%d] %s (user: %s)", preferredIdx, upstream.Name, maskUserID(userID))
							return &SelectionResult{
								Upstream:     upstream,
								ChannelIndex: preferredIdx,
								Reason:       "trace_affinity",
							}, nil
						}
					}
				}
			}
		}
	}

	// 2. 选择健康渠道：先筛出“最高优先级组”，再按策略选择
	healthyCandidates := make([]ChannelInfo, 0, len(activeChannels))
	for _, ch := range activeChannels {
		if failedChannels[ch.Index] {
			continue
		}
		if ch.Status != "active" {
			continue
		}

		upstream := s.getGeminiUpstreamByIndex(ch.Index)
		if upstream == nil || len(upstream.APIKeys) == 0 {
			continue
		}

		if !metricsManager.IsChannelHealthyWithKeys(upstream.BaseURL, upstream.APIKeys) {
			failureRate := metricsManager.CalculateChannelFailureRate(upstream.BaseURL, upstream.APIKeys)
			log.Printf("[Scheduler-Gemini-Channel] 警告: 跳过不健康渠道: [%d] %s (失败率: %.1f%%)", ch.Index, ch.Name, failureRate*100)
			continue
		}
		healthyCandidates = append(healthyCandidates, ch)
	}

	if len(healthyCandidates) > 0 {
		topPriority := healthyCandidates[0].Priority
		topCandidates := make([]ChannelInfo, 0, len(healthyCandidates))
		for _, ch := range healthyCandidates {
			if ch.Priority != topPriority {
				break
			}
			topCandidates = append(topCandidates, ch)
		}

		selected, reason := s.pickFromTopGeminiCandidates(topCandidates)
		upstream := s.getGeminiUpstreamByIndex(selected.Index)
		if upstream != nil {
			log.Printf("[Scheduler-Gemini-Channel] 选择渠道: [%d] %s (优先级: %d, 策略: %s)", selected.Index, upstream.Name, selected.Priority, reason)
			return &SelectionResult{
				Upstream:     upstream,
				ChannelIndex: selected.Index,
				Reason:       reason,
			}, nil
		}
	}

	// 3. 所有健康渠道都失败，选择失败率最低的作为降级
	return s.selectFallbackGeminiChannel(activeChannels, failedChannels)
}

func (s *ChannelScheduler) getBestHealthyGeminiPriority(
	channels []ChannelInfo,
	failedChannels map[int]bool,
	metricsManager *metrics.MetricsManager,
) (int, bool) {
	bestPriority := 0
	hasBest := false

	for _, ch := range channels {
		if failedChannels[ch.Index] {
			continue
		}
		if ch.Status != "active" {
			continue
		}
		upstream := s.getGeminiUpstreamByIndex(ch.Index)
		if upstream == nil || len(upstream.APIKeys) == 0 {
			continue
		}
		if !metricsManager.IsChannelHealthyWithKeys(upstream.BaseURL, upstream.APIKeys) {
			continue
		}

		if !hasBest || ch.Priority < bestPriority {
			bestPriority = ch.Priority
			hasBest = true
		}
	}

	return bestPriority, hasBest
}

func (s *ChannelScheduler) pickFromTopGeminiCandidates(candidates []ChannelInfo) (ChannelInfo, string) {
	if len(candidates) == 0 {
		return ChannelInfo{}, "no_candidates"
	}

	switch s.schedulerConfig.LoadBalanceStrategy {
	case LoadBalanceWeightedRandom:
		return pickWeightedRandom(candidates), "weighted_random"
	case LoadBalanceRoundRobin:
		return pickRoundRobin(candidates, &s.rrLastGemini), "round_robin"
	default:
		return candidates[0], "priority_order"
	}
}

// findPromotedGeminiChannel 查找处于促销期的 Gemini 渠道
func (s *ChannelScheduler) findPromotedGeminiChannel(activeChannels []ChannelInfo) *ChannelInfo {
	for i := range activeChannels {
		ch := &activeChannels[i]
		if ch.Status != "active" {
			continue
		}
		upstream := s.getGeminiUpstreamByIndex(ch.Index)
		if upstream != nil {
			if config.IsChannelInPromotion(upstream) {
				log.Printf("[Scheduler-Gemini-Promotion] 找到促销渠道: [%d] %s (promotionUntil: %v)", ch.Index, upstream.Name, upstream.PromotionUntil)
				return ch
			}
		}
	}
	return nil
}

// selectFallbackGeminiChannel 选择 Gemini 降级渠道（失败率最低的）
func (s *ChannelScheduler) selectFallbackGeminiChannel(
	activeChannels []ChannelInfo,
	failedChannels map[int]bool,
) (*SelectionResult, error) {
	metricsManager := s.geminiMetricsManager
	cfg := s.schedulerConfig.Fallback

	type candidate struct {
		ch          ChannelInfo
		upstream    *config.UpstreamConfig
		failureRate float64
	}

	candidates := make([]candidate, 0, len(activeChannels))

	for i := range activeChannels {
		ch := &activeChannels[i]
		if failedChannels[ch.Index] {
			continue
		}
		if ch.Status != "active" {
			continue
		}

		upstream := s.getGeminiUpstreamByIndex(ch.Index)
		if upstream == nil || len(upstream.APIKeys) == 0 {
			continue
		}

		failureRate := metricsManager.CalculateChannelFailureRate(upstream.BaseURL, upstream.APIKeys)
		candidates = append(candidates, candidate{
			ch:          *ch,
			upstream:    upstream,
			failureRate: failureRate,
		})
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("所有 Gemini 渠道都不可用")
	}

	sort.Slice(candidates, func(i, j int) bool {
		if cfg.PriorityFirst {
			if candidates[i].ch.Priority != candidates[j].ch.Priority {
				return candidates[i].ch.Priority < candidates[j].ch.Priority
			}
			if candidates[i].failureRate != candidates[j].failureRate {
				return candidates[i].failureRate < candidates[j].failureRate
			}
			return candidates[i].ch.Index < candidates[j].ch.Index
		}

		if candidates[i].failureRate != candidates[j].failureRate {
			return candidates[i].failureRate < candidates[j].failureRate
		}
		if candidates[i].ch.Priority != candidates[j].ch.Priority {
			return candidates[i].ch.Priority < candidates[j].ch.Priority
		}
		return candidates[i].ch.Index < candidates[j].ch.Index
	})

	best := candidates[0]
	if best.upstream != nil {
		log.Printf("[Scheduler-Gemini-Fallback] 警告: 降级选择渠道: [%d] %s (状态: %s, 优先级: %d, 失败率: %.1f%%)",
			best.ch.Index, best.upstream.Name, best.ch.Status, best.ch.Priority, best.failureRate*100)
		return &SelectionResult{
			Upstream:     best.upstream,
			ChannelIndex: best.ch.Index,
			Reason:       "fallback",
		}, nil
	}

	return nil, fmt.Errorf("所有 Gemini 渠道都不可用")
}

// getActiveGeminiChannels 获取可调度 Gemini 渠道列表（仅 active；空 status 视为 active）
func (s *ChannelScheduler) getActiveGeminiChannels() []ChannelInfo {
	cfg := s.configManager.GetConfig()
	upstreams := cfg.GeminiUpstream

	var activeChannels []ChannelInfo
	for i, upstream := range upstreams {
		status := upstream.Status
		if status == "" {
			status = "active"
		}

		// 仅 active 参与调度；disabled/suspended/unknown 都不参与。
		if status != "active" {
			continue
		}

		priority := upstream.Priority
		if priority == 0 {
			priority = i
		}

		activeChannels = append(activeChannels, ChannelInfo{
			Index:    i,
			Name:     upstream.Name,
			Priority: priority,
			Weight:   upstream.Weight,
			Status:   status,
		})
	}

	sort.Slice(activeChannels, func(i, j int) bool {
		if activeChannels[i].Priority != activeChannels[j].Priority {
			return activeChannels[i].Priority < activeChannels[j].Priority
		}
		return activeChannels[i].Index < activeChannels[j].Index
	})

	return activeChannels
}

// getGeminiUpstreamByIndex 根据索引获取 Gemini 上游配置
func (s *ChannelScheduler) getGeminiUpstreamByIndex(index int) *config.UpstreamConfig {
	cfg := s.configManager.GetConfig()
	upstreams := cfg.GeminiUpstream

	if index >= 0 && index < len(upstreams) {
		upstream := upstreams[index]
		return &upstream
	}
	return nil
}

// RecordGeminiSuccess 记录 Gemini 渠道成功
func (s *ChannelScheduler) RecordGeminiSuccess(baseURL, apiKey string) {
	s.geminiMetricsManager.RecordSuccess(baseURL, apiKey)
}

// RecordGeminiSuccessWithUsage 记录 Gemini 渠道成功（带 Usage 数据）
func (s *ChannelScheduler) RecordGeminiSuccessWithUsage(baseURL, apiKey string, usage *types.Usage, model string, costCents int64) {
	s.geminiMetricsManager.RecordSuccessWithUsage(baseURL, apiKey, usage, model, costCents)
}

// RecordGeminiFailure 记录 Gemini 渠道失败
func (s *ChannelScheduler) RecordGeminiFailure(baseURL, apiKey string) {
	s.geminiMetricsManager.RecordFailure(baseURL, apiKey)
}

// GetGeminiMetricsManager 获取 Gemini 渠道指标管理器
func (s *ChannelScheduler) GetGeminiMetricsManager() *metrics.MetricsManager {
	return s.geminiMetricsManager
}

// ResetGeminiChannelMetrics 重置 Gemini 渠道所有 Key 的指标
func (s *ChannelScheduler) ResetGeminiChannelMetrics(channelIndex int) {
	upstream := s.getGeminiUpstreamByIndex(channelIndex)
	if upstream == nil {
		return
	}
	for _, apiKey := range upstream.APIKeys {
		s.geminiMetricsManager.ResetKey(upstream.BaseURL, apiKey)
	}
	log.Printf("[Scheduler-Gemini-Reset] 渠道 [%d] %s 的所有 Key 指标已重置", channelIndex, upstream.Name)
}

// GetActiveGeminiChannelCount 获取活跃 Gemini 渠道数量
func (s *ChannelScheduler) GetActiveGeminiChannelCount() int {
	return len(s.getActiveGeminiChannels())
}

// IsMultiChannelModeGemini 判断 Gemini 是否为多渠道模式
func (s *ChannelScheduler) IsMultiChannelModeGemini() bool {
	return s.GetActiveGeminiChannelCount() > 1
}
