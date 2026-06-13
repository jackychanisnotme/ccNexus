package config

import "testing"

func TestNormalizeThinkingEffortPreservesProviderDefault(t *testing.T) {
	tests := map[string]string{
		"":        "",
		" ":       "",
		"default": "",
		"auto":    "",
		"inherit": "",
		"off":     "off",
		"low":     "low",
		"medium":  "medium",
		"high":    "high",
		"xhigh":   "xhigh",
		"invalid": "off",
	}

	for input, want := range tests {
		if got := NormalizeThinkingEffort(input); got != want {
			t.Fatalf("NormalizeThinkingEffort(%q) = %q, want %q", input, got, want)
		}
	}
}

type fakeConfigStorage struct {
	endpoints []StorageEndpoint
	configs   map[string]string
}

func newFakeConfigStorage() *fakeConfigStorage {
	return &fakeConfigStorage{configs: make(map[string]string)}
}

func (s *fakeConfigStorage) GetEndpoints() ([]StorageEndpoint, error) {
	endpoints := make([]StorageEndpoint, len(s.endpoints))
	copy(endpoints, s.endpoints)
	return endpoints, nil
}

func (s *fakeConfigStorage) SaveEndpoint(ep *StorageEndpoint) error {
	s.endpoints = append(s.endpoints, *ep)
	return nil
}

func (s *fakeConfigStorage) UpdateEndpoint(ep *StorageEndpoint) error {
	for i := range s.endpoints {
		if s.endpoints[i].Name == ep.Name {
			s.endpoints[i] = *ep
			return nil
		}
	}
	s.endpoints = append(s.endpoints, *ep)
	return nil
}

func (s *fakeConfigStorage) DeleteEndpoint(name string) error {
	for i := range s.endpoints {
		if s.endpoints[i].Name == name {
			s.endpoints = append(s.endpoints[:i], s.endpoints[i+1:]...)
			return nil
		}
	}
	return nil
}

func (s *fakeConfigStorage) GetConfig(key string) (string, error) {
	return s.configs[key], nil
}

func (s *fakeConfigStorage) SetConfig(key, value string) error {
	s.configs[key] = value
	return nil
}

func TestLoadFromStorageUsesDefaultFailover(t *testing.T) {
	store := newFakeConfigStorage()

	cfg, err := LoadFromStorage(store)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	failover := cfg.GetFailover()
	if failover.RecoveredEndpointPolicy != RecoveredEndpointPolicyDeprioritize {
		t.Fatalf("expected default policy %q, got %q", RecoveredEndpointPolicyDeprioritize, failover.RecoveredEndpointPolicy)
	}
	if failover.Cooldowns.QuotaExhaustedSec != 3600 ||
		failover.Cooldowns.RateLimitedSec != 120 ||
		failover.Cooldowns.UpstreamErrorSec != 60 ||
		failover.Cooldowns.NetworkErrorSec != 30 ||
		failover.Cooldowns.TokenUnavailableSec != 600 ||
		failover.Cooldowns.ConfigErrorSec != 1800 {
		t.Fatalf("unexpected default cooldowns: %#v", failover.Cooldowns)
	}
	if failover.CircuitBreaker.ConsecutiveFailures != 3 ||
		failover.CircuitBreaker.WindowSec != 60 ||
		failover.CircuitBreaker.FailureRateThreshold != 0.60 ||
		failover.CircuitBreaker.MinRequests != 5 ||
		failover.CircuitBreaker.CooldownSec != 600 {
		t.Fatalf("unexpected default circuit breaker: %#v", failover.CircuitBreaker)
	}
}

func TestFailoverConfigPersistsAndNormalizes(t *testing.T) {
	store := newFakeConfigStorage()
	cfg := DefaultConfig()
	cfg.UpdateEndpoints(nil)
	cfg.UpdateFailover(&FailoverConfig{
		RecoveredEndpointPolicy: RecoveredEndpointPolicyAutoReturn,
		Cooldowns: &FailoverCooldownConfig{
			QuotaExhaustedSec:   0,
			RateLimitedSec:      7,
			UpstreamErrorSec:    8,
			NetworkErrorSec:     9,
			TokenUnavailableSec: 10,
			ConfigErrorSec:      11,
		},
		CircuitBreaker: &FailoverCircuitBreakerConfig{
			ConsecutiveFailures:  2,
			WindowSec:            12,
			FailureRateThreshold: 0.75,
			MinRequests:          4,
			CooldownSec:          300,
		},
	})

	if err := cfg.SaveToStorage(store); err != nil {
		t.Fatalf("save config: %v", err)
	}
	reloaded, err := LoadFromStorage(store)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	failover := reloaded.GetFailover()
	if failover.RecoveredEndpointPolicy != RecoveredEndpointPolicyAutoReturn {
		t.Fatalf("expected persisted policy auto_return, got %q", failover.RecoveredEndpointPolicy)
	}
	if failover.Cooldowns.QuotaExhaustedSec != 0 ||
		failover.Cooldowns.RateLimitedSec != 7 ||
		failover.Cooldowns.UpstreamErrorSec != 8 ||
		failover.Cooldowns.NetworkErrorSec != 9 ||
		failover.Cooldowns.TokenUnavailableSec != 10 ||
		failover.Cooldowns.ConfigErrorSec != 11 {
		t.Fatalf("unexpected persisted cooldowns: %#v", failover.Cooldowns)
	}
	if failover.CircuitBreaker.ConsecutiveFailures != 2 ||
		failover.CircuitBreaker.WindowSec != 12 ||
		failover.CircuitBreaker.FailureRateThreshold != 0.75 ||
		failover.CircuitBreaker.MinRequests != 4 ||
		failover.CircuitBreaker.CooldownSec != 300 {
		t.Fatalf("unexpected persisted circuit breaker: %#v", failover.CircuitBreaker)
	}

	cfg.UpdateFailover(&FailoverConfig{
		RecoveredEndpointPolicy: "bad-policy",
		Cooldowns: &FailoverCooldownConfig{
			QuotaExhaustedSec: -1,
		},
		CircuitBreaker: &FailoverCircuitBreakerConfig{
			FailureRateThreshold: 2,
		},
	})
	failover = cfg.GetFailover()
	if failover.RecoveredEndpointPolicy != RecoveredEndpointPolicyDeprioritize {
		t.Fatalf("expected invalid policy to normalize to deprioritize, got %q", failover.RecoveredEndpointPolicy)
	}
	if failover.Cooldowns.QuotaExhaustedSec != 3600 {
		t.Fatalf("expected negative cooldown to normalize to default, got %d", failover.Cooldowns.QuotaExhaustedSec)
	}
	if failover.CircuitBreaker.FailureRateThreshold != 0.60 {
		t.Fatalf("expected invalid failure rate threshold to normalize to default, got %f", failover.CircuitBreaker.FailureRateThreshold)
	}
}

func TestEndpointProxyURLPersistsThroughStorageRoundTrip(t *testing.T) {
	store := newFakeConfigStorage()
	cfg := DefaultConfig()
	cfg.UpdateEndpoints([]Endpoint{
		{
			Name:        "Primary",
			APIUrl:      "https://api.example.com",
			APIKey:      "key",
			AuthMode:    AuthModeAPIKey,
			Enabled:     true,
			Transformer: "claude",
			ProxyURL:    "http://127.0.0.1:7890",
		},
	})

	if err := cfg.SaveToStorage(store); err != nil {
		t.Fatalf("save config: %v", err)
	}
	reloaded, err := LoadFromStorage(store)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if len(reloaded.Endpoints) != 1 {
		t.Fatalf("expected one endpoint, got %d", len(reloaded.Endpoints))
	}
	if got := reloaded.Endpoints[0].ProxyURL; got != "http://127.0.0.1:7890" {
		t.Fatalf("expected proxy URL to round-trip, got %q", got)
	}
}

func TestListenModeDefaultsToLocalAndBuildsListenAddr(t *testing.T) {
	cfg := DefaultConfig()

	if got := cfg.GetListenMode(); got != ListenModeLocal {
		t.Fatalf("default listen mode = %q, want %q", got, ListenModeLocal)
	}
	if got := cfg.GetListenAddr(); got != "127.0.0.1:3000" {
		t.Fatalf("default listen addr = %q, want 127.0.0.1:3000", got)
	}

	cfg.UpdateListenMode(ListenModeLAN)
	if got := cfg.GetListenAddr(); got != "0.0.0.0:3000" {
		t.Fatalf("LAN listen addr = %q, want 0.0.0.0:3000", got)
	}
}

func TestListenModePersistsAndNormalizes(t *testing.T) {
	store := newFakeConfigStorage()
	cfg := DefaultConfig()
	cfg.UpdateEndpoints(nil)
	cfg.UpdateListenMode(ListenModeLAN)

	if err := cfg.SaveToStorage(store); err != nil {
		t.Fatalf("save config: %v", err)
	}
	reloaded, err := LoadFromStorage(store)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if got := reloaded.GetListenMode(); got != ListenModeLAN {
		t.Fatalf("reloaded listen mode = %q, want %q", got, ListenModeLAN)
	}

	store.configs["listenMode"] = "bad-mode"
	reloaded, err = LoadFromStorage(store)
	if err != nil {
		t.Fatalf("reload invalid config: %v", err)
	}
	if got := reloaded.GetListenMode(); got != ListenModeLocal {
		t.Fatalf("invalid listen mode = %q, want %q", got, ListenModeLocal)
	}
}
