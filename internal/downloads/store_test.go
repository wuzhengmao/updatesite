package downloads

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// count records n downloads of one artifact.
func count(s *Store, n int, app, version, file string) {
	for i := 0; i < n; i++ {
		s.Count(app, version, file)
	}
}

// readDoc decodes the counter file a store writes.
func readDoc(t *testing.T, dir string) fileFormat {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("reading the counter file: %v", err)
	}
	var doc fileFormat
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decoding the counter file: %v\n%s", err, data)
	}
	return doc
}

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	s.Count("demo", "1.2.0", "Demo-1.2.0-windows-x64.exe")
	count(s, 4, "demo", "1.2.0", "Demo-1.2.0-macos-universal.dmg")
	count(s, 2, "edge", "2.0.0-rc.1", "edge-2.0.0-rc.1-windows-x64.exe")

	if got := s.Total(); got != 7 {
		t.Errorf("total before saving = %d, want 7", got)
	}
	s.Save()

	doc := readDoc(t, dir)
	if doc.Version != FormatVersion {
		t.Errorf("format version = %d, want %d", doc.Version, FormatVersion)
	}
	if len(doc.Entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(doc.Entries))
	}
	// Entries are sorted so the file stays diffable.
	wantOrder := []string{"demo\x001.2.0\x00Demo-1.2.0-macos-universal.dmg", "demo\x001.2.0\x00Demo-1.2.0-windows-x64.exe", "edge\x002.0.0-rc.1\x00edge-2.0.0-rc.1-windows-x64.exe"}
	for i, e := range doc.Entries {
		got := e.App + "\x00" + e.Version + "\x00" + e.File
		if got != wantOrder[i] {
			t.Errorf("entry %d = %q, want %q", i, got, wantOrder[i])
		}
	}

	// A fresh store reads the same numbers back.
	again := New(dir)
	if got := again.FileCount("demo", "1.2.0", "Demo-1.2.0-windows-x64.exe"); got != 1 {
		t.Errorf("file count after reload = %d, want 1", got)
	}
	if got := again.VersionCount("demo", "1.2.0"); got != 5 {
		t.Errorf("version count after reload = %d, want 5", got)
	}
	if got := again.AppCount("demo"); got != 5 {
		t.Errorf("app count after reload = %d, want 5", got)
	}
	if got := again.Total(); got != 7 {
		t.Errorf("total after reload = %d, want 7", got)
	}
	if got := again.Stats(); got.Apps != 2 || got.Files != 3 || !got.Writable {
		t.Errorf("stats after reload = %+v, want 2 apps, 3 files, writable", got)
	}
}

func TestStoreAggregates(t *testing.T) {
	s := New(t.TempDir())
	count(s, 3, "demo", "1.0.0", "a.exe")
	count(s, 2, "demo", "1.0.0", "b.dmg")
	count(s, 5, "demo", "1.2.0", "c.exe")
	count(s, 7, "other", "0.1.0", "d.apk")

	cases := []struct {
		name string
		got  int64
		want int64
	}{
		{"file a", s.FileCount("demo", "1.0.0", "a.exe"), 3},
		{"file b", s.FileCount("demo", "1.0.0", "b.dmg"), 2},
		{"unknown file", s.FileCount("demo", "9.9.9", "nope.exe"), 0},
		{"version 1.0.0", s.VersionCount("demo", "1.0.0"), 5},
		{"version 1.2.0", s.VersionCount("demo", "1.2.0"), 5},
		{"app demo", s.AppCount("demo"), 10},
		{"app other", s.AppCount("other"), 7},
		{"unknown app", s.AppCount("ghost"), 0},
		{"total", s.Total(), 17},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
}

func TestStoreCorruptFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New(dir) // must not panic or fail
	if got := s.Total(); got != 0 {
		t.Errorf("total from a corrupt file = %d, want 0", got)
	}
	s.Count("demo", "1.0.0", "a.exe")
	s.Save()

	doc := readDoc(t, dir)
	if len(doc.Entries) != 1 || doc.Entries[0].Count != 1 {
		t.Errorf("the corrupt file was not replaced: %+v", doc)
	}
}

func TestStoreUnknownFormatVersion(t *testing.T) {
	dir := t.TempDir()
	body := `{"version": 99, "entries": [{"app":"demo","version":"1.0.0","file":"a.exe","count":12}]}`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New(dir)
	if got := s.Total(); got != 0 {
		t.Errorf("a future format was read anyway: total = %d, want 0", got)
	}
}

func TestStoreSkipsUnusableEntries(t *testing.T) {
	dir := t.TempDir()
	body := `{"version": 1, "entries": [
		{"app":"demo","version":"1.0.0","file":"a.exe","count":4},
		{"app":"","version":"1.0.0","file":"b.exe","count":4},
		{"app":"demo","version":"1.0.0","file":"c.exe","count":0},
		{"app":"demo","version":"1.0.0","file":"d.exe","count":-2}
	]}`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New(dir)
	if got := s.Total(); got != 4 {
		t.Errorf("total = %d, want 4 (only the usable entry)", got)
	}
	if got := s.Stats().Files; got != 1 {
		t.Errorf("files = %d, want 1", got)
	}
	// Count rejects empty names too, so no unreachable key can be created.
	s.Count("", "1.0.0", "x.exe")
	s.Count("demo", "1.0.0", "")
	if got := s.Total(); got != 4 {
		t.Errorf("total after rejected counts = %d, want 4", got)
	}
}

func TestStoreReadOnlyDirKeepsCounting(t *testing.T) {
	// A regular file where the data directory should be makes MkdirAll fail,
	// which is the portable way to simulate an unwritable archive.
	dir := t.TempDir()
	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New(blocked)
	if !s.Stats().Writable {
		t.Error("a store that has not written yet should report itself writable")
	}
	count(s, 3, "demo", "1.0.0", "a.exe")
	s.Save()

	if got := s.Total(); got != 3 {
		t.Errorf("total = %d, want 3: counting must survive an unwritable directory", got)
	}
	if s.Stats().Writable {
		t.Error("writable is still true after a failed write")
	}
}

func TestStoreSaveIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	count(s, 2, "demo", "1.0.0", "a.exe")
	s.Save()

	before, err := os.Stat(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	s.Save() // nothing changed, so the file must be left alone
	after, err := os.Stat(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("a clean store rewrote the counter file")
	}
}

func TestStoreConcurrentCounts(t *testing.T) {
	s := New(t.TempDir())
	const workers, perWorker = 8, 50

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				s.Count("demo", "1.0.0", "shared.exe")
				s.Count("demo", "1.0.0", "extra.exe")
			}
		}(w)
	}
	wg.Wait()

	want := int64(workers * perWorker)
	if got := s.FileCount("demo", "1.0.0", "shared.exe"); got != want {
		t.Errorf("shared file = %d, want %d", got, want)
	}
	if got := s.VersionCount("demo", "1.0.0"); got != want*2 {
		t.Errorf("version = %d, want %d", got, want*2)
	}
	if got := s.Total(); got != want*2 {
		t.Errorf("total = %d, want %d", got, want*2)
	}
}

func TestStoreRunFlushesOnShutdown(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	count(s, 3, "demo", "1.0.0", "a.exe")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Run(ctx, time.Hour) // long enough that only the shutdown can flush
		close(done)
	}()
	cancel()
	<-done

	doc := readDoc(t, dir)
	if len(doc.Entries) != 1 || doc.Entries[0].Count != 3 {
		t.Errorf("shutdown did not flush: %+v", doc)
	}
}

func TestStoreRunFlushesAfterCounts(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		s.Run(ctx, 10*time.Millisecond)
		close(done)
	}()

	s.Count("demo", "1.0.0", "a.exe")
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, FileName)); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("the counter was never flushed while running")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
}
