package store

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestUnzipRejectsTraversal(t *testing.T) {
	d := t.TempDir()
	archive := filepath.Join(d, "bad.zip")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("../escaped")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("bad"))
	_ = zw.Close()
	_ = f.Close()

	if err := unzip(archive, filepath.Join(d, "out")); err == nil {
		t.Fatal("expected path traversal rejection")
	}
	if _, err := os.Stat(filepath.Join(d, "escaped")); !os.IsNotExist(err) {
		t.Fatal("archive escaped destination")
	}
}

func TestDefaults(t *testing.T) {
	s := New(t.TempDir())
	if err := os.MkdirAll(s.CandidateDir("java", "21-tem"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDefault("java", "21-tem"); err != nil {
		t.Fatal(err)
	}
	d, err := s.Defaults()
	if err != nil || d["java"] != "21-tem" {
		t.Fatalf("defaults=%v err=%v", d, err)
	}
}
