package cache

import (
	"container/list"
	"net/http"
	"sync"
	"time"

	"github.com/BenedictKing/claude-proxy/internal/metrics"
)

// HTTPResponse 表示可缓存的 HTTP 响应数据。
// 注意：Get/Set 都会复制 Header/Body，避免外部修改污染缓存。
type HTTPResponse struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// HTTPResponseCache 是一个带 TTL + 容量限制的并发安全 LRU 缓存。
type HTTPResponseCache struct {
	mu       sync.Mutex
	ttl      time.Duration
	capacity int
	metrics  *metrics.CacheMetrics
	now      func() time.Time

	lru   *list.List
	items map[string]*list.Element
}

type httpResponseCacheEntry struct {
	key      string
	resp     HTTPResponse
	expireAt time.Time
}

func NewHTTPResponseCache(capacity int, ttl time.Duration, m *metrics.CacheMetrics) *HTTPResponseCache {
	if capacity < 0 {
		capacity = 0
	}

	c := &HTTPResponseCache{
		ttl:      ttl,
		capacity: capacity,
		metrics:  m,
		now:      time.Now,
		lru:      list.New(),
		items:    make(map[string]*list.Element, capacity),
	}
	if c.metrics != nil {
		c.metrics.SetCapacity(int64(capacity))
		c.metrics.SetEntries(0)
	}
	return c
}

func (c *HTTPResponseCache) Get(key string) (HTTPResponse, bool) {
	if c == nil {
		return HTTPResponse{}, false
	}
	if key == "" || c.capacity <= 0 {
		if c.metrics != nil {
			c.metrics.IncReadMiss()
		}
		return HTTPResponse{}, false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[key]
	if !ok {
		if c.metrics != nil {
			c.metrics.IncReadMiss()
		}
		return HTTPResponse{}, false
	}

	ent := elem.Value.(*httpResponseCacheEntry)
	now := c.now()
	if c.isExpiredLocked(ent, now) {
		c.removeKeyLocked(key, elem)
		c.updateMetricsLocked()
		if c.metrics != nil {
			c.metrics.IncReadMiss()
		}
		return HTTPResponse{}, false
	}

	c.lru.MoveToFront(elem)
	if c.metrics != nil {
		c.metrics.IncReadHit()
	}
	return cloneHTTPResponse(ent.resp), true
}

func (c *HTTPResponseCache) Set(key string, resp HTTPResponse) {
	if c == nil {
		return
	}
	if key == "" || c.capacity <= 0 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	c.purgeExpiredLocked(now)

	if elem, ok := c.items[key]; ok {
		ent := elem.Value.(*httpResponseCacheEntry)
		ent.resp = cloneHTTPResponse(resp)
		ent.expireAt = c.expireAtLocked(now)
		c.lru.MoveToFront(elem)
		if c.metrics != nil {
			c.metrics.IncWriteUpdate()
		}
	} else {
		ent := &httpResponseCacheEntry{
			key:      key,
			resp:     cloneHTTPResponse(resp),
			expireAt: c.expireAtLocked(now),
		}
		c.items[key] = c.lru.PushFront(ent)
		if c.metrics != nil {
			c.metrics.IncWriteSet()
		}
	}

	for c.lru.Len() > c.capacity {
		c.evictBackLocked()
	}
	c.updateMetricsLocked()
}

func (c *HTTPResponseCache) Len() int {
	if c == nil {
		return 0
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.purgeExpiredLocked(c.now())
	c.updateMetricsLocked()
	return len(c.items)
}

func (c *HTTPResponseCache) Cap() int {
	if c == nil {
		return 0
	}
	return c.capacity
}

func (c *HTTPResponseCache) expireAtLocked(now time.Time) time.Time {
	if c.ttl <= 0 {
		return time.Time{}
	}
	return now.Add(c.ttl)
}

func (c *HTTPResponseCache) isExpiredLocked(ent *httpResponseCacheEntry, now time.Time) bool {
	if ent.expireAt.IsZero() {
		return false
	}
	return !now.Before(ent.expireAt)
}

func (c *HTTPResponseCache) purgeExpiredLocked(now time.Time) {
	if c.ttl <= 0 {
		return
	}
	for key, elem := range c.items {
		ent := elem.Value.(*httpResponseCacheEntry)
		if c.isExpiredLocked(ent, now) {
			c.removeKeyLocked(key, elem)
		}
	}
}

func (c *HTTPResponseCache) evictBackLocked() {
	elem := c.lru.Back()
	if elem == nil {
		return
	}
	ent := elem.Value.(*httpResponseCacheEntry)
	c.removeKeyLocked(ent.key, elem)
}

func (c *HTTPResponseCache) removeKeyLocked(key string, elem *list.Element) {
	if elem != nil {
		c.lru.Remove(elem)
	}
	delete(c.items, key)
}

func (c *HTTPResponseCache) updateMetricsLocked() {
	if c.metrics == nil {
		return
	}
	c.metrics.SetEntries(int64(len(c.items)))
	c.metrics.SetCapacity(int64(c.capacity))
}

func cloneHTTPResponse(resp HTTPResponse) HTTPResponse {
	return HTTPResponse{
		StatusCode: resp.StatusCode,
		Header:     resp.Header.Clone(),
		Body:       cloneBytes(resp.Body),
	}
}

func cloneBytes(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}
