package index

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// sumEntry remembers the digest of one file so that restarts do not re-hash a
// whole archive.
type sumEntry struct {
	Size  int64  `json:"size"`
	MTime int64  `json:"mtime"` // UnixNano
	SHA   string `json:"sha256"`
}

// sumCache is a size+mtime keyed sha256 cache, persisted as a single JSON file.
type sumCache struct {
	mu     sync.Mutex
	path   string
	m      map[string]sumEntry
	dirty  bool
	broken map[string]bool // files that failed to hash, keyed the same way
}

func newSumCache(dir string) *sumCache {
	return &sumCache{
		path:   filepath.Join(dir, "sha256.json"),
		m:      map[string]sumEntry{},
		broken: map[string]bool{},
	}
}

// Load reads the persisted cache. A missing or unreadable file is not an error.
func (c *sumCache) Load() {
	data, err := os.ReadFile(c.path)
	if err != nil {
		return
	}
	m := map[string]sumEntry{}
	if err := json.Unmarshal(data, &m); err != nil {
		log.Printf("index: ignoring corrupt checksum cache %s: %v", c.path, err)
		return
	}
	c.mu.Lock()
	c.m = m
	c.mu.Unlock()
	log.Printf("index: loaded %d cached checksums", len(m))
}

// Lookup returns the cached digest of a file when size and mtime still match.
func (c *sumCache) Lookup(path string, size, mtime int64) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[path]
	if !ok || e.Size != size || e.MTime != mtime {
		return "", false
	}
	return e.SHA, true
}

// Store records a freshly computed digest.
func (c *sumCache) Store(path string, size, mtime int64, sha string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[path] = sumEntry{Size: size, MTime: mtime, SHA: sha}
	c.dirty = true
}

// Failed reports whether a file recently failed to hash.
func (c *sumCache) Failed(path string, size, mtime int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.broken[brokenKey(path, size, mtime)]
}

// MarkFailed remembers that a file cannot be hashed so the scanner stops
// retrying it on every cycle.
func (c *sumCache) MarkFailed(path string, size, mtime int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.broken[brokenKey(path, size, mtime)] = true
}

func brokenKey(path string, size, mtime int64) string {
	return path + "\x00" + strconv.FormatInt(size, 10) + "\x00" + strconv.FormatInt(mtime, 10)
}

// Save writes the cache back to disk when it changed. Failures are logged once
// and otherwise ignored: a read-only data directory must not break the site.
func (c *sumCache) Save() {
	c.mu.Lock()
	if !c.dirty {
		c.mu.Unlock()
		return
	}
	data, err := json.Marshal(c.m)
	c.dirty = false
	c.mu.Unlock()
	if err != nil {
		log.Printf("index: cannot encode checksum cache: %v", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		log.Printf("index: cannot create cache dir: %v", err)
		return
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		log.Printf("index: cannot write checksum cache: %v", err)
		return
	}
	if err := os.Rename(tmp, c.path); err != nil {
		log.Printf("index: cannot replace checksum cache: %v", err)
	}
}

// HashFile computes the sha256 of a file, returned as lower-case hex.
func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
