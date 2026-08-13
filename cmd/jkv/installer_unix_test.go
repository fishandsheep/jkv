//go:build !windows

package main

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUnixInstallerUsesActualDirectoryAndManagedBlock(t *testing.T) {
	server := installerFixtureServer(t, false)
	defer server.Close()
	home := t.TempDir()
	installDir := filepath.Join(t.TempDir(), "jkv custom")

	for range 2 {
		runUnixInstaller(t, home, installDir, server.URL, "", nil)
	}

	profile, err := os.ReadFile(filepath.Join(home, ".bashrc"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(profile)
	if strings.Count(text, "# >>> jkv managed >>>") != 1 || strings.Count(text, "# <<< jkv managed <<<") != 1 {
		t.Fatalf("managed block = %q", text)
	}
	if !strings.Contains(text, installDir) {
		t.Fatalf("profile does not contain actual install dir: %q", text)
	}
	if _, err := os.Stat(filepath.Join(installDir, "bin", "jkv")); err != nil {
		t.Fatal(err)
	}
	receipt, err := os.ReadFile(filepath.Join(installDir, "jkv-install.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"schema_version": 1`, installDir, filepath.Join(home, ".bashrc")} {
		if !strings.Contains(string(receipt), want) {
			t.Fatalf("receipt missing %q: %s", want, receipt)
		}
	}
}

func TestUnixInstallerCanSkipProfile(t *testing.T) {
	server := installerFixtureServer(t, false)
	defer server.Close()
	home := t.TempDir()
	runUnixInstaller(t, home, t.TempDir(), server.URL, "", []string{"--no-modify-profile"})
	if _, err := os.Stat(filepath.Join(home, ".bashrc")); !os.IsNotExist(err) {
		t.Fatalf("profile unexpectedly created: %v", err)
	}
}

func TestUnixInstallerFallsBackOnlyOnTransferFailure(t *testing.T) {
	fallback := installerFixtureServer(t, false)
	defer fallback.Close()
	home := t.TempDir()
	installDir := t.TempDir()
	runUnixInstaller(t, home, installDir, fallback.URL+"/missing", fallback.URL, nil)
	if _, err := os.Stat(filepath.Join(installDir, "bin", "jkv")); err != nil {
		t.Fatal(err)
	}
}

func TestUnixInstallerDoesNotFallbackAfterChecksumFailure(t *testing.T) {
	primary := installerFixtureServer(t, true)
	defer primary.Close()
	fallback := installerFixtureServer(t, false)
	defer fallback.Close()
	home := t.TempDir()
	installDir := t.TempDir()
	cmd := unixInstallerCommand(home, installDir, primary.URL, fallback.URL, nil)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected checksum failure, output=%s", output)
	}
	if !strings.Contains(string(output), "拒绝尝试其他来源") {
		t.Fatalf("output = %s", output)
	}
	if _, err := os.Stat(filepath.Join(installDir, "bin", "jkv")); !os.IsNotExist(err) {
		t.Fatalf("failed install created target: %v", err)
	}
}

func TestUnixInstallerUninstallPreservesCandidates(t *testing.T) {
	server := installerFixtureServer(t, false)
	defer server.Close()
	home := t.TempDir()
	installDir := t.TempDir()
	runUnixInstaller(t, home, installDir, server.URL, "", nil)
	candidate := filepath.Join(installDir, "candidates", "java", "21")
	if err := os.MkdirAll(candidate, 0o755); err != nil {
		t.Fatal(err)
	}
	runUnixInstaller(t, home, installDir, server.URL, "", []string{"--uninstall"})
	if _, err := os.Stat(filepath.Join(installDir, "bin", "jkv")); !os.IsNotExist(err) {
		t.Fatalf("binary remains: %v", err)
	}
	if _, err := os.Stat(candidate); err != nil {
		t.Fatalf("candidate removed: %v", err)
	}
	profile, err := os.ReadFile(filepath.Join(home, ".bashrc"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(profile), "# >>> jkv managed >>>") {
		t.Fatalf("managed block remains: %q", profile)
	}
}

func TestUnixInstallerPurgeSafetyAndConfirmationPrecedeMutation(t *testing.T) {
	server := installerFixtureServer(t, false)
	defer server.Close()
	home := t.TempDir()
	installDir := filepath.Join(t.TempDir(), "nested", "jkv")
	runUnixInstaller(t, home, installDir, server.URL, "", nil)

	cmd := unixInstallerCommand(home, installDir, server.URL, "", []string{"--purge"})
	if output, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(output), "必须同时使用 --yes") {
		t.Fatalf("unconfirmed purge = %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(installDir, "bin", "jkv")); err != nil {
		t.Fatalf("unconfirmed purge removed binary: %v", err)
	}
	profilePath := filepath.Join(home, ".bashrc")
	if profile, err := os.ReadFile(profilePath); err != nil || !strings.Contains(string(profile), "# >>> jkv managed >>>") {
		t.Fatalf("unconfirmed purge modified profile: %v\n%s", err, profile)
	}

	dangerous := unixInstallerCommand(home, home+string(os.PathSeparator), server.URL, "", []string{"--purge", "--yes"})
	if output, err := dangerous.CombinedOutput(); err == nil || !strings.Contains(string(output), "拒绝清理危险目录") {
		t.Fatalf("dangerous purge = %v\n%s", err, output)
	}
	if _, err := os.Stat(home); err != nil {
		t.Fatalf("dangerous purge removed HOME: %v", err)
	}
}

func TestUnixInstallerRejectsForeignManagedBlockBeforeMutation(t *testing.T) {
	server := installerFixtureServer(t, false)
	defer server.Close()
	home := t.TempDir()
	installDir := t.TempDir()
	profilePath := filepath.Join(home, ".bashrc")
	foreign := "before\n# >>> jkv managed >>>\nexport JKV_DIR='/other/jkv'\n# <<< jkv managed <<<\nafter\n"
	if err := os.WriteFile(profilePath, []byte(foreign), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := unixInstallerCommand(home, installDir, server.URL, "", nil)
	if output, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(output), "指向其他 JKV_DIR") {
		t.Fatalf("foreign block install = %v\n%s", err, output)
	}
	got, _ := os.ReadFile(profilePath)
	if string(got) != foreign {
		t.Fatalf("foreign profile changed: %q", got)
	}
}

func TestUnixInstallerConfiguresFish(t *testing.T) {
	server := installerFixtureServer(t, false)
	defer server.Close()
	home := t.TempDir()
	installDir := t.TempDir()
	cmd := unixInstallerCommand(home, installDir, server.URL, "", nil)
	cmd.Env = append(cmd.Env, "SHELL=/usr/bin/fish")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fish installer: %v\n%s", err, output)
	}
	config, err := os.ReadFile(filepath.Join(home, ".config", "fish", "config.fish"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"set -gx JKV_DIR", "fish_add_path", "jkv init fish | source"} {
		if !strings.Contains(string(config), want) {
			t.Errorf("fish config missing %q: %s", want, config)
		}
	}
}

func installerFixtureServer(t *testing.T, badChecksum bool) *httptest.Server {
	t.Helper()
	asset := fmt.Sprintf("jkv-%s-%s", runtime.GOOS, runtime.GOARCH)
	content := []byte("#!/bin/sh\necho 'jkv test'\n")
	sum := sha256.Sum256(content)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + asset:
			_, _ = w.Write(content)
		case "/" + asset + ".sha256":
			if badChecksum {
				_, _ = fmt.Fprintf(w, "%064d  %s\n", 0, asset)
			} else {
				_, _ = fmt.Fprintf(w, "%x  %s\n", sum, asset)
			}
		default:
			http.NotFound(w, r)
		}
	}))
}

func runUnixInstaller(t *testing.T, home, installDir, primary, fallback string, args []string) {
	t.Helper()
	cmd := unixInstallerCommand(home, installDir, primary, fallback, args)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("installer: %v\n%s", err, output)
	}
}

func unixInstallerCommand(home, installDir, primary, fallback string, args []string) *exec.Cmd {
	commandArgs := append([]string{"../../install.sh"}, args...)
	cmd := exec.Command("/bin/sh", commandArgs...)
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"JKV_DIR="+installDir,
		"JKV_DOWNLOAD_BASE="+primary,
		"JKV_FALLBACK_BASE="+fallback,
		"SHELL=/bin/bash",
	)
	return cmd
}
