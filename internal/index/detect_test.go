package index

import "testing"

func TestDetect(t *testing.T) {
	cases := []struct {
		name string
		os   string
		arch string
		kind string
	}{
		{"MyApp-1.2.3-windows-x64.exe", OSWindows, ArchX64, "installer"},
		{"MyApp-1.2.3-win64-setup.exe", OSWindows, ArchX64, "installer"},
		{"app_1.2.3_win_x86_64.msi", OSWindows, ArchX64, "installer"},
		{"App-1.0.0-windows-386.zip", OSWindows, ArchX86, "archive"},
		{"App-1.0.0-win32.zip", OSWindows, ArchX86, "archive"},
		{"app-2.0.0-linux-amd64.tar.gz", OSLinux, ArchX64, "archive"},
		{"app-2.0.0-linux-arm64.deb", OSLinux, ArchARM64, "package"},
		{"app-2.0.0-linux-armv7l.rpm", OSLinux, ArchARM32, "package"},
		{"App-3.0.0-macos-universal.dmg", OSMacOS, ArchUniversal, "installer"},
		{"App-3.0.0-darwin-arm64.pkg", OSMacOS, ArchARM64, "installer"},
		{"App-3.0.0-osx-x86_64.dmg", OSMacOS, ArchX64, "installer"},
		{"MyApp-1.0.0-release.apk", OSAndroid, ArchUniversal, "package"},
		{"MyApp-1.0.0.ipa", OSIOS, ArchUniversal, "package"},
		{"tool-1.0.0-linux-x86_64.AppImage", OSLinux, ArchX64, "portable"},
		{"tool-1.0.0-unknown-file.bin", "", "", "other"},
		{"service-1.0.0-linux-aarch64.tar.xz", OSLinux, ArchARM64, "archive"},
		{"win-tool.exe", OSWindows, ArchX64, "installer"},
		{"data-only.zip", "", "", "archive"},
	}
	for _, c := range cases {
		os, arch, kind := Detect(c.name)
		if os != c.os || arch != c.arch || kind != c.kind {
			t.Errorf("Detect(%q) = (%q, %q, %q), want (%q, %q, %q)",
				c.name, os, arch, kind, c.os, c.arch, c.kind)
		}
	}
}

func TestNormalizeOS(t *testing.T) {
	for in, want := range map[string]string{
		"Windows": OSWindows, "WIN": OSWindows, "win64": OSWindows,
		"Win32": OSWindows, "macOS": OSMacOS, "darwin": OSMacOS, "osx": OSMacOS,
		"Mac OS X": OSMacOS, "linux": OSLinux, "Linux64": OSLinux,
		"android": OSAndroid, "freebsd": "freebsd",
	} {
		if got := NormalizeOS(in); got != want {
			t.Errorf("NormalizeOS(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeArch(t *testing.T) {
	for in, want := range map[string]string{
		"aarch64": ArchARM64, "amd64": ArchX64, "x86_64": ArchX64,
		"x86-64": ArchX64, "X64": ArchX64, "i686": ArchX86,
		"armv7l": ArchARM32, "noarch": ArchUniversal, "universal2": ArchUniversal,
		"win64": ArchX64, "linux32": ArchX86, "riscv64": "riscv64",
	} {
		if got := NormalizeArch(in); got != want {
			t.Errorf("NormalizeArch(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeKind(t *testing.T) {
	for in, want := range map[string]string{
		"installer": "installer", "Setup": "installer", "portable": "portable",
		"": "other", "whatever": "whatever",
	} {
		if got := NormalizeKind(in); got != want {
			t.Errorf("NormalizeKind(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsVersionDir(t *testing.T) {
	for _, s := range []string{"1.0.0", "v1.2", "2026.10.03", "1.2.3-rc.1", "1.0.0+build"} {
		if !IsVersionDir(s) {
			t.Errorf("IsVersionDir(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"latest", "tmp", "1.2.3 notes", ".git", "v", ""} {
		if IsVersionDir(s) {
			t.Errorf("IsVersionDir(%q) = true, want false", s)
		}
	}
}

func TestExtension(t *testing.T) {
	for in, want := range map[string]string{
		"a.tar.gz": ".tar.gz", "a.TAR.GZ": ".tar.gz", "a.zip": ".zip",
		"a.exe": ".exe", "noext": "", "a.tar.zst": ".tar.zst",
	} {
		if got := Extension(in); got != want {
			t.Errorf("Extension(%q) = %q, want %q", in, got, want)
		}
	}
}
