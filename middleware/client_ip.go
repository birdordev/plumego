package middleware

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// TrustedProxyClientIP 返回供传输中间件复用的地址解析器。
// 直连对端可信时才从最近一跳开始解析转发链，遇到第一个不可信地址即停止。
// 已考察的转发链无效或超限时使用直连地址。
func TrustedProxyClientIP(trusted ...netip.Prefix) (func(*http.Request) string, error) {
	prefixes := append([]netip.Prefix(nil), trusted...)
	for _, prefix := range prefixes {
		if !prefix.IsValid() || prefix.Addr().Is4In6() {
			return nil, errors.New("invalid trusted proxy prefix")
		}
	}
	return func(r *http.Request) string {
		if r == nil {
			return ""
		}
		peer := r.RemoteAddr
		if host, _, err := net.SplitHostPort(peer); err == nil {
			peer = host
		}
		address, err := netip.ParseAddr(peer)
		if err != nil {
			return ""
		}
		address = address.Unmap().WithZone("")
		peer = address.String()
		if !proxyAddressTrusted(address, prefixes) {
			return peer
		}
		headers := r.Header.Values("X-Forwarded-For")
		hops := 0
		for i := len(headers) - 1; i >= 0; i-- {
			header := headers[i]
			for {
				hops++
				if hops > 32 {
					return peer
				}
				cut := strings.LastIndexByte(header, ',')
				address, err = netip.ParseAddr(strings.TrimSpace(header[cut+1:]))
				if err != nil || address.Zone() != "" {
					return peer
				}
				address = address.Unmap()
				if !proxyAddressTrusted(address, prefixes) {
					return address.String()
				}
				if cut < 0 {
					break
				}
				header = header[:cut]
			}
		}
		return address.String()
	}, nil
}

func proxyAddressTrusted(address netip.Addr, prefixes []netip.Prefix) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
