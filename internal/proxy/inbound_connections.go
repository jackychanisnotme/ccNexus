package proxy

import (
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type InboundConnectionCategory string

const (
	InboundConnectionCategoryProxy   InboundConnectionCategory = "proxy"
	InboundConnectionCategoryAdminUI InboundConnectionCategory = "admin_ui"
	InboundConnectionCategoryAPI     InboundConnectionCategory = "api"
	InboundConnectionCategoryHealth  InboundConnectionCategory = "health"
	InboundConnectionCategoryEvents  InboundConnectionCategory = "events"
)

type InboundConnection struct {
	ID             string                    `json:"id"`
	RequestID      string                    `json:"requestId"`
	Category       InboundConnectionCategory `json:"category"`
	ClientIP       string                    `json:"clientIp"`
	Method         string                    `json:"method"`
	Path           string                    `json:"path"`
	UserAgent      string                    `json:"userAgent"`
	StartedAt      time.Time                 `json:"startedAt"`
	DurationMillis int64                     `json:"durationMillis"`
}

type InboundConnectionsSnapshot struct {
	TotalActive int                               `json:"totalActive"`
	ByCategory  map[InboundConnectionCategory]int `json:"byCategory"`
	Connections []InboundConnection               `json:"connections"`
	UpdatedAt   time.Time                         `json:"updatedAt"`
}

type InboundConnectionTracker struct {
	mu          sync.RWMutex
	nextID      atomic.Uint64
	connections map[string]InboundConnection
}

func NewInboundConnectionTracker() *InboundConnectionTracker {
	return &InboundConnectionTracker{
		connections: make(map[string]InboundConnection),
	}
}

func ClassifyInboundConnection(r *http.Request) InboundConnectionCategory {
	if r == nil || r.URL == nil {
		return InboundConnectionCategoryProxy
	}
	path := r.URL.Path
	switch {
	case path == "/health" || path == "/stats":
		return InboundConnectionCategoryHealth
	case path == "/api/events":
		return InboundConnectionCategoryEvents
	case strings.HasPrefix(path, "/api/"):
		return InboundConnectionCategoryAPI
	case path == "/admin" || path == "/ui" || strings.HasPrefix(path, "/ui/"):
		return InboundConnectionCategoryAdminUI
	default:
		return InboundConnectionCategoryProxy
	}
}

func (t *InboundConnectionTracker) TrackRequest(r *http.Request, category InboundConnectionCategory) func() {
	if t == nil {
		return func() {}
	}
	id := t.newID()
	requestID := ""
	method := ""
	path := ""
	userAgent := ""
	clientIP := "unknown"
	if r != nil {
		requestID = firstNonEmptyHeader(r.Header, headerCCNexusRequestID, "X-Request-ID", "X-Correlation-ID")
		method = r.Method
		clientIP = extractClientIP(r)
		userAgent = extractAgentHeader(r)
		if r.URL != nil {
			path = r.URL.Path
		}
	}
	if requestID == "" {
		requestID = id
	}
	if category == "" {
		category = ClassifyInboundConnection(r)
	}

	t.mu.Lock()
	t.connections[id] = InboundConnection{
		ID:        id,
		RequestID: requestID,
		Category:  category,
		ClientIP:  clientIP,
		Method:    method,
		Path:      path,
		UserAgent: userAgent,
		StartedAt: time.Now().UTC(),
	}
	t.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			t.mu.Lock()
			delete(t.connections, id)
			t.mu.Unlock()
		})
	}
}

func (t *InboundConnectionTracker) Snapshot() InboundConnectionsSnapshot {
	now := time.Now().UTC()
	snapshot := InboundConnectionsSnapshot{
		ByCategory: map[InboundConnectionCategory]int{
			InboundConnectionCategoryProxy:   0,
			InboundConnectionCategoryAdminUI: 0,
			InboundConnectionCategoryAPI:     0,
			InboundConnectionCategoryHealth:  0,
			InboundConnectionCategoryEvents:  0,
		},
		UpdatedAt: now,
	}
	if t == nil {
		return snapshot
	}

	t.mu.RLock()
	snapshot.Connections = make([]InboundConnection, 0, len(t.connections))
	for _, conn := range t.connections {
		conn.DurationMillis = now.Sub(conn.StartedAt).Milliseconds()
		snapshot.TotalActive++
		snapshot.ByCategory[conn.Category]++
		snapshot.Connections = append(snapshot.Connections, conn)
	}
	t.mu.RUnlock()

	sort.Slice(snapshot.Connections, func(i, j int) bool {
		return snapshot.Connections[i].StartedAt.Before(snapshot.Connections[j].StartedAt)
	})
	return snapshot
}

func (t *InboundConnectionTracker) newID() string {
	id := t.nextID.Add(1)
	return "inbound-" + strconvFormatUint(id)
}

func (p *Proxy) SetOnInboundConnectionsChanged(callback func(InboundConnectionsSnapshot)) {
	p.onInboundConnectionsChanged = callback
}

func (p *Proxy) GetInboundConnectionsSnapshot() InboundConnectionsSnapshot {
	if p == nil || p.inboundTracker == nil {
		return NewInboundConnectionTracker().Snapshot()
	}
	return p.inboundTracker.Snapshot()
}

func (p *Proxy) trackInboundConnections(next http.Handler) http.Handler {
	if next == nil || p == nil || p.inboundTracker == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		category := ClassifyInboundConnection(r)
		done := p.inboundTracker.TrackRequest(r, category)
		p.emitInboundConnectionsChanged()
		defer func() {
			done()
			p.emitInboundConnectionsChanged()
		}()
		next.ServeHTTP(w, r)
	})
}

func (p *Proxy) emitInboundConnectionsChanged() {
	if p == nil || p.onInboundConnectionsChanged == nil {
		return
	}
	p.onInboundConnectionsChanged(p.GetInboundConnectionsSnapshot())
}

func strconvFormatUint(value uint64) string {
	if value == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for value > 0 {
		i--
		buf[i] = byte('0' + value%10)
		value /= 10
	}
	return string(buf[i:])
}
