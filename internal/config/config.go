// Package config loads the runtime configuration from environment variables.
package config

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds every tunable knob of the update site. All of them come from
// environment variables so the Docker image needs no configuration file.
type Config struct {
	Addr         string        // listen address, e.g. ":8080"
	DataDir      string        // archive root, contains an "apps" sub directory
	CacheDir     string        // where the sha256 cache is persisted
	ScanInterval time.Duration // how often the archive is re-scanned
	SiteTitle    string
	SiteSubtitle string
	BaseURL      string // absolute prefix used when building download URLs
	CORSOrigin   string // value for Access-Control-Allow-Origin on the API
	RescanToken  string // when set, POST /api/v1/rescan requires this bearer token
	LogRequests  bool

	UploadEnabled bool  // expose the release upload endpoint
	MaxUpload     int64 // largest accepted upload, in bytes

	TLSAddr     string // HTTPS listen address, used only when a certificate is set
	TLSCert     string // path to a PEM certificate (or a full chain)
	TLSKey      string // path to the matching PEM private key
	TLSRedirect bool   // send plain HTTP visitors to HTTPS
}

// TLSEnabled reports whether an HTTPS listener should be started. Both halves
// of the key pair must be present; a half-configured pair is reported once at
// load time and otherwise ignored.
func (c Config) TLSEnabled() bool { return c.TLSCert != "" && c.TLSKey != "" }

// Load reads the configuration from the environment, applying defaults.
func Load() Config {
	c := Config{
		Addr:         env("ADDR", ":8080"),
		DataDir:      env("DATA_DIR", "/data"),
		CacheDir:     env("CACHE_DIR", "/var/cache/updatesite"),
		ScanInterval: envDuration("SCAN_INTERVAL", 15*time.Second),
		SiteTitle:    env("SITE_TITLE", "软件更新中心"),
		SiteSubtitle: env("SITE_SUBTITLE", ""),
		BaseURL:      strings.TrimRight(env("BASE_URL", ""), "/"),
		CORSOrigin:   env("CORS_ORIGIN", "*"),
		RescanToken:  env("RESCAN_TOKEN", ""),
		LogRequests:  envBool("LOG_REQUESTS", true),

		UploadEnabled: envBool("UPLOAD_ENABLED", true),
		MaxUpload:     envBytes("MAX_UPLOAD", 2<<30),

		TLSAddr:     env("TLS_ADDR", ":443"),
		TLSCert:     env("TLS_CERT", ""),
		TLSKey:      env("TLS_KEY", ""),
		TLSRedirect: envBool("TLS_REDIRECT", false),
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		log.Printf("config: TLS_CERT and TLS_KEY must be set together, HTTPS will stay off")
		c.TLSCert, c.TLSKey = "", ""
	}
	if c.MaxUpload < 1<<20 {
		log.Printf("config: MAX_UPLOAD %d is too small, using 1MiB", c.MaxUpload)
		c.MaxUpload = 1 << 20
	}
	if c.ScanInterval < time.Second {
		log.Printf("config: SCAN_INTERVAL %s is too small, using 1s", c.ScanInterval)
		c.ScanInterval = time.Second
	}
	return c
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		log.Printf("config: invalid %s=%q (%v), using %s", key, v, err, def)
		return def
	}
	return d
}

// envBytes parses a byte count, accepting a plain number or a suffix such as
// "512MiB", "2GiB" or "500MB".
func envBytes(key string, def int64) int64 {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	n, err := parseBytes(v)
	if err != nil {
		log.Printf("config: invalid %s=%q (%v), using %d", key, v, err, def)
		return def
	}
	return n
}

func parseBytes(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	units := []struct {
		suffix string
		scale  int64
	}{
		{"GIB", 1 << 30}, {"MIB", 1 << 20}, {"KIB", 1 << 10},
		{"GB", 1e9}, {"MB", 1e6}, {"KB", 1e3},
		{"G", 1 << 30}, {"M", 1 << 20}, {"K", 1 << 10},
		{"B", 1},
	}
	scale := int64(1)
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			scale = u.scale
			s = strings.TrimSpace(strings.TrimSuffix(s, u.suffix))
			break
		}
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	return int64(n * float64(scale)), nil
}

func envBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}
