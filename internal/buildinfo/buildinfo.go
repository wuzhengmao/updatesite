// Package buildinfo carries the values injected at link time.
package buildinfo

// Version, Commit and Date are overridden with -ldflags -X at build time.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)
