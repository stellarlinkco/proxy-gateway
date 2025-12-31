// Package usage 提供使用量记录存储
package usage

import (
	"sync"
	"time"
)

// Record 使用量记录
type Record struct {
	ID           string    `json:"id"`
	RequestID    string    `json:"request_id"`
	APIKey       string    `json:"api_key"`
	Model        string    `json:"model"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
	CostCents    int64     `json:"cost_cents"`
	CreatedAt    time.Time `json:"created_at"`
}

// Store 使用量存储
type Store struct {
	records []Record
	mu      sync.RWMutex
	maxSize int
}

// NewStore 创建使用量存储
func NewStore(maxSize int) *Store {
	if maxSize <= 0 {
		maxSize = 10000
	}
	return &Store{
		records: make([]Record, 0, maxSize),
		maxSize: maxSize,
	}
}

// Add 添加使用量记录
func (s *Store) Add(record Record) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now()
	}

	// 超过最大容量时移除最旧的记录
	if len(s.records) >= s.maxSize {
		s.records = s.records[1:]
	}
	s.records = append(s.records, record)
}

// GetByAPIKey 获取指定 API Key 的使用量记录
func (s *Store) GetByAPIKey(apiKey string, limit int) []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []Record
	for i := len(s.records) - 1; i >= 0 && len(result) < limit; i-- {
		if s.records[i].APIKey == apiKey {
			result = append(result, s.records[i])
		}
	}
	return result
}

// GetRecent 获取最近的使用量记录
func (s *Store) GetRecent(limit int) []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()

	start := len(s.records) - limit
	if start < 0 {
		start = 0
	}
	result := make([]Record, len(s.records)-start)
	copy(result, s.records[start:])
	// 倒序返回（最新的在前）
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result
}

// SumByAPIKey 统计指定 API Key 的总使用量
func (s *Store) SumByAPIKey(apiKey string, since time.Time) (inputTokens, outputTokens int, costCents int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, r := range s.records {
		if r.APIKey == apiKey && r.CreatedAt.After(since) {
			inputTokens += r.InputTokens
			outputTokens += r.OutputTokens
			costCents += r.CostCents
		}
	}
	return
}

// Count 返回记录总数
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.records)
}
