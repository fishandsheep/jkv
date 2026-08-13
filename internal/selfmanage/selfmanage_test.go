//go:build !windows

package selfmanage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestUpdateFromCNBAndNoOp(t *testing.T) {
	root, target := managedFixture(t, []byte("old"))
	if err := SaveReceipt(root, target, nil); err != nil {
		t.Fatal(err)
	}
	asset, err := assetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	newBody := []byte("new binary")
	sum := sha256.Sum256(newBody)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/latest":
			fmt.Fprint(writer, `<svg><text>v0.0.2</text></svg>`)
		case "/cnb/v0.0.2/" + asset:
			_, _ = writer.Write(newBody)
		case "/cnb/v0.0.2/" + asset + ".sha256":
			fmt.Fprintf(writer, "%x  %s\n", sum, asset)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	manager := New(Config{
		Root: root, Executable: target, CurrentVersion: "v0.0.1", HTTP: server.Client(),
		CNBLatestURL: server.URL + "/latest", GitHubLatestURL: server.URL + "/github",
		CNBDownloadBase: server.URL + "/cnb", GitHubDownloadBase: server.URL + "/github-download",
	})
	latest, changed, err := manager.Update(context.Background())
	if err != nil || !changed || latest != "v0.0.2" {
		t.Fatalf("Update = %q, %v, %v", latest, changed, err)
	}
	if got, err := os.ReadFile(target); err != nil || !bytes.Equal(got, newBody) {
		t.Fatalf("target = %q, %v", got, err)
	}

	manager.config.CurrentVersion = "v0.0.2"
	latest, changed, err = manager.Update(context.Background())
	if err != nil || changed || latest != "v0.0.2" {
		t.Fatalf("no-op Update = %q, %v, %v", latest, changed, err)
	}
}

func TestUpdateFallsBackForDiscoveryAndTransfer(t *testing.T) {
	root, target := managedFixture(t, []byte("old"))
	if err := SaveReceipt(root, target, nil); err != nil {
		t.Fatal(err)
	}
	asset, _ := assetName(runtime.GOOS, runtime.GOARCH)
	newBody := []byte("fallback")
	sum := sha256.Sum256(newBody)
	var githubAssetRequests int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/cnb-latest":
			http.Error(writer, "missing", http.StatusNotFound)
		case "/github-latest":
			fmt.Fprint(writer, `{"tag_name":"v0.0.2","draft":false,"prerelease":false}`)
		case "/cnb/v0.0.2/" + asset:
			http.Error(writer, "unavailable", http.StatusServiceUnavailable)
		case "/github/v0.0.2/" + asset:
			githubAssetRequests++
			_, _ = writer.Write(newBody)
		case "/github/v0.0.2/" + asset + ".sha256":
			fmt.Fprintf(writer, "%x  %s\n", sum, asset)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	manager := New(Config{
		Root: root, Executable: target, CurrentVersion: "v0.0.1", HTTP: server.Client(),
		CNBLatestURL: server.URL + "/cnb-latest", GitHubLatestURL: server.URL + "/github-latest",
		CNBDownloadBase: server.URL + "/cnb", GitHubDownloadBase: server.URL + "/github",
	})
	if _, changed, err := manager.Update(context.Background()); err != nil || !changed || githubAssetRequests != 1 {
		t.Fatalf("fallback Update = %v, requests=%d, err=%v", changed, githubAssetRequests, err)
	}
}

func TestUpdateChecksumFailurePreservesBinaryWithoutFallback(t *testing.T) {
	oldBody := []byte("old")
	root, target := managedFixture(t, oldBody)
	if err := SaveReceipt(root, target, nil); err != nil {
		t.Fatal(err)
	}
	asset, _ := assetName(runtime.GOOS, runtime.GOARCH)
	fallbackRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/latest":
			fmt.Fprint(writer, `<svg><text>v0.0.2</text></svg>`)
		case "/cnb/v0.0.2/" + asset:
			fmt.Fprint(writer, "tampered")
		case "/cnb/v0.0.2/" + asset + ".sha256":
			fmt.Fprintf(writer, "%064d  %s\n", 0, asset)
		default:
			fallbackRequests++
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	manager := New(Config{
		Root: root, Executable: target, CurrentVersion: "v0.0.1", HTTP: server.Client(),
		CNBLatestURL: server.URL + "/latest", CNBDownloadBase: server.URL + "/cnb",
		GitHubDownloadBase: server.URL + "/github", GitHubLatestURL: server.URL + "/github-latest",
	})
	if _, _, err := manager.Update(context.Background()); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("Update error = %v", err)
	}
	if fallbackRequests != 0 {
		t.Fatalf("checksum failure used fallback %d times", fallbackRequests)
	}
	if got, err := os.ReadFile(target); err != nil || !bytes.Equal(got, oldBody) {
		t.Fatalf("target changed = %q, %v", got, err)
	}
}

func TestUpdateRejectsRollbackUnmanagedAndConcurrentLock(t *testing.T) {
	root, target := managedFixture(t, []byte("old"))
	if err := SaveReceipt(root, target, nil); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(writer, `<svg><text>v0.0.1</text></svg>`)
	}))
	defer server.Close()
	manager := New(Config{Root: root, Executable: target, CurrentVersion: "v0.0.2", HTTP: server.Client(), CNBLatestURL: server.URL})
	if _, _, err := manager.Update(context.Background()); !errors.Is(err, ErrState) || !strings.Contains(err.Error(), "拒绝回滚") {
		t.Fatalf("rollback error = %v", err)
	}

	unmanaged := New(Config{Root: root, Executable: filepath.Join(root, "elsewhere", "jkv"), CurrentVersion: "v0.0.1"})
	if _, _, err := unmanaged.Update(context.Background()); !errors.Is(err, ErrState) {
		t.Fatalf("unmanaged error = %v", err)
	}

	if err := os.MkdirAll(filepath.Join(root, "locks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "locks", "self-update.lock"), []byte("busy"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, _, err := manager.Update(ctx); !errors.Is(err, ErrState) || !strings.Contains(err.Error(), "锁") {
		t.Fatalf("lock error = %v", err)
	}
}

func TestUninstallUsesReceiptAndPreservesData(t *testing.T) {
	root, target := managedFixture(t, []byte("binary"))
	home := t.TempDir()
	t.Setenv("HOME", home)
	profile := filepath.Join(home, ".zshrc")
	block := "before\n" + ManagedBegin + "\nexport JKV_DIR='" + root + "'\n" + ManagedEnd + "\nafter\n"
	if err := os.WriteFile(profile, []byte(block), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "candidates", "java", "21"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SaveReceipt(root, target, []string{profile}); err != nil {
		t.Fatal(err)
	}
	manager := New(Config{Root: root, Executable: target})
	if err := manager.Uninstall(false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("binary remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "candidates", "java", "21")); err != nil {
		t.Fatalf("candidate removed: %v", err)
	}
	profileBody, _ := os.ReadFile(profile)
	if strings.Contains(string(profileBody), ManagedBegin) || !strings.Contains(string(profileBody), "before") || !strings.Contains(string(profileBody), "after") {
		t.Fatalf("profile = %q", profileBody)
	}
}

func TestUninstallValidatesAllBlocksBeforeMutation(t *testing.T) {
	root, target := managedFixture(t, []byte("binary"))
	profileOne := filepath.Join(t.TempDir(), "one")
	profileTwo := filepath.Join(t.TempDir(), "two")
	valid := ManagedBegin + "\nexport JKV_DIR='" + root + "'\n" + ManagedEnd + "\n"
	invalid := ManagedBegin + "\nexport JKV_DIR='/other'\n" + ManagedEnd + "\n"
	_ = os.WriteFile(profileOne, []byte(valid), 0o600)
	_ = os.WriteFile(profileTwo, []byte(invalid), 0o600)
	if err := SaveReceipt(root, target, []string{profileOne, profileTwo}); err != nil {
		t.Fatal(err)
	}
	manager := New(Config{Root: root, Executable: target})
	if err := manager.Uninstall(false, false); !errors.Is(err, ErrState) {
		t.Fatalf("Uninstall error = %v", err)
	}
	if got, _ := os.ReadFile(profileOne); string(got) != valid {
		t.Fatalf("first profile changed: %q", got)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("binary changed: %v", err)
	}
}

func TestBlockOwnershipRequiresExactAssignments(t *testing.T) {
	root := filepath.Join(t.TempDir(), "jkv")
	cases := []struct {
		name  string
		block string
		valid bool
	}{
		{name: "exact", block: "export JKV_DIR='" + root + "'", valid: true},
		{name: "prefix is not exact", block: "export JKV_DIR='" + root + "-other'", valid: false},
		{name: "other assignment wins", block: "export JKV_DIR='" + root + "'\nexport JKV_DIR='/other'", valid: false},
		{name: "fish", block: "set -gx JKV_DIR '" + root + "'", valid: true},
		{name: "powershell", block: "$env:JKV_DIR = '" + root + "'", valid: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := blockOwnsRoot(tc.block, root); got != tc.valid {
				t.Fatalf("blockOwnsRoot = %v, want %v", got, tc.valid)
			}
		})
	}
}

func TestBlockOwnershipUnquotesManagedPathEscapes(t *testing.T) {
	root := filepath.Join(t.TempDir(), "jkv'quoted")
	cases := []string{
		"export JKV_DIR='" + strings.ReplaceAll(root, "'", "'\\''") + "'",
		"set -gx JKV_DIR '" + strings.ReplaceAll(root, "'", "\\'") + "'",
		"$env:JKV_DIR = '" + strings.ReplaceAll(root, "'", "''") + "'",
	}
	for _, block := range cases {
		if !blockOwnsRoot(block, root) {
			t.Fatalf("escaped managed block rejected: %q", block)
		}
	}
}

func TestPurgeConfirmationAndSafety(t *testing.T) {
	root, target := managedFixture(t, []byte("binary"))
	manager := New(Config{Root: root, Executable: target})
	if err := manager.Uninstall(true, false); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("noninteractive purge error = %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("unconfirmed purge changed target: %v", err)
	}

	home := t.TempDir()
	bin := filepath.Join(home, "bin", "jkv")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	dangerous := New(Config{Root: home, Executable: bin})
	if err := dangerous.Uninstall(true, true); !errors.Is(err, ErrState) || !strings.Contains(err.Error(), "危险目录") {
		t.Fatalf("dangerous purge error = %v", err)
	}
}

func managedFixture(t *testing.T, body []byte) (string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "jkv")
	target := filepath.Join(root, "bin", "jkv")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, body, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, target
}
