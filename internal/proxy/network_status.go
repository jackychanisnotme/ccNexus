package proxy

import (
	"net"
	"strconv"
	"strings"

	"github.com/lich0821/ccNexus/internal/config"
)

type NetworkStatus struct {
	ListenMode      string                     `json:"listenMode"`
	ListenAddr      string                     `json:"listenAddr"`
	Port            int                        `json:"port"`
	LocalURL        string                     `json:"localURL"`
	LANURLs         []string                   `json:"lanURLs"`
	RestartRequired bool                       `json:"restartRequired"`
	Connections     InboundConnectionsSnapshot `json:"connections"`
}

func (p *Proxy) GetNetworkStatus() NetworkStatus {
	if p == nil || p.config == nil {
		return BuildNetworkStatus(config.DefaultConfig(), InboundConnectionsSnapshot{})
	}
	return BuildNetworkStatus(p.config, p.GetInboundConnectionsSnapshot())
}

func BuildNetworkStatus(cfg *config.Config, connections InboundConnectionsSnapshot) NetworkStatus {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	port := cfg.GetPort()
	return NetworkStatus{
		ListenMode:  cfg.GetListenMode(),
		ListenAddr:  cfg.GetListenAddr(),
		Port:        port,
		LocalURL:    "http://127.0.0.1:" + strconv.Itoa(port),
		LANURLs:     buildLANURLs(port),
		Connections: connections,
	}
}

func buildLANURLs(port int) []string {
	addrs := localLANIPv4Addresses()
	urls := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		urls = append(urls, "http://"+addr+":"+strconv.Itoa(port))
	}
	return urls
}

func localLANIPv4Addresses() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	seen := make(map[string]bool)
	addrs := make([]string, 0)
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		ifaceAddrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, rawAddr := range ifaceAddrs {
			ip := ipFromAddr(rawAddr)
			if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
				continue
			}
			ip4 := ip.To4()
			if ip4 == nil {
				continue
			}
			value := strings.TrimSpace(ip4.String())
			if value == "" || seen[value] {
				continue
			}
			seen[value] = true
			addrs = append(addrs, value)
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
