package catalog

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRemoteClientFetchesReleaseEndpointLayout(t *testing.T) {
	snapshot := readCatalogFixture(t, "catalog-v1-valid.json")
	hash := sha256.Sum256(snapshot)
	latest, err := json.Marshal(Latest{
		SchemaVersion:  1,
		Sequence:       42,
		SnapshotSHA256: hex.EncodeToString(hash[:]),
		ReleaseTag:     "catalog-v1-000042",
		SnapshotAsset:  "catalog-v1.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	latestSig := signedEnvelope(t, latest, testKeyID)
	snapshotSig := signedEnvelope(t, snapshot, testKeyID)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
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

	fetched, err := (RemoteClient{
		Endpoints:   []Endpoint{ReleaseEndpoint("test", server.URL+"/download")},
		HTTP:        server.Client(),
		TrustedKeys: map[string]ed25519.PublicKey{testKeyID: testPrivateKey.Public().(ed25519.PublicKey)},
	}).FetchDocument(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if fetched.Endpoint != "test" || fetched.Snapshot.Sequence != 42 || fetched.Latest.ReleaseTag != "catalog-v1-000042" {
		t.Fatalf("fetched = %#v", fetched)
	}
}

func TestRemoteClientFallsBackOnlyForDownloadFailure(t *testing.T) {
	snapshot := readCatalogFixture(t, "catalog-v1-valid.json")
	hash := sha256.Sum256(snapshot)
	latest, err := json.Marshal(Latest{SchemaVersion: 1, Sequence: 42, SnapshotSHA256: hex.EncodeToString(hash[:]), ReleaseTag: "catalog-v1-000042", SnapshotAsset: "catalog-v1.json"})
	if err != nil {
		t.Fatal(err)
	}
	latestSig := signedEnvelope(t, latest, testKeyID)
	snapshotSig := signedEnvelope(t, snapshot, testKeyID)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/primary/catalog-latest/latest.json":
			http.Error(w, "down", http.StatusServiceUnavailable)
		case "/fallback/catalog-latest/latest.json":
			_, _ = w.Write(latest)
		case "/fallback/catalog-latest/latest.json.sig":
			_, _ = w.Write(latestSig)
		case "/fallback/catalog-v1-000042/catalog-v1.json":
			_, _ = w.Write(snapshot)
		case "/fallback/catalog-v1-000042/catalog-v1.json.sig":
			_, _ = w.Write(snapshotSig)
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	fetched, err := (RemoteClient{
		Endpoints: []Endpoint{
			ReleaseEndpoint("primary", server.URL+"/primary"),
			ReleaseEndpoint("fallback", server.URL+"/fallback"),
		},
		HTTP:        server.Client(),
		TrustedKeys: map[string]ed25519.PublicKey{testKeyID: testPrivateKey.Public().(ed25519.PublicKey)},
	}).FetchDocument(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if fetched.Endpoint != "fallback" {
		t.Fatalf("endpoint = %q", fetched.Endpoint)
	}
}

func TestRemoteClientDoesNotFallbackAfterSignatureFailure(t *testing.T) {
	snapshot := readCatalogFixture(t, "catalog-v1-valid.json")
	hash := sha256.Sum256(snapshot)
	latest, err := json.Marshal(Latest{SchemaVersion: 1, Sequence: 42, SnapshotSHA256: hex.EncodeToString(hash[:]), ReleaseTag: "catalog-v1-000042", SnapshotAsset: "catalog-v1.json"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/primary/catalog-latest/latest.json":
			_, _ = w.Write(latest)
		case "/primary/catalog-latest/latest.json.sig":
			_, _ = w.Write([]byte(`{"signatures":[]}`))
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	_, err = (RemoteClient{
		Endpoints: []Endpoint{
			ReleaseEndpoint("primary", server.URL+"/primary"),
			ReleaseEndpoint("fallback", server.URL+"/fallback"),
		},
		HTTP:        server.Client(),
		TrustedKeys: map[string]ed25519.PublicKey{testKeyID: testPrivateKey.Public().(ed25519.PublicKey)},
	}).FetchDocument(t.Context())
	if err == nil {
		t.Fatal("signature failure fell back")
	}
}
