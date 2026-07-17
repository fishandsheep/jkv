package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"jkv/internal/catalog"
	"jkv/internal/store"
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
		{[]string{"init", "in"}, []string{"bash", "powershell", "pwsh", "zsh"}},
		{[]string{"mirror", "m"}, []string{"gradle", "maven", "status"}},
		{[]string{"clean", "cl"}, []string{"catalog", "downloads"}},
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
		"powershell": `Invoke-Expression ((jkv init powershell) -join [Environment]::NewLine)`,
		"pwsh":       `Invoke-Expression ((jkv init powershell) -join [Environment]::NewLine)`,
	}
	for shell, want := range tests {
		if got := shellInitHint(shell); got != want {
			t.Errorf("shellInitHint(%q) = %q, want %q", shell, got, want)
		}
	}
}
