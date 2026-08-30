package web

import (
	"net"
	"net/http"
	"strings"
)

func clientIP(r *http.Request, trustProxy bool) string {
	remote := remoteIP(r.RemoteAddr)
	if trustProxy && isTrustedProxy(remote) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			ip := strings.TrimSpace(parts[0])
			if ip != "" {
				return ip
			}
		}
		if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); xr != "" {
			return xr
		}
	}
	return remote
}

func remoteIP(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

// isTrustedProxy is true for loopback/private/link-local peers (Cloudflare
// tunnel, docker). Public RemoteAddr means ignore client-supplied XFF.
func isTrustedProxy(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	return parsed.IsLoopback() || parsed.IsPrivate() || parsed.IsLinkLocalUnicast()
}
