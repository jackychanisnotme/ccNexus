package proxy

import (
	"net/http"
	"testing"
	"time"
)

func TestInboundConnectionTrackerTracksLifecycleByCategory(t *testing.T) {
	tracker := NewInboundConnectionTracker()
	req, err := http.NewRequest(http.MethodPost, "/v1/messages", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.RemoteAddr = "192.168.1.25:53210"
	req.Header.Set("User-Agent", "codex-cli-test")
	req.Header.Set(headerCCNexusRequestID, "req-1")

	done := tracker.TrackRequest(req, InboundConnectionCategoryProxy)

	snapshot := tracker.Snapshot()
	if snapshot.TotalActive != 1 {
		t.Fatalf("total active = %d, want 1", snapshot.TotalActive)
	}
	if snapshot.ByCategory[InboundConnectionCategoryProxy] != 1 {
		t.Fatalf("proxy active = %d, want 1", snapshot.ByCategory[InboundConnectionCategoryProxy])
	}
	if len(snapshot.Connections) != 1 {
		t.Fatalf("connections len = %d, want 1", len(snapshot.Connections))
	}
	conn := snapshot.Connections[0]
	if conn.ID == "" || conn.RequestID != "req-1" || conn.ClientIP != "192.168.1.25" ||
		conn.Category != InboundConnectionCategoryProxy || conn.Path != "/v1/messages" ||
		conn.Method != http.MethodPost || conn.UserAgent != "codex-cli-test" {
		t.Fatalf("unexpected connection snapshot: %#v", conn)
	}
	if conn.StartedAt.IsZero() || conn.DurationMillis < 0 {
		t.Fatalf("expected timing fields, got %#v", conn)
	}

	done()
	snapshot = tracker.Snapshot()
	if snapshot.TotalActive != 0 {
		t.Fatalf("total active after done = %d, want 0", snapshot.TotalActive)
	}
}

func TestInboundConnectionTrackerClassifiesPaths(t *testing.T) {
	tests := map[string]InboundConnectionCategory{
		"/":           InboundConnectionCategoryProxy,
		"/v1/models":  InboundConnectionCategoryProxy,
		"/api/events": InboundConnectionCategoryEvents,
		"/api/config": InboundConnectionCategoryAPI,
		"/ui/":        InboundConnectionCategoryAdminUI,
		"/admin":      InboundConnectionCategoryAdminUI,
		"/health":     InboundConnectionCategoryHealth,
		"/stats":      InboundConnectionCategoryHealth,
	}

	for path, want := range tests {
		req, err := http.NewRequest(http.MethodGet, path, nil)
		if err != nil {
			t.Fatalf("new request %s: %v", path, err)
		}
		if got := ClassifyInboundConnection(req); got != want {
			t.Fatalf("ClassifyInboundConnection(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestInboundConnectionSnapshotDurationUpdates(t *testing.T) {
	tracker := NewInboundConnectionTracker()
	req, err := http.NewRequest(http.MethodGet, "/api/events", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	done := tracker.TrackRequest(req, InboundConnectionCategoryEvents)
	defer done()

	time.Sleep(2 * time.Millisecond)
	snapshot := tracker.Snapshot()
	if len(snapshot.Connections) != 1 {
		t.Fatalf("connections len = %d, want 1", len(snapshot.Connections))
	}
	if snapshot.Connections[0].DurationMillis <= 0 {
		t.Fatalf("duration did not update: %#v", snapshot.Connections[0])
	}
}
