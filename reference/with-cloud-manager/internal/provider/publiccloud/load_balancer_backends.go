package publiccloud

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/netip"
	"strconv"
	"strings"
	"unicode"

	"with-cloud-manager/internal/provider"
)

const maxLoadBalancerBackendRequests = 128

type loadBalancerBackendRead struct {
	key      []byte
	stable   bool
	requests int
	snapshot provider.LoadBalancerBackendSnapshot
}

func newLoadBalancerBackendRead(secret []byte) *loadBalancerBackendRead {
	stable := len(secret) == 32
	if len(secret) == 0 {
		secret = []byte(rand.Text())
	}
	return &loadBalancerBackendRead{key: secret, stable: stable}
}

func (read *loadBalancerBackendRead) request() error {
	read.requests++
	if read.requests > maxLoadBalancerBackendRequests {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	return nil
}

func (read *loadBalancerBackendRead) endpointKey(id, address string, port int) (string, error) {
	if (id == "" && address == "") || len(id) > provider.MaxObjectIDSize || strings.ContainsFunc(id, unicode.IsControl) {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	if address != "" {
		ip, err := netip.ParseAddr(address)
		if err != nil || ip.Zone() != "" {
			return "", &provider.Failure{Kind: provider.InvalidResponse}
		}
		address = ip.Unmap().String()
	}
	return read.keyFor(id + "\x00" + address + "\x00" + strconv.Itoa(port)), nil
}

func (read *loadBalancerBackendRead) keyFor(value string) string {
	sum := hmac.New(sha256.New, read.key)
	sum.Write([]byte(value))
	return hex.EncodeToString(sum.Sum(nil)[:16])
}

func (read *loadBalancerBackendRead) addMember(member provider.LoadBalancerBackendMember) error {
	if len(read.snapshot.Members)+len(read.snapshot.Checks) >= provider.MaxLoadBalancerBackendRecords {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	read.snapshot.Members = append(read.snapshot.Members, member)
	return nil
}

func (read *loadBalancerBackendRead) addCheck(check provider.LoadBalancerBackendCheck) error {
	if len(read.snapshot.Members)+len(read.snapshot.Checks) >= provider.MaxLoadBalancerBackendRecords {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	read.snapshot.Checks = append(read.snapshot.Checks, check)
	return nil
}

func backendHealth(status string) string {
	switch strings.ToLower(status) {
	case "healthy", "online", "normal":
		return "healthy"
	case "unhealthy", "offline", "abnormal", "error":
		return "unhealthy"
	case "initial", "initializing":
		return "initial"
	case "unavailable", "no_monitor", "disabled":
		return "disabled"
	case "unused", "draining":
		return "unused"
	default:
		return "unknown"
	}
}

func invalidLoadBalancerBackend() *provider.Failure {
	return &provider.Failure{Kind: provider.InvalidResponse}
}

type loadBalancerListenerPort struct {
	Port     int
	Protocol string
}

func newLoadBalancerListenerPort(port int, protocol string) loadBalancerListenerPort {
	return loadBalancerListenerPort{Port: port, Protocol: strings.ToUpper(protocol)}
}

func loadBalancerListenersByPort(listeners []provider.LoadBalancerListener) map[loadBalancerListenerPort]string {
	byPort := make(map[loadBalancerListenerPort]string, len(listeners))
	for _, listener := range listeners {
		key := newLoadBalancerListenerPort(listener.Port, listener.Protocol)
		if _, ambiguous := byPort[key]; ambiguous {
			byPort[key] = ""
		} else {
			byPort[key] = listener.ID
		}
	}
	return byPort
}
