package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fishandsheep/jkv/internal/catalog"
	"github.com/fishandsheep/jkv/internal/store"
)

func TestSelectReleaseAlias(t *testing.T) {
	releases := []catalog.Release{
		{Candidate: "java", Version: "21.0.2+1-tem", Vendor: "temurin"},
		{Candidate: "java", Version: "21.0.3.0.1-dragonwell", Vendor: "dragonwell"},
	}
	for want, version := range map[string]string{
		"": "21.0.2+1-tem", "21-tem": "21.0.2+1-tem", "21-dragonwell": "21.0.3.0.1-dragonwell",
	} {
		got, err := selectRelease(releases, want)
		if err != nil || got.Version != version {
			t.Fatalf("%q: got=%v err=%v", want, got, err)
		}
	}
}

func TestReleaseGroupsJavaVendorOrder(t *testing.T) {
	releases := []catalog.Release{
		{Vendor: "bisheng", Version: "21-bisheng"},
		{Vendor: "temurin", Version: "21-tem"},
		{Vendor: "dragonwell", Version: "21-dragonwell"},
	}
	groups := releaseGroups("java", releases)
	got := []string{groups[0].vendor, groups[1].vendor, groups[2].vendor}
	want := []string{"temurin", "dragonwell", "bisheng"}
	if !slices.Equal(got, want) {
		t.Fatalf("vendor order = %v", got)
	}
}

func TestInstalledVersionCompletionSupportsAliases(t *testing.T) {
	s := store.New(t.TempDir())
	version := "21.0.1-tem"
	if err := os.MkdirAll(filepath.Join(s.CandidateDir("java", version)), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"use", "u", "default", "d", "uninstall", "rm", "home", "h"} {
		got := completions(context.Background(), s, []string{command, "java", "21"})
		if !slices.Contains(got, version) {
			t.Fatalf("%s completion = %v", command, got)
		}
	}
}

func TestInstallVersionCompletionUsesCatalogCache(t *testing.T) {
	s := store.New(t.TempDir())
	cache := store.CatalogCache{
		FetchedAt: time.Now(),
		Releases: []catalog.Release{
			{Candidate: "java", Version: "21.0.8+9-tem", Vendor: "temurin"},
			{Candidate: "java", Version: "17.0.16+8-tem", Vendor: "temurin"},
		},
	}
	if err := s.SaveCatalog(catalog.CurrentPlatform(), "java", cache); err != nil {
		t.Fatal(err)
	}

	got := completions(context.Background(), s, []string{"install", "java", "21"})
	want := []string{"21.0.8+9-tem"}
	if !slices.Equal(got, want) {
		t.Fatalf("install completion = %v, want %v", got, want)
	}
	if got := completions(context.Background(), s, []string{"i", "java", "lat"}); !slices.Equal(got, []string{"latest"}) {
		t.Fatalf("install alias completion = %v", got)
	}
}

func TestStaticArgumentCompletionsIncludeAcceptedAliases(t *testing.T) {
	s := store.New(t.TempDir())
	if got := completions(context.Background(), s, []string{"init", "p"}); !slices.Equal(got, []string{"powershell", "pwsh"}) {
		t.Fatalf("init completion = %v", got)
	}
}

func TestCompletionCoversEveryCandidateAndCommandAlias(t *testing.T) {
	s := store.New(t.TempDir())
	versions := map[string]string{}
	for i, candidate := range catalog.Candidates {
		candidate := candidate.Name
		version := fmt.Sprintf("%d.0-%s", i+1, candidate)
		versions[candidate] = version
		if err := os.MkdirAll(s.CandidateDir(candidate, version), 0o755); err != nil {
			t.Fatal(err)
		}
		cache := store.CatalogCache{
			FetchedAt: time.Now(),
			Releases:  []catalog.Release{{Candidate: candidate, Version: version}},
		}
		if err := s.SaveCatalog(catalog.CurrentPlatform(), candidate, cache); err != nil {
			t.Fatal(err)
		}
		downloadDir := filepath.Join(s.Root, "cache", "downloads", candidate, version)
		if err := os.MkdirAll(downloadDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(downloadDir, "archive"), []byte("cached archive"), 0o600); err != nil {
			t.Fatal(err)
		}
		metadata, err := json.Marshal(struct {
			Release catalog.Release `json:"release"`
			SHA256  string          `json:"sha256"`
		}{Release: catalog.Release{Candidate: candidate, Version: version, URL: "https://example.test/archive"}, SHA256: "test"})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(downloadDir, "metadata.json"), metadata, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	for _, command := range []string{"list", "ls", "install", "i", "use", "u", "default", "d", "current", "c", "uninstall", "rm", "home", "h"} {
		got := completions(context.Background(), s, []string{command, ""})
		for candidate := range versions {
			if !slices.Contains(got, candidate) {
				t.Errorf("%s candidate completion missing %s: %v", command, candidate, got)
			}
		}
	}

	for _, command := range []string{"install", "i"} {
		for candidate, version := range versions {
			got := completions(context.Background(), s, []string{command, candidate, version})
			if !slices.Contains(got, version) {
				t.Errorf("%s %s install completion missing %s: %v", command, candidate, version, got)
			}
		}
	}

	for _, command := range []string{"use", "u", "default", "d", "uninstall", "rm", "home", "h"} {
		for candidate, version := range versions {
			got := completions(context.Background(), s, []string{command, candidate, version})
			if !slices.Contains(got, version) {
				t.Errorf("%s %s installed completion missing %s: %v", command, candidate, version, got)
			}
		}
	}

	for _, command := range []string{"clean", "cl"} {
		for _, kind := range []string{"downloads", "catalog"} {
			got := completions(context.Background(), s, []string{command, kind, ""})
			for candidate := range versions {
				if !slices.Contains(got, candidate) {
					t.Errorf("%s %s completion missing %s: %v", command, kind, candidate, got)
				}
			}
		}
		for candidate, version := range versions {
			got := completions(context.Background(), s, []string{command, "downloads", candidate, version})
			if !slices.Contains(got, version) {
				t.Errorf("%s downloads %s completion missing %s: %v", command, candidate, version, got)
			}
		}
	}
}

func TestCompletionCoversEveryTopLevelNameAndStaticArgument(t *testing.T) {
	s := store.New(t.TempDir())
	topLevel := completions(context.Background(), s, []string{""})
	for name := range commandAliases {
		if !slices.Contains(topLevel, name) {
			t.Errorf("top-level completion missing %s: %v", name, topLevel)
		}
	}

	tests := []struct {
		commands []string
		want     []string
	}{
		{[]string{"env", "e"}, []string{"apply", "clear", "defaults", "init"}},
		{[]string{"init", "in"}, []string{"bash", "fish", "powershell", "pwsh", "zsh"}},
		{[]string{"mirror", "m"}, []string{"gradle", "maven", "status"}},
		{[]string{"clean", "cl"}, []string{"--dry-run", "catalog", "downloads"}},
	}
	for _, test := range tests {
		for _, command := range test.commands {
			got := completions(context.Background(), s, []string{command, ""})
			if !slices.Equal(got, test.want) {
				t.Errorf("%s completion = %v, want %v", command, got, test.want)
			}
		}
	}
}

func TestCompletionSupportsListAndInstallOptionsBeforePositionals(t *testing.T) {
	s := store.New(t.TempDir())
	version := "3.9.11"
	cache := store.CatalogCache{
		FetchedAt: time.Now(),
		Releases:  []catalog.Release{{Candidate: "maven", Version: version}},
	}
	if err := s.SaveCatalog(catalog.CurrentPlatform(), "maven", cache); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"list", "--refresh", ""},
		{"ls", "--refresh", ""},
		{"install", "--default", ""},
		{"i", "--default", ""},
	} {
		got := completions(context.Background(), s, args)
		if !slices.Contains(got, "maven") {
			t.Errorf("completion %v missing maven: %v", args, got)
		}
	}
	for _, args := range [][]string{
		{"install", "--default", "maven", ""},
		{"i", "maven", "--default", ""},
	} {
		got := completions(context.Background(), s, args)
		if !slices.Contains(got, version) {
			t.Errorf("completion %v missing %s: %v", args, version, got)
		}
	}
}

func TestInitZshPreservesCompletionArguments(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	originalStdout := os.Stdout
	os.Stdout = w
	if err := cmdInit([]string{"zsh"}); err != nil {
		w.Close()
		os.Stdout = originalStdout
		t.Fatal(err)
	}
	w.Close()
	os.Stdout = originalStdout
	script, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), `command jkv __complete "${(@)words[2,CURRENT]}"`) {
		t.Fatal("zsh completion arguments are not expanded as separate words")
	}
}

func TestCommandAliases(t *testing.T) {
	for alias, command := range map[string]string{
		"ls": "list", "i": "install", "u": "use", "d": "default", "c": "current",
		"rm": "uninstall", "h": "home", "e": "env", "in": "init", "m": "mirror",
		"cl": "clean", "v": "version",
	} {
		if commandAliases[alias] != command {
			t.Fatalf("alias %s = %q", alias, commandAliases[alias])
		}
	}
}

func TestShellInitHint(t *testing.T) {
	tests := map[string]string{
		"bash":       `eval "$(jkv init bash)"`,
		"zsh":        `eval "$(jkv init zsh)"`,
		"fish":       `jkv init fish | source`,
		"powershell": `Invoke-Expression ((jkv init powershell) -join [Environment]::NewLine)`,
		"pwsh":       `Invoke-Expression ((jkv init powershell) -join [Environment]::NewLine)`,
	}
	for shell, want := range tests {
		if got := shellInitHint(shell); got != want {
			t.Errorf("shellInitHint(%q) = %q, want %q", shell, got, want)
		}
	}
}

func TestInitFishEmitsFunctionAndCompletion(t *testing.T) {
	script := captureStdout(t, func() error { return cmdInit([]string{"fish"}) })
	for _, want := range []string{
		"function jkv",
		"command jkv __complete",
		"complete --command jkv",
		"command jkv env --shell fish | source",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("fish init missing %q", want)
		}
	}
}

func TestRunCurrentJSON(t *testing.T) {
	root := t.TempDir()
	t.Setenv("JKV_DIR", root)
	s := store.New(root)
	if err := os.MkdirAll(s.CandidateDir("java", "21.0.1-tem"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDefault("java", "21.0.1-tem"); err != nil {
		t.Fatal(err)
	}

	output := captureStdout(t, func() error {
		return run(context.Background(), []string{"--json", "current", "java"})
	})
	var got map[string]string
	if err := json.Unmarshal([]byte(output), &got); err != nil {
		t.Fatalf("JSON output = %q: %v", output, err)
	}
	if got["candidate"] != "java" || got["version"] != "21.0.1-tem" {
		t.Fatalf("current JSON = %#v", got)
	}
}

func TestReadEnvFileRejectsPathSegments(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".jkvrc")
	if err := os.WriteFile(path, []byte("java=../../../tmp/tool\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readEnvFile(path); err == nil {
		t.Fatal("expected path-like version to be rejected")
	}
}

func TestRunListCandidatesJSON(t *testing.T) {
	output := captureStdout(t, func() error {
		return run(context.Background(), []string{"list", "--json"})
	})
	var got []map[string]any
	if err := json.Unmarshal([]byte(output), &got); err != nil {
		t.Fatalf("JSON output = %q: %v", output, err)
	}
	if len(got) == 0 || got[0]["name"] != "java" {
		t.Fatalf("candidate JSON = %#v", got)
	}
}

func TestRunHomeJSON(t *testing.T) {
	root := t.TempDir()
	t.Setenv("JKV_DIR", root)
	s := store.New(root)
	version := "3.9.11"
	if err := os.MkdirAll(s.CandidateDir("maven", version), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDefault("maven", version); err != nil {
		t.Fatal(err)
	}

	output := captureStdout(t, func() error {
		return run(context.Background(), []string{"home", "maven", "--json"})
	})
	var got map[string]string
	if err := json.Unmarshal([]byte(output), &got); err != nil {
		t.Fatal(err)
	}
	if got["candidate"] != "maven" || got["version"] != version || got["home"] != s.CandidateDir("maven", version) {
		t.Fatalf("home JSON = %#v", got)
	}
}

func TestRunDoctorJSON(t *testing.T) {
	root := t.TempDir()
	t.Setenv("JKV_DIR", root)
	output := captureStdout(t, func() error {
		return run(context.Background(), []string{"doctor", "--json"})
	})
	var got map[string]any
	if err := json.Unmarshal([]byte(output), &got); err != nil {
		t.Fatalf("doctor JSON = %q: %v", output, err)
	}
	if got["root"] != root || got["os"] == "" || got["arch"] == "" || got["writable"] != true {
		t.Fatalf("doctor = %#v", got)
	}
}

func TestExitCodeCategories(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{errors.New("用法: jkv install <candidate>"), 2},
		{errors.New("未安装 java 21"), 3},
		{errors.New("下载失败: HTTP 503 Service Unavailable"), 4},
		{errors.New("SHA-256 不匹配"), 5},
		{errors.New("已安装 java 21"), 6},
	}
	for _, test := range tests {
		if got := exitCodeFor(test.err); got != test.want {
			t.Errorf("exitCodeFor(%q) = %d, want %d", test.err, got, test.want)
		}
	}
}

func TestInstalledCommandWorkflow(t *testing.T) {
	root := t.TempDir()
	t.Setenv("JKV_DIR", root)
	s := store.New(root)
	for candidate, version := range map[string]string{"java": "21.0.1-tem", "maven": "3.9.11"} {
		if err := os.MkdirAll(filepath.Join(s.CandidateDir(candidate, version), "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := cmdDefault(s, []string{"java", "21"}); err != nil {
		t.Fatal(err)
	}
	if got := captureStdout(t, func() error {
		return cmdCurrent(context.Background(), s, []string{"java"})
	}); !strings.Contains(got, "21.0.1-tem") {
		t.Fatalf("current = %q", got)
	}
	if got := captureStdout(t, func() error {
		return cmdHome(context.Background(), s, []string{"java"})
	}); !strings.Contains(got, "21.0.1-tem") {
		t.Fatalf("home = %q", got)
	}
	for _, shell := range []string{"bash", "powershell", "fish"} {
		got := captureStdout(t, func() error {
			return cmdUse(s, []string{"java", "21", "--shell", shell})
		})
		if !strings.Contains(got, "JAVA_HOME") {
			t.Fatalf("%s use output = %q", shell, got)
		}
	}
	if err := cmdDefault(s, []string{"maven", "3.9", "--shell", "zsh"}); err != nil {
		t.Fatal(err)
	}
	if err := cmdUninstall(s, []string{"maven", "3.9"}); err != nil {
		t.Fatal(err)
	}
	if err := cmdUse(s, []string{"java", "21"}); err == nil {
		t.Fatal("use without shell hook succeeded")
	}
	if _, _, err := shellFlag([]string{"--shell"}); err == nil {
		t.Fatal("missing shell value accepted")
	}
}

func TestEnvMirrorCleanAndInitCommands(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("JKV_DIR", root)
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/usr/bin/fish")
	s := store.New(root)
	version := "21.0.1-tem"
	if err := os.MkdirAll(filepath.Join(s.CandidateDir("java", version), "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDefault("java", version); err != nil {
		t.Fatal(err)
	}
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(originalDir) }()

	if err := cmdEnv(s, []string{"init"}); err != nil {
		t.Fatal(err)
	}
	if got := captureStdout(t, func() error {
		return cmdEnv(s, []string{"apply", "--shell", "fish"})
	}); !strings.Contains(got, "fish_add_path") {
		t.Fatalf("fish env = %q", got)
	}
	if got := captureStdout(t, func() error {
		return cmdEnv(s, []string{"clear", "--shell", "powershell"})
	}); !strings.Contains(got, "$env:JKV_DIR") {
		t.Fatalf("powershell env = %q", got)
	}

	if err := cmdMirror(context.Background(), []string{"maven"}); err != nil {
		t.Fatal(err)
	}
	if err := cmdMirror(context.Background(), []string{"gradle", "--apply"}); err != nil {
		t.Fatal(err)
	}
	jsonContext := context.WithValue(context.Background(), cliOptionsKey{}, cliOptions{JSON: true})
	if got := captureStdout(t, func() error {
		return cmdMirror(jsonContext, []string{"status"})
	}); !json.Valid([]byte(got)) {
		t.Fatalf("mirror JSON = %q", got)
	}

	cachePath := filepath.Join(root, "cache", "downloads", "java", version, "archive")
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, []byte("cache"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := captureStdout(t, func() error {
		return cmdClean(context.Background(), s, []string{"downloads", "java", version, "--dry-run"})
	}); !strings.Contains(got, "将清理") {
		t.Fatalf("clean dry-run = %q", got)
	}
	if err := cmdClean(context.Background(), s, []string{"downloads", "java", version}); err != nil {
		t.Fatal(err)
	}

	for _, shell := range []string{"bash", "powershell"} {
		if got := captureStdout(t, func() error {
			return cmdInit([]string{shell})
		}); !strings.Contains(got, "jkv") {
			t.Fatalf("%s init = %q", shell, got)
		}
	}
	if guessedShell() != "fish" {
		t.Fatalf("guessed shell = %q", guessedShell())
	}
}

func TestListInstallRepairAndCompletion(t *testing.T) {
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	file, err := zw.Create("tool/bin/tool")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(file, "working")
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive.Bytes())
	}))
	defer server.Close()

	root := t.TempDir()
	t.Setenv("JKV_DIR", root)
	s := store.New(root)
	release := catalog.Release{
		Candidate: "maven", Version: "3.9.11", Vendor: "maven", SupportTier: "core",
		URL: server.URL + "/maven.zip", Available: true, AvailabilityKnown: true,
	}
	if err := s.SaveCatalog(catalog.CurrentPlatform(), "maven", store.CatalogCache{
		FetchedAt: time.Now(), CheckedAt: time.Now(), Releases: []catalog.Release{release},
	}); err != nil {
		t.Fatal(err)
	}
	if got := captureStdout(t, func() error {
		return cmdList(context.Background(), s, []string{"maven"})
	}); !strings.Contains(got, "3.9.11") || !strings.Contains(got, "maven") {
		t.Fatalf("list = %q", got)
	}
	if got := captureStdout(t, func() error {
		return cmdInstall(context.Background(), s, []string{"maven", "3.9.11", "--default"})
	}); !strings.Contains(got, "已安装并设为默认") {
		t.Fatalf("install = %q", got)
	}
	tool := filepath.Join(s.CandidateDir("maven", "3.9.11"), "bin", "tool")
	if err := os.WriteFile(tool, []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := captureStdout(t, func() error {
		return cmdRepair(context.Background(), s, []string{"maven", "3.9.11"})
	}); !strings.Contains(got, "已修复") {
		t.Fatalf("repair = %q", got)
	}
	if body, err := os.ReadFile(tool); err != nil || string(body) != "working" {
		t.Fatalf("repaired tool = %q, %v", body, err)
	}
	if got := captureStdout(t, func() error {
		return cmdComplete(context.Background(), s, []string{"install", "maven", "3"})
	}); !strings.Contains(got, "3.9.11") {
		t.Fatalf("completion output = %q", got)
	}
}

func TestFormattingAndOptionHelpers(t *testing.T) {
	if got := hostOf("https://example.test/a"); got != "example.test" {
		t.Fatalf("host = %q", got)
	}
	if vendorDisplay("temurin") != "Temurin" || vendorDisplay("other") != "other" {
		t.Fatal("vendor display failed")
	}
	if !envEnabledValue(t, "YES") || envEnabledValue(t, "no") {
		t.Fatal("env flag classification failed")
	}
	if formatBytes(512) != "512 B" || !strings.Contains(formatBytes(2048), "KiB") {
		t.Fatal("byte formatting failed")
	}
	if shQuote("a'b") != `'a'\''b'` || psQuote("a'b") != "'a''b'" || fishQuote("a'b") != `'a\'b'` {
		t.Fatal("shell quoting failed")
	}
	if releasesNeedCheck([]catalog.Release{{AvailabilityKnown: true}}) ||
		!releasesNeedCheck([]catalog.Release{{AvailabilityKnown: false}}) {
		t.Fatal("availability classification failed")
	}
}

func envEnabledValue(t *testing.T, value string) bool {
	t.Helper()
	t.Setenv("JKV_TEST_FLAG", value)
	return envEnabled("JKV_TEST_FLAG")
}

func captureStdout(t *testing.T, fn func() error) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	os.Stdout = w
	runErr := fn()
	_ = w.Close()
	os.Stdout = original
	b, readErr := io.ReadAll(r)
	_ = r.Close()
	if runErr != nil {
		t.Fatal(runErr)
	}
	if readErr != nil {
		t.Fatal(readErr)
	}
	return string(b)
}
