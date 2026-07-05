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

	proxyURL := resolveAuthProxyURL(cfg, endpoint)
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

func resolveAuthProxyURL(cfg *config.Config, endpoint config.Endpoint) string {
	var globalProxy *config.ProxyConfig
	var codexProxy *config.ProxyConfig
	if cfg != nil {
		globalProxy = cfg.GetProxy()
		codexProxy = cfg.GetCodexProxy()
	}
	return config.ResolveEndpointProxyURL(&endpoint, config.CodexTokenPoolAPIURL, globalProxy, codexProxy)
}
