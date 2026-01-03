package metrics

import "sync/atomic"

// CacheMetrics 记录 HTTP 响应缓存的核心指标。
//
// 设计目标：零依赖、零分配、并发安全。只提供原始计数/容量信息，派生指标由调用方计算。
type CacheMetrics struct {
	readHit     atomic.Int64
	readMiss    atomic.Int64
	writeSet    atomic.Int64
	writeUpdate atomic.Int64
	entries     atomic.Int64
	capacity    atomic.Int64
}

type CacheMetricsSnapshot struct {
	ReadHit     int64 `json:"readHit"`
	ReadMiss    int64 `json:"readMiss"`
	WriteSet    int64 `json:"writeSet"`
	WriteUpdate int64 `json:"writeUpdate"`
	Entries     int64 `json:"entries"`
	Capacity    int64 `json:"capacity"`
}

func (m *CacheMetrics) IncReadHit() {
	m.readHit.Add(1)
}

func (m *CacheMetrics) IncReadMiss() {
	m.readMiss.Add(1)
}

func (m *CacheMetrics) IncWriteSet() {
	m.writeSet.Add(1)
}

func (m *CacheMetrics) IncWriteUpdate() {
	m.writeUpdate.Add(1)
}

func (m *CacheMetrics) SetEntries(n int64) {
	m.entries.Store(n)
}

func (m *CacheMetrics) SetCapacity(n int64) {
	m.capacity.Store(n)
}

func (m *CacheMetrics) Snapshot() CacheMetricsSnapshot {
	return CacheMetricsSnapshot{
		ReadHit:     m.readHit.Load(),
		ReadMiss:    m.readMiss.Load(),
		WriteSet:    m.writeSet.Load(),
		WriteUpdate: m.writeUpdate.Load(),
		Entries:     m.entries.Load(),
		Capacity:    m.capacity.Load(),
	}
}
