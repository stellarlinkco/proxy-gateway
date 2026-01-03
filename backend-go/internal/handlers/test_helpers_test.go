package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BenedictKing/claude-proxy/internal/config"
	"github.com/BenedictKing/claude-proxy/internal/metrics"
	"github.com/BenedictKing/claude-proxy/internal/scheduler"
	"github.com/BenedictKing/claude-proxy/internal/session"
	"github.com/BenedictKing/claude-proxy/internal/warmup"
)

func newTestConfigManager(t *testing.T, cfg config.Config) (*config.ConfigManager, string) {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}

	cm, err := config.NewConfigManager(path)
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}
	t.Cleanup(func() {
		_ = cm.Close()
	})
	return cm, path
}

func newTestScheduler(t *testing.T, cm *config.ConfigManager) (*scheduler.ChannelScheduler, func()) {
	t.Helper()

	messagesMetrics := metrics.NewMetricsManagerWithConfig(3, 0.5)
	responsesMetrics := metrics.NewMetricsManagerWithConfig(3, 0.5)
	geminiMetrics := metrics.NewMetricsManagerWithConfig(3, 0.5)
	traceAffinity := session.NewTraceAffinityManagerWithTTL(2 * time.Minute)
	urlMgr := warmup.NewURLManager(30*time.Second, 3)

	sch := scheduler.NewChannelScheduler(cm, messagesMetrics, responsesMetrics, geminiMetrics, traceAffinity, urlMgr)
	cleanup := func() {
		messagesMetrics.Stop()
		responsesMetrics.Stop()
		geminiMetrics.Stop()
		traceAffinity.Stop()
	}
	return sch, cleanup
}
