package handlers

import (
	"net/http"
	"time"

	"github.com/BenedictKing/claude-proxy/internal/cache"
	"github.com/BenedictKing/claude-proxy/internal/metrics"
	"github.com/gin-gonic/gin"
)

type CacheStats struct {
	ReadHit     int64   `json:"readHit"`
	ReadMiss    int64   `json:"readMiss"`
	WriteSet    int64   `json:"writeSet"`
	WriteUpdate int64   `json:"writeUpdate"`
	Entries     int64   `json:"entries"`
	Capacity    int64   `json:"capacity"`
	HitRate     float64 `json:"hitRate"`
}

type CacheStatsResponse struct {
	Timestamp time.Time  `json:"timestamp"`
	Models    CacheStats `json:"models"`
}

func GetCacheStats(modelsCache *cache.HTTPResponseCache, modelsMetrics *metrics.CacheMetrics) gin.HandlerFunc {
	return func(c *gin.Context) {
		if modelsCache != nil {
			modelsCache.Len()
		}

		var snap metrics.CacheMetricsSnapshot
		if modelsMetrics != nil {
			snap = modelsMetrics.Snapshot()
		}

		reads := snap.ReadHit + snap.ReadMiss
		hitRate := 0.0
		if reads > 0 {
			hitRate = float64(snap.ReadHit) / float64(reads)
		}

		c.JSON(http.StatusOK, CacheStatsResponse{
			Timestamp: time.Now(),
			Models: CacheStats{
				ReadHit:     snap.ReadHit,
				ReadMiss:    snap.ReadMiss,
				WriteSet:    snap.WriteSet,
				WriteUpdate: snap.WriteUpdate,
				Entries:     snap.Entries,
				Capacity:    snap.Capacity,
				HitRate:     hitRate,
			},
		})
	}
}
