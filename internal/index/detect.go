package index

import (
	"path"
	"regexp"
	"strings"
)

// Canonical OS identifiers used throughout the site and the API.
const (
	OSWindows = "windows"
	OSLinux   = "linux"
	OSMacOS   = "macos"
	OSAndroid = "android"
	OSIOS     = "ios"
	OSWeb     = "web"
)

// Canonical architecture identifiers used throughout the site and the API.
const (
	ArchX64       = "x64"
	ArchX86       = "x86"
	ArchARM64     = "arm64"
	ArchARM32     = "arm32"
	ArchUniversal = "universal"
)

// osAliases maps a token to a canonical OS id.
var osAliases = map[string]string{
	"windows": OSWindows, "win": OSWindows, "winnt": OSWindows,
	"mswindows": OSWindows, "mingw": OSWindows, "cygwin": OSWindows,
	"linux": OSLinux, "linuxgnu": OSLinux, "linuxmusl": OSLinux, "gnu": OSLinux,
	"macos": OSMacOS, "mac": OSMacOS, "macosx": OSMacOS, "macos64": OSMacOS,
	"android": OSAndroid, "apk": OSAndroid,
	"ios": OSIOS, "iphoneos": OSIOS, "ipados": OSIOS,
	"web": OSWeb, "wasm": OSWeb, "webassembly": OSWeb,
	"freebsd": "freebsd", "openbsd": "openbsd", "netbsd": "netbsd",
}

// archAliases maps a token to a canonical architecture id.
var archAliases = map[string]string{
	"x64": ArchX64, "amd64": ArchX64,
	"x86": ArchX86, "i386": ArchX86, "i486": ArchX86, "i586": ArchX86,
	"i686": ArchX86, "386": ArchX86, "ia32": ArchX86, "win32": ArchX86,
	"arm64": ArchARM64, "aarch64": ArchARM64, "arm64e": ArchARM64,
	"armv8": ArchARM64, "armv8l": ArchARM64, "arm64v8": ArchARM64,
	"arm": ArchARM32, "arm32": ArchARM32, "armv6": ArchARM32, "armv6l": ArchARM32,
	"armv7": ArchARM32, "armv7l": ArchARM32, "armhf": ArchARM32,
	"armeabi": ArchARM32, "armeabiv7a": ArchARM32,
	"universal": ArchUniversal, "noarch": ArchUniversal,
	"all": ArchUniversal, "any": ArchUniversal, "fat": ArchUniversal,
	"riscv64": "riscv64", "s390x": "s390x", "ppc64le": "ppc64le", "mips64": "mips64",
}

// compounds collapses multi-token spellings into a single token before the
// name is split, so "x86_64" never degrades into "x86" + "64".
var compounds = strings.NewReplacer(
	"x86_64", "x64",
	"x86-64", "x64",
	"x86.64", "x64",
	"amd64", "amd64",
	"aarch64", "arm64",
	"arm64e", "arm64",
	"universal2", "universal",
	"armv7l", "armv7",
	"armv6l", "armv6",
	"armv8l", "arm64",
	"macosx", "macos",
	"osx", "macos",
	"darwin", "macos",
	"iphoneos", "ios",
	"wasm32", "web",
	"64bit", "x64",
	"32bit", "x86",
	"64-bit", "x64",
	"32-bit", "x86",
)

var (
	tokenSplit = regexp.MustCompile(`[^a-z0-9]+`)
	versionDir = regexp.MustCompile(`^[vV]?\d+(\.\d+)*([-_+][0-9A-Za-z.\-+]*)?$`)
)

// extensions maps a file extension to the artifact kind.
var extensions = map[string]string{
	".exe": "installer", ".msi": "installer", ".msix": "installer", ".msu": "installer",
	".dmg": "installer", ".pkg": "installer", ".mpkg": "installer",
	".apk": "package", ".aab": "package", ".ipa": "package",
	".deb": "package", ".rpm": "package",
	".snap": "package", ".flatpak": "package", ".flatpakref": "package",
	".jar": "package", ".war": "package", ".aar": "package",
	".appimage": "portable", ".app": "portable",
	".zip": "archive", ".7z": "archive", ".rar": "archive",
	".tar": "archive", ".gz": "archive", ".tgz": "archive", ".bz2": "archive",
	".xz": "archive", ".zst": "archive", ".tar.gz": "archive", ".tar.xz": "archive",
	".tar.bz2": "archive", ".tar.zst": "archive",
	".iso": "image", ".img": "image", ".vhd": "image",
}

// compoundExts must be probed before the plain extension.
var compoundExts = []string{".tar.gz", ".tar.xz", ".tar.bz2", ".tar.zst"}

// extOS infers the target OS from a file extension when the name carries no
// explicit OS token.
var extOS = map[string]string{
	".exe": OSWindows, ".msi": OSWindows, ".msix": OSWindows, ".msu": OSWindows,
	".dmg": OSMacOS, ".pkg": OSMacOS, ".mpkg": OSMacOS, ".app": OSMacOS,
	".apk": OSAndroid, ".aab": OSAndroid,
	".ipa": OSIOS,
	".deb": OSLinux, ".rpm": OSLinux, ".appimage": OSLinux, ".snap": OSLinux,
	".flatpak": OSLinux,
}

// NormalizeOS maps an arbitrary OS spelling (query parameter or manifest value)
// to its canonical id. Unknown values are returned lower-cased.
func NormalizeOS(s string) string {
	key := tokenKey(s)
	if key == "" {
		return ""
	}
	if v, ok := osAliases[key]; ok {
		return v
	}
	// "win64" and "linux32" carry the OS and the bit width in one word.
	if o, _, ok := splitOSArch(key); ok {
		return o
	}
	return key
}

// NormalizeArch maps an arbitrary architecture spelling to its canonical id.
func NormalizeArch(s string) string {
	key := tokenKey(s)
	if key == "" {
		return ""
	}
	if v, ok := archAliases[key]; ok {
		return v
	}
	if _, a, ok := splitOSArch(key); ok {
		return a
	}
	return key
}

// NormalizeKind maps a kind spelling to one of the known kinds.
func NormalizeKind(s string) string {
	switch k := strings.ToLower(strings.TrimSpace(s)); k {
	case "installer", "setup":
		return "installer"
	case "portable", "green":
		return "portable"
	case "archive", "zip":
		return "archive"
	case "package", "pkg":
		return "package"
	case "image", "iso":
		return "image"
	case "other", "":
		return "other"
	default:
		return k
	}
}

// tokenKey normalises a single spelling for alias lookup: lower case, compound
// spellings collapsed, separators removed.
func tokenKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = compounds.Replace(s)
	return tokenSplit.ReplaceAllString(s, "")
}

// Extension returns the extension of a file name, preferring compound ones such
// as ".tar.gz". The result is lower case and includes the leading dot.
func Extension(name string) string {
	lower := strings.ToLower(name)
	for _, e := range compoundExts {
		if strings.HasSuffix(lower, e) {
			return e
		}
	}
	return path.Ext(lower)
}

// KindFor returns the artifact kind implied by a file name.
func KindFor(name string) string {
	if k, ok := extensions[Extension(name)]; ok {
		return k
	}
	return "other"
}

// Detect guesses the os/arch/kind of an artifact from its file name. The
// returned os and arch are canonical ids, or "" when the name carries no hint.
//
// Tokens are searched anywhere in the name, so "App-1.2.3-windows-x64.exe",
// "app_1.2.3_win_x86_64.msi" and "App-1.2.3-linux-arm64.tar.gz" all work.
func Detect(name string) (os, arch, kind string) {
	stem := strings.TrimSuffix(name, Extension(name))
	// Compound spellings must be collapsed on the whole name, before the
	// separators inside them ("x86_64") are used to split it into tokens.
	tokens := SplitName(compounds.Replace(strings.ToLower(stem)))

	for _, tok := range tokens {
		if tok == "" {
			continue
		}
		key := tokenKey(tok)

		// "win64" / "linux32": one token carrying both facts.
		if o, a, ok := splitOSArch(key); ok {
			if os == "" {
				os = o
			}
			if arch == "" {
				arch = a
			}
			continue
		}
		if arch == "" {
			if a, ok := archAliases[key]; ok {
				arch = a
			}
		}
		if os == "" {
			if o, ok := osAliases[key]; ok {
				os = o
			}
		}
	}

	// A bare "64" or "32" token is the last architectural resort.
	if arch == "" {
		for _, tok := range tokens {
			switch tok {
			case "64":
				arch = ArchX64
			case "32":
				arch = ArchX86
			}
		}
	}

	kind = KindFor(name)
	if os == "" {
		os = extOS[Extension(name)]
	}
	if arch == "" {
		switch os {
		case OSAndroid, OSIOS:
			arch = ArchUniversal // one APK usually serves every phone
		case OSWindows, OSMacOS:
			arch = ArchX64 // the overwhelmingly common desktop target
		}
	}
	return os, arch, kind
}

// splitOSArch recognises tokens that combine an OS and a bit width in one word,
// such as "win64", "win32" or "linux64".
func splitOSArch(key string) (os, arch string, ok bool) {
	for _, suffix := range []struct{ s, arch string }{
		{"64", ArchX64}, {"32", ArchX86},
	} {
		if !strings.HasSuffix(key, suffix.s) {
			continue
		}
		base := strings.TrimSuffix(key, suffix.s)
		if base == "" {
			continue
		}
		if o, hit := osAliases[base]; hit {
			return o, suffix.arch, true
		}
	}
	return "", "", false
}

// SplitName splits a file name on every non alphanumeric separator and lower
// cases the result.
func SplitName(name string) []string {
	return tokenSplit.Split(strings.ToLower(name), -1)
}

// IsVersionDir reports whether a directory name can be used as a version.
func IsVersionDir(name string) bool {
	return versionDir.MatchString(name)
}
