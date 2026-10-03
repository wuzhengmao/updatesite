package index

import (
	"strings"
	"time"

	"github.com/wuzhengmao/updatesite/internal/semver"
)

// DefaultChannel is used whenever a release does not declare one.
const DefaultChannel = "stable"

// App is one application published on the site.
type App struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Summary      string            `json:"summary,omitempty"`
	Description  string            `json:"description,omitempty"`
	Homepage     string            `json:"homepage,omitempty"`
	Vendor       string            `json:"vendor,omitempty"`
	License      string            `json:"license,omitempty"`
	Tags         []string          `json:"tags,omitempty"`
	Platforms    []string          `json:"platforms,omitempty"`
	Channel      string            `json:"channel"`
	IconURL      string            `json:"iconUrl,omitempty"`
	Latest       map[string]string `json:"latest"`
	ReleaseCount int               `json:"releaseCount"`
	UpdatedAt    time.Time         `json:"updatedAt"`
	Releases     []*Release        `json:"releases"`

	Dir     string `json:"-"` // absolute directory of the app
	Order   int    `json:"-"` // sortOrder from app.json
	Hidden  bool   `json:"-"`
	iconSrc string // icon file name inside Dir, empty when absent
}

// Release is one version of an application.
type Release struct {
	App         string      `json:"app"`
	Version     string      `json:"version"`
	Channel     string      `json:"channel"`
	Title       string      `json:"title,omitempty"`
	Notes       string      `json:"notes,omitempty"`
	PublishedAt time.Time   `json:"publishedAt"`
	Prerelease  bool        `json:"prerelease"`
	Mandatory   bool        `json:"mandatory"`
	MinVersion  string      `json:"minVersion,omitempty"`
	Artifacts   []*Artifact `json:"artifacts"`

	Dir string `json:"-"` // absolute directory of this version
}

// Artifact is a single downloadable file of a release.
type Artifact struct {
	File      string    `json:"file"`           // path relative to the version directory, "/"-separated
	Name      string    `json:"name,omitempty"` // display name
	OS        string    `json:"os,omitempty"`   // canonical OS id
	Arch      string    `json:"arch,omitempty"` // canonical architecture id
	Kind      string    `json:"kind,omitempty"` // installer | portable | archive | package | image | other
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256,omitempty"`
	External  string    `json:"externalUrl,omitempty"` // download hosted elsewhere
	Path      string    `json:"downloadPath,omitempty"`
	Modified  time.Time `json:"modified,omitempty"`
	Available bool      `json:"available"`
	Detected  bool      `json:"detected,omitempty"` // true when os/arch came from the file name
}

// IconFile returns the icon's path inside the application directory, or "".
func (a *App) IconFile() string { return a.iconSrc }

// Artifact returns the artifact whose relative file path matches, or nil.
func (r *Release) Artifact(file string) *Artifact {
	for _, a := range r.Artifacts {
		if a.File == file {
			return a
		}
	}
	return nil
}

// Matches reports whether the artifact can run on the given os/arch pair.
// "universal" and "any" architectures match everything, and an artifact with an
// unknown architecture is offered as a last resort.
func (a *Artifact) Matches(os, arch string) bool {
	if os != "" && a.OS != "" && a.OS != os && a.OS != "any" {
		return false
	}
	if arch != "" && a.Arch != "" && a.Arch != arch && a.Arch != "universal" {
		return false
	}
	return true
}

// Best picks the artifact that fits the requested platform best: an exact
// os/arch match wins over a universal build, which wins over an unclassified
// file. Returns nil when nothing matches.
func (r *Release) Best(os, arch string) *Artifact {
	score := func(a *Artifact) int {
		s := 0
		if os == "" || a.OS == os {
			s += 4
		}
		if arch == "" || a.Arch == arch {
			s += 2
		} else if a.Arch == "universal" {
			s++
		}
		if a.Available {
			s += 8
		}
		if a.External == "" {
			s++ // local files are preferred over external redirects
		}
		return s
	}
	var best *Artifact
	bestScore := -1
	for _, a := range r.Artifacts {
		if !a.Matches(os, arch) {
			continue
		}
		if s := score(a); s > bestScore {
			best, bestScore = a, s
		}
	}
	return best
}

// Filter returns the artifacts matching an os/arch pair.
func (r *Release) Filter(os, arch string) []*Artifact {
	out := make([]*Artifact, 0, len(r.Artifacts))
	for _, a := range r.Artifacts {
		if a.Matches(os, arch) {
			out = append(out, a)
		}
	}
	return out
}

// Snapshot is an immutable view of the whole archive. It is swapped atomically
// so HTTP handlers never block on a running scan.
type Snapshot struct {
	BuiltAt  time.Time `json:"builtAt"`
	AppsDir  string    `json:"appsDir"`
	Apps     []*App    `json:"apps"`
	Warnings []string  `json:"warnings,omitempty"`

	byID map[string]*App
}

// App looks an application up by id.
func (s *Snapshot) App(id string) (*App, bool) {
	a, ok := s.byID[id]
	return a, ok
}

// Release looks a version up inside an application. The special version
// "latest" resolves to the newest release of the requested channel.
func (a *App) Release(version string) (*Release, bool) {
	if version == "latest" {
		return a.LatestRelease(a.Channel), true
	}
	for _, r := range a.Releases {
		if r.Version == version || strings.TrimPrefix(r.Version, "v") == strings.TrimPrefix(version, "v") {
			return r, true
		}
	}
	return nil, false
}

// LatestRelease returns the highest version of a channel, or nil when the
// channel has no releases. Ordering follows semantic versioning, so a
// pre-release such as 1.3.0-rc.1 outranks 1.2.0 but loses to 1.3.0.
func (a *App) LatestRelease(channel string) *Release {
	if channel == "" {
		channel = a.Channel
	}
	for _, r := range a.Releases {
		if r.Channel == channel {
			return r
		}
	}
	return nil
}

// NormalizeChannel lower-cases a channel name. The aliases "latest", "default"
// and "release" mean "use the application default channel".
func NormalizeChannel(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "latest", "default", "release":
		return ""
	}
	return s
}

// PickLatest returns the newest release of a channel. Releases are already
// sorted newest first, so this is a filter, not a comparison. When prerelease
// is false, pre-releases are skipped entirely — which means a channel that only
// ships pre-releases yields nil and the caller should treat the client as up to
// date.
func PickLatest(app *App, channel string, prerelease bool) *Release {
	if channel == "" {
		channel = app.Channel
	}
	for _, r := range app.Releases {
		if r.Channel != channel {
			continue
		}
		if r.Prerelease && !prerelease {
			continue
		}
		return r
	}
	return nil
}

// Channels lists the distinct channels an application publishes, default first.
func (a *App) Channels() []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(c string) {
		if c != "" && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	add(a.Channel)
	for _, r := range a.Releases {
		add(r.Channel)
	}
	return out
}

// sortReleases orders releases newest first and fills the Latest map.
func (a *App) finalize() {
	if len(a.Releases) == 0 {
		return
	}
	// Insertion sort: release counts are tiny and this keeps the code stable.
	for i := 1; i < len(a.Releases); i++ {
		for j := i; j > 0 && semver.Compare(a.Releases[j].Version, a.Releases[j-1].Version) > 0; j-- {
			a.Releases[j], a.Releases[j-1] = a.Releases[j-1], a.Releases[j]
		}
	}
	a.Latest = map[string]string{}
	for _, r := range a.Releases {
		if _, ok := a.Latest[r.Channel]; !ok {
			a.Latest[r.Channel] = r.Version
		}
		if r.PublishedAt.After(a.UpdatedAt) {
			a.UpdatedAt = r.PublishedAt
		}
	}
	a.ReleaseCount = len(a.Releases)
	a.Platforms = a.detectPlatforms()
}

func (a *App) detectPlatforms() []string {
	seen := map[string]bool{}
	out := []string{}
	for _, p := range a.Platforms {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, r := range a.Releases {
		for _, art := range r.Artifacts {
			if art.OS != "" && !seen[art.OS] {
				seen[art.OS] = true
				out = append(out, art.OS)
			}
		}
	}
	return out
}
