package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/lich0821/ccNexus/internal/codexauth"
	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/proxy"
	"github.com/lich0821/ccNexus/internal/storage"
)

type codexCredentialAuthManager interface {
	Start(ctx context.Context, endpoint config.Endpoint) (codexauth.StartResponse, error)
	Status(loginID string) (codexauth.StatusResponse, error)
	Cancel(loginID string) error
}

// Handler handles API requests
type Handler struct {
	config    *config.Config
	proxy     *proxy.Proxy
	storage   *storage.SQLiteStorage
	auth      AuthConfig
	codexAuth codexCredentialAuthManager
}

// NewHandler creates a new API handler
func NewHandler(cfg *config.Config, p *proxy.Proxy, s *storage.SQLiteStorage) *Handler {
	h := &Handler{
		config:  cfg,
		proxy:   p,
		storage: s,
		auth: AuthConfig{
			Enabled:  cfg.BasicAuthEnabled,
			Username: cfg.BasicAuthUsername,
			Password: cfg.BasicAuthPassword,
		},
	}
	h.resetCodexAuthManager()
	return h
}

func (h *Handler) resetCodexAuthManager() {
	h.codexAuth = codexauth.NewManager(codexauth.Options{
		Storage:    h.storage,
		HTTPClient: codexauth.HTTPClientForConfig(h.config),
		HTTPClientForEndpoint: func(endpoint config.Endpoint) *http.Client {
			return codexauth.HTTPClientForEndpoint(h.config, endpoint)
		},
	})
}

// ServeHTTP implements http.Handler interface
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	authMiddleware := BasicAuthMiddleware(h.auth)

	switch path {
	case "/api/endpoints":
		authMiddleware(http.HandlerFunc(h.handleEndpoints)).ServeHTTP(w, r)
	case "/api/endpoints/current":
		authMiddleware(http.HandlerFunc(h.handleCurrentEndpoint)).ServeHTTP(w, r)
	case "/api/endpoints/switch":
		authMiddleware(http.HandlerFunc(h.handleSwitchEndpoint)).ServeHTTP(w, r)
	case "/api/endpoints/reorder":
		authMiddleware(http.HandlerFunc(h.handleReorderEndpoints)).ServeHTTP(w, r)
	case "/api/endpoints/fetch-models":
		authMiddleware(http.HandlerFunc(h.handleFetchModels)).ServeHTTP(w, r)
	case "/api/stats/summary":
		authMiddleware(http.HandlerFunc(h.handleStatsSummary)).ServeHTTP(w, r)
	case "/api/stats/daily":
		authMiddleware(http.HandlerFunc(h.handleStatsDaily)).ServeHTTP(w, r)
	case "/api/stats/weekly":
		authMiddleware(http.HandlerFunc(h.handleStatsWeekly)).ServeHTTP(w, r)
	case "/api/stats/monthly":
		authMiddleware(http.HandlerFunc(h.handleStatsMonthly)).ServeHTTP(w, r)
	case "/api/stats/filters":
		authMiddleware(http.HandlerFunc(h.handleStatsFilters)).ServeHTTP(w, r)
	case "/api/stats/trends":
		authMiddleware(http.HandlerFunc(h.handleStatsTrends)).ServeHTTP(w, r)
	case "/api/config":
		authMiddleware(http.HandlerFunc(h.handleConfig)).ServeHTTP(w, r)
	case "/api/config/port":
		authMiddleware(http.HandlerFunc(h.handleConfigPort)).ServeHTTP(w, r)
	case "/api/config/log-level":
		authMiddleware(http.HandlerFunc(h.handleConfigLogLevel)).ServeHTTP(w, r)
	case "/api/config/basic-auth":
		authMiddleware(http.HandlerFunc(h.handleBasicAuthConfig)).ServeHTTP(w, r)
	case "/api/config/basic-auth/reset-password":
		authMiddleware(http.HandlerFunc(h.handleResetBasicAuthPassword)).ServeHTTP(w, r)
	case "/api/network":
		authMiddleware(http.HandlerFunc(h.handleNetwork)).ServeHTTP(w, r)
	case "/api/events":
		authMiddleware(http.HandlerFunc(h.handleEvents)).ServeHTTP(w, r)
	default:
		if strings.HasPrefix(path, "/api/endpoints/") {
			authMiddleware(http.HandlerFunc(h.handleEndpointByName)).ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	}
}
