package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fishandsheep/jkv/internal/catalog"
	"github.com/fishandsheep/jkv/internal/store"
)

func TestSaveFetchedCatalogRejectsRollbackAndSameSequenceRewrite(t *testing.T) {
	s := store.New(t.TempDir())
	var state store.TrustedCatalogState
	if err := saveFetchedCatalog(s, state, testFetchedCatalog(2, "two")); err != nil {
		t.Fatal(err)
	}
	state, err := s.LoadTrustedCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if err := saveFetchedCatalog(s, state, testFetchedCatalog(1, "one")); err == nil {
		t.Fatal("rollback accepted")
	}
	if err := saveFetchedCatalog(s, state, testFetchedCatalog(2, "different")); err == nil {
		t.Fatal("same sequence rewrite accepted")
	}
	if err := saveFetchedCatalog(s, state, testFetchedCatalog(2, "two")); err != nil {
		t.Fatal(err)
	}
}

func TestLoadSignedSnapshotUsesReleaseLayoutAndTrustedCache(t *testing.T) {
	snapshot := readV03Snapshot(t)
	private := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, ed25519.SeedSize))
	keyID := "test-v03"
	latest := signedLatest(t, snapshot, private, keyID)
	snapshotSig := signedDocument(t, snapshot, private, keyID)
	latestSig := signedDocument(t, latest, private, keyID)
	available := true
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if !available {
			http.Error(w, "offline", http.StatusServiceUnavailable)
			return
		}
		switch request.URL.Path {
		case "/download/catalog-latest/latest.json":
			_, _ = w.Write(latest)
		case "/download/catalog-latest/latest.json.sig":
			_, _ = w.Write(latestSig)
		case "/download/catalog-v1-000042/catalog-v1.json":
			_, _ = w.Write(snapshot)
		case "/download/catalog-v1-000042/catalog-v1.json.sig":
			_, _ = w.Write(snapshotSig)
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()
	t.Setenv("JKV_EXPERIMENTAL_CATALOG", "true")
	t.Setenv("JKV_LEGACY_PROVIDER", "false")
	t.Setenv("JKV_CATALOG_KEY_ID", keyID)
	t.Setenv("JKV_CATALOG_PUBLIC_KEY", base64.StdEncoding.EncodeToString(private.Public().(ed25519.PublicKey)))
	t.Setenv("JKV_CATALOG_ENDPOINT", server.URL+"/download")
	t.Setenv("JKV_CATALOG_FALLBACK_ENDPOINT", "")
	originalClient := catalogHTTPClient
	catalogHTTPClient = server.Client()
	defer func() { catalogHTTPClient = originalClient }()
	s := store.New(t.TempDir())
	got, err := loadSignedSnapshot(context.Background(), s, true, true)
	if err != nil || got.Sequence != 42 {
		t.Fatalf("snapshot = %#v, %v", got, err)
	}
	state, err := s.LoadTrustedCatalog()
	if err != nil || state.HighestSequence != 42 || len(state.Snapshots) != 1 {
		t.Fatalf("state = %#v, %v", state, err)
	}
	available = false
	cached, err := loadSignedSnapshot(context.Background(), s, true, true)
	if err != nil || cached.Sequence != 42 {
		t.Fatalf("cached = %#v, %v", cached, err)
	}
}

func TestCatalogSettingsUsesBuildTimeTrustRoot(t *testing.T) {
	private := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{4}, ed25519.SeedSize))
	originalKeyID, originalPublic := catalogKeyID, catalogPublicKeyBase64
	catalogKeyID = "build-key"
	catalogPublicKeyBase64 = base64.StdEncoding.EncodeToString(private.Public().(ed25519.PublicKey))
	defer func() {
		catalogKeyID = originalKeyID
		catalogPublicKeyBase64 = originalPublic
	}()
	t.Setenv("JKV_CATALOG_KEY_ID", "")
	t.Setenv("JKV_CATALOG_PUBLIC_KEY", "")
	t.Setenv("JKV_CATALOG_ENDPOINT", "")
	t.Setenv("JKV_CATALOG_FALLBACK_ENDPOINT", "")
	keys, endpoints, err := catalogSettings()
	if err != nil || len(keys) != 1 || len(endpoints) != 2 || endpoints[0].Name != "CNB" || endpoints[1].Name != "GitHub" {
		t.Fatalf("keys = %#v, endpoints = %#v, err = %v", keys, endpoints, err)
	}
}

func TestCatalogMinimumClientVersion(t *testing.T) {
	original := version
	defer func() { version = original }()

	version = "v0.0.1"
	if _, err := compatibleCatalogSnapshot(catalog.Snapshot{MinClientVersion: "0.0.1"}); err != nil {
		t.Fatalf("matching version rejected: %v", err)
	}
	version = "v0.0.2"
	if _, err := compatibleCatalogSnapshot(catalog.Snapshot{MinClientVersion: "0.0.1"}); err != nil {
		t.Fatalf("newer version rejected: %v", err)
	}
	version = "v0.0.0"
	if _, err := compatibleCatalogSnapshot(catalog.Snapshot{MinClientVersion: "0.0.1"}); err == nil || !strings.Contains(err.Error(), "请升级") {
		t.Fatalf("old client accepted: %v", err)
	}
	version = "dev"
	if _, err := compatibleCatalogSnapshot(catalog.Snapshot{MinClientVersion: "999.0.0"}); err != nil {
		t.Fatalf("development client rejected: %v", err)
	}
}

func TestRejectRevokedArtifact(t *testing.T) {
	s := store.New(t.TempDir())
	state := store.TrustedCatalogState{Revocations: []catalog.Revocation{{ArtifactID: "bad", Reason: "compromised", RevokedAt: "2026-07-31T00:00:00Z"}}}
	if err := saveFetchedCatalog(s, state, testFetchedCatalog(1, "one")); err != nil {
		t.Fatal(err)
	}
	if err := rejectRevokedArtifact(s, catalog.Release{ArtifactID: "bad"}); err == nil || !strings.Contains(err.Error(), "已撤销") {
		t.Fatalf("revocation error = %v", err)
	}
}

func TestDoctorShowsTrustedCatalogState(t *testing.T) {
	root := t.TempDir()
	t.Setenv("JKV_DIR", root)
	s := store.New(root)
	if err := saveFetchedCatalog(s, store.TrustedCatalogState{}, testFetchedCatalog(1, "one")); err != nil {
		t.Fatal(err)
	}
	original := doctorHTTPClient
	doctorHTTPClient = &http.Client{Transport: testRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header), Request: request}, nil
	})}
	defer func() { doctorHTTPClient = original }()
	output := captureStdout(t, func() error { return cmdDoctor(context.Background(), s, nil) })
	if !strings.Contains(output, "可信 Catalog: sequence 1") {
		t.Fatalf("doctor = %q", output)
	}
}

func TestSaveFetchedCatalogKeepsThreeSnapshotsAndRevocations(t *testing.T) {
	s := store.New(t.TempDir())
	var state store.TrustedCatalogState
	for sequence := uint64(1); sequence <= 4; sequence++ {
		fetched := testFetchedCatalog(sequence, string(rune('a'+sequence)))
		if sequence == 1 {
			fetched.Snapshot.Revocations = []catalog.Revocation{{ArtifactID: "java-temurin-1", Reason: "bad archive", RevokedAt: "2026-07-31T00:00:00Z"}}
		}
		if err := saveFetchedCatalog(s, state, fetched); err != nil {
			t.Fatal(err)
		}
		var err error
		state, err = s.LoadTrustedCatalog()
		if err != nil {
			t.Fatal(err)
		}
	}
	if state.HighestSequence != 4 || len(state.Snapshots) != 3 {
		t.Fatalf("state = %#v", state)
	}
	if len(state.Revocations) != 1 || state.Revocations[0].ArtifactID != "java-temurin-1" {
		t.Fatalf("revocations = %#v", state.Revocations)
	}
	for _, snapshot := range state.Snapshots {
		if snapshot.Sequence == 1 {
			t.Fatal("oldest snapshot was retained")
		}
	}
}

func TestMergeRevocationsPreservesExistingArtifact(t *testing.T) {
	got := mergeRevocations(
		[]catalog.Revocation{{ArtifactID: "old", Reason: "old", RevokedAt: "2026-07-31T00:00:00Z"}},
		[]catalog.Revocation{{ArtifactID: "new", Reason: "new", RevokedAt: "2026-07-31T00:00:00Z"}},
	)
	if len(got) != 2 || got[0].ArtifactID != "new" || got[1].ArtifactID != "old" {
		t.Fatalf("revocations = %#v", got)
	}
}

func TestTrustedSnapshotUpdateRefreshesOnlyMetadata(t *testing.T) {
	s := store.New(t.TempDir())
	fetched := testFetchedCatalog(1, "same")
	if err := saveFetchedCatalog(s, store.TrustedCatalogState{}, fetched); err != nil {
		t.Fatal(err)
	}
	state, err := s.LoadTrustedCatalog()
	if err != nil {
		t.Fatal(err)
	}
	before := state.Snapshots[0].FetchedAt
	time.Sleep(time.Millisecond)
	if err := saveFetchedCatalog(s, state, fetched); err != nil {
		t.Fatal(err)
	}
	state, err = s.LoadTrustedCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if !state.Snapshots[0].FetchedAt.After(before) || !bytes.Equal(state.Snapshots[0].Snapshot, fetched.SnapshotBytes) {
		t.Fatalf("snapshot = %#v", state.Snapshots[0])
	}
}

func testFetchedCatalog(sequence uint64, body string) catalog.FetchedSnapshot {
	b := []byte(body)
	return catalog.FetchedSnapshot{
		Endpoint:          "test",
		KeyIDs:            []string{"test-key"},
		Snapshot:          catalog.Snapshot{Sequence: sequence},
		SnapshotBytes:     b,
		SnapshotSignature: append([]byte("snapshot-"), b...),
		LatestBytes:       append([]byte("latest-"), b...),
		LatestSignature:   append([]byte("latest-signature-"), b...),
	}
}

func readV03Snapshot(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "internal", "catalog", "testdata", "catalog-v1-valid.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func signedLatest(t *testing.T, snapshot []byte, private ed25519.PrivateKey, keyID string) []byte {
	t.Helper()
	hash := sha256.Sum256(snapshot)
	data, err := json.Marshal(catalog.Latest{SchemaVersion: 1, Sequence: 42, SnapshotSHA256: hex.EncodeToString(hash[:]), ReleaseTag: "catalog-v1-000042", SnapshotAsset: "catalog-v1.json"})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func signedDocument(t *testing.T, data []byte, private ed25519.PrivateKey, keyID string) []byte {
	t.Helper()
	envelope, err := json.Marshal(catalog.SignatureEnvelope{Signatures: []catalog.Signature{{KeyID: keyID, Algorithm: "ed25519", Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(private, data))}}})
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}
