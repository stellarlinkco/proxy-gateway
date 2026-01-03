package metrics

import (
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/BenedictKing/claude-proxy/internal/types"
)

func TestCacheMetrics_SnapshotAndConcurrent(t *testing.T) {
	var m CacheMetrics

	if got := m.Snapshot(); got != (CacheMetricsSnapshot{}) {
		t.Fatalf("zero Snapshot() = %+v, want all zeros", got)
	}

	m.IncReadHit()
	m.IncReadMiss()
	m.IncWriteSet()
	m.IncWriteUpdate()
	m.SetEntries(123)
	m.SetCapacity(456)

	got := m.Snapshot()
	if got.ReadHit != 1 || got.ReadMiss != 1 || got.WriteSet != 1 || got.WriteUpdate != 1 {
		t.Fatalf("after Inc* Snapshot() = %+v, want each counter=1", got)
	}
	if got.Entries != 123 || got.Capacity != 456 {
		t.Fatalf("after Set* Snapshot() = %+v, want entries=123 capacity=456", got)
	}

	m.SetEntries(math.MaxInt64)
	m.SetCapacity(math.MaxInt64 - 1)
	got = m.Snapshot()
	if got.Entries != math.MaxInt64 || got.Capacity != math.MaxInt64-1 {
		t.Fatalf("large Set* Snapshot() = %+v, want entries/capacity updated", got)
	}

	const goroutines = 32
	const loops = 2000

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < loops; j++ {
				m.IncReadHit()
				m.IncReadMiss()
				m.IncWriteSet()
				m.IncWriteUpdate()
			}
		}()
	}
	wg.Wait()

	got = m.Snapshot()
	wantDelta := int64(goroutines * loops)
	if got.ReadHit != 1+wantDelta || got.ReadMiss != 1+wantDelta || got.WriteSet != 1+wantDelta || got.WriteUpdate != 1+wantDelta {
		t.Fatalf("concurrent Snapshot() = %+v, want each counter increased by %d", got, wantDelta)
	}
}

func newTestSQLiteStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(&SQLiteStoreConfig{
		DBPath:        t.TempDir() + "/metrics.db",
		RetentionDays: 7,
	})
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestSQLiteStore_CoreQueriesAndBufferEdges(t *testing.T) {
	store := newTestSQLiteStore(t)

	// 覆盖 WriteBufferStats / AddRecord / FlushNow / LoadRecords / Query* 这条主路径。
	store.batchSize = 1000 // 避免自动异步 flush，便于断言

	baseURL := "https://example.com"
	apiKey := "sk-test-1234567890"
	metricsKey := generateMetricsKey(baseURL, apiKey)
	keyMask := "sk-****"

	now := time.Now().UTC().Truncate(time.Second)
	records := []PersistentRecord{
		{
			MetricsKey:          metricsKey,
			BaseURL:             baseURL,
			KeyMask:             keyMask,
			Timestamp:           now.Add(-2 * time.Hour),
			Success:             true,
			InputTokens:         10,
			OutputTokens:        3,
			CacheCreationTokens: 1,
			CacheReadTokens:     0,
			Model:               "m1",
			CostCents:           7,
			APIType:             "messages",
		},
		{
			MetricsKey:          metricsKey,
			BaseURL:             baseURL,
			KeyMask:             keyMask,
			Timestamp:           now.Add(-30 * time.Minute),
			Success:             false,
			InputTokens:         0,
			OutputTokens:        0,
			CacheCreationTokens: 0,
			CacheReadTokens:     0,
			Model:               "m1",
			CostCents:           0,
			APIType:             "messages",
		},
	}
	for _, r := range records {
		store.AddRecord(r)
	}

	stats := store.GetWriteBufferStats()
	if stats.BufferedRecords != len(records) {
		t.Fatalf("GetWriteBufferStats().BufferedRecords = %d, want %d", stats.BufferedRecords, len(records))
	}
	if stats.MaxBufferRecords <= 0 || stats.BufferUsage < 0 {
		t.Fatalf("GetWriteBufferStats() = %+v, want sane values", stats)
	}

	store.FlushNow()

	count, err := store.GetRecordCount()
	if err != nil {
		t.Fatalf("GetRecordCount() err = %v", err)
	}
	if count != int64(len(records)) {
		t.Fatalf("GetRecordCount() = %d, want %d", count, len(records))
	}

	loaded, err := store.LoadRecords(now.Add(-3*time.Hour), "messages")
	if err != nil {
		t.Fatalf("LoadRecords() err = %v", err)
	}
	if len(loaded) != len(records) {
		t.Fatalf("LoadRecords() len=%d, want %d", len(loaded), len(records))
	}

	totals, err := store.QueryRequestRecordTotals("messages", now.Add(-3*time.Hour), now.Add(1*time.Second), []string{metricsKey})
	if err != nil {
		t.Fatalf("QueryRequestRecordTotals() err = %v", err)
	}
	if totals.RequestCount != int64(len(records)) || totals.SuccessCount != 1 || totals.FailureCount != 1 {
		t.Fatalf("QueryRequestRecordTotals() = %+v, want requests=2 success=1 failure=1", totals)
	}

	// interval 过小：覆盖错误分支
	if _, err := store.QueryRequestRecordBucketStats("messages", now.Add(-1*time.Hour), now.Add(1*time.Second), 500*time.Millisecond, nil); err == nil {
		t.Fatalf("QueryRequestRecordBucketStats(interval<1s) err=nil, want error")
	}

	// 正常 bucket 查询：至少应返回一个 bucket
	buckets, err := store.QueryRequestRecordBucketStats("messages", now.Add(-3*time.Hour), now.Add(1*time.Second), 1*time.Hour, []string{metricsKey})
	if err != nil {
		t.Fatalf("QueryRequestRecordBucketStats() err = %v", err)
	}
	if len(buckets) == 0 {
		t.Fatalf("QueryRequestRecordBucketStats() buckets empty, want non-empty")
	}

	// daily_stats：构造一条“整日”数据，覆盖 QueryDailyTotals 的扫描路径。
	loc := time.Now().Location()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -2)
	store.AddRecord(PersistentRecord{
		MetricsKey: metricsKey,
		BaseURL:    baseURL,
		KeyMask:    keyMask,
		Timestamp:  day.Add(1 * time.Hour),
		Success:    true,
		APIType:    "messages",
	})
	store.FlushNow()
	if err := store.AggregateDailyStats(day); err != nil {
		t.Fatalf("AggregateDailyStats() err = %v", err)
	}

	dayStr := day.Format("2006-01-02")
	daily, err := store.QueryDailyTotals("messages", dayStr, dayStr, []string{metricsKey})
	if err != nil {
		t.Fatalf("QueryDailyTotals() err = %v", err)
	}
	if daily[dayStr].RequestCount == 0 {
		t.Fatalf("QueryDailyTotals()[%s]=%+v, want RequestCount>0", dayStr, daily[dayStr])
	}

	// AddRequestLog：覆盖 Closed 检查分支（不影响底层 DB）
	store.bufferMu.Lock()
	store.closed = true
	store.bufferMu.Unlock()
	if err := store.AddRequestLog(RequestLogRecord{RequestID: "req-1", APIType: "messages"}); err == nil {
		t.Fatalf("AddRequestLog(closed) err=nil, want error")
	}
}

type stubStore struct {
	loadErr error
}

func (s stubStore) AddRecord(PersistentRecord) {}
func (s stubStore) LoadRecords(time.Time, string) ([]PersistentRecord, error) {
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	return nil, nil
}
func (s stubStore) CleanupOldRecords(time.Time) (int64, error) { return 0, nil }
func (s stubStore) Close() error                               { return nil }

func TestMetricsManager_BasicAndHistoryPaths(t *testing.T) {
	// 覆盖 NewMetricsManagerWithConfig 的参数修正分支。
	m := NewMetricsManagerWithConfig(0, -1)
	t.Cleanup(m.Stop)

	baseURL := "https://example.com"
	key1 := "sk-1"
	key2 := "sk-2"

	if !m.IsKeyHealthy(baseURL, key1) {
		t.Fatalf("IsKeyHealthy(no data)=false, want true")
	}

	// 触发熔断：windowSize 会被修正到 >=3，minRequests=max(3, windowSize/2)=3。
	m.RecordFailure(baseURL, key1)
	m.RecordFailure(baseURL, key1)
	m.RecordFailure(baseURL, key1)

	if m.IsKeyHealthy(baseURL, key1) {
		t.Fatalf("IsKeyHealthy(all failures)=true, want false")
	}
	if !m.ShouldSuspendKey(baseURL, key1) {
		t.Fatalf("ShouldSuspendKey(all failures)=false, want true")
	}

	usage := &types.Usage{
		InputTokens:              10,
		OutputTokens:             5,
		CacheCreationInputTokens: 3,
		CacheReadInputTokens:     2,
	}
	m.RecordSuccessWithUsage(baseURL, key1, usage, "m1", 7)
	m.RecordSuccess(baseURL, key1)
	m.RecordSuccess(baseURL, key1)

	if !m.IsKeyHealthy(baseURL, key1) {
		t.Fatalf("IsKeyHealthy(after enough successes)=false, want true")
	}

	// key2：用于聚合与排序覆盖
	m.RecordSuccess(baseURL, key2)
	m.RecordFailure(baseURL, key2)

	if rate := m.CalculateKeyFailureRate(baseURL, key1); rate < 0 || rate > 1 {
		t.Fatalf("CalculateKeyFailureRate() = %f, want in [0,1]", rate)
	}
	if rate := m.CalculateChannelFailureRate(baseURL, []string{key1, key2}); rate < 0 || rate > 1 {
		t.Fatalf("CalculateChannelFailureRate() = %f, want in [0,1]", rate)
	}

	if km := m.GetKeyMetrics(baseURL, key1); km == nil || km.RequestCount == 0 {
		t.Fatalf("GetKeyMetrics() = %+v, want non-nil with RequestCount>0", km)
	}
	agg := m.GetChannelAggregatedMetrics(1, baseURL, []string{key1, key2})
	if agg == nil || agg.RequestCount == 0 {
		t.Fatalf("GetChannelAggregatedMetrics() = %+v, want non-nil with RequestCount>0", agg)
	}

	info := m.GetChannelKeyUsageInfo(baseURL, []string{key1, key2, "sk-3"})
	if len(info) != 3 {
		t.Fatalf("GetChannelKeyUsageInfo() len=%d, want 3", len(info))
	}
	top := SelectTopKeys(info, 10)
	if len(top) != len(info) {
		t.Fatalf("SelectTopKeys(len=%d,maxDisplay=10) = %d, want %d", len(info), len(top), len(info))
	}
	many := make([]KeyUsageInfo, 0, 12)
	for len(many) < 12 {
		many = append(many, info...)
	}
	many = many[:12]
	top = SelectTopKeys(many, 10)
	if len(top) != 10 {
		t.Fatalf("SelectTopKeys(len=%d,maxDisplay=10) = %d, want 10", len(many), len(top))
	}

	all := m.GetAllKeyMetrics()
	if len(all) < 2 {
		t.Fatalf("GetAllKeyMetrics() len=%d, want >=2", len(all))
	}

	// 时间窗口统计：至少保证不崩溃且返回结构合理。
	w := m.GetTimeWindowStatsForKey(baseURL, key1, 24*time.Hour)
	if w.RequestCount == 0 {
		t.Fatalf("GetTimeWindowStatsForKey() = %+v, want RequestCount>0", w)
	}
	windows := m.GetAllTimeWindowStatsForKey(baseURL, key1)
	if len(windows) != 4 {
		t.Fatalf("GetAllTimeWindowStatsForKey() len=%d, want 4", len(windows))
	}

	// ToResponse：覆盖聚合计算与时间窗口统计。
	resp := m.ToResponse(7, baseURL, []string{key1, key2}, 123)
	if resp == nil || resp.ChannelIndex != 7 || resp.Latency != 123 {
		t.Fatalf("ToResponse() = %+v, want channelIndex=7 latency=123", resp)
	}
	if resp.SuccessRate < 0 || resp.SuccessRate > 100 {
		t.Fatalf("ToResponse().SuccessRate=%f, want in [0,100]", resp.SuccessRate)
	}

	// 覆盖 deprecated 的旧 API。
	_ = m.IsChannelHealthy(1)
	_ = m.CalculateFailureRate(1)
	_ = m.CalculateSuccessRate(1)
	m.Reset(1)
	_ = m.GetMetrics(1)
	_ = m.GetAllMetrics()
	_ = m.GetTimeWindowStats(1, time.Minute)
	_ = m.GetAllTimeWindowStats(1)
	_ = m.ShouldSuspend(1)

	// ResetKey/ResetAll：覆盖重置分支。
	m.ResetKey(baseURL, key1)
	if m.GetKeyMetrics(baseURL, key1) == nil {
		t.Fatalf("ResetKey() should keep key entry (copy), want non-nil metrics")
	}
	m.ResetAll()
	if len(m.GetAllKeyMetrics()) != 0 {
		t.Fatalf("ResetAll() did not clear metrics")
	}

	// 覆盖 getters（简单但算语句覆盖）。
	_ = m.GetCircuitRecoveryTime()
	_ = m.GetFailureThreshold()
	_ = m.GetWindowSize()

	// 覆盖 cleanup 逻辑（不等 ticker）。
	m2 := NewMetricsManagerWithConfig(4, 0.5)
	t.Cleanup(m2.Stop)
	m2.RecordFailure(baseURL, key1)
	m2.mu.Lock()
	mk := generateMetricsKey(baseURL, key1)
	if km, ok := m2.keyMetrics[mk]; ok {
		old := time.Now().Add(-2 * time.Hour)
		km.CircuitBrokenAt = &old
		km.LastSuccessAt = &old
	}
	m2.circuitRecoveryTime = 1 * time.Minute
	m2.mu.Unlock()

	m2.recoverExpiredCircuitBreakers()
	m2.cleanupStaleKeys()

	// 覆盖持久化加载失败分支（loadFromStore 返回 error）
	m3 := NewMetricsManagerWithPersistence(4, 0.5, stubStore{loadErr: errors.New("boom")}, "messages")
	t.Cleanup(m3.Stop)

	// 覆盖历史查询的参数校验 early return。
	if got := m3.GetHistoricalStats(baseURL, nil, 0, time.Minute); len(got) != 0 {
		t.Fatalf("GetHistoricalStats(invalid args) len=%d, want 0", len(got))
	}

	// CalculateTodayDuration：只要求非负。
	if d := CalculateTodayDuration(); d < 0 {
		t.Fatalf("CalculateTodayDuration() = %v, want >=0", d)
	}
}

func TestMetricsManager_DBBackedHistoryAndFallback(t *testing.T) {
	store := newTestSQLiteStore(t)
	store.batchSize = 1000

	baseURL := "https://example.com"
	apiKey := "sk-test-1234567890"
	metricsKey := generateMetricsKey(baseURL, apiKey)
	keyMask := "sk-****"
	now := time.Now().Truncate(time.Second)

	// 插入一条最近记录，确保 QueryRequestRecordBucketStats 有数据行可扫。
	store.AddRecord(PersistentRecord{
		MetricsKey:          metricsKey,
		BaseURL:             baseURL,
		KeyMask:             keyMask,
		Timestamp:           now.Add(-2 * time.Hour),
		Success:             true,
		InputTokens:         1,
		OutputTokens:        1,
		CacheCreationTokens: 1,
		CacheReadTokens:     1,
		CostCents:           1,
		APIType:             "messages",
	})
	store.FlushNow()

	// duration>7d 的 daily_stats 路径 + warning 分支：构造起始日“只有整日汇总，无明细命中 since..end”。
	duration := 8*24*time.Hour + 1*time.Hour
	since := time.Now().Add(-duration)
	loc := since.Location()
	sinceDayStart := time.Date(since.Year(), since.Month(), since.Day(), 0, 0, 0, 0, loc)
	store.AddRecord(PersistentRecord{
		MetricsKey: metricsKey,
		BaseURL:    baseURL,
		KeyMask:    keyMask,
		// 早于 since，确保 partialStart=0，但 dailyTotals 仍有整日数据。
		Timestamp: sinceDayStart.Add(1 * time.Hour),
		Success:   true,
		APIType:   "messages",
	})
	store.FlushNow()
	if err := store.AggregateDailyStats(sinceDayStart); err != nil {
		t.Fatalf("AggregateDailyStats(start day) err = %v", err)
	}

	m := NewMetricsManagerWithPersistence(4, 0.5, store, "messages")
	t.Cleanup(m.Stop)

	// <=24h：走内存路径
	dp, warn := m.GetHistoricalStatsWithWarning(baseURL, []string{apiKey}, 1*time.Hour, 15*time.Minute)
	if warn != "" {
		t.Fatalf("GetHistoricalStatsWithWarning(<=24h) warning=%q, want empty", warn)
	}
	if len(dp) == 0 {
		t.Fatalf("GetHistoricalStatsWithWarning(<=24h) datapoints empty")
	}

	// 24h<duration<=7d：走 request_records 路径
	dp, warn = m.GetHistoricalStatsWithWarning(baseURL, []string{apiKey}, 48*time.Hour, 1*time.Hour)
	if warn != "" {
		t.Fatalf("GetHistoricalStatsWithWarning(<=7d) warning=%q, want empty", warn)
	}
	if len(dp) == 0 {
		t.Fatalf("GetHistoricalStatsWithWarning(<=7d) datapoints empty")
	}

	// >7d：走 daily_stats 路径，并触发 warning
	dp, warn = m.GetHistoricalStatsWithWarning(baseURL, []string{apiKey}, duration, 1*time.Hour)
	if len(dp) == 0 {
		t.Fatalf("GetHistoricalStatsWithWarning(>7d) datapoints empty")
	}
	if warn == "" {
		t.Fatalf("GetHistoricalStatsWithWarning(>7d) warning empty, want non-empty (start-day fallback)")
	}

	// Key 级别历史查询（覆盖 in-memory / request_records / daily_stats）。
	kdp := m.GetKeyHistoricalStats(baseURL, apiKey, 1*time.Hour, 15*time.Minute)
	if len(kdp) == 0 {
		t.Fatalf("GetKeyHistoricalStats() empty, want non-empty")
	}
	kdp, warn = m.GetKeyHistoricalStatsWithWarning(baseURL, apiKey, 48*time.Hour, 1*time.Hour)
	if warn != "" || len(kdp) == 0 {
		t.Fatalf("GetKeyHistoricalStatsWithWarning(<=7d) warn=%q len=%d, want warn empty and len>0", warn, len(kdp))
	}
	kdp, warn = m.GetKeyHistoricalStatsWithWarning(baseURL, apiKey, duration, 1*time.Hour)
	if warn == "" || len(kdp) == 0 {
		t.Fatalf("GetKeyHistoricalStatsWithWarning(>7d) warn=%q len=%d, want warn non-empty and len>0", warn, len(kdp))
	}

	// 全局历史统计（覆盖 <=24h / request_records / daily_stats）。
	g := m.GetGlobalHistoricalStatsWithTokens(1*time.Hour, 15*time.Minute)
	if len(g.DataPoints) == 0 {
		t.Fatalf("GetGlobalHistoricalStatsWithTokens(<=24h) empty DataPoints")
	}
	g = m.GetGlobalHistoricalStatsWithTokens(48*time.Hour, 1*time.Hour)
	if g.Warning != "" || len(g.DataPoints) == 0 {
		t.Fatalf("GetGlobalHistoricalStatsWithTokens(<=7d) warning=%q len=%d, want warning empty and len>0", g.Warning, len(g.DataPoints))
	}
	g = m.GetGlobalHistoricalStatsWithTokens(duration, 1*time.Hour)
	if g.Warning == "" || len(g.DataPoints) == 0 {
		t.Fatalf("GetGlobalHistoricalStatsWithTokens(>7d) warning=%q len=%d, want warning non-empty and len>0", g.Warning, len(g.DataPoints))
	}

	// DB 查询失败 fallback：关闭 store 后触发 warning 分支（不要求日志静默）。
	store2, err := NewSQLiteStore(&SQLiteStoreConfig{
		DBPath:        t.TempDir() + "/metrics.db",
		RetentionDays: 7,
	})
	if err != nil {
		t.Fatalf("NewSQLiteStore(store2) err = %v", err)
	}
	store2.batchSize = 1000
	_ = store2.Close() // 先关闭，制造后续查询错误

	mFail := NewMetricsManagerWithPersistence(4, 0.5, store2, "messages")
	t.Cleanup(mFail.Stop)

	_, warn = mFail.GetHistoricalStatsWithWarning(baseURL, []string{apiKey}, 48*time.Hour, 1*time.Hour)
	if warn == "" {
		t.Fatalf("GetHistoricalStatsWithWarning(DB fail) warning empty, want non-empty")
	}
	_, warn = mFail.GetKeyHistoricalStatsWithWarning(baseURL, apiKey, 48*time.Hour, 1*time.Hour)
	if warn == "" {
		t.Fatalf("GetKeyHistoricalStatsWithWarning(DB fail) warning empty, want non-empty")
	}
	g = mFail.GetGlobalHistoricalStatsWithTokens(48*time.Hour, 1*time.Hour)
	if g.Warning == "" {
		t.Fatalf("GetGlobalHistoricalStatsWithTokens(DB fail) warning empty, want non-empty")
	}
}

func TestSQLiteStore_WriteBufferFailurePaths(t *testing.T) {
	store := newTestSQLiteStore(t)

	// AddRecord: 已关闭时直接忽略（覆盖 closed 分支）
	store.bufferMu.Lock()
	store.closed = true
	store.bufferMu.Unlock()
	store.AddRecord(PersistentRecord{MetricsKey: "k", BaseURL: "u", KeyMask: "m", Timestamp: time.Now(), APIType: "messages"})

	// requeueOrDropOnFailure: maxBuffer<=0 分支
	store.batchSize = 0
	store.requeueOrDropOnFailure([]PersistentRecord{
		{MetricsKey: "k", BaseURL: "u", KeyMask: "m", Timestamp: time.Now(), APIType: "messages"},
	})

	// requeueOrDropOnFailure: buffer 已满分支
	store.batchSize = 1
	maxBuffer := store.batchSize * maxBufferMultiplier
	store.bufferMu.Lock()
	store.closed = false
	store.writeBuffer = make([]PersistentRecord, maxBuffer)
	store.bufferMu.Unlock()

	store.requeueOrDropOnFailure([]PersistentRecord{
		{MetricsKey: "k", BaseURL: "u", KeyMask: "m", Timestamp: time.Now(), APIType: "messages"},
	})

	// requeueOrDropOnFailure: available 不足 + 截断保留新记录分支
	store.bufferMu.Lock()
	store.writeBuffer = make([]PersistentRecord, maxBuffer-1)
	store.bufferMu.Unlock()

	store.requeueOrDropOnFailure([]PersistentRecord{
		{MetricsKey: "k1", BaseURL: "u", KeyMask: "m", Timestamp: time.Now(), APIType: "messages"},
		{MetricsKey: "k2", BaseURL: "u", KeyMask: "m", Timestamp: time.Now(), APIType: "messages"},
	})

	// batchInsertRecords: 空输入快速返回
	if err := store.batchInsertRecords(nil); err != nil {
		t.Fatalf("batchInsertRecords(nil) err=%v, want nil", err)
	}
}

func TestMetricsManager_IsChannelHealthyWithKeys(t *testing.T) {
	m := NewMetricsManager()
	t.Cleanup(m.Stop)

	baseURL := "https://example.com"
	apiKey := "sk-1"

	if m.IsChannelHealthyWithKeys(baseURL, nil) {
		t.Fatalf("IsChannelHealthyWithKeys(empty)=true, want false")
	}
	if !m.IsChannelHealthyWithKeys(baseURL, []string{apiKey}) {
		t.Fatalf("IsChannelHealthyWithKeys(no data)=false, want true")
	}

	// NewMetricsManager 默认 windowSize=10，minRequests=max(3,10/2)=5。
	for i := 0; i < 4; i++ {
		m.RecordFailure(baseURL, apiKey)
	}
	if !m.IsChannelHealthyWithKeys(baseURL, []string{apiKey}) {
		t.Fatalf("IsChannelHealthyWithKeys(<minRequests)=false, want true")
	}

	m.RecordFailure(baseURL, apiKey)
	if m.IsChannelHealthyWithKeys(baseURL, []string{apiKey}) {
		t.Fatalf("IsChannelHealthyWithKeys(>=minRequests, all failure)=true, want false")
	}

	// 恢复：连续写入足够成功，窗口内失败率降到阈值以下。
	for i := 0; i < 6; i++ {
		m.RecordSuccess(baseURL, apiKey)
	}
	if !m.IsChannelHealthyWithKeys(baseURL, []string{apiKey}) {
		t.Fatalf("IsChannelHealthyWithKeys(after successes)=false, want true")
	}
}

func TestMetricsManager_AllKeysHistoryAndGlobalInMemoryData(t *testing.T) {
	m := NewMetricsManagerWithConfig(4, 0.5)
	t.Cleanup(m.Stop)

	baseURL := "https://example.com"
	key1 := "sk-1"
	key2 := "sk-2"

	usage := &types.Usage{
		InputTokens:              11,
		OutputTokens:             7,
		CacheCreationInputTokens: 3,
		CacheReadInputTokens:     2,
	}
	m.RecordSuccessWithUsage(baseURL, key1, usage, "m1", 13)
	m.RecordFailure(baseURL, key1)
	m.RecordSuccessWithUsage(baseURL, key2, usage, "m2", 17)

	// GetAllKeysHistoricalStats：此前未覆盖。
	dps := m.GetAllKeysHistoricalStats(1*time.Hour, 15*time.Minute)
	if len(dps) == 0 {
		t.Fatalf("GetAllKeysHistoricalStats() empty")
	}
	var sum int64
	for _, dp := range dps {
		sum += dp.RequestCount
	}
	if sum < 3 {
		t.Fatalf("GetAllKeysHistoricalStats() sum=%d, want >=3", sum)
	}

	// GetGlobalHistoricalStatsWithTokens：让 in-memory 分支吃到非空数据，覆盖 token/cost 汇总。
	g := m.GetGlobalHistoricalStatsWithTokens(1*time.Hour, 15*time.Minute)
	if len(g.DataPoints) == 0 {
		t.Fatalf("GetGlobalHistoricalStatsWithTokens() empty DataPoints")
	}
	if g.Summary.TotalRequests < 3 {
		t.Fatalf("GetGlobalHistoricalStatsWithTokens() TotalRequests=%d, want >=3", g.Summary.TotalRequests)
	}
	if g.Summary.TotalInputTokens < 22 || g.Summary.TotalOutputTokens < 14 {
		t.Fatalf("GetGlobalHistoricalStatsWithTokens() Summary=%+v, want tokens accumulated", g.Summary)
	}
	if g.Summary.TotalCostCents < 30 {
		t.Fatalf("GetGlobalHistoricalStatsWithTokens() TotalCostCents=%d, want >=30", g.Summary.TotalCostCents)
	}

	// Key/Channel 历史统计：让循环与分桶逻辑真正吃到数据（补齐低覆盖率函数）。
	kdps := m.GetKeyHistoricalStats(baseURL, key1, 1*time.Hour, 15*time.Minute)
	var keySum int64
	for _, dp := range kdps {
		keySum += dp.RequestCount
	}
	if keySum < 2 {
		t.Fatalf("GetKeyHistoricalStats() sum=%d, want >=2", keySum)
	}
	missing := m.GetKeyHistoricalStats(baseURL, "sk-missing", 1*time.Hour, 15*time.Minute)
	var missingSum int64
	for _, dp := range missing {
		missingSum += dp.RequestCount
	}
	if missingSum != 0 {
		t.Fatalf("GetKeyHistoricalStats(missing key) sum=%d, want 0", missingSum)
	}

	hist := m.GetHistoricalStats(baseURL, []string{key1, key2}, 1*time.Hour, 15*time.Minute)
	var histSum int64
	for _, dp := range hist {
		histSum += dp.RequestCount
	}
	if histSum < 3 {
		t.Fatalf("GetHistoricalStats() sum=%d, want >=3", histSum)
	}

	// 参数校验 early return：补齐未覆盖分支。
	g = m.GetGlobalHistoricalStatsWithTokens(0, 15*time.Minute)
	if len(g.DataPoints) != 0 {
		t.Fatalf("GetGlobalHistoricalStatsWithTokens(duration<=0) len=%d, want 0", len(g.DataPoints))
	}
	g = m.GetGlobalHistoricalStatsWithTokens(1*time.Hour, 0)
	if len(g.DataPoints) != 0 {
		t.Fatalf("GetGlobalHistoricalStatsWithTokens(interval<=0) len=%d, want 0", len(g.DataPoints))
	}

	if got := m.GetKeyHistoricalStats(baseURL, key1, 0, 15*time.Minute); len(got) != 0 {
		t.Fatalf("GetKeyHistoricalStats(duration<=0) len=%d, want 0", len(got))
	}
	if got := m.GetKeyHistoricalStats(baseURL, key1, 1*time.Hour, 0); len(got) != 0 {
		t.Fatalf("GetKeyHistoricalStats(interval<=0) len=%d, want 0", len(got))
	}
}

func TestSQLiteStore_AddRecordAsyncFlushAndDrop(t *testing.T) {
	// retentionDays 下限修正（<3 -> 3）
	store, err := NewSQLiteStore(&SQLiteStoreConfig{
		DBPath:        t.TempDir() + "/metrics.db",
		RetentionDays: 1,
	})
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if store.retentionDays != 3 {
		t.Fatalf("retentionDays = %d, want 3", store.retentionDays)
	}

	// AddRecord: shouldFlush=true 的异步路径
	store.batchSize = 1
	store.AddRecord(PersistentRecord{
		MetricsKey: "k",
		BaseURL:    "u",
		KeyMask:    "m",
		Timestamp:  time.Now(),
		APIType:    "messages",
	})
	store.flushWg.Wait()

	count, err := store.GetRecordCount()
	if err != nil {
		t.Fatalf("GetRecordCount() err=%v", err)
	}
	if count != 1 {
		t.Fatalf("GetRecordCount()=%d, want 1", count)
	}

	// AddRecord: buffer 满时丢弃分支
	maxBuffer := store.batchSize * maxBufferMultiplier
	store.bufferMu.Lock()
	store.writeBuffer = make([]PersistentRecord, maxBuffer)
	store.droppedRecords = 0
	store.bufferMu.Unlock()

	store.AddRecord(PersistentRecord{
		MetricsKey: "k2",
		BaseURL:    "u",
		KeyMask:    "m",
		Timestamp:  time.Now(),
		APIType:    "messages",
	})

	store.bufferMu.Lock()
	dropped := store.droppedRecords
	store.bufferMu.Unlock()
	if dropped == 0 {
		t.Fatalf("droppedRecords=0, want >0")
	}
}

func TestSQLiteStore_QueryRequestLogs_NormalizationAndCleanup(t *testing.T) {
	store := newTestSQLiteStore(t)

	// 参数校验：apiType 不能为空
	if _, _, err := store.QueryRequestLogs("", 10, 0); err == nil {
		t.Fatalf("QueryRequestLogs(empty apiType) err=nil, want error")
	}

	// 插入一条日志：覆盖 limit/offset 归一化分支
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.AddRequestLog(RequestLogRecord{
		RequestID:    "req-1",
		ChannelIndex: 1,
		ChannelName:  "ch-1",
		KeyMask:      "sk-****",
		Timestamp:    now,
		DurationMs:   1,
		StatusCode:   200,
		Success:      true,
		APIType:      "messages",
	}); err != nil {
		t.Fatalf("AddRequestLog() err = %v", err)
	}

	logs, total, err := store.QueryRequestLogs("messages", 0, -1)
	if err != nil {
		t.Fatalf("QueryRequestLogs(limit<=0,offset<0) err=%v", err)
	}
	if total != 1 || len(logs) != 1 {
		t.Fatalf("QueryRequestLogs() total=%d len=%d, want (1,1)", total, len(logs))
	}

	logs, total, err = store.QueryRequestLogs("messages", 500, 0)
	if err != nil {
		t.Fatalf("QueryRequestLogs(limit>200) err=%v", err)
	}
	if total != 1 || len(logs) != 1 {
		t.Fatalf("QueryRequestLogs(limit>200) total=%d len=%d, want (1,1)", total, len(logs))
	}

	// doCleanup：触发 deleted>0 和 logDeleted>0 的分支。
	store.retentionDays = 1
	store.batchSize = 1000
	store.AddRecord(PersistentRecord{
		MetricsKey: "k",
		BaseURL:    "u",
		KeyMask:    "m",
		Timestamp:  time.Now().AddDate(0, 0, -2),
		Success:    true,
		APIType:    "messages",
	})
	store.FlushNow()

	if err := store.AddRequestLog(RequestLogRecord{
		RequestID:    "req-old",
		ChannelIndex: 1,
		ChannelName:  "ch-1",
		KeyMask:      "sk-****",
		Timestamp:    time.Now().Add(-25 * time.Hour),
		DurationMs:   1,
		StatusCode:   200,
		Success:      true,
		APIType:      "messages",
	}); err != nil {
		t.Fatalf("AddRequestLog(old) err=%v", err)
	}

	beforeRecords, err := store.GetRecordCount()
	if err != nil {
		t.Fatalf("GetRecordCount(before cleanup) err=%v", err)
	}
	store.doCleanup()
	afterRecords, err := store.GetRecordCount()
	if err != nil {
		t.Fatalf("GetRecordCount(after cleanup) err=%v", err)
	}
	if afterRecords >= beforeRecords {
		t.Fatalf("cleanup did not delete records: before=%d after=%d", beforeRecords, afterRecords)
	}

	var logCount int64
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM request_logs`).Scan(&logCount); err != nil {
		t.Fatalf("count request_logs err=%v", err)
	}
	if logCount != 1 {
		t.Fatalf("request_logs count=%d, want 1 after cleanup", logCount)
	}
}

func TestSQLiteStore_BatchInsertWithRetry_Failure(t *testing.T) {
	store := newTestSQLiteStore(t)

	// 关闭底层 DB，强制 batchInsertWithRetry 走重试 + backoff（覆盖低覆盖率函数）。
	_ = store.db.Close()

	start := time.Now()
	err := store.batchInsertWithRetry([]PersistentRecord{
		{MetricsKey: "k", BaseURL: "u", KeyMask: "m", Timestamp: time.Now(), APIType: "messages"},
	})
	if err == nil {
		t.Fatalf("batchInsertWithRetry(closed db) err=nil, want error")
	}
	if time.Since(start) < 250*time.Millisecond {
		t.Fatalf("batchInsertWithRetry() returned too fast, want retries/backoff to have executed")
	}
}
