package upload

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// NotWritableError reports that the archive directory could not be written to.
// It is the deployment's problem rather than the caller's, so the HTTP layer
// turns it into a 503 with an actionable message.
type NotWritableError struct {
	Path string
	Err  error
}

func (e *NotWritableError) Error() string {
	return fmt.Sprintf("cannot write to %s: %v (is the archive mounted read-write?)", e.Path, e.Err)
}

func (e *NotWritableError) Unwrap() error { return e.Err }

// IsNotWritable reports whether err stems from an unwritable archive directory.
func IsNotWritable(err error) bool {
	var target *NotWritableError
	return errors.As(err, &target)
}

// Spool copies an uploaded stream into a hidden temporary file inside the
// application directory. That directory is the one place the site is known to
// be able to write, and a leading dot keeps the scanner away from the file.
//
// The caller owns the returned file and must invoke cleanup when finished.
func Spool(appsDir, appID string, r io.Reader, max int64) (f *os.File, size int64, cleanup func(), err error) {
	if err := ValidateAppID(appID); err != nil {
		return nil, 0, nil, err
	}
	appDir := filepath.Join(appsDir, appID)
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		return nil, 0, nil, &NotWritableError{Path: appDir, Err: err}
	}
	f, err = os.CreateTemp(appDir, ".upload-")
	if err != nil {
		return nil, 0, nil, &NotWritableError{Path: appDir, Err: err}
	}
	cleanup = func() {
		f.Close()
		os.Remove(f.Name())
	}

	// Copy one byte beyond the budget so an oversized upload is detected
	// without being read to the end.
	n, err := io.Copy(f, io.LimitReader(r, max+1))
	if err != nil {
		cleanup()
		return nil, 0, nil, fmt.Errorf("cannot read the upload: %w", err)
	}
	if n > max {
		cleanup()
		return nil, 0, nil, ErrTooLarge
	}
	return f, n, cleanup, nil
}

// ErrTooLarge is returned when an upload exceeds the configured limit.
var ErrTooLarge = fmt.Errorf("the upload is larger than the configured limit")
