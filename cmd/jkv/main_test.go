package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/fishandsheep/jkv/internal/catalog"
	"github.com/fishandsheep/jkv/internal/store"
)

type testSnapshotRelease struct {
	candidate string
	vendor    string
	version   string
	selector  string
	url       string
	tier      string
}

// seedTrustedCatalog stores a signed, locally trusted catalog snapshot so
// tests can drive list/install/completion through the signed path without
// network access.
func seedTrustedCatalog(t *testing.T, s *store.Store, sequence uint64, releases ...testSnapshotRelease) {
	t.Helper()
	keyID := "main-test-key"
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("JKV_CATALOG_KEY_ID", keyID)
	t.Setenv("JKV_CATALOG_PUBLIC_KEY", base64.StdEncoding.EncodeToString(private.Public().(ed25519.PublicKey)))

	allPlatforms := []catalog.Platform{
		{OS: "linux", Arch: "x64"}, {OS: "linux", Arch: "aarch64"},
		{OS: "darwin", Arch: "x64"}, {OS: "darwin", Arch: "aarch64"},
		{OS: "windows", Arch: "x64"}, {OS: "windows", Arch: "aarch64"},
	}
	releasesByVendor := map[string]map[string][]catalog.SnapshotRelease{}
	firstVendor := map[string]string{}
	for _, item := range releases {
		if releasesByVendor[item.candidate] == nil {
			releasesByVendor[item.candidate] = map[string][]catalog.SnapshotRelease{}
		}
		if _, seen := firstVendor[item.candidate]; !seen {
			firstVendor[item.candidate] = item.vendor
		}
		releasesByVendor[item.candidate][item.vendor] = append(releasesByVendor[item.candidate][item.vendor], catalog.SnapshotRelease{
			Version: item.version, Selector: item.selector, SupportTier: item.tier,
			Artifacts: []catalog.Artifact{{
				ArtifactID:  fmt.Sprintf("%s/%s/%s/all/r1", item.candidate, item.vendor, item.version),
				ArchiveType: "zip",
				Platforms:   allPlatforms,
				URL:         item.url,
			}},
		})
	}

	snapshot := catalog.Snapshot{SchemaVersion: 1, Sequence: sequence, PublishedAt: "2026-07-30T12:00:00Z", SourceCommit: "0123456789abcdef0123456789abcdef01234567", MinClientVersion: "0.0.1"}
	for _, name := range sortedKeys(releasesByVendor) {
		candidate := catalog.SnapshotCandidate{Name: name, DisplayName: name, Description: name + " (test fixture)", Homepage: "https://example.test/" + name, DefaultVendor: firstVendor[name]}
		for _, vendorName := range sortedKeys(releasesByVendor[name]) {
			candidate.Vendors = append(candidate.Vendors, catalog.Vendor{Name: vendorName, DisplayName: vendorName, Releases: releasesByVendor[name][vendorName]})
		}
		snapshot.Candidates = append(snapshot.Candidates, candidate)
	}
	snapshotBytes, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(snapshotBytes)
	latestBytes, err := json.Marshal(catalog.Latest{SchemaVersion: 1, Sequence: sequence, SnapshotSHA256: hex.EncodeToString(digest[:]), ReleaseTag: fmt.Sprintf("catalog-v1-%06d", sequence), SnapshotAsset: "catalog-v1.json"})
	if err != nil {
		t.Fatal(err)
	}
	fetched := catalog.FetchedSnapshot{
		Endpoint:          "test",
		KeyIDs:            []string{keyID},
		Snapshot:          snapshot,
		SnapshotBytes:     snapshotBytes,
		SnapshotSignature: signedDocument(t, snapshotBytes, private, keyID),
		LatestBytes:       latestBytes,
		LatestSignature:   signedDocument(t, latestBytes, private, keyID),
	}
	state, stateErr := s.LoadTrustedCatalog()
	if stateErr != nil && !os.IsNotExist(stateErr) {
		t.Fatal(stateErr)
	}
	if err := saveFetchedCatalog(s, state, fetched); err != nil {
		t.Fatal(err)
	}
}

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

func TestSelectReleaseSkipsKnownUnavailable(t *testing.T) {
	releases := []catalog.Release{
		{Candidate: "maven", Version: "4.0", AvailabilityKnown: true},
		{Candidate: "maven", Version: "3.9", AvailabilityKnown: true, Available: true},
	}
	got, err := selectRelease(releases, "")
	if err != nil || got.Version != "3.9" {
		t.Fatalf("selected = %#v, %v", got, err)
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

func TestMergeInstalledReleasesPreservesCatalogAndLocalMetadata(t *testing.T) {
	s := store.New(t.TempDir())
	catalogReleases := []catalog.Release{
		{Candidate: "java", Version: "21.0.2-tem", Vendor: "temurin", URL: "https://example.test/21"},
		{Candidate: "java", Version: "17.0.9-tem", Vendor: "temurin", URL: "https://example.test/17"},
	}
	for _, version := range []string{"21.0.2-tem", "11.0.22-dragonwell", "8.0.2-local"} {
		if err := os.MkdirAll(s.CandidateDir("java", version), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	metadata := `{"schema_version":1,"release":{"candidate":"java","version":"11.0.22-dragonwell","vendor":"dragonwell","support_tier":"beta","integrity_level":"checksum","url":"https://example.test/old"}}`
	if err := os.WriteFile(filepath.Join(s.CandidateDir("java", "11.0.22-dragonwell"), ".jkv-release.json"), []byte(metadata), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.CandidateDir("java", "8.0.2-local"), ".jkv-release.json"), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := mergeInstalledReleases(s, "java", catalogReleases, []string{"21.0.2-tem", "11.0.22-dragonwell", "8.0.2-local"})
	if len(got) != 4 {
		t.Fatalf("merged releases = %#v", got)
	}
	if got[0].Version != "21.0.2-tem" || got[1].Version != "17.0.9-tem" {
		t.Fatalf("catalog order changed: %#v", got)
	}
	if got[2].Version != "11.0.22-dragonwell" || got[2].Vendor != "dragonwell" || got[2].IntegrityLevel != "checksum" || got[2].AvailabilityStatus != "installed-only" {
		t.Fatalf("metadata local release = %#v", got[2])
	}
	if got[3].Version != "8.0.2-local" || got[3].Vendor != "local" || got[3].SupportTier != "local" {
		t.Fatalf("fallback local release = %#v", got[3])
	}
	groups := releaseGroups("java", got)
	if groups[len(groups)-1].vendor != "local" {
		t.Fatalf("local group order = %#v", groups)
	}
}

func TestListJSONIncludesInstalledOnlyContract(t *testing.T) {
	root := t.TempDir()
	t.Setenv("JKV_DIR", root)
	t.Setenv("JKV_CURRENT_SPRINGBOOT", "")
	s := store.New(root)
	localVersion := "2.7.18"
	if err := os.MkdirAll(s.CandidateDir("springboot", localVersion), 0o755); err != nil {
		t.Fatal(err)
	}
	seedTrustedCatalog(t, s, 1, testSnapshotRelease{candidate: "springboot", vendor: "spring", version: "3.5.1", selector: "3.5.1", url: "https://example.test/spring.zip", tier: "core"})
	jsonContext := context.WithValue(context.Background(), cliOptionsKey{}, cliOptions{JSON: true})
	output := captureStdout(t, func() error { return cmdList(jsonContext, s, []string{"springboot"}) })
	var got []map[string]any
	if err := json.Unmarshal([]byte(output), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0]["in_catalog"] != true || got[1]["in_catalog"] != false || got[1]["installed"] != true || got[1]["availability_known"] != false || got[1]["availability_status"] != "installed-only" {
		t.Fatalf("list JSON = %#v", got)
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
	seedTrustedCatalog(t, s, 1,
		testSnapshotRelease{candidate: "java", vendor: "temurin", version: "21.0.8+9", selector: "21.0.8+9-tem", url: "https://example.test/jdk21.tar.gz", tier: "core"},
		testSnapshotRelease{candidate: "java", vendor: "temurin", version: "17.0.16+8", selector: "17.0.16+8-tem", url: "https://example.test/jdk17.tar.gz", tier: "core"},
	)

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

func TestSelfCommandAliasesAndCompletions(t *testing.T) {
	if commandAliases["self"] != "self" || commandAliases["s"] != "self" {
		t.Fatalf("self aliases = %#v", commandAliases)
	}
	s := store.New(t.TempDir())
	if got := completions(context.Background(), s, []string{"s", "u"}); !slices.Equal(got, []string{"uninstall", "up", "update"}) {
		t.Fatalf("self subcommand completion = %v", got)
	}
	if got := completions(context.Background(), s, []string{"self", "rm", "--"}); !slices.Equal(got, []string{"--purge", "--yes"}) {
		t.Fatalf("self option completion = %v", got)
	}
	for _, args := range [][]string{{"update", "extra"}, {"up", "extra"}, {"uninstall", "--bad"}, {"rm", "--yes"}} {
		if err := cmdSelf(context.Background(), s, args); err == nil {
			t.Fatalf("cmdSelf(%v) accepted invalid arguments", args)
		}
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

	specs := make([]testSnapshotRelease, 0, len(catalog.Candidates))
	for i, candidate := range catalog.Candidates {
		version := fmt.Sprintf("%d.0-%s", i+1, candidate.Name)
		specs = append(specs, testSnapshotRelease{candidate: candidate.Name, vendor: candidate.Name, version: version, selector: version, url: "https://example.test/" + candidate.Name + ".zip", tier: "core"})
	}
	seedTrustedCatalog(t, s, 1, specs...)

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
		for _, kind := range []string{"downloads"} {
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
		{[]string{"clean", "cl"}, []string{"--dry-run", "--older-than", "downloads", "partials"}},
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
	seedTrustedCatalog(t, s, 1, testSnapshotRelease{candidate: "maven", vendor: "maven", version: version, selector: version, url: "https://example.test/maven.zip", tier: "core"})

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
	t.Setenv("JKV_CURRENT_JAVA", "")
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

func TestRunVersionUsesBuildValue(t *testing.T) {
	original := version
	defer func() { version = original }()

	for _, test := range []struct {
		name  string
		value string
		want  string
	}{
		{name: "release", value: "v0.0.1", want: "jkv v0.0.1\n"},
		{name: "source", value: "dev", want: "jkv dev\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			version = test.value
			output := captureStdout(t, func() error {
				return run(context.Background(), []string{"v"})
			})
			if output != test.want {
				t.Fatalf("version output = %q, want %q", output, test.want)
			}
		})
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
	originalClient := doctorHTTPClient
	doctorHTTPClient = &http.Client{Transport: testRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})}
	defer func() { doctorHTTPClient = originalClient }()
	output := captureStdout(t, func() error {
		return run(context.Background(), []string{"doctor", "--json"})
	})
	var got map[string]any
	if err := json.Unmarshal([]byte(output), &got); err != nil {
		t.Fatalf("doctor JSON = %q: %v", output, err)
	}
	if got["root"] != "<JKV_DIR>" || got["os"] == "" || got["arch"] == "" || got["writable"] != true {
		t.Fatalf("doctor = %#v", got)
	}
	if mirrors, ok := got["mirrors"].([]any); !ok || len(mirrors) != 3 {
		t.Fatalf("doctor mirrors = %#v", got["mirrors"])
	}
}

type testRoundTripFunc func(*http.Request) (*http.Response, error)

func (f testRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestExitCodeCategories(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{usageErrorf("用法: jkv install <工具>"), exitUsage},
		{usageErrorf("wrapped: %w", errors.New("任意内层错误")), exitUsage},
		{notFoundErrorf("未安装 java 21"), exitNotFound},
		{stateErrorf("已存在且内容不同，拒绝覆盖"), exitState},
		{fmt.Errorf("网络: %w", store.ErrNetwork), exitNetwork},
		{fmt.Errorf("镜像重试: %w", store.ErrNetwork), exitNetwork},
		{fmt.Errorf("bad archive: %w", store.ErrIntegrity), exitIntegrity},
		{fmt.Errorf("回滚拒绝: %w", store.ErrState), exitState},
		{fmt.Errorf("缺失: %w", store.ErrNotFound), exitNotFound},
		{&catalog.DownloadError{Endpoint: "CNB", Err: errors.New("connection refused")}, exitNetwork},
		{errors.New("完全未分类错误"), 1},
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
	t.Setenv("JKV_CURRENT_JAVA", "")
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
	}); !strings.Contains(got, "set -gx PATH") {
		t.Fatalf("fish env = %q", got)
	}
	if got := captureStdout(t, func() error {
		return cmdEnv(s, []string{"clear", "--shell", "powershell"})
	}); !strings.Contains(got, "Remove-Item Env:JAVA_HOME") || !strings.Contains(got, "Remove-Item Env:JKV_CURRENT_JAVA") {
		t.Fatalf("powershell clear = %q", got)
	}
	if got := captureStdout(t, func() error {
		return cmdEnv(s, []string{"clear", "--shell", "bash"})
	}); !strings.Contains(got, "unset ANT_HOME") || !strings.Contains(got, "JKV_CURRENT_JAVA") || !strings.Contains(got, `PATH=":$PATH:"`) {
		t.Fatalf("bash clear = %q", got)
	}

	if got := captureStdout(t, func() error {
		return cmdMirror(context.Background(), []string{"maven"})
	}); !strings.Contains(got, "预览") || !strings.Contains(got, "--apply") {
		t.Fatalf("maven mirror preview = %q", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".m2", "settings-jkv.xml")); !os.IsNotExist(err) {
		t.Fatalf("maven preview wrote settings-jkv.xml: %v", err)
	}
	if err := cmdMirror(context.Background(), []string{"maven", "--apply"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".m2", "settings-jkv.xml")); err != nil {
		t.Fatalf("maven apply missing settings-jkv.xml: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".m2", "settings.xml")); err != nil {
		t.Fatalf("maven apply missing settings.xml: %v", err)
	}
	if got := captureStdout(t, func() error {
		return cmdMirror(context.Background(), []string{"gradle"})
	}); !strings.Contains(got, "预览") {
		t.Fatalf("gradle mirror preview = %q", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".gradle", "init.d", "jkv-mirrors.gradle")); !os.IsNotExist(err) {
		t.Fatalf("gradle preview wrote init.d file: %v", err)
	}
	if err := cmdMirror(context.Background(), []string{"gradle", "--apply"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".gradle", "init.d", "jkv-mirrors.gradle")); err != nil {
		t.Fatalf("gradle apply missing init.d file: %v", err)
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
	wantShell := "fish"
	if runtime.GOOS == "windows" {
		wantShell = "powershell"
	}
	if guessedShell() != wantShell {
		t.Fatalf("guessed shell = %q, want %q", guessedShell(), wantShell)
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
	root := t.TempDir()
	t.Setenv("JKV_DIR", root)
	s := store.New(root)
	seedTrustedCatalog(t, s, 1, testSnapshotRelease{candidate: "maven", vendor: "maven", version: "3.9.11", selector: "3.9.11", url: "https://example.test/maven.zip", tier: "core"})
	digest := sha256.Sum256(archive.Bytes())
	cacheArchive := filepath.Join(s.Root, "cache", "downloads", "maven", "3.9.11", "archive")
	if err := os.MkdirAll(filepath.Dir(cacheArchive), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cacheArchive, archive.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	cacheMetadata, err := json.Marshal(struct {
		SchemaVersion int             `json:"schema_version"`
		Release       catalog.Release `json:"release"`
		SHA256        string          `json:"sha256"`
	}{SchemaVersion: 1, Release: catalog.Release{Candidate: "maven", Version: "3.9.11", URL: "https://example.test/maven.zip"}, SHA256: hex.EncodeToString(digest[:])})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(cacheArchive), "metadata.json"), cacheMetadata, 0o600); err != nil {
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
	if got := padRight("中文", 6); got != "中文  " {
		t.Fatalf("CJK padding = %q", got)
	}
	if shQuote("a'b") != `'a'\''b'` || psQuote("a'b") != "'a''b'" || fishQuote("a'b") != `'a\'b'` {
		t.Fatal("shell quoting failed")
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

func TestEveryCommandSupportsHelpFlag(t *testing.T) {
	for alias, command := range commandAliases {
		if command == "help" {
			continue
		}
		output := captureStdout(t, func() error {
			return run(context.Background(), []string{alias, "--help"})
		})
		if !strings.Contains(output, "用法") {
			t.Errorf("%s --help lacks usage text: %q", alias, output)
		}
		if !printCommandHelp(command) {
			t.Errorf("no help entry for command %q", command)
		}
	}
}

func TestUserFacingErrorsSayToolInsteadOfCandidate(t *testing.T) {
	s := store.New(t.TempDir())
	cleanErr := cmdClean(context.Background(), s, []string{"downloads", "notatool"})
	if cleanErr == nil || !strings.Contains(cleanErr.Error(), `不支持工具 "notatool"`) {
		t.Fatalf("clean error = %v", cleanErr)
	}
	_, _, shellErr := shellFlag([]string{"use", "a/b", "21"})
	if shellErr != nil {
		t.Fatal(shellErr)
	}
	if _, err := installedMatch(s, "a/b", "21"); err == nil || !strings.Contains(err.Error(), "不支持工具") {
		t.Fatalf("installedMatch error = %v", err)
	}
	cleanErr2 := usageErrorf("不支持工具 %q", "x")
	if got := exitCodeFor(cleanErr2); got != exitUsage {
		t.Fatalf("exitCodeFor(不支持工具) = %d", got)
	}
}

func TestSelectReleasePrefersTemurinAcrossVendors(t *testing.T) {
	releases := []catalog.Release{
		{Candidate: "java", Version: "21.0.1-dragonwell", Vendor: "dragonwell"},
		{Candidate: "java", Version: "21.0.8+9-tem", Vendor: "temurin"},
		{Candidate: "java", Version: "21.0.2-bisheng", Vendor: "bisheng"},
		{Candidate: "java", Version: "17.0.12+11-tem", Vendor: "temurin"},
	}
	got, err := selectRelease(releases, "21")
	if err != nil || got.Version != "21.0.8+9-tem" {
		t.Fatalf("prefix 21 = %#v, %v; want temurin", got, err)
	}
	got, err = selectRelease(releases, "21-dragonwell")
	if err != nil || got.Vendor != "dragonwell" {
		t.Fatalf("explicit vendor = %#v, %v", got, err)
	}
	got, err = selectRelease(releases, "17")
	if err != nil || got.Version != "17.0.12+11-tem" {
		t.Fatalf("single-vendor prefix = %#v, %v", got, err)
	}
}

func TestListDefaultColumnsHideGovernanceDetail(t *testing.T) {
	s := store.New(t.TempDir())
	seedTrustedCatalog(t, s, 1, testSnapshotRelease{candidate: "java", vendor: "temurin", version: "21.0.8+9", selector: "21.0.8+9-tem", url: "https://example.test/jdk.tar.gz", tier: "core"})
	plain := captureStdout(t, func() error {
		return cmdList(context.Background(), s, []string{"java"})
	})
	if !strings.Contains(plain, "AVAILABLE") || strings.Contains(plain, "TIER") || strings.Contains(plain, "INTEGRITY") {
		t.Fatalf("default columns = %q", plain)
	}
	verboseContext := context.WithValue(context.Background(), cliOptionsKey{}, cliOptions{Verbose: true})
	detailed := captureStdout(t, func() error {
		return cmdList(verboseContext, s, []string{"java"})
	})
	for _, want := range []string{"TIER", "INTEGRITY", "SOURCE"} {
		if !strings.Contains(detailed, want) {
			t.Fatalf("verbose columns missing %s: %q", want, detailed)
		}
	}
}
