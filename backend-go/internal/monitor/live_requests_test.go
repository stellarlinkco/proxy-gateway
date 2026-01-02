package monitor

import (
	"testing"
	"time"
)

func TestLiveRequestManager_StartEndAndCount(t *testing.T) {
	m := NewLiveRequestManager(50)
	if got := m.Count(); got != 0 {
		t.Fatalf("Count() = %d, want 0", got)
	}

	now := time.Now().UTC().Truncate(time.Second)
	m.StartRequest(&LiveRequest{RequestID: "req-1", StartTime: now, APIType: "messages"})
	m.StartRequest(&LiveRequest{RequestID: "req-2", StartTime: now.Add(1 * time.Second), APIType: "responses"})

	if got := m.Count(); got != 2 {
		t.Fatalf("Count() = %d, want 2", got)
	}

	m.EndRequest("req-1")
	if got := m.Count(); got != 1 {
		t.Fatalf("Count() after EndRequest = %d, want 1", got)
	}
}

func TestLiveRequestManager_EvictOldestWhenFull(t *testing.T) {
	m := NewLiveRequestManager(2)

	base := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	m.StartRequest(&LiveRequest{RequestID: "old", StartTime: base.Add(0 * time.Second)})
	m.StartRequest(&LiveRequest{RequestID: "new", StartTime: base.Add(10 * time.Second)})

	// 触发裁剪：应删除 oldest("old")
	m.StartRequest(&LiveRequest{RequestID: "newer", StartTime: base.Add(20 * time.Second)})

	all := m.GetAllRequests()
	if len(all) != 2 {
		t.Fatalf("len(GetAllRequests()) = %d, want 2", len(all))
	}
	for _, r := range all {
		if r.RequestID == "old" {
			t.Fatalf("unexpected retained requestID = %q", r.RequestID)
		}
	}
}

func TestLiveRequestManager_GetAllRequests_SortedDesc(t *testing.T) {
	m := NewLiveRequestManager(50)

	base := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	m.StartRequest(&LiveRequest{RequestID: "r1", StartTime: base.Add(1 * time.Second)})
	m.StartRequest(&LiveRequest{RequestID: "r2", StartTime: base.Add(3 * time.Second)})
	m.StartRequest(&LiveRequest{RequestID: "r3", StartTime: base.Add(2 * time.Second)})

	all := m.GetAllRequests()
	if len(all) != 3 {
		t.Fatalf("len(GetAllRequests()) = %d, want 3", len(all))
	}
	if all[0].RequestID != "r2" || all[1].RequestID != "r3" || all[2].RequestID != "r1" {
		t.Fatalf("order = [%s, %s, %s], want [r2, r3, r1]", all[0].RequestID, all[1].RequestID, all[2].RequestID)
	}
}

func TestLiveRequestManager_GetRequestsByAPIType_FilterAndSort(t *testing.T) {
	m := NewLiveRequestManager(50)

	base := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	m.StartRequest(&LiveRequest{RequestID: "m1", StartTime: base.Add(1 * time.Second), APIType: "messages"})
	m.StartRequest(&LiveRequest{RequestID: "r1", StartTime: base.Add(2 * time.Second), APIType: "responses"})
	m.StartRequest(&LiveRequest{RequestID: "m2", StartTime: base.Add(3 * time.Second), APIType: "messages"})

	msg := m.GetRequestsByAPIType("messages")
	if len(msg) != 2 {
		t.Fatalf("len(GetRequestsByAPIType(messages)) = %d, want 2", len(msg))
	}
	if msg[0].RequestID != "m2" || msg[1].RequestID != "m1" {
		t.Fatalf("order = [%s, %s], want [m2, m1]", msg[0].RequestID, msg[1].RequestID)
	}

	none := m.GetRequestsByAPIType("gemini")
	if len(none) != 0 {
		t.Fatalf("len(GetRequestsByAPIType(gemini)) = %d, want 0", len(none))
	}
}

func TestLiveRequestManager_StartRequest_InvalidInputNoop(t *testing.T) {
	var m *LiveRequestManager
	m.StartRequest(&LiveRequest{RequestID: "req"})

	m = NewLiveRequestManager(50)
	m.StartRequest(nil)
	m.StartRequest(&LiveRequest{})
	if got := m.Count(); got != 0 {
		t.Fatalf("Count() = %d, want 0", got)
	}
}
