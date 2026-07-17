package store

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"jkv/internal/catalog"
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

func TestInstallReusesDownloadedArchive(t *testing.T) {
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	w, err := zw.Create("jdk/bin/java")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, "java"); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write(archive.Bytes())
	}))
	defer server.Close()

	s := New(t.TempDir())
	r := catalog.Release{Candidate: "java", Version: "21-test", Vendor: "test", URL: server.URL + "/jdk.zip"}
	if err := s.Install(context.Background(), r, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(r.Candidate, r.Version); err != nil {
		t.Fatal(err)
	}
	var progress bytes.Buffer
	if err := s.Install(context.Background(), r, &progress); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("download requests = %d", requests.Load())
	}
	if !strings.Contains(progress.String(), "使用本地下载缓存") {
		t.Fatalf("progress = %q", progress.String())
	}
}

func TestCatalogCacheAndClean(t *testing.T) {
	s := New(t.TempDir())
	p := catalog.Platform{OS: "linux", Arch: "x64"}
	want := CatalogCache{
		FetchedAt: time.Now(),
		Releases:  []catalog.Release{{Candidate: "java", Version: "21-test", URL: "https://example.com/jdk.zip"}},
	}
	if err := s.SaveCatalog(p, "java", want); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadCatalog(p, "java")
	if err != nil || len(got.Releases) != 1 || got.Releases[0].Version != "21-test" {
		t.Fatalf("cache=%#v err=%v", got, err)
	}
	result, err := s.CleanCache("catalog", "java", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 1 || result.Bytes == 0 {
		t.Fatalf("clean result = %#v", result)
	}
	if _, err := s.LoadCatalog(p, "java"); !os.IsNotExist(err) {
		t.Fatalf("cache remains: %v", err)
	}
}
