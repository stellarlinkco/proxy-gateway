package usage

import (
	"testing"
	"time"
)

func TestStore_Add(t *testing.T) {
	store := NewStore(3)

	store.Add(Record{ID: "1", APIKey: "key1", InputTokens: 100})
	store.Add(Record{ID: "2", APIKey: "key1", InputTokens: 200})
	store.Add(Record{ID: "3", APIKey: "key2", InputTokens: 300})

	if store.Count() != 3 {
		t.Errorf("Count() = %v, want 3", store.Count())
	}

	// 超过容量时应该移除最旧的
	store.Add(Record{ID: "4", APIKey: "key1", InputTokens: 400})
	if store.Count() != 3 {
		t.Errorf("Count() = %v, want 3 after overflow", store.Count())
	}
}

func TestStore_GetByAPIKey(t *testing.T) {
	store := NewStore(100)

	store.Add(Record{ID: "1", APIKey: "key1", InputTokens: 100})
	store.Add(Record{ID: "2", APIKey: "key2", InputTokens: 200})
	store.Add(Record{ID: "3", APIKey: "key1", InputTokens: 300})

	records := store.GetByAPIKey("key1", 10)
	if len(records) != 2 {
		t.Errorf("GetByAPIKey() returned %v records, want 2", len(records))
	}

	// 应该按最新优先返回
	if records[0].ID != "3" {
		t.Errorf("GetByAPIKey() first record ID = %v, want 3", records[0].ID)
	}
}

func TestStore_GetRecent(t *testing.T) {
	store := NewStore(100)

	store.Add(Record{ID: "1"})
	store.Add(Record{ID: "2"})
	store.Add(Record{ID: "3"})

	records := store.GetRecent(2)
	if len(records) != 2 {
		t.Errorf("GetRecent() returned %v records, want 2", len(records))
	}

	// 应该按最新优先返回
	if records[0].ID != "3" {
		t.Errorf("GetRecent() first record ID = %v, want 3", records[0].ID)
	}
}

func TestStore_SumByAPIKey(t *testing.T) {
	store := NewStore(100)
	now := time.Now()

	store.Add(Record{
		APIKey:       "key1",
		InputTokens:  100,
		OutputTokens: 50,
		CostCents:    10,
		CreatedAt:    now.Add(-1 * time.Hour),
	})
	store.Add(Record{
		APIKey:       "key1",
		InputTokens:  200,
		OutputTokens: 100,
		CostCents:    20,
		CreatedAt:    now.Add(-30 * time.Minute),
	})
	store.Add(Record{
		APIKey:       "key2",
		InputTokens:  300,
		OutputTokens: 150,
		CostCents:    30,
		CreatedAt:    now.Add(-15 * time.Minute),
	})

	// 统计 key1 最近 2 小时
	input, output, cost := store.SumByAPIKey("key1", now.Add(-2*time.Hour))
	if input != 300 {
		t.Errorf("SumByAPIKey() inputTokens = %v, want 300", input)
	}
	if output != 150 {
		t.Errorf("SumByAPIKey() outputTokens = %v, want 150", output)
	}
	if cost != 30 {
		t.Errorf("SumByAPIKey() costCents = %v, want 30", cost)
	}

	// 统计 key1 最近 45 分钟（只包含第二条记录）
	input, output, cost = store.SumByAPIKey("key1", now.Add(-45*time.Minute))
	if input != 200 {
		t.Errorf("SumByAPIKey() inputTokens = %v, want 200", input)
	}
}

func TestNewStore_DefaultMaxSize(t *testing.T) {
	store := NewStore(0)
	if store.maxSize != 10000 {
		t.Errorf("NewStore(0) maxSize = %v, want 10000", store.maxSize)
	}

	store = NewStore(-1)
	if store.maxSize != 10000 {
		t.Errorf("NewStore(-1) maxSize = %v, want 10000", store.maxSize)
	}
}
