package semver

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		ok   bool
		pre  bool
		want string
	}{
		{"1.2.3", true, false, "1.2.3"},
		{"v1.2.3", true, false, "v1.2.3"},
		{"1.2", true, false, "1.2"},
		{"2026.10.03", true, false, "2026.10.03"},
		{"1.2.3-rc.1", true, true, "1.2.3-rc.1"},
		{"1.2.3+build.5", true, false, "1.2.3+build.5"},
		{"v2.0.0_beta", true, true, "v2.0.0_beta"},
		{"nightly", false, false, ""},
		{"1.x.0", false, false, ""},
		{"", false, false, ""},
	}
	for _, c := range cases {
		v, ok := Parse(c.in)
		if ok != c.ok {
			t.Errorf("Parse(%q) ok = %v, want %v", c.in, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if got := len(v.Pre) > 0; got != c.pre {
			t.Errorf("Parse(%q) prerelease = %v, want %v", c.in, got, c.pre)
		}
	}
}

func TestCompare(t *testing.T) {
	// Each group must be strictly ascending.
	ordered := []string{
		"0.9.0",
		"1.0.0-alpha",
		"1.0.0-alpha.1",
		"1.0.0-beta",
		"1.0.0-rc.1",
		"1.0.0",
		"1.0.1",
		"1.2",
		"1.10.0",
		"2.0.0",
	}
	for i := 0; i < len(ordered); i++ {
		for j := 0; j < len(ordered); j++ {
			got := Compare(ordered[i], ordered[j])
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got != want {
				t.Errorf("Compare(%q, %q) = %d, want %d", ordered[i], ordered[j], got, want)
			}
		}
	}
}

func TestCompareEqual(t *testing.T) {
	for _, pair := range [][2]string{
		{"1.2.3", "v1.2.3"},
		{"1.2", "1.2.0"},
		{"1.0.0+build1", "1.0.0+build2"},
	} {
		if got := Compare(pair[0], pair[1]); got != 0 {
			t.Errorf("Compare(%q, %q) = %d, want 0", pair[0], pair[1], got)
		}
	}
}

func TestIsVersionLike(t *testing.T) {
	for _, s := range []string{"1", "1.2.3", "v1.0", "2026.10.03", "1.2.3-rc.1", "1.0.0-beta_2"} {
		if !IsVersionLike(s) {
			t.Errorf("IsVersionLike(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "latest", "releases", "1.2.3 notes", "..", "v"} {
		if IsVersionLike(s) {
			t.Errorf("IsVersionLike(%q) = true, want false", s)
		}
	}
}
