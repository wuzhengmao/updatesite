// Package config loads the runtime configuration from environment variables.
package config

import (
	"log"
	"os"
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
}

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
