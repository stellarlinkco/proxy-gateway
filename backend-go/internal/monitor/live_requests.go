package monitor

import (
	"sort"
	"sync"
	"time"
)

// LiveRequest 正在进行的请求
type LiveRequest struct {
	RequestID    string    `json:"requestId"`
	ChannelIndex int       `json:"channelIndex"`
	ChannelName  string    `json:"channelName"`
	KeyMask      string    `json:"keyMask"`
	Model        string    `json:"model"`
	StartTime    time.Time `json:"startTime"`
	APIType      string    `json:"apiType"` // messages, responses, gemini
	IsStreaming  bool      `json:"isStreaming"`
}

// LiveRequestsResponse API 响应
type LiveRequestsResponse struct {
	Requests []*LiveRequest `json:"requests"`
	Count    int            `json:"count"`
}

// LiveRequestManager 管理正在进行的请求
type LiveRequestManager struct {
	mu       sync.RWMutex
	requests map[string]*LiveRequest // key: requestID
	maxSize  int
}

// NewLiveRequestManager 创建管理器
func NewLiveRequestManager(maxSize int) *LiveRequestManager {
	if maxSize <= 0 {
		maxSize = 50
	}
	return &LiveRequestManager{
		requests: make(map[string]*LiveRequest),
		maxSize:  maxSize,
	}
}

// StartRequest 记录请求开始
func (m *LiveRequestManager) StartRequest(req *LiveRequest) {
	if m == nil || req == nil || req.RequestID == "" {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// 如果超过最大数量，删除最老的请求
	if len(m.requests) >= m.maxSize {
		var oldestID string
		var oldestStart time.Time
		for id, r := range m.requests {
			if r == nil {
				continue
			}
			if oldestID == "" || r.StartTime.Before(oldestStart) {
				oldestID = id
				oldestStart = r.StartTime
			}
		}
		if oldestID != "" {
			delete(m.requests, oldestID)
		}
	}

	copied := *req
	m.requests[req.RequestID] = &copied
}

// EndRequest 请求结束，从内存中移除
func (m *LiveRequestManager) EndRequest(requestID string) {
	if m == nil || requestID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.requests, requestID)
}

// GetAllRequests 获取所有正在进行的请求（按开始时间倒序）
func (m *LiveRequestManager) GetAllRequests() []*LiveRequest {
	if m == nil {
		return nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*LiveRequest, 0, len(m.requests))
	for _, req := range m.requests {
		if req == nil {
			continue
		}
		copied := *req
		result = append(result, &copied)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].StartTime.After(result[j].StartTime)
	})

	return result
}

// GetRequestsByAPIType 按 API 类型获取请求（按开始时间倒序）
func (m *LiveRequestManager) GetRequestsByAPIType(apiType string) []*LiveRequest {
	if m == nil || apiType == "" {
		return nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*LiveRequest, 0)
	for _, req := range m.requests {
		if req == nil || req.APIType != apiType {
			continue
		}
		copied := *req
		result = append(result, &copied)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].StartTime.After(result[j].StartTime)
	})

	return result
}

// Count 获取当前请求数量
func (m *LiveRequestManager) Count() int {
	if m == nil {
		return 0
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.requests)
}
