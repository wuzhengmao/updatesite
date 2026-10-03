// Package index turns a directory tree of release artifacts into an in-memory,
// atomically swappable catalogue.
//
// The layout it expects is documented in docs/RELEASE-SPEC.md:
//
//	<data>/apps/<app-id>/<version>/<artifact files>
//
// Every scan happens on a background goroutine which publishes an immutable
// snapshot; HTTP handlers only ever read the current snapshot and therefore
// never block on the filesystem.
package index

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// hashWorkers bounds how many files are hashed concurrently.
const hashWorkers = 4

// Index owns the archive scanner and the current snapshot.
type Index struct {
	scanner *Scanner
	sums    *sumCache

	snap   atomic.Pointer[Snapshot]
	rescan chan struct{}

	lastScan atomic.Int64 // UnixNano
	scans    atomic.Int64
	hashed   atomic.Int64

	rescanOnce sync.Once
}

// New builds an Index over an archive directory and loads the checksum cache.
func New(dataDir, cacheDir string) *Index {
	sums := newSumCache(cacheDir)
	sums.Load()
	ix := &Index{
		scanner: newScanner(filepath.Join(dataDir, "apps"), sums),
		sums:    sums,
		rescan:  make(chan struct{}, 1),
	}
	// An empty snapshot keeps handlers working before the first scan finishes.
	ix.snap.Store(&Snapshot{BuiltAt: time.Now(), byID: map[string]*App{}})
	return ix
}

// Current returns the snapshot that is live right now. The returned value must
// be treated as read-only.
func (ix *Index) Current() *Snapshot { return ix.snap.Load() }

// Rescan requests a scan. It never blocks: if a scan is already queued the call
// is a no-op.
func (ix *Index) Rescan() {
	select {
	case ix.rescan <- struct{}{}:
	default:
	}
}

// Scan runs one scan synchronously, including checksum computation. Run uses it
// internally; it is exported for tests and one-shot validation.
func (ix *Index) Scan() { ix.scanOnce() }

// Run performs the initial scan and then rescans on a ticker, on demand, or
// when ctx is cancelled. It blocks until ctx is done.
func (ix *Index) Run(ctx context.Context, interval time.Duration) {
	ix.scanOnce()

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			ix.sums.Save()
			return
		case <-t.C:
		case <-ix.rescan:
		}
		ix.scanOnce()
	}
}

// scanOnce runs a full scan, hashes any newly seen files and republishes.
func (ix *Index) scanOnce() {
	start := time.Now()
	snap, pending := ix.scanner.Scan()
	ix.publish(snap)

	if len(pending) > 0 {
		n := ix.hashAll(pending)
		ix.sums.Save()
		if n > 0 {
			// Rescan so the published catalogue carries the new digests.
			// The checksum cache is warm now, so this walk does no hashing.
			final, _ := ix.scanner.Scan()
			ix.publish(final)
		}
	}
	ix.lastScan.Store(time.Now().UnixNano())
	ix.scans.Add(1)
	log.Printf("index: scanned %d apps (%d releases) in %s, %d new checksums",
		len(snap.Apps), countReleases(snap), time.Since(start).Round(time.Millisecond), len(pending))
}

func (ix *Index) publish(s *Snapshot) { ix.snap.Store(s) }

// hashAll computes digests for files the scanner has not seen before.
func (ix *Index) hashAll(paths []string) int {
	sem := make(chan struct{}, hashWorkers)
	var wg sync.WaitGroup
	var done atomic.Int64

	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		size, mtime := st.Size(), st.ModTime().UnixNano()

		wg.Add(1)
		sem <- struct{}{}
		go func(p string, size, mtime int64) {
			defer wg.Done()
			defer func() { <-sem }()
			sum, err := HashFile(p)
			if err != nil {
				log.Printf("index: cannot hash %s: %v", p, err)
				ix.sums.MarkFailed(p, size, mtime)
				return
			}
			ix.sums.Store(p, size, mtime, sum)
			done.Add(1)
		}(p, size, mtime)
	}
	wg.Wait()
	ix.hashed.Add(done.Load())
	return int(done.Load())
}

// Stats summarises the current catalogue for the health endpoint.
type Stats struct {
	Apps       int       `json:"apps"`
	Releases   int       `json:"releases"`
	Artifacts  int       `json:"artifacts"`
	Scans      int64     `json:"scans"`
	Hashes     int64     `json:"checksumsComputed"`
	LastScan   time.Time `json:"lastScan"`
	ScanAt     time.Time `json:"snapshotBuiltAt"`
	AppsDir    string    `json:"appsDir"`
	Warnings   []string  `json:"warnings,omitempty"`
	ScanMillis int64     `json:"lastScanMillis"`
}

// Stats reports the state of the index.
func (ix *Index) Stats() Stats {
	s := ix.Current()
	st := Stats{
		Apps:     len(s.Apps),
		AppsDir:  s.AppsDir,
		ScanAt:   s.BuiltAt,
		Scans:    ix.scans.Load(),
		Hashes:   ix.hashed.Load(),
		Warnings: s.Warnings,
	}
	if ns := ix.lastScan.Load(); ns > 0 {
		st.LastScan = time.Unix(0, ns)
	}
	for _, a := range s.Apps {
		st.Releases += len(a.Releases)
		for _, r := range a.Releases {
			st.Artifacts += len(r.Artifacts)
		}
	}
	return st
}

func countReleases(s *Snapshot) int {
	n := 0
	for _, a := range s.Apps {
		n += len(a.Releases)
	}
	return n
}
