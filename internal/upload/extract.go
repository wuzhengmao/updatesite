// Package upload unpacks a release archive and installs it into the archive
// directory.
package upload

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Format is a supported archive container.
type Format string

// Formats backed by the standard library. Notably absent are .tar.xz and
// .tar.zst: Go ships no decoder for either, and this project has no third-party
// dependencies.
const (
	FormatZip    Format = "zip"
	FormatTar    Format = "tar"
	FormatTarGz  Format = "tar.gz"
	FormatTarBz2 Format = "tar.bz2"
)

// ErrUnknownFormat is returned when the payload is not a recognised archive.
var ErrUnknownFormat = errors.New("unrecognised archive format")

// Unsuitable lists the containers we knowingly cannot handle, so the error can
// say what to do instead.
var unsuitable = map[string]string{
	".xz":  "tar.xz",
	".zst": "tar.zst",
	".7z":  "7z",
	".rar": "rar",
}

// Limits bounds what a single archive may expand to.
type Limits struct {
	// MaxTotalBytes caps the sum of the extracted file sizes.
	MaxTotalBytes int64
	// MaxEntries caps how many files an archive may contain.
	MaxEntries int
}

// DefaultLimits is generous enough for a multi-gigabyte installer while still
// stopping a decompression bomb.
var DefaultLimits = Limits{MaxTotalBytes: 8 << 30, MaxEntries: 20000}

// File is one extracted file, relative to the extraction root.
type File struct {
	Path string `json:"file"`
	Size int64  `json:"size"`
}

// Sniff identifies an archive from its leading bytes. It is preferred over the
// file name because it cannot be spoofed.
func Sniff(head []byte) (Format, bool) {
	switch {
	case len(head) >= 4 && head[0] == 'P' && head[1] == 'K' &&
		(head[2] == 3 || head[2] == 5 || head[2] == 7):
		return FormatZip, true
	case len(head) >= 2 && head[0] == 0x1f && head[1] == 0x8b:
		return FormatTarGz, true
	case len(head) >= 3 && head[0] == 'B' && head[1] == 'Z' && head[2] == 'h':
		return FormatTarBz2, true
	case len(head) >= 263 && string(head[257:262]) == "ustar":
		return FormatTar, true
	}
	return "", false
}

// FormatForName maps a file name to a format, used when sniffing is
// inconclusive.
func FormatForName(name string) (Format, bool) {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return FormatZip, true
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return FormatTarGz, true
	case strings.HasSuffix(lower, ".tar.bz2"), strings.HasSuffix(lower, ".tbz2"):
		return FormatTarBz2, true
	case strings.HasSuffix(lower, ".tar"):
		return FormatTar, true
	}
	return "", false
}

// UnsupportedHint returns advice for containers this build cannot open.
func UnsupportedHint(name string) string {
	lower := strings.ToLower(name)
	for ext, label := range unsuitable {
		if strings.HasSuffix(lower, ext) {
			return fmt.Sprintf("%s archives are not supported, repack as zip or tar.gz", label)
		}
	}
	return ""
}

// Extract unpacks an archive into dest and returns the files it wrote. Paths
// that would escape dest, symlinks and device nodes are all refused, and the
// expansion is bounded by lim.
func Extract(r io.ReaderAt, size int64, format Format, dest string, lim Limits) ([]File, error) {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, err
	}
	switch format {
	case FormatZip:
		return extractZip(r, size, dest, lim)
	case FormatTar, FormatTarGz, FormatTarBz2:
		return extractTar(r, size, format, dest, lim)
	}
	return nil, ErrUnknownFormat
}

// ------------------------------------------------------------------ tarballs

func extractTar(r io.ReaderAt, size int64, format Format, dest string, lim Limits) ([]File, error) {
	var (
		src io.Reader = io.NewSectionReader(r, 0, size)
		err error
	)
	switch format {
	case FormatTarGz:
		var zr *gzip.Reader
		if zr, err = gzip.NewReader(src); err != nil {
			return nil, fmt.Errorf("not a valid gzip stream: %w", err)
		}
		defer zr.Close()
		src = zr
	case FormatTarBz2:
		src = bzip2.NewReader(src)
	}

	tr := tar.NewReader(src)
	w := &writer{dest: dest, lim: lim}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("corrupt tar stream: %w", err)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			continue // directories are created on demand
		case tar.TypeReg, tar.TypeRegA:
			// fine
		default:
			// Symlinks, hard links and device nodes are silently dropped: they
			// have no business in a release artefact and are a classic escape
			// vector.
			continue
		}
		if err := w.write(hdr.Name, hdr.Size, tr); err != nil {
			return nil, err
		}
	}
	return w.files, nil
}

// ---------------------------------------------------------------------- zip

func extractZip(r io.ReaderAt, size int64, dest string, lim Limits) ([]File, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("not a valid zip archive: %w", err)
	}
	w := &writer{dest: dest, lim: lim}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if f.Mode()&(os.ModeSymlink|os.ModeDevice|os.ModeNamedPipe|os.ModeSocket) != 0 {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("cannot read %s: %w", f.Name, err)
		}
		// Zip headers carry a size that may be a lie, so the writer counts the
		// bytes it actually copies.
		err = w.write(f.Name, int64(f.UncompressedSize64), rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
	}
	return w.files, nil
}

// -------------------------------------------------------------------- writer

// writer copies archive entries into dest while enforcing the path and size
// rules.
type writer struct {
	dest  string
	lim   Limits
	total int64
	files []File
}

// safeRel normalises an archive entry name and rejects anything that would
// escape the destination. It returns "" for entries that should be skipped.
func safeRel(name string) (string, error) {
	// Zip archives written on Windows sometimes use backslashes.
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.TrimPrefix(name, "./")
	if name == "" || strings.HasPrefix(name, "/") {
		return "", nil
	}
	clean := path.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("archive entry %q escapes the release directory", name)
	}
	// Dot files and directories are dropped, matching what the scanner reads.
	// This also removes .DS_Store, ._resource forks and similar noise.
	for _, seg := range strings.Split(clean, "/") {
		if strings.HasPrefix(seg, ".") {
			return "", nil
		}
	}
	if clean == "__MACOSX" || strings.HasPrefix(clean, "__MACOSX/") {
		return "", nil
	}
	return clean, nil
}

func (w *writer) write(name string, declared int64, src io.Reader) error {
	rel, err := safeRel(name)
	if err != nil {
		return err
	}
	if rel == "" {
		return nil
	}
	if len(w.files) >= w.lim.MaxEntries {
		return fmt.Errorf("archive holds more than %d files", w.lim.MaxEntries)
	}
	if w.total+declared > w.lim.MaxTotalBytes {
		return fmt.Errorf("archive expands to more than %s", humanBytes(w.lim.MaxTotalBytes))
	}

	target := filepath.Join(w.dest, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	// Copy one byte beyond the remaining budget so an archive that lies about
	// its sizes still cannot overrun the limit.
	remaining := w.lim.MaxTotalBytes - w.total
	n, copyErr := io.Copy(out, io.LimitReader(src, remaining+1))
	closeErr := out.Close()
	if copyErr != nil {
		os.Remove(target)
		return fmt.Errorf("cannot extract %s: %w", rel, copyErr)
	}
	if closeErr != nil {
		return closeErr
	}
	if n > remaining {
		os.Remove(target)
		return fmt.Errorf("archive expands to more than %s", humanBytes(w.lim.MaxTotalBytes))
	}

	w.total += n
	w.files = append(w.files, File{Path: rel, Size: n})
	return nil
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.0f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
