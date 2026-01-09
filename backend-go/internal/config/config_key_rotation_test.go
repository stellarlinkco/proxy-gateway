package config

import (
	"testing"
	"time"
)

func newTestConfigManager() *ConfigManager {
	return &ConfigManager{
		failedKeysCache: make(map[string]*FailedKey),
		keyIndex:        make(map[string]int),
		keyRecoveryTime: time.Minute,
		maxFailureCount: 3,
	}
}

func TestGetNextAPIKey_RoundRobin(t *testing.T) {
	cm := newTestConfigManager()
	upstream := &UpstreamConfig{
		Name:    "test-channel",
		APIKeys: []string{"k1", "k2"},
	}

	got1, err := cm.GetNextAPIKey(upstream, nil)
	if err != nil {
		t.Fatalf("GetNextAPIKey #1 失败: %v", err)
	}
	got2, err := cm.GetNextAPIKey(upstream, nil)
	if err != nil {
		t.Fatalf("GetNextAPIKey #2 失败: %v", err)
	}
	got3, err := cm.GetNextAPIKey(upstream, nil)
	if err != nil {
		t.Fatalf("GetNextAPIKey #3 失败: %v", err)
	}

	if got1 != "k1" || got2 != "k2" || got3 != "k1" {
		t.Fatalf("round-robin 结果异常: got=[%s %s %s], want=[k1 k2 k1]", got1, got2, got3)
	}
}

func TestGetNextAPIKey_SkipFailedKeys(t *testing.T) {
	cm := newTestConfigManager()
	upstream := &UpstreamConfig{
		Name:    "test-channel",
		APIKeys: []string{"k1", "k2"},
	}

	failedKeys := map[string]bool{"k1": true}
	got, err := cm.GetNextAPIKey(upstream, failedKeys)
	if err != nil {
		t.Fatalf("GetNextAPIKey 失败: %v", err)
	}
	if got != "k2" {
		t.Fatalf("应跳过 failedKeys 中的 key: got=%s, want=k2", got)
	}
}

func TestGetNextAPIKey_SkipFailedKeysCache(t *testing.T) {
	cm := newTestConfigManager()
	cm.keyRecoveryTime = time.Hour

	upstream := &UpstreamConfig{
		Name:    "test-channel",
		APIKeys: []string{"k1", "k2"},
	}

	cm.MarkKeyAsFailed("k1")

	got, err := cm.GetNextAPIKey(upstream, nil)
	if err != nil {
		t.Fatalf("GetNextAPIKey 失败: %v", err)
	}
	if got != "k2" {
		t.Fatalf("应跳过 failedKeysCache 中的 key: got=%s, want=k2", got)
	}
}

func TestGetNextAPIKey_NamespaceIsolation(t *testing.T) {
	cm := newTestConfigManager()
	upstream := &UpstreamConfig{
		Name:    "test-channel",
		APIKeys: []string{"k1", "k2"},
	}

	gotMessages, err := cm.GetNextAPIKey(upstream, nil)
	if err != nil {
		t.Fatalf("GetNextAPIKey 失败: %v", err)
	}
	gotResponses, err := cm.GetNextResponsesAPIKey(upstream, nil)
	if err != nil {
		t.Fatalf("GetNextResponsesAPIKey 失败: %v", err)
	}
	gotGemini, err := cm.GetNextGeminiAPIKey(upstream, nil)
	if err != nil {
		t.Fatalf("GetNextGeminiAPIKey 失败: %v", err)
	}

	if gotMessages != "k1" || gotResponses != "k1" || gotGemini != "k1" {
		t.Fatalf("不同命名空间应独立轮询: got=[messages:%s responses:%s gemini:%s], want=[k1 k1 k1]",
			gotMessages, gotResponses, gotGemini)
	}
}
