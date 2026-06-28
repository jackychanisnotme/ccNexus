package landiscovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lich0821/ccNexus/internal/config"
)

const (
	ServiceName           = "ainexus-proxy"
	ProductName           = "AINexus"
	ProtocolVersion       = 1
	DiscoveryPath         = "/.well-known/ainexus/discovery"
	ReservedPairingMethod = "code-v1"
	UnpairedAPIKey        = "ainexus-lan-unpaired"
	EndpointRemark        = "AINexus LAN discovery; pairing reserved but disabled"
	DefaultScanTimeout    = 700 * time.Millisecond
)

type PairingInfo struct {
	Supported bool   `json:"supported"`
	Enabled   bool   `json:"enabled"`
	Method    string `json:"method"`
}

type Announcement struct {
	Product         string      `json:"product"`
	Service         string      `json:"service"`
	Version         int         `json:"version"`
	Name            string      `json:"name,omitempty"`
	DeviceID        string      `json:"deviceId,omitempty"`
	Port            int         `json:"port"`
	BaseURL         string      `json:"baseUrl"`
	RequiresPairing bool        `json:"requiresPairing"`
	Pairing         PairingInfo `json:"pairing"`
}

type Candidate struct {
	ID              string      `json:"id"`
	Name            string      `json:"name"`
	Host            string      `json:"host"`
	Port            int         `json:"port"`
	BaseURL         string      `json:"baseUrl"`
	Source          string      `json:"source"`
	RequiresPairing bool        `json:"requiresPairing"`
	Pairing         PairingInfo `json:"pairing"`
	LastSeen        string      `json:"lastSeen"`
	Added           bool        `json:"added"`
}

func BuildAnnouncement(cfg *config.Config, deviceID, displayName string) Announcement {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	port := cfg.GetPort()
	return Announcement{
		Product:         ProductName,
		Service:         ServiceName,
		Version:         ProtocolVersion,
		Name:            strings.TrimSpace(displayName),
		DeviceID:        strings.TrimSpace(deviceID),
		Port:            port,
		BaseURL:         "http://127.0.0.1:" + strconv.Itoa(port),
		RequiresPairing: false,
		Pairing: PairingInfo{
			Supported: true,
			Enabled:   false,
			Method:    ReservedPairingMethod,
		},
	}
}

func DecodeAnnouncement(raw []byte) (Announcement, bool) {
	var ann Announcement
	if err := json.Unmarshal(raw, &ann); err != nil {
		return Announcement{}, false
	}
	if ann.Product != ProductName || ann.Service != ServiceName || ann.Version != ProtocolVersion {
		return Announcement{}, false
	}
	if ann.Port < 1 || ann.Port > 65535 {
		return Announcement{}, false
	}
	if ann.Pairing.Method == "" {
		ann.Pairing.Method = ReservedPairingMethod
	}
	return ann, true
}

func CandidateFromAnnouncement(host, source string, ann Announcement, now time.Time) (Candidate, bool) {
	cleanHost := strings.TrimSpace(host)
	if cleanHost == "" {
		return Candidate{}, false
	}
	baseURL := "http://" + cleanHost + ":" + strconv.Itoa(ann.Port)
	id := strings.TrimSpace(ann.DeviceID)
	if id == "" {
		id = cleanHost + ":" + strconv.Itoa(ann.Port)
	}
	name := strings.TrimSpace(ann.Name)
	if name == "" {
		name = cleanHost
	}
	return Candidate{
		ID:              id,
		Name:            name,
		Host:            cleanHost,
		Port:            ann.Port,
		BaseURL:         baseURL,
		Source:          strings.TrimSpace(source),
		RequiresPairing: ann.RequiresPairing,
		Pairing:         ann.Pairing,
		LastSeen:        now.UTC().Format(time.RFC3339),
	}, true
}

func ParseCandidateJSON(raw string) (Candidate, error) {
	var candidate Candidate
	if err := json.Unmarshal([]byte(raw), &candidate); err != nil {
		return Candidate{}, fmt.Errorf("invalid discovery candidate: %w", err)
	}
	if candidate.Port < 1 || candidate.Port > 65535 {
		return Candidate{}, fmt.Errorf("invalid discovery port")
	}
	parsed, err := url.Parse(strings.TrimSpace(candidate.BaseURL))
	if err != nil || parsed == nil || parsed.Scheme != "http" || parsed.Host == "" {
		return Candidate{}, fmt.Errorf("invalid discovery base URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return Candidate{}, fmt.Errorf("invalid discovery base URL")
	}
	host, portStr, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		return Candidate{}, fmt.Errorf("invalid discovery host")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port != candidate.Port {
		return Candidate{}, fmt.Errorf("discovery port mismatch")
	}
	if net.ParseIP(host) == nil {
		return Candidate{}, fmt.Errorf("discovery host must be an IP address")
	}
	candidate.Host = host
	candidate.BaseURL = strings.TrimRight(candidate.BaseURL, "/")
	if strings.TrimSpace(candidate.Name) == "" {
		candidate.Name = host
	}
	if strings.TrimSpace(candidate.ID) == "" {
		candidate.ID = host + ":" + strconv.Itoa(candidate.Port)
	}
	return candidate, nil
}

func EndpointName(candidate Candidate) string {
	base := strings.TrimSpace(candidate.Name)
	if base == "" {
		base = candidate.Host
	}
	return "局域网 AINexus - " + base
}

func EndpointForCandidate(candidate Candidate) config.Endpoint {
	return config.Endpoint{
		Name:        EndpointName(candidate),
		APIUrl:      strings.TrimRight(candidate.BaseURL, "/"),
		APIKey:      UnpairedAPIKey,
		AuthMode:    config.AuthModeAPIKey,
		Enabled:     true,
		Transformer: "claude",
		Remark:      EndpointRemark,
	}
}

type Scanner struct {
	client     *http.Client
	timeout    time.Duration
	localHosts func() []string

	mu         sync.Mutex
	candidates map[string]Candidate
}

func NewScanner() *Scanner {
	return &Scanner{
		client:     &http.Client{Timeout: DefaultScanTimeout},
		timeout:    DefaultScanTimeout,
		localHosts: localIPv4Addresses,
		candidates: make(map[string]Candidate),
	}
}

func (s *Scanner) Snapshot(existing []config.Endpoint) []Candidate {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked(existing)
}

func (s *Scanner) Scan(ctx context.Context, port int, existing []config.Endpoint) []Candidate {
	if s == nil {
		return nil
	}
	if port < 1 || port > 65535 {
		return s.Snapshot(existing)
	}
	hosts := candidateHosts(s.localHosts())
	now := time.Now()
	var wg sync.WaitGroup
	sem := make(chan struct{}, 64)
	for _, host := range hosts {
		wg.Add(1)
		go func(host string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			if candidate, ok := s.probe(ctx, host, port, now); ok {
				s.mu.Lock()
				s.candidates[candidate.ID] = candidate
				s.mu.Unlock()
			}
		}(host)
	}
	wg.Wait()

	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked(existing)
}

func (s *Scanner) probe(ctx context.Context, host string, port int, now time.Time) (Candidate, bool) {
	timeout := s.timeout
	if timeout <= 0 {
		timeout = DefaultScanTimeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, "http://"+host+":"+strconv.Itoa(port)+DiscoveryPath, nil)
	if err != nil {
		return Candidate{}, false
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return Candidate{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Candidate{}, false
	}
	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return Candidate{}, false
	}
	ann, ok := DecodeAnnouncement(raw)
	if !ok {
		return Candidate{}, false
	}
	return CandidateFromAnnouncement(host, "scan", ann, now)
}

func (s *Scanner) snapshotLocked(existing []config.Endpoint) []Candidate {
	endpointURLs := make(map[string]bool)
	for _, ep := range existing {
		endpointURLs[strings.TrimRight(strings.ToLower(strings.TrimSpace(ep.APIUrl)), "/")] = true
	}
	result := make([]Candidate, 0, len(s.candidates))
	for _, candidate := range s.candidates {
		candidate.Added = endpointURLs[strings.TrimRight(strings.ToLower(candidate.BaseURL), "/")]
		result = append(result, candidate)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Added != result[j].Added {
			return !result[i].Added
		}
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		return result[i].BaseURL < result[j].BaseURL
	})
	return result
}

func candidateHosts(local []string) []string {
	seen := map[string]bool{}
	var hosts []string
	for _, addr := range local {
		ip := net.ParseIP(strings.TrimSpace(addr)).To4()
		if ip == nil || ip.IsLoopback() {
			continue
		}
		for i := 1; i <= 254; i++ {
			if i == int(ip[3]) {
				continue
			}
			host := fmt.Sprintf("%d.%d.%d.%d", ip[0], ip[1], ip[2], i)
			if !seen[host] {
				seen[host] = true
				hosts = append(hosts, host)
			}
		}
	}
	return hosts
}

func localIPv4Addresses() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var addrs []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		ifaceAddrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, raw := range ifaceAddrs {
			if ip := ipFromAddr(raw); ip != nil {
				if ip4 := ip.To4(); ip4 != nil && !ip4.IsLoopback() && !ip4.IsUnspecified() {
					addrs = append(addrs, ip4.String())
				}
			}
		}
	}
	return addrs
}

func ipFromAddr(addr net.Addr) net.IP {
	switch value := addr.(type) {
	case *net.IPNet:
		return value.IP
	case *net.IPAddr:
		return value.IP
	default:
		host, _, err := net.SplitHostPort(value.String())
		if err != nil {
			host = value.String()
		}
		return net.ParseIP(host)
	}
}
