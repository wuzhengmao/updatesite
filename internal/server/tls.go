package server

import (
	"net"
	"net/http"
	"strings"

	"github.com/mti/updatesite/internal/config"
)

// HealthPath is the liveness endpoint. It is exempt from the HTTPS redirect so
// that a container probe on the plain HTTP port keeps working.
const HealthPath = "/api/v1/health"

// RedirectToTLS wraps a handler so that plain HTTP requests are sent to the
// HTTPS listener. It is meant for the HTTP listener only.
//
// The target host comes from X-Forwarded-Host when a proxy set it, otherwise
// from the request. The port is only appended when HTTPS does not run on the
// conventional 443, so the redirect stays correct for a non-standard port.
func RedirectToTLS(next http.Handler, cfg config.Config) http.Handler {
	port := portOf(cfg.TLSAddr)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == HealthPath {
			next.ServeHTTP(w, r)
			return
		}

		host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
		if host == "" {
			host = r.Host
		}
		if h, p, err := net.SplitHostPort(host); err == nil {
			host = h
			_ = p
		}
		if port != "" && port != "443" {
			host = net.JoinHostPort(host, port)
		}

		http.Redirect(w, r, "https://"+host+r.URL.RequestURI(), http.StatusMovedPermanently)
	})
}

// portOf extracts the port from a listen address such as ":443" or "0.0.0.0:8443".
func portOf(addr string) string {
	if addr == "" {
		return ""
	}
	if _, port, err := net.SplitHostPort(addr); err == nil {
		return port
	}
	if strings.HasPrefix(addr, ":") {
		return strings.TrimPrefix(addr, ":")
	}
	return ""
}
