package metrics

import (
	"sync"
	"time"
)

// ChannelErrorEntry 单条渠道错误记录
type ChannelErrorEntry struct {
	Timestamp  time.Time `json:"timestamp"`
	StatusCode int       `json:"statusCode"`
	KeyMask    string    `json:"keyMask"`
	BaseURL    string    `json:"baseUrl"`
	Message    string    `json:"message"`    // 错误消息摘要（截断到 500 字符）
	APIType    string    `json:"apiType"`    // messages / responses / gemini
	IsQuota    bool      `json:"isQuota"`    // 是否为配额相关错误
}

// ChannelErrorLog 渠道错误日志（线程安全环形缓冲区）
// 每个渠道（按 apiType + channelIndex 区分）保留最近 N 条错误记录
type ChannelErrorLog struct {
	mu       sync.RWMutex
	entries  map[string][]ChannelErrorEntry // key: "apiType:channelIndex"
	capacity int                            // 每个渠道最大保留条数
}

// NewChannelErrorLog 创建渠道错误日志
func NewChannelErrorLog(capacity int) *ChannelErrorLog {
	if capacity <= 0 {
		capacity = 20
	}
	return &ChannelErrorLog{
		entries:  make(map[string][]ChannelErrorEntry),
		capacity: capacity,
	}
}

// channelKey 生成渠道的存储 key
func channelKey(apiType string, channelIndex int) string {
	buf := make([]byte, 0, len(apiType)+12)
	buf = append(buf, apiType...)
	buf = append(buf, ':')
	buf = appendInt(buf, channelIndex)
	return string(buf)
}

func appendInt(buf []byte, n int) []byte {
	if n < 0 {
		buf = append(buf, '-')
		n = -n
	}
	if n == 0 {
		return append(buf, '0')
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return append(buf, digits[i:]...)
}

// AddError 添加一条错误记录
func (l *ChannelErrorLog) AddError(apiType string, channelIndex int, entry ChannelErrorEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()

	key := channelKey(apiType, channelIndex)
	entries := l.entries[key]
	entries = append(entries, entry)

	// 保持环形缓冲区大小
	if len(entries) > l.capacity {
		entries = entries[len(entries)-l.capacity:]
	}
	l.entries[key] = entries
}

// GetErrors 获取渠道最近的错误记录（从新到旧排序）
func (l *ChannelErrorLog) GetErrors(apiType string, channelIndex int) []ChannelErrorEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	key := channelKey(apiType, channelIndex)
	entries := l.entries[key]
	if len(entries) == 0 {
		return []ChannelErrorEntry{}
	}

	// 返回副本，从新到旧排序
	result := make([]ChannelErrorEntry, len(entries))
	for i, j := 0, len(entries)-1; j >= 0; i, j = i+1, j-1 {
		result[i] = entries[j]
	}
	return result
}

// DeleteChannel 删除渠道的错误日志
func (l *ChannelErrorLog) DeleteChannel(apiType string, channelIndex int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	key := channelKey(apiType, channelIndex)
	delete(l.entries, key)
}

// TruncateMessage 截断错误消息到指定长度
func TruncateMessage(msg string, maxLen int) string {
	if len(msg) <= maxLen {
		return msg
	}
	return msg[:maxLen] + "..."
}
