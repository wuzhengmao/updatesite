package server

import (
	"net"
	"net/http"
	"strings"

	"github.com/wuzhengmao/updatesite/internal/config"
)

// HealthPath is the liveness endpoint. It is exempt from the HTTPS redirect so
// that a container probe on the plain HTTP port keeps working.
const HealthPath = "/api/v1/health"

// RedirectToTLS wraps a handler so that plain HTTP requests are sent to the
// HTTPS listener. It is meant for the HTTP listener only.
//
// The target is chosen in this order:
//
//  1. BASE_URL, when it is an https address. This is the only way to get the
//     redirect right when the host ports differ from the container ports, as
//     with a "8080:80 / 8443:443" mapping.
//  2. X-Forwarded-Host from a proxy, keeping the port it reports.
//  3. The request host, with the TLS port appended unless it is 443.
func RedirectToTLS(next http.Handler, cfg config.Config) http.Handler {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base != "" && !strings.HasPrefix(strings.ToLower(base), "https://") {
		base = ""
	}
	port := portOf(cfg.TLSAddr)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == HealthPath {
			next.ServeHTTP(w, r)
			return
		}

		target := base
		if target == "" {
			host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
			if host == "" {
				// Drop whatever port the request arrived on — that is the
				// plain HTTP port — and advertise the TLS one instead.
				host = r.Host
				if h, _, err := net.SplitHostPort(host); err == nil {
					host = h
				}
				if port != "" && port != "443" {
					host = net.JoinHostPort(host, port)
				}
			}
			target = "https://" + host
		}

		http.Redirect(w, r, target+r.URL.RequestURI(), http.StatusMovedPermanently)
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
