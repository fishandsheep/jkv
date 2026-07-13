package catalog

import (
	"context"
	"testing"
)

func TestVersionLess(t *testing.T) {
	for _, tc := range []struct{ a, b string }{
		{"8.9", "8.10"}, {"17.0.9", "17.0.10"}, {"21.0.1", "25.0.0"},
	} {
		if !versionLess(tc.a, tc.b) {
			t.Fatalf("expected %s < %s", tc.a, tc.b)
		}
	}
}

func TestLinks(t *testing.T) {
	got := links([]byte(`<a href="one.zip">x</a><a href=two.tar.gz>y</a>`))
	if len(got) != 2 || got[0] != "one.zip" || got[1] != "two.tar.gz" {
		t.Fatalf("links = %#v", got)
	}
}

func TestDragonwellLive(t *testing.T) {
	if testing.Short() {
		t.Skip("live mirror test")
	}
	r, err := NewClient().dragonwell(context.Background(), Platform{OS: "linux", Arch: "x64"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r) < 4 {
		t.Fatalf("expected Dragonwell releases, got %#v", r)
	}
}
