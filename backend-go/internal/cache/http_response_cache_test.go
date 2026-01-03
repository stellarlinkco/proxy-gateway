package cache

import (
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/BenedictKing/claude-proxy/internal/metrics"
)

func TestHTTPResponseCache_NilReceiver(t *testing.T) {
	var c *HTTPResponseCache
	c.Set("k", HTTPResponse{StatusCode: 200, Body: []byte("v")})
	if _, ok := c.Get("k"); ok {
		t.Fatalf("nil cache Get() ok=true, want false")
	}
	if got := c.Len(); got != 0 {
		t.Fatalf("nil cache Len()=%d, want 0", got)
	}
	if got := c.Cap(); got != 0 {
		t.Fatalf("nil cache Cap()=%d, want 0", got)
	}
}

func TestHTTPResponseCache_DisabledCapacity(t *testing.T) {
	var m metrics.CacheMetrics
	c := NewHTTPResponseCache(0, time.Minute, &m)

	c.Set("k", HTTPResponse{StatusCode: 200, Body: []byte("v")})
	if _, ok := c.Get("k"); ok {
		t.Fatalf("Get() ok=true with capacity=0, want false")
	}
	if got := c.Len(); got != 0 {
		t.Fatalf("Len()=%d with capacity=0, want 0", got)
	}

	s := m.Snapshot()
	if s.Capacity != 0 {
		t.Fatalf("metrics capacity=%d, want 0", s.Capacity)
	}
	if s.WriteSet != 0 || s.WriteUpdate != 0 {
		t.Fatalf("metrics writes=%+v, want all zeros", s)
	}
	if s.ReadMiss != 1 {
		t.Fatalf("metrics ReadMiss=%d, want 1", s.ReadMiss)
	}
}

func TestHTTPResponseCache_GetSetAndMetrics(t *testing.T) {
	var m metrics.CacheMetrics
	c := NewHTTPResponseCache(2, time.Minute, &m)

	if got := c.Cap(); got != 2 {
		t.Fatalf("Cap()=%d, want 2", got)
	}
	if got := c.Len(); got != 0 {
		t.Fatalf("Len()=%d, want 0", got)
	}

	if _, ok := c.Get("k1"); ok {
		t.Fatalf("Get() ok=true on empty cache, want false")
	}

	resp := HTTPResponse{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       []byte("ok"),
	}
	c.Set("k1", resp)

	s := m.Snapshot()
	if s.WriteSet != 1 || s.WriteUpdate != 0 {
		t.Fatalf("after Set metrics=%+v, want WriteSet=1 WriteUpdate=0", s)
	}
	if s.Entries != 1 || s.Capacity != 2 {
		t.Fatalf("after Set metrics=%+v, want Entries=1 Capacity=2", s)
	}

	got, ok := c.Get("k1")
	if !ok {
		t.Fatalf("Get() ok=false, want true")
	}
	if got.StatusCode != 200 || string(got.Body) != "ok" {
		t.Fatalf("Get()=%+v, want status=200 body=ok", got)
	}
	if got.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("Get() header Content-Type=%q, want application/json", got.Header.Get("Content-Type"))
	}

	s = m.Snapshot()
	if s.ReadHit != 1 || s.ReadMiss != 1 {
		t.Fatalf("after Get metrics=%+v, want ReadHit=1 ReadMiss=1", s)
	}

	// 返回值应为拷贝，外部修改不能污染缓存。
	got.Body[0] = 'X'
	got.Header.Set("X-Test", "1")
	got2, ok := c.Get("k1")
	if !ok {
		t.Fatalf("2nd Get() ok=false, want true")
	}
	if string(got2.Body) != "ok" {
		t.Fatalf("2nd Get() body=%q, want ok", string(got2.Body))
	}
	if got2.Header.Get("X-Test") != "" {
		t.Fatalf("2nd Get() header X-Test=%q, want empty", got2.Header.Get("X-Test"))
	}
}

func TestHTTPResponseCache_SetUpdate(t *testing.T) {
	var m metrics.CacheMetrics
	c := NewHTTPResponseCache(2, time.Minute, &m)

	c.Set("k", HTTPResponse{StatusCode: 200, Body: []byte("v1")})
	c.Set("k", HTTPResponse{StatusCode: 201, Body: []byte("v2")})

	s := m.Snapshot()
	if s.WriteSet != 1 || s.WriteUpdate != 1 {
		t.Fatalf("metrics=%+v, want WriteSet=1 WriteUpdate=1", s)
	}
	if s.Entries != 1 {
		t.Fatalf("metrics Entries=%d, want 1", s.Entries)
	}

	got, ok := c.Get("k")
	if !ok {
		t.Fatalf("Get() ok=false, want true")
	}
	if got.StatusCode != 201 || string(got.Body) != "v2" {
		t.Fatalf("Get()=%+v, want status=201 body=v2", got)
	}
}

func TestHTTPResponseCache_TTLExpiration(t *testing.T) {
	var m metrics.CacheMetrics
	c := NewHTTPResponseCache(10, 10*time.Second, &m)

	now := time.Unix(0, 0).UTC()
	c.now = func() time.Time { return now }

	c.Set("k", HTTPResponse{StatusCode: 200, Body: []byte("v")})
	if _, ok := c.Get("k"); !ok {
		t.Fatalf("Get() ok=false before expiry, want true")
	}

	now = now.Add(11 * time.Second)
	if _, ok := c.Get("k"); ok {
		t.Fatalf("Get() ok=true after expiry, want false")
	}
	if got := c.Len(); got != 0 {
		t.Fatalf("Len()=%d after expiry, want 0", got)
	}

	s := m.Snapshot()
	if s.ReadHit != 1 || s.ReadMiss != 1 {
		t.Fatalf("metrics=%+v, want ReadHit=1 ReadMiss=1", s)
	}
	if s.Entries != 0 {
		t.Fatalf("metrics Entries=%d, want 0", s.Entries)
	}
}

func TestHTTPResponseCache_NoTTL(t *testing.T) {
	c := NewHTTPResponseCache(1, 0, nil)

	now := time.Unix(0, 0).UTC()
	c.now = func() time.Time { return now }

	c.Set("k", HTTPResponse{StatusCode: 200, Body: []byte("v")})
	now = now.Add(24 * time.Hour)

	if _, ok := c.Get("k"); !ok {
		t.Fatalf("Get() ok=false with ttl=0, want true")
	}
}

func TestHTTPResponseCache_LRUEviction(t *testing.T) {
	c := NewHTTPResponseCache(2, time.Minute, nil)

	c.Set("a", HTTPResponse{StatusCode: 200, Body: []byte("a")})
	c.Set("b", HTTPResponse{StatusCode: 200, Body: []byte("b")})

	if _, ok := c.Get("a"); !ok {
		t.Fatalf("Get(a) ok=false, want true")
	}

	c.Set("c", HTTPResponse{StatusCode: 200, Body: []byte("c")})

	if _, ok := c.Get("b"); ok {
		t.Fatalf("Get(b) ok=true after eviction, want false")
	}
	if _, ok := c.Get("a"); !ok {
		t.Fatalf("Get(a) ok=false after eviction, want true")
	}
	if _, ok := c.Get("c"); !ok {
		t.Fatalf("Get(c) ok=false after eviction, want true")
	}
	if got := c.Len(); got != 2 {
		t.Fatalf("Len()=%d after eviction, want 2", got)
	}
}

func TestHTTPResponseCache_SetPurgesExpiredBeforeEvict(t *testing.T) {
	c := NewHTTPResponseCache(2, 5*time.Second, nil)

	now := time.Unix(0, 0).UTC()
	c.now = func() time.Time { return now }

	c.Set("old", HTTPResponse{StatusCode: 200, Body: []byte("old")}) // exp at 5s
	now = now.Add(3 * time.Second)
	c.Set("live", HTTPResponse{StatusCode: 200, Body: []byte("live")}) // exp at 8s
	now = now.Add(1 * time.Second)

	if _, ok := c.Get("old"); !ok {
		t.Fatalf("Get(old) ok=false before expiry, want true")
	}

	now = now.Add(2 * time.Second) // now=6s: old expired, live not
	c.Set("new", HTTPResponse{StatusCode: 200, Body: []byte("new")})

	if _, ok := c.Get("old"); ok {
		t.Fatalf("Get(old) ok=true after purge, want false")
	}
	if _, ok := c.Get("live"); !ok {
		t.Fatalf("Get(live) ok=false after purge, want true")
	}
	if _, ok := c.Get("new"); !ok {
		t.Fatalf("Get(new) ok=false after purge, want true")
	}
	if got := c.Len(); got != 2 {
		t.Fatalf("Len()=%d after purge, want 2", got)
	}
}

func TestHTTPResponseCache_ConcurrentAccess_NoRace(t *testing.T) {
	var m metrics.CacheMetrics
	c := NewHTTPResponseCache(50, time.Second, &m)

	keys := make([]string, 16)
	for i := range keys {
		keys[i] = "k" + strconv.Itoa(i)
	}

	const goroutines = 32
	const loops = 2000

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			for j := 0; j < loops; j++ {
				key := keys[(i+j)%len(keys)]
				if j%3 == 0 {
					c.Set(key, HTTPResponse{StatusCode: 200, Body: []byte(key)})
				} else {
					c.Get(key)
				}
			}
		}()
	}
	wg.Wait()

	if got := c.Len(); got > c.Cap() {
		t.Fatalf("Len()=%d > Cap()=%d", got, c.Cap())
	}

	s := m.Snapshot()
	if s.Capacity != 50 {
		t.Fatalf("metrics Capacity=%d, want 50", s.Capacity)
	}
	if s.ReadHit+s.ReadMiss == 0 {
		t.Fatalf("metrics reads all zero, want non-zero")
	}
}
