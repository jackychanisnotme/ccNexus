package service

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/proxy"
	"github.com/lich0821/ccNexus/internal/storage"
)

func TestStatsServiceTreatsRequestsAsSuccesses(t *testing.T) {
	store, err := storage.NewSQLiteStorage(filepath.Join(t.TempDir(), "ainexus.db"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer store.Close()

	today := time.Now().Format("2006-01-02")
	if err := store.RecordDailyStat(&storage.DailyStat{
		EndpointName: "Primary",
		Date:         today,
		Requests:     0,
		Errors:       66,
		DeviceID:     "test-device",
		ClientIP:     "unknown",
	}); err != nil {
		t.Fatalf("record daily stats: %v", err)
	}

	cfg := config.DefaultConfig()
	proxyInstance := proxy.New(cfg, storage.NewStatsStorageAdapter(store), store, "test-device")
	statsService := NewStatsService(proxyInstance, cfg, store)

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(statsService.GetStatsDaily()), &result); err != nil {
		t.Fatalf("decode daily stats: %v", err)
	}

	if result["totalRequests"] != float64(0) {
		t.Fatalf("legacy totalRequests = %#v, want 0", result["totalRequests"])
	}
	if result["totalAttempts"] != float64(66) {
		t.Fatalf("totalAttempts = %#v, want 66", result["totalAttempts"])
	}
	if result["successfulRequests"] != float64(0) {
		t.Fatalf("successfulRequests = %#v, want 0", result["successfulRequests"])
	}
	if result["totalErrors"] != float64(66) {
		t.Fatalf("totalErrors = %#v, want 66", result["totalErrors"])
	}
}
