package store

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fishandsheep/jkv/internal/catalog"
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

func TestUnzipRejectsExpandedSizeLimit(t *testing.T) {
	d := t.TempDir()
	archive := filepath.Join(d, "large.zip")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("tool/data")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(w, "0123456789")
	_ = zw.Close()
	_ = f.Close()

	limits := archiveLimits{MaxFiles: 10, MaxFileBytes: 5, MaxTotalBytes: 5}
	err = unzipWithLimits(archive, filepath.Join(d, "out"), limits)
	if err == nil || !strings.Contains(err.Error(), "展开上限") {
		t.Fatalf("size limit error = %v", err)
	}
}

func TestUntarRejectsSpecialFiles(t *testing.T) {
	d := t.TempDir()
	archive := filepath.Join(d, "special.tar.gz")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "tool/device", Typeflag: tar.TypeChar, Mode: 0o600}); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	_ = f.Close()

	err = untar(archive, filepath.Join(d, "out"))
	if err == nil || !strings.Contains(err.Error(), "特殊文件") {
		t.Fatalf("special file error = %v", err)
	}
}

func TestUntarRejectsEscapingSymlink(t *testing.T) {
	d := t.TempDir()
	archive := writeTarGz(t, d, []tar.Header{{
		Name: "tool/link", Typeflag: tar.TypeSymlink, Linkname: "../../outside",
	}})
	err := untar(archive, filepath.Join(d, "out"))
	if err == nil || !strings.Contains(err.Error(), "越界链接") {
		t.Fatalf("symlink error = %v", err)
	}
}

func TestUntarRejectsInvalidHardlink(t *testing.T) {
	d := t.TempDir()
	archive := writeTarGz(t, d, []tar.Header{{
		Name: "tool/link", Typeflag: tar.TypeLink, Linkname: "tool/missing",
	}})
	err := untar(archive, filepath.Join(d, "out"))
	if err == nil || !strings.Contains(err.Error(), "无效硬链接") {
		t.Fatalf("hardlink error = %v", err)
	}
}

func TestUntarRejectsExpandedSizeLimit(t *testing.T) {
	d := t.TempDir()
	archive := writeTarGz(t, d, []tar.Header{{
		Name: "tool/data", Typeflag: tar.TypeReg, Mode: 0o600, Size: 10,
	}})
	err := untarWithLimits(archive, filepath.Join(d, "out"), archiveLimits{
		MaxFiles: 10, MaxFileBytes: 5, MaxTotalBytes: 5,
	})
	if err == nil || !strings.Contains(err.Error(), "展开上限") {
		t.Fatalf("size limit error = %v", err)
	}
}

func TestUnzipRejectsDuplicateEntries(t *testing.T) {
	d := t.TempDir()
	archive := filepath.Join(d, "duplicate.zip")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for range 2 {
		w, createErr := zw.Create("tool/data")
		if createErr != nil {
			t.Fatal(createErr)
		}
		_, _ = io.WriteString(w, "data")
	}
	_ = zw.Close()
	_ = f.Close()
	if err := unzip(archive, filepath.Join(d, "out")); err == nil {
		t.Fatal("expected duplicate entry rejection")
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

func TestConcurrentDefaultsPreserveBothCandidates(t *testing.T) {
	s := New(t.TempDir())
	for candidate, version := range map[string]string{"java": "21-tem", "maven": "3.9.11"} {
		if err := os.MkdirAll(s.CandidateDir(candidate, version), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for candidate, version := range map[string]string{"java": "21-tem", "maven": "3.9.11"} {
		candidate, version := candidate, version
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- s.SetDefault(candidate, version)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	defaults, err := s.Defaults()
	if err != nil {
		t.Fatal(err)
	}
	if defaults["java"] != "21-tem" || defaults["maven"] != "3.9.11" {
		t.Fatalf("defaults = %#v", defaults)
	}
}

func TestLegacyDefaultsMigrateWithBackup(t *testing.T) {
	s := New(t.TempDir())
	if err := os.MkdirAll(s.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := []byte("{\"java\":\"21-tem\"}\n")
	if err := os.WriteFile(s.defaultsPath(), legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(s.CandidateDir("maven", "3.9.11"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDefault("maven", "3.9.11"); err != nil {
		t.Fatal(err)
	}
	defaults, err := s.Defaults()
	if err != nil {
		t.Fatal(err)
	}
	if defaults["java"] != "21-tem" || defaults["maven"] != "3.9.11" {
		t.Fatalf("migrated defaults = %#v", defaults)
	}
	backup, err := os.ReadFile(s.defaultsPath() + ".v0.backup")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(backup, legacy) {
		t.Fatalf("backup = %q", backup)
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

func TestInstallIsIdempotent(t *testing.T) {
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	w, err := zw.Create("tool/bin/tool")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(w, "tool")
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
	release := catalog.Release{Candidate: "maven", Version: "1.0", URL: server.URL + "/tool.zip"}
	if err := s.Install(context.Background(), release, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := s.Install(context.Background(), release, io.Discard); err != nil {
		t.Fatalf("repeated install: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("download requests = %d", requests.Load())
	}
}

func TestRepairRestoresInstalledVersionFromCache(t *testing.T) {
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	w, err := zw.Create("tool/bin/tool")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(w, "healthy")
	_ = zw.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive.Bytes())
	}))
	defer server.Close()

	s := New(t.TempDir())
	release := catalog.Release{Candidate: "maven", Version: "1.0", URL: server.URL + "/tool.zip"}
	if err := s.Install(context.Background(), release, io.Discard); err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(s.CandidateDir("maven", "1.0"), "bin", "tool")
	if err := os.WriteFile(tool, []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Repair(context.Background(), release, io.Discard); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(tool)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "healthy" {
		t.Fatalf("repaired tool = %q", got)
	}
}

func TestRepairFailurePreservesInstalledVersion(t *testing.T) {
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	w, _ := zw.Create("tool/bin/tool")
	_, _ = io.WriteString(w, "healthy")
	_ = zw.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/good.zip" {
			_, _ = w.Write(archive.Bytes())
			return
		}
		_, _ = io.WriteString(w, "not a zip")
	}))
	defer server.Close()

	s := New(t.TempDir())
	release := catalog.Release{Candidate: "maven", Version: "1.0", URL: server.URL + "/good.zip"}
	if err := s.Install(context.Background(), release, io.Discard); err != nil {
		t.Fatal(err)
	}
	bad := release
	bad.URL = server.URL + "/bad.zip"
	if err := s.Repair(context.Background(), bad, io.Discard); err == nil {
		t.Fatal("expected repair failure")
	}
	got, err := os.ReadFile(filepath.Join(s.CandidateDir("maven", "1.0"), "bin", "tool"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "healthy" {
		t.Fatalf("existing install changed to %q", got)
	}
}

func TestInstallStrictChecksumRejectsBeforeDownload(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()

	s := New(t.TempDir())
	release := catalog.Release{Candidate: "maven", Version: "1.0", URL: server.URL + "/tool.zip"}
	err := s.InstallWithOptions(context.Background(), release, io.Discard, InstallOptions{RequireChecksum: true})
	if err == nil || !strings.Contains(err.Error(), "要求 SHA-256") {
		t.Fatalf("strict install error = %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("download started before strict checksum rejection")
	}
}

func TestConcurrentInstallSameVersionIsSafe(t *testing.T) {
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	w, err := zw.Create("tool/bin/tool")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(w, "tool")
	_ = zw.Close()

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		time.Sleep(25 * time.Millisecond)
		_, _ = w.Write(archive.Bytes())
	}))
	defer server.Close()

	s := New(t.TempDir())
	release := catalog.Release{Candidate: "maven", Version: "1.0", URL: server.URL + "/tool.zip"}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- s.Install(context.Background(), release, io.Discard)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent install: %v", err)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("download requests = %d", requests.Load())
	}
}

func TestDownloadResumesAfterInterruptedResponse(t *testing.T) {
	payload := []byte("0123456789")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := requests.Add(1)
		if request == 1 {
			w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
			_, _ = w.Write(payload[:5])
			return
		}
		if got := r.Header.Get("Range"); got != "bytes=5-" {
			t.Errorf("Range = %q", got)
		}
		w.Header().Set("Content-Range", "bytes 5-9/10")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[5:])
	}))
	defer server.Close()

	s := New(t.TempDir())
	path := filepath.Join(t.TempDir(), "archive.partial")
	got, err := s.download(context.Background(), server.URL+"/archive.zip", path, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	wantHash := sha256.Sum256(payload)
	if got != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("hash = %s", got)
	}
	gotPayload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotPayload, payload) {
		t.Fatalf("payload = %q", gotPayload)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestDownloadStopsAfterNoProgress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "10")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		time.Sleep(500 * time.Millisecond)
	}))
	defer server.Close()

	s := New(t.TempDir())
	s.RetryMax = 1
	s.NoProgressTimeout = 50 * time.Millisecond
	started := time.Now()
	_, err := s.download(context.Background(), server.URL+"/stalled.zip", filepath.Join(t.TempDir(), "partial"), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "无下载进度") {
		t.Fatalf("stalled download error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 300*time.Millisecond {
		t.Fatalf("stalled download took %s", elapsed)
	}
}

func TestVerifyChecksumRejectsNonHexDigest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("z", 64)+"  archive.zip\n")
	}))
	defer server.Close()
	s := New(t.TempDir())
	err := s.verifyChecksum(context.Background(), server.URL+"/archive.zip.sha256", strings.Repeat("z", 64))
	if err == nil || !strings.Contains(err.Error(), "无效 SHA-256") {
		t.Fatalf("checksum error = %v", err)
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

func TestRejectsFutureStateSchemas(t *testing.T) {
	s := New(t.TempDir())
	p := catalog.Platform{OS: "linux", Arch: "x64"}
	catalogPath := s.catalogCachePath(p, "java")
	if err := os.MkdirAll(filepath.Dir(catalogPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalogPath, []byte(`{"schema_version":999,"releases":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadCatalog(p, "java"); err == nil {
		t.Fatal("future catalog schema accepted")
	}
	installDir := s.CandidateDir("java", "21")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installDir, ".jkv-release.json"), []byte(
		`{"schema_version":999,"release":{"candidate":"java","version":"21","url":"https://example.test/jdk.zip"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InstalledRelease("java", "21"); err == nil {
		t.Fatal("future install schema accepted")
	}
}

func TestInspectCacheDoesNotDelete(t *testing.T) {
	s := New(t.TempDir())
	path := filepath.Join(s.cacheRoot(), "downloads", "maven", "1.0", "archive")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("cached"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := s.InspectCache("downloads", "maven", "1.0")
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 1 || result.Bytes != 6 {
		t.Fatalf("inspect result = %#v", result)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("dry-run deleted cache: %v", err)
	}
}

func TestCleanCacheByAge(t *testing.T) {
	s := New(t.TempDir())
	oldPath := filepath.Join(s.cacheRoot(), "downloads", "maven", "old", "archive")
	newPath := filepath.Join(s.cacheRoot(), "downloads", "maven", "new", "archive")
	for _, path := range []string{oldPath, newPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("cache"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	preview, err := s.InspectCacheOlderThan("downloads", "maven", "", 24*time.Hour)
	if err != nil || preview.Files != 1 {
		t.Fatalf("age preview = %#v, %v", preview, err)
	}
	result, err := s.CleanCacheOlderThan("downloads", "maven", "", 24*time.Hour)
	if err != nil || result.Files != 1 {
		t.Fatalf("age clean = %#v, %v", result, err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old cache remains: %v", err)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("new cache removed: %v", err)
	}
}

func TestCleanInterruptedDownloadsByAge(t *testing.T) {
	s := New(t.TempDir())
	path := filepath.Join(s.Root, "partials", "downloads", "java", "21", "archive.partial")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(path, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	preview, err := s.InspectPartialsOlderThan("java", "", 24*time.Hour)
	if err != nil || preview.Files != 1 {
		t.Fatalf("partial preview = %#v, %v", preview, err)
	}
	result, err := s.CleanPartialsOlderThan("java", "", 24*time.Hour)
	if err != nil || result.Files != 1 {
		t.Fatalf("partial clean = %#v, %v", result, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("partial remains: %v", err)
	}
}

func TestStoreQueriesAndRemoval(t *testing.T) {
	root := t.TempDir()
	t.Setenv("JKV_DIR", root)
	if got := DefaultRoot(); got != root {
		t.Fatalf("DefaultRoot = %q", got)
	}
	s := New(root)
	javaDir := s.CandidateDir("java", "21-test")
	macHome := filepath.Join(javaDir, "Contents", "Home")
	if err := os.MkdirAll(macHome, 0o755); err != nil {
		t.Fatal(err)
	}
	release := catalog.Release{Candidate: "java", Version: "21-test", URL: "https://example.test/jdk.zip"}
	data, err := json.Marshal(metadata{SchemaVersion: stateSchemaVersion, Release: release, InstalledAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(javaDir, ".jkv-release.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	home, err := s.Home("java", "21-test")
	if err != nil || home != macHome {
		t.Fatalf("Home = %q, %v", home, err)
	}
	installed, err := s.Installed("java")
	if err != nil || len(installed) != 1 || installed[0] != "21-test" {
		t.Fatalf("Installed = %#v, %v", installed, err)
	}
	got, err := s.InstalledRelease("java", "21-test")
	if err != nil || got.URL != release.URL {
		t.Fatalf("InstalledRelease = %#v, %v", got, err)
	}
	if err := s.SetDefault("java", "21-test"); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove("java", "21-test"); err != nil {
		t.Fatal(err)
	}
	defaults, err := s.Defaults()
	if err != nil || defaults["java"] != "" {
		t.Fatalf("defaults after remove = %#v, %v", defaults, err)
	}
	if _, err := s.Home("java", "21-test"); err == nil {
		t.Fatal("removed home still exists")
	}
	if err := s.Remove("java", "missing"); err == nil {
		t.Fatal("missing remove succeeded")
	}
}

func TestCachedReleaseAndVersions(t *testing.T) {
	s := New(t.TempDir())
	release := catalog.Release{Candidate: "maven", Version: "3.9.11", URL: "https://example.test/maven.zip"}
	archive, _, ok := s.archivePaths(release.Candidate, release.Version)
	if !ok {
		t.Fatal("archive path rejected")
	}
	if err := os.MkdirAll(filepath.Dir(archive), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive, []byte("archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum, err := fileSHA256(archive)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.saveArchiveMetadata(release, sum); err != nil {
		t.Fatal(err)
	}
	got, ok := s.CachedRelease("maven", "3.9.11")
	if !ok || got.URL != release.URL {
		t.Fatalf("CachedRelease = %#v, %v", got, ok)
	}
	versions := s.CachedVersions("maven")
	if len(versions) != 1 || versions[0] != "3.9.11" {
		t.Fatalf("CachedVersions = %#v", versions)
	}
	if _, ok := s.CachedRelease("../maven", "3.9.11"); ok {
		t.Fatal("invalid cached release accepted")
	}
	if got := s.CachedVersions("../maven"); got != nil {
		t.Fatalf("invalid cached versions = %#v", got)
	}
}

func TestVerifyChecksumSuccessAndMismatch(t *testing.T) {
	digest := strings.Repeat("a", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "mismatch") {
			_, _ = io.WriteString(w, strings.Repeat("b", 64)+"\n")
			return
		}
		_, _ = io.WriteString(w, digest+"  archive.zip\n")
	}))
	defer server.Close()
	s := New(t.TempDir())
	if err := s.verifyChecksum(context.Background(), server.URL+"/ok.sha256", digest); err != nil {
		t.Fatal(err)
	}
	if err := s.verifyChecksum(context.Background(), server.URL+"/mismatch.sha256", digest); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("mismatch = %v", err)
	}
}

func TestVerifyChecksumRetriesTransientFailure(t *testing.T) {
	digest := strings.Repeat("a", 64)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			http.Error(w, "retry", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, digest+"\n")
	}))
	defer server.Close()
	s := New(t.TempDir())
	s.RetryBase = time.Millisecond
	if err := s.verifyChecksum(context.Background(), server.URL+"/archive.sha256", digest); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("checksum requests = %d", requests.Load())
	}
}

func writeTarGz(t *testing.T, dir string, headers []tar.Header) string {
	t.Helper()
	path := filepath.Join(dir, "fixture.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for i := range headers {
		header := headers[i]
		if err := tw.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeReg && header.Size > 0 {
			if _, err := io.CopyN(tw, strings.NewReader(strings.Repeat("x", int(header.Size))), header.Size); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
