package common

import (
	"log"
	"time"

	"github.com/BenedictKing/claude-proxy/internal/metrics"
	"github.com/BenedictKing/claude-proxy/internal/monitor"
	"github.com/BenedictKing/claude-proxy/internal/types"
	"github.com/BenedictKing/claude-proxy/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RequestMonitorContext tracks per-request metadata for live monitoring and request logging.
// NOT thread-safe: assumes synchronous execution within a single request handler goroutine.
// Closures passed to TryUpstreamWithAllKeys may mutate fields (e.g. APIKey, ChannelIndex),
// which is safe only because those callbacks execute sequentially, not concurrently.
type RequestMonitorContext struct {
	RequestID    string
	StartTime    time.Time
	APIType      string // "messages", "responses", "gemini"
	Model        string
	IsStreaming  bool
	ChannelIndex int
	ChannelName  string
	APIKey       string
	Usage        *types.Usage
	Success      bool
	ErrorMsg     string

	liveRequestManager *monitor.LiveRequestManager
	sqliteStore        *metrics.SQLiteStore
}

// NewRequestMonitorContext creates a new monitoring context.
func NewRequestMonitorContext(apiType string, lrm *monitor.LiveRequestManager, store *metrics.SQLiteStore) *RequestMonitorContext {
	return &RequestMonitorContext{
		RequestID:          uuid.New().String(),
		StartTime:          time.Now(),
		APIType:            apiType,
		liveRequestManager: lrm,
		sqliteStore:        store,
	}
}

// UpdateLive pushes current metadata to the live request manager.
func (r *RequestMonitorContext) UpdateLive() {
	if r == nil || r.liveRequestManager == nil {
		return
	}
	r.liveRequestManager.StartRequest(&monitor.LiveRequest{
		RequestID:    r.RequestID,
		ChannelIndex: r.ChannelIndex,
		ChannelName:  r.ChannelName,
		KeyMask:      utils.MaskAPIKey(r.APIKey),
		Model:        r.Model,
		StartTime:    r.StartTime,
		APIType:      r.APIType,
		IsStreaming:  r.IsStreaming,
	})
}

// EndLive removes the request from the live request manager.
func (r *RequestMonitorContext) EndLive() {
	if r == nil || r.liveRequestManager == nil {
		return
	}
	r.liveRequestManager.EndRequest(r.RequestID)
}

// Finalize writes the completed request log to the SQLite store.
// Should be called via defer after NewRequestMonitorContext.
func (r *RequestMonitorContext) Finalize(c *gin.Context) {
	if r == nil || r.sqliteStore == nil {
		return
	}

	statusCode := c.Writer.Status() // Returns 0 if no response was written (e.g. panic before response)
	durationMs := time.Since(r.StartTime).Milliseconds()

	var inputTokens, outputTokens, cacheCreation, cacheRead int64
	if r.Usage != nil {
		inputTokens = int64(r.Usage.InputTokens)
		outputTokens = int64(r.Usage.OutputTokens)
		cacheCreation = int64(r.Usage.CacheCreationInputTokens)
		cacheRead = int64(r.Usage.CacheReadInputTokens)
	}

	record := metrics.RequestLogRecord{
		RequestID:           r.RequestID,
		ChannelIndex:        r.ChannelIndex,
		ChannelName:         r.ChannelName,
		KeyMask:             utils.MaskAPIKey(r.APIKey),
		Timestamp:           r.StartTime,
		DurationMs:          durationMs,
		StatusCode:          statusCode,
		Success:             r.Success,
		Model:               r.Model,
		InputTokens:         inputTokens,
		OutputTokens:        outputTokens,
		CacheCreationTokens: cacheCreation,
		CacheReadTokens:     cacheRead,
		ErrorMessage:        truncateErrorMsg(r.ErrorMsg),
		APIType:             r.APIType,
	}

	if err := r.sqliteStore.AddRequestLog(record); err != nil {
		log.Printf("[RequestMonitor] 警告: 写入请求日志失败: %v", err)
	}
}

// truncateErrorMsg limits error message length to avoid bloating the DB.
func truncateErrorMsg(msg string) string {
	const maxLen = 1024
	if len(msg) > maxLen {
		return msg[:maxLen]
	}
	return msg
}
