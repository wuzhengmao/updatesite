// Package downloads keeps a persistent per-artifact download counter.
//
// The counter deliberately lives outside the archive index: the index publishes
// a fresh immutable snapshot on every scan, so a counter stored in it would be
// reset every 15 seconds. Entries are keyed by name — application, version and
// file — which also means a version re-uploaded under the same name inherits
// its history, and a rescan never disturbs the numbers.
package downloads

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// FileName is the counter file, stored next to the apps/ archive directory so
// that backing up the archive backs up the statistics too.
const FileName = "downloads.json"

// FormatVersion is the layout version of the counter file. A file written by a
// newer build is reported and ignored rather than misread.
const FormatVersion = 1

// FlushInterval is how long a count may live only in memory under normal
// operation. Shutdown flushes as well; only a hard kill can lose this window.
const FlushInterval = 30 * time.Second

type key struct {
	app     string
	version string
	file    string
}

type versionKey struct {
	app     string
	version string
}

// fileFormat is the on-disk document. Entries are sorted before writing so the
// file stays diffable and hand-editable.
type fileFormat struct {
	Version int     `json:"version"`
	Entries []entry `json:"entries"`
}

type entry struct {
	App     string `json:"app"`
	Version string `json:"version"`
	File    string `json:"file"`
	Count   int64  `json:"count"`
}

// Stats summarises the counter for the health endpoint.
type Stats struct {
	Total    int64 `json:"total"`    // every download ever recorded
	Apps     int   `json:"apps"`     // applications with at least one download
	Files    int   `json:"files"`    // distinct artifact entries
	Writable bool  `json:"writable"` // false once a write failed; counts stay in memory
}

// Store counts downloads. It is safe for concurrent use; the site reads it from
// every page render while download requests write to it.
type Store struct {
	mu   sync.RWMutex
	path string

	files    map[key]int64        // the persisted state
	versions map[versionKey]int64 // maintained alongside, so lookups are O(1)
	apps     map[string]int64
	total    int64

	gen      int64 // bumped by every Count
	savedGen int64 // the gen captured by the last successful write
	writable bool

	saveMu sync.Mutex // serialises whole Save bodies
	warned bool       // a write failure has already been logged
}

// New creates a store backed by <dataDir>/downloads.json and loads it. A
// missing, unreadable or corrupt file is not an error: the counter simply
// starts from zero and the next flush rewrites the file.
func New(dataDir string) *Store {
	s := &Store{
		path:     filepath.Join(dataDir, FileName),
		files:    map[key]int64{},
		versions: map[versionKey]int64{},
		apps:     map[string]int64{},
		writable: true,
	}
	s.Load()
	return s
}

// Path is the counter file's location, for the start up log.
func (s *Store) Path() string { return s.path }

// Load reads the persisted counters, replacing whatever is in memory.
func (s *Store) Load() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return // a missing counter file is the normal first run
	}
	var doc fileFormat
	if err := json.Unmarshal(data, &doc); err != nil {
		log.Printf("downloads: ignoring corrupt counter file %s: %v", s.path, err)
		return
	}
	if doc.Version != FormatVersion {
		log.Printf("downloads: ignoring counter file %s: format version %d, this build understands %d",
			s.path, doc.Version, FormatVersion)
		return
	}

	files := make(map[key]int64, len(doc.Entries))
	versions := map[versionKey]int64{}
	apps := map[string]int64{}
	var total int64
	for _, e := range doc.Entries {
		// A hand-edited file must not be able to create unreachable entries.
		if e.App == "" || e.Version == "" || e.File == "" || e.Count <= 0 {
			continue
		}
		files[key{e.App, e.Version, e.File}] += e.Count
		versions[versionKey{e.App, e.Version}] += e.Count
		apps[e.App] += e.Count
		total += e.Count
	}

	s.mu.Lock()
	s.files, s.versions, s.apps, s.total = files, versions, apps, total
	s.mu.Unlock()
	if len(files) > 0 {
		log.Printf("downloads: loaded %d counters totalling %d downloads", len(files), total)
	}
}

// Count records one download. Empty arguments are ignored.
func (s *Store) Count(app, version, file string) {
	if app == "" || version == "" || file == "" {
		return
	}
	s.mu.Lock()
	s.files[key{app, version, file}]++
	s.versions[versionKey{app, version}]++
	s.apps[app]++
	s.total++
	s.gen++
	s.mu.Unlock()
}

// FileCount reports how often one artifact was downloaded.
func (s *Store) FileCount(app, version, file string) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.files[key{app, version, file}]
}

// VersionCount reports the downloads of every artifact of one version.
func (s *Store) VersionCount(app, version string) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.versions[versionKey{app, version}]
}

// AppCount reports the downloads of every version of one application.
func (s *Store) AppCount(app string) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.apps[app]
}

// Total reports the downloads recorded since the counter was last reset.
func (s *Store) Total() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.total
}

// Stats summarises the counter.
func (s *Store) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Stats{Total: s.total, Apps: len(s.apps), Files: len(s.files), Writable: s.writable}
}

// Save writes the counter back to disk when it changed. Failures are logged
// once and otherwise ignored: a read-only data directory must not break the
// site, and the counts keep accumulating in memory.
func (s *Store) Save() {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()

	s.mu.RLock()
	if s.gen == s.savedGen {
		s.mu.RUnlock()
		return
	}
	gen := s.gen
	entries := make([]entry, 0, len(s.files))
	for k, n := range s.files {
		entries = append(entries, entry{App: k.app, Version: k.version, File: k.file, Count: n})
	}
	s.mu.RUnlock()

	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.App != b.App {
			return a.App < b.App
		}
		if a.Version != b.Version {
			return a.Version < b.Version
		}
		return a.File < b.File
	})

	data, err := json.MarshalIndent(fileFormat{Version: FormatVersion, Entries: entries}, "", "  ")
	if err != nil {
		log.Printf("downloads: cannot encode counters: %v", err)
		return
	}
	data = append(data, '\n')

	if err := s.writeFile(data); err != nil {
		s.mu.Lock()
		s.writable = false
		first := !s.warned
		s.warned = true
		s.mu.Unlock()
		if first {
			log.Printf("downloads: cannot write %s: %v (counts are kept in memory only)", s.path, err)
		}
		return
	}

	// Only a completed rename makes the state on disk current. Counts that
	// arrived while this write was running keep the store dirty.
	s.mu.Lock()
	s.writable = true
	s.warned = false
	s.savedGen = gen
	s.mu.Unlock()
}

// Run flushes the counter on a ticker and once more when ctx is done. It blocks
// until ctx is cancelled.
func (s *Store) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = FlushInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.Save()
			return
		case <-t.C:
			s.Save()
		}
	}
}

// writeFile replaces the counter file atomically: a temporary file in the same
// directory, then a rename.
func (s *Store) writeFile(data []byte) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".downloads-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := renameWithRetry(tmp, s.path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// renameWithRetry replaces target with tmp. On Windows a virus scanner or the
// search indexer can hold a handle on the freshly created file for a moment, so
// a rename that fails once usually succeeds shortly after. The rename stays
// atomic either way.
func renameWithRetry(tmp, target string) error {
	var err error
	for attempt := 0; attempt < 6; attempt++ {
		if err = os.Rename(tmp, target); err == nil {
			return nil
		}
		time.Sleep(time.Duration(10*(attempt+1)) * time.Millisecond)
	}
	return err
}
