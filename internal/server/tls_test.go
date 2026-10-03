package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mti/updatesite/internal/config"
	"github.com/mti/updatesite/internal/server"
)

func TestRedirectToTLS(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	cases := []struct {
		name    string
		tlsAddr string
		baseURL string
		path    string
		host    string
		fwdHost string
		want    string // expected Location, or "" when the request passes through
	}{
		{name: "default port", tlsAddr: ":443", path: "/a/demo", host: "up.example.com",
			want: "https://up.example.com/a/demo"},
		{name: "host with port", tlsAddr: ":443", path: "/docs", host: "up.example.com:8080",
			want: "https://up.example.com/docs"},
		{name: "keeps query", tlsAddr: ":443", path: "/api/v1/apps/demo/check?version=1.0.0",
			host: "up.example.com", want: "https://up.example.com/api/v1/apps/demo/check?version=1.0.0"},
		{name: "non standard port", tlsAddr: ":8443", path: "/", host: "up.example.com",
			want: "https://up.example.com:8443/"},
		{name: "forwarded host wins", tlsAddr: ":443", path: "/", host: "10.0.0.5:80",
			fwdHost: "public.example.com", want: "https://public.example.com/"},
		// BASE_URL is the escape hatch when the host ports differ from the
		// container ports, as with a 8080:80 / 8443:443 compose mapping.
		{name: "base url wins", tlsAddr: ":443", baseURL: "https://localhost:8443",
			path: "/docs", host: "localhost:8080", want: "https://localhost:8443/docs"},
		{name: "base url with trailing slash", tlsAddr: ":443", baseURL: "https://up.example.com/",
			path: "/docs", host: "localhost:8080", want: "https://up.example.com/docs"},
		{name: "http base url is ignored", tlsAddr: ":443", baseURL: "http://up.example.com",
			path: "/docs", host: "up.example.com", want: "https://up.example.com/docs"},
		// The container probe must keep working, so health is never redirected.
		{name: "health is exempt", tlsAddr: ":443", path: server.HealthPath, host: "up.example.com"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := server.RedirectToTLS(ok, config.Config{TLSAddr: c.tlsAddr, BaseURL: c.baseURL})
			req := httptest.NewRequest(http.MethodGet, "http://"+c.host+c.path, nil)
			req.Host = c.host
			if c.fwdHost != "" {
				req.Header.Set("X-Forwarded-Host", c.fwdHost)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if c.want == "" {
				if rec.Code != http.StatusOK {
					t.Fatalf("status = %d, want 200 (should pass through)", rec.Code)
				}
				return
			}
			if rec.Code != http.StatusMovedPermanently {
				t.Fatalf("status = %d, want 301", rec.Code)
			}
			if got := rec.Header().Get("Location"); got != c.want {
				t.Errorf("Location = %q, want %q", got, c.want)
			}
		})
	}
}
