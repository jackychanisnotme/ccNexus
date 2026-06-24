package proxy

import "github.com/lich0821/ccNexus/internal/config"

type proxyRuntimeSnapshot struct {
	config    *config.Config
	endpoints []config.Endpoint
	resolver  *EndpointResolver
	version   uint64
}

func newProxyRuntimeSnapshot(cfg *config.Config) *proxyRuntimeSnapshot {
	snapshot := cfg.Snapshot()
	endpoints := snapshot.GetEndpoints()
	return &proxyRuntimeSnapshot{
		config:    snapshot,
		endpoints: endpoints,
		resolver:  NewEndpointResolver(endpoints),
		version:   cfg.Version(),
	}
}

func (p *Proxy) runtimeSnapshot() *proxyRuntimeSnapshot {
	if p != nil {
		cfg := p.config
		if cfg == nil {
			cfg = config.DefaultConfig()
		}
		version := cfg.Version()
		if snapshot := p.runtime.Load(); snapshot != nil && snapshot.version == version {
			return snapshot
		}
		snapshot := newProxyRuntimeSnapshot(cfg)
		p.runtime.Store(snapshot)
		return snapshot
	}
	return newProxyRuntimeSnapshot(config.DefaultConfig())
}
