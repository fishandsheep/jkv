package catalog

import "testing"

func TestVersionLess(t *testing.T) {
	for _, tc := range []struct{ a, b string }{
		{"8.9", "8.10"}, {"17.0.9", "17.0.10"}, {"21.0.1", "25.0.0"},
	} {
		if !versionLess(tc.a, tc.b) {
			t.Fatalf("expected %s < %s", tc.a, tc.b)
		}
	}
}

func TestStableVersion(t *testing.T) {
	stable := []string{"21.0.8", "3.9.11", "8.14.3"}
	unstable := []string{"22-ea.1", "9.0-rc1", "5.0.0-M1", "1.0-SNAPSHOT", "2.0-alpha", "3.0-beta", "4.0-milestone"}
	for _, version := range stable {
		if !stableVersion(version) {
			t.Errorf("stableVersion(%q) = false, want true", version)
		}
	}
	for _, version := range unstable {
		if stableVersion(version) {
			t.Errorf("stableVersion(%q) = true, want false", version)
		}
	}
}
