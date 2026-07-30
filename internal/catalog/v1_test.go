package catalog

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These deterministic keys exist only for protocol tests. They must never be used for publication.
const testKeyID = "catalog-test-2026-a"

var testPrivateKey = ed25519.NewKeyFromSeed([]byte{
	0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
	0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f,
	0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17,
	0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f,
})

var testPrivateKeyB = ed25519.NewKeyFromSeed([]byte{
	0x20, 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27,
	0x28, 0x29, 0x2a, 0x2b, 0x2c, 0x2d, 0x2e, 0x2f,
	0x30, 0x31, 0x32, 0x33, 0x34, 0x35, 0x36, 0x37,
	0x38, 0x39, 0x3a, 0x3b, 0x3c, 0x3d, 0x3e, 0x3f,
})

func TestVerifySnapshotAcceptsSignedFixture(t *testing.T) {
	data := readCatalogFixture(t, "catalog-v1-valid.json")
	golden := readCatalogFixture(t, "catalog-v1-valid.sha256")
	actual := sha256.Sum256(data)
	if want := strings.Fields(string(golden)); len(want) != 2 || !strings.EqualFold(want[0], hex.EncodeToString(actual[:])) {
		t.Fatalf("fixture SHA-256 mismatch: %q", golden)
	}
	envelope := signedEnvelope(t, data, testKeyID)

	got, verification, err := VerifySnapshot(data, envelope, map[string]ed25519.PublicKey{
		testKeyID: testPrivateKey.Public().(ed25519.PublicKey),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != 1 || got.Sequence != 42 {
		t.Fatalf("snapshot identity = schema %d sequence %d", got.SchemaVersion, got.Sequence)
	}
	if len(verification.KeyIDs) != 1 || verification.KeyIDs[0] != testKeyID {
		t.Fatalf("verified keys = %#v", verification.KeyIDs)
	}
}

func TestParseSnapshotAcceptsMinimalFixture(t *testing.T) {
	if _, err := ParseSnapshot(readCatalogFixture(t, "catalog-v1-minimal.json")); err != nil {
		t.Fatal(err)
	}
}

func TestSignatureEnvelopeFixtures(t *testing.T) {
	data := readCatalogFixture(t, "catalog-v1-minimal.json")
	keys := map[string]ed25519.PublicKey{
		testKeyID:             testPrivateKey.Public().(ed25519.PublicKey),
		"catalog-test-2026-b": testPrivateKeyB.Public().(ed25519.PublicKey),
	}
	if _, _, err := VerifySnapshot(data, readCatalogFixture(t, "signature-envelope-v1-valid.json"), keys); err != nil {
		t.Fatal(err)
	}
	if verification, err := VerifySignatures(data, readCatalogFixture(t, "signature-envelope-v1-multi.json"), keys); err != nil {
		t.Fatal(err)
	} else if len(verification.KeyIDs) != 2 {
		t.Fatalf("verified keys = %#v", verification.KeyIDs)
	}
	if _, err := VerifySignatures(data, readCatalogFixture(t, "signature-envelope-v1-invalid.json"), keys); err == nil {
		t.Fatal("invalid envelope accepted")
	}
}

func TestProtocolSchemasAreValidJSON(t *testing.T) {
	for _, name := range []string{
		"catalog-v1.schema.json",
		"latest-v1.schema.json",
		"signature-envelope-v1.schema.json",
	} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("schema", name))
			if err != nil {
				t.Fatal(err)
			}
			if !json.Valid(data) {
				t.Fatal("schema is not valid JSON")
			}
		})
	}
}

func TestSchemaRequiredFieldsHaveValidatorCoverage(t *testing.T) {
	type schemaNode struct {
		Required []string `json:"required"`
	}
	var schema struct {
		Required []string              `json:"required"`
		Defs     map[string]schemaNode `json:"$defs"`
	}
	data, err := os.ReadFile(filepath.Join("schema", "catalog-v1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	for _, required := range [][]string{
		schema.Required,
		schema.Defs["candidate"].Required,
		schema.Defs["vendor"].Required,
		schema.Defs["release"].Required,
		schema.Defs["artifact"].Required,
	} {
		if len(required) == 0 {
			t.Fatal("schema required list is empty")
		}
	}

	var snapshot map[string]json.RawMessage
	if err := json.Unmarshal(readCatalogFixture(t, "catalog-v1-valid.json"), &snapshot); err != nil {
		t.Fatal(err)
	}
	for _, field := range schema.Required {
		field := field
		t.Run("missing/"+field, func(t *testing.T) {
			copy := make(map[string]json.RawMessage, len(snapshot)-1)
			for key, value := range snapshot {
				if key != field {
					copy[key] = value
				}
			}
			missing, err := json.Marshal(copy)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParseSnapshot(missing); err == nil {
				t.Fatalf("missing required field %q accepted", field)
			}
		})
	}
}

func TestVerifySignaturesRejectsUntrustedAndMalformedEnvelopes(t *testing.T) {
	data := readCatalogFixture(t, "catalog-v1-valid.json")
	valid := signedEnvelope(t, data, testKeyID)
	publicKey := testPrivateKey.Public().(ed25519.PublicKey)

	if _, err := VerifySignatures(data, valid, map[string]ed25519.PublicKey{"other-key": publicKey}); err == nil {
		t.Fatal("untrusted key accepted")
	}
	if _, err := VerifySignatures(data, []byte(`{"signatures":[{"key_id":"`+testKeyID+`","algorithm":"ed25519","signature":"bad"}]}`), map[string]ed25519.PublicKey{testKeyID: publicKey}); err == nil {
		t.Fatal("malformed signature accepted")
	}
	if _, err := VerifySignatures(data, []byte(`{"signatures":[]}`), map[string]ed25519.PublicKey{testKeyID: publicKey}); err == nil {
		t.Fatal("empty envelope accepted")
	}
}

func TestParseSnapshotRejectsTrailingJSONAndOversizedInput(t *testing.T) {
	valid := readCatalogFixture(t, "catalog-v1-valid.json")
	if _, err := ParseSnapshot(append(append([]byte{}, valid...), []byte(` {}`)...)); err == nil {
		t.Fatal("trailing JSON accepted")
	}
	if _, err := ParseSnapshot(make([]byte, maxSnapshotBytes+1)); err == nil {
		t.Fatal("oversized snapshot accepted")
	}
}

func TestVerifySnapshotBindsSignatureToOriginalBytes(t *testing.T) {
	data := readCatalogFixture(t, "catalog-v1-valid.json")
	envelope := signedEnvelope(t, data, testKeyID)
	changed := append([]byte("\n"), data...)

	_, _, err := VerifySnapshot(changed, envelope, map[string]ed25519.PublicKey{
		testKeyID: testPrivateKey.Public().(ed25519.PublicKey),
	})
	if err == nil {
		t.Fatalf("changed bytes accepted: %v", err)
	}
}

func TestVerifySnapshotAllowsUnknownSignatureWithTrustedSignature(t *testing.T) {
	data := readCatalogFixture(t, "catalog-v1-valid.json")
	envelope := signedEnvelope(t, data, testKeyID)
	var parsed SignatureEnvelope
	if err := json.Unmarshal(envelope, &parsed); err != nil {
		t.Fatal(err)
	}
	parsed.Signatures = append(parsed.Signatures, Signature{
		KeyID:     "future-key",
		Algorithm: "ed25519",
		Signature: base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize)),
	})
	envelope, err := json.Marshal(parsed)
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := VerifySnapshot(data, envelope, map[string]ed25519.PublicKey{
		testKeyID: testPrivateKey.Public().(ed25519.PublicKey),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestVerifySnapshotAcceptsMultipleTrustedSignatures(t *testing.T) {
	data := readCatalogFixture(t, "catalog-v1-valid.json")
	envelope := SignatureEnvelope{Signatures: []Signature{
		{KeyID: testKeyID, Algorithm: "ed25519", Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(testPrivateKey, data))},
		{KeyID: "catalog-test-2026-b", Algorithm: "ed25519", Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(testPrivateKeyB, data))},
	}}
	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	_, verification, err := VerifySnapshot(data, envelopeBytes, map[string]ed25519.PublicKey{
		testKeyID:             testPrivateKey.Public().(ed25519.PublicKey),
		"catalog-test-2026-b": testPrivateKeyB.Public().(ed25519.PublicKey),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(verification.KeyIDs) != 2 {
		t.Fatalf("verified keys = %#v", verification.KeyIDs)
	}
}

func TestVerifySnapshotRejectsInvalidFixtures(t *testing.T) {
	for _, name := range []string{
		"catalog-v1-invalid-duplicate-selector.json",
		"catalog-v1-invalid-duplicate-artifact-id.json",
		"catalog-v1-invalid-platform-ambiguity.json",
		"catalog-v1-invalid-archive-type.json",
		"catalog-v1-invalid-http-url.json",
		"catalog-v1-invalid-private-url.json",
		"catalog-v1-invalid-redirect-host.json",
		"catalog-v1-invalid-checksum.json",
		"catalog-v1-invalid-prerelease.json",
		"catalog-v1-invalid-revoked-active.json",
		"catalog-v1-invalid-future-schema.json",
	} {
		t.Run("static/"+name, func(t *testing.T) {
			if _, err := ParseSnapshot(readCatalogFixture(t, name)); err == nil {
				t.Fatal("invalid fixture accepted")
			}
		})
	}

	for _, tc := range []struct {
		name   string
		mutate func(*Snapshot)
	}{
		{"duplicate selector", func(snapshot *Snapshot) {
			releases := &snapshot.Candidates[0].Vendors[0].Releases
			(*releases)[1].Selector = (*releases)[0].Selector
		}},
		{"duplicate artifact ID", func(snapshot *Snapshot) {
			artifacts := &snapshot.Candidates[0].Vendors[0].Releases[1].Artifacts
			(*artifacts)[0].ArtifactID = snapshot.Candidates[0].Vendors[0].Releases[0].Artifacts[0].ArtifactID
		}},
		{"platform ambiguity", func(snapshot *Snapshot) {
			release := &snapshot.Candidates[0].Vendors[0].Releases[0]
			duplicate := release.Artifacts[0]
			duplicate.ArtifactID += "/duplicate"
			duplicate.Platforms = []Platform{{OS: "linux", Arch: "x64"}}
			release.Artifacts = append(release.Artifacts, duplicate)
		}},
		{"HTTP URL", func(snapshot *Snapshot) {
			snapshot.Candidates[0].Vendors[0].Releases[0].Artifacts[0].URL = "http://artifacts.example.invalid/jdk.tar.gz"
		}},
		{"private URL", func(snapshot *Snapshot) {
			snapshot.Candidates[0].Vendors[0].Releases[0].Artifacts[0].URL = "https://127.0.0.1/jdk.tar.gz"
		}},
		{"checksum format", func(snapshot *Snapshot) {
			snapshot.Candidates[0].Vendors[0].Releases[0].Artifacts[0].Checksum.Value = strings.Repeat("z", 64)
		}},
		{"prerelease", func(snapshot *Snapshot) {
			snapshot.Candidates[0].Vendors[0].Releases[0].Version = "21.0.8-RC1"
		}},
		{"active revoked artifact", func(snapshot *Snapshot) {
			snapshot.Revocations[0].ArtifactID = snapshot.Candidates[0].Vendors[0].Releases[0].Artifacts[0].ArtifactID
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var snapshot Snapshot
			if err := json.Unmarshal(readCatalogFixture(t, "catalog-v1-valid.json"), &snapshot); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&snapshot)
			data, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParseSnapshot(data); err == nil {
				t.Fatal("invalid fixture accepted")
			}
		})
	}
}

func TestVerifyLatestSnapshotChecksPointerBinding(t *testing.T) {
	snapshot := readCatalogFixture(t, "catalog-v1-valid.json")
	hash := sha256.Sum256(snapshot)
	latest := Latest{
		SchemaVersion:  1,
		Sequence:       42,
		SnapshotSHA256: hex.EncodeToString(hash[:]),
		ReleaseTag:     "catalog-v1-000042",
		SnapshotAsset:  "catalog-v1.json",
	}
	latestBytes, err := json.Marshal(latest)
	if err != nil {
		t.Fatal(err)
	}
	latestEnvelope := signedEnvelope(t, latestBytes, testKeyID)
	snapshotEnvelope := signedEnvelope(t, snapshot, testKeyID)
	keys := map[string]ed25519.PublicKey{testKeyID: testPrivateKey.Public().(ed25519.PublicKey)}

	got, err := VerifyLatestSnapshot(latestBytes, latestEnvelope, snapshot, snapshotEnvelope, keys)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sequence != latest.Sequence {
		t.Fatalf("sequence = %d", got.Sequence)
	}

	latest.SnapshotSHA256 = strings.Repeat("0", sha256.Size*2)
	latestBytes, err = json.Marshal(latest)
	if err != nil {
		t.Fatal(err)
	}
	latestEnvelope = signedEnvelope(t, latestBytes, testKeyID)
	if _, err := VerifyLatestSnapshot(latestBytes, latestEnvelope, snapshot, snapshotEnvelope, keys); err == nil {
		t.Fatal("snapshot hash mismatch accepted")
	}
}

func TestParseLatestRejectsOversizedPointer(t *testing.T) {
	data := []byte(`{"schema_version":1,"sequence":42,"snapshot_sha256":"` + strings.Repeat("0", 64) + `","release_tag":"catalog-v1-000042","snapshot_asset":"` + strings.Repeat("x", maxIdentifierBytes+1) + `"}`)
	if _, err := ParseLatest(data); err == nil {
		t.Fatal("oversized latest pointer accepted")
	}
}

func signedEnvelope(t *testing.T, data []byte, keyID string) []byte {
	t.Helper()
	envelope := SignatureEnvelope{Signatures: []Signature{{
		KeyID:     keyID,
		Algorithm: "ed25519",
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(testPrivateKey, data)),
	}}}
	b, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func readCatalogFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
