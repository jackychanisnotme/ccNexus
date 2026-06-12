package codexauth

import (
	"net/http"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/logger"
	"github.com/lich0821/ccNexus/internal/proxy"
)

func HTTPClientForConfig(cfg *config.Config) *http.Client {
	return HTTPClientForEndpoint(cfg, config.Endpoint{})
}

func HTTPClientForEndpoint(cfg *config.Config, endpoint config.Endpoint) *http.Client {
	client := &http.Client{Timeout: defaultHTTPTimeout}
	if cfg == nil {
		return client
	}

	proxyURL := config.ResolveEndpointProxyURL(&endpoint, config.CodexTokenPoolAPIURL, cfg.GetProxy(), cfg.GetCodexProxy())
	if proxyURL == "" {
		return client
	}

	transport, err := proxy.CreateProxyTransport(proxyURL)
	if err != nil {
		logger.Warn("Failed to create proxy transport for Codex auth: %v", err)
		return client
	}
	client.Transport = transport
	return client
}
