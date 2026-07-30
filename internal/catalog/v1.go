package catalog

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxLatestBytes      = 16 << 10
	maxEnvelopeBytes    = 64 << 10
	maxSnapshotBytes    = 8 << 20
	maxCandidates       = 500
	maxReleases         = 50_000
	maxArtifacts        = 100_000
	maxURLBytes         = 2_048
	maxIdentifierBytes  = 128
	maxDescriptionBytes = 2_048
	maxRedirectHosts    = 5
)

var (
	gitSHA       = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
	releaseTagRE = regexp.MustCompile(`^catalog-v1-[0-9]{6}$`)
	identifierRE = regexp.MustCompile(`^[^/\\[:cntrl:]]+$`)
	platformsV1  = map[string]bool{
		"linux/x64":       true,
		"linux/aarch64":   true,
		"darwin/x64":      true,
		"darwin/aarch64":  true,
		"windows/x64":     true,
		"windows/aarch64": true,
	}
)

// Snapshot is the signed, immutable Catalog v1 payload.
type Snapshot struct {
	SchemaVersion    int                 `json:"schema_version"`
	Sequence         uint64              `json:"sequence"`
	PublishedAt      string              `json:"published_at"`
	SourceCommit     string              `json:"source_commit"`
	MinClientVersion string              `json:"min_client_version"`
	Candidates       []SnapshotCandidate `json:"candidates"`
	Revocations      []Revocation        `json:"revocations,omitempty"`
}

type SnapshotCandidate struct {
	Name          string   `json:"name"`
	DisplayName   string   `json:"display_name"`
	Description   string   `json:"description"`
	Homepage      string   `json:"homepage"`
	HomeEnv       string   `json:"home_env,omitempty"`
	DefaultVendor string   `json:"default_vendor"`
	Vendors       []Vendor `json:"vendors"`
}

type Vendor struct {
	Name        string            `json:"name"`
	DisplayName string            `json:"display_name"`
	Releases    []SnapshotRelease `json:"releases"`
}

type SnapshotRelease struct {
	Version     string     `json:"version"`
	Selector    string     `json:"selector"`
	SupportTier string     `json:"support_tier"`
	Artifacts   []Artifact `json:"artifacts"`
}

type Artifact struct {
	ArtifactID           string     `json:"artifact_id"`
	ArchiveType          string     `json:"archive_type"`
	Platforms            []Platform `json:"platforms"`
	URL                  string     `json:"url"`
	AllowedRedirectHosts []string   `json:"allowed_redirect_hosts,omitempty"`
	Checksum             *Checksum  `json:"checksum,omitempty"`
}

type Checksum struct {
	Algorithm string `json:"algorithm"`
	Value     string `json:"value"`
	Source    string `json:"source"`
	SourceURL string `json:"source_url,omitempty"`
}

type Revocation struct {
	ArtifactID            string `json:"artifact_id"`
	Reason                string `json:"reason"`
	Message               string `json:"message,omitempty"`
	RevokedAt             string `json:"revoked_at"`
	ReplacementArtifactID string `json:"replacement_artifact_id,omitempty"`
}

// Latest is the signed mutable pointer to one immutable Snapshot asset.
type Latest struct {
	SchemaVersion  int    `json:"schema_version"`
	Sequence       uint64 `json:"sequence"`
	SnapshotSHA256 string `json:"snapshot_sha256"`
	ReleaseTag     string `json:"release_tag"`
	SnapshotAsset  string `json:"snapshot_asset"`
}

type SignatureEnvelope struct {
	Signatures []Signature `json:"signatures"`
}

type Signature struct {
	KeyID     string `json:"key_id"`
	Algorithm string `json:"algorithm"`
	Signature string `json:"signature"`
}

type SignatureVerification struct {
	KeyIDs []string
}

// ParseSnapshot validates JSON and all v1 cross-field constraints.
func ParseSnapshot(data []byte) (Snapshot, error) {
	if len(data) > maxSnapshotBytes {
		return Snapshot{}, fmt.Errorf("catalog snapshot exceeds %d bytes", maxSnapshotBytes)
	}
	var snapshot Snapshot
	if err := decodeJSON(data, &snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("invalid catalog snapshot: %w", err)
	}
	if err := ValidateSnapshot(snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("invalid catalog snapshot: %w", err)
	}
	return snapshot, nil
}

// ParseLatest validates a signed latest.json payload without trusting its pointer.
func ParseLatest(data []byte) (Latest, error) {
	if len(data) > maxLatestBytes {
		return Latest{}, fmt.Errorf("latest pointer exceeds %d bytes", maxLatestBytes)
	}
	var latest Latest
	if err := decodeJSON(data, &latest); err != nil {
		return Latest{}, fmt.Errorf("invalid latest pointer: %w", err)
	}
	if err := ValidateLatest(latest); err != nil {
		return Latest{}, fmt.Errorf("invalid latest pointer: %w", err)
	}
	return latest, nil
}

// ValidateSnapshot checks a decoded Snapshot without serializing or mutating it.
func ValidateSnapshot(snapshot Snapshot) error {
	return validateSnapshot(snapshot)
}

// ValidateLatest checks a decoded latest pointer without trusting its target.
func ValidateLatest(latest Latest) error {
	return validateLatest(latest)
}

// VerifySnapshot checks the detached envelope against the original bytes before parsing them.
func VerifySnapshot(data, envelope []byte, trustedKeys map[string]ed25519.PublicKey) (Snapshot, SignatureVerification, error) {
	if len(data) > maxSnapshotBytes {
		return Snapshot{}, SignatureVerification{}, fmt.Errorf("catalog snapshot exceeds %d bytes", maxSnapshotBytes)
	}
	verification, err := VerifySignatures(data, envelope, trustedKeys)
	if err != nil {
		return Snapshot{}, SignatureVerification{}, err
	}
	snapshot, err := ParseSnapshot(data)
	if err != nil {
		return Snapshot{}, SignatureVerification{}, err
	}
	return snapshot, verification, nil
}

// VerifyLatest checks a detached envelope against the original latest.json bytes.
func VerifyLatest(data, envelope []byte, trustedKeys map[string]ed25519.PublicKey) (Latest, SignatureVerification, error) {
	if len(data) > maxLatestBytes {
		return Latest{}, SignatureVerification{}, fmt.Errorf("latest pointer exceeds %d bytes", maxLatestBytes)
	}
	verification, err := VerifySignatures(data, envelope, trustedKeys)
	if err != nil {
		return Latest{}, SignatureVerification{}, err
	}
	latest, err := ParseLatest(data)
	if err != nil {
		return Latest{}, SignatureVerification{}, err
	}
	return latest, verification, nil
}

// VerifySignatures verifies original bytes and accepts the envelope when any trusted key signs them.
// Unknown key IDs are ignored so key rotation can use one envelope before every client knows all keys.
func VerifySignatures(data, envelope []byte, trustedKeys map[string]ed25519.PublicKey) (SignatureVerification, error) {
	if len(envelope) > maxEnvelopeBytes {
		return SignatureVerification{}, fmt.Errorf("signature envelope exceeds %d bytes", maxEnvelopeBytes)
	}
	var parsed SignatureEnvelope
	if err := decodeJSON(envelope, &parsed); err != nil {
		return SignatureVerification{}, fmt.Errorf("invalid signature envelope: %w", err)
	}
	if len(parsed.Signatures) == 0 {
		return SignatureVerification{}, errors.New("signature envelope has no signatures")
	}
	seen := make(map[string]bool, len(parsed.Signatures))
	verified := make([]string, 0, len(parsed.Signatures))
	for i, signature := range parsed.Signatures {
		if err := validateSignature(signature); err != nil {
			return SignatureVerification{}, fmt.Errorf("signature %d: %w", i, err)
		}
		if seen[signature.KeyID] {
			return SignatureVerification{}, fmt.Errorf("duplicate signature key %q", signature.KeyID)
		}
		seen[signature.KeyID] = true
		key, trusted := trustedKeys[signature.KeyID]
		if !trusted {
			continue
		}
		if len(key) != ed25519.PublicKeySize {
			return SignatureVerification{}, fmt.Errorf("trusted key %q has invalid length", signature.KeyID)
		}
		rawSignature, _ := base64.StdEncoding.DecodeString(signature.Signature)
		if ed25519.Verify(key, data, rawSignature) {
			verified = append(verified, signature.KeyID)
		}
	}
	if len(verified) == 0 {
		return SignatureVerification{}, errors.New("no trusted catalog signature verified")
	}
	return SignatureVerification{KeyIDs: verified}, nil
}

// VerifyLatestSnapshot verifies both signed documents and their sequence/hash binding.
func VerifyLatestSnapshot(latestData, latestEnvelope, snapshotData, snapshotEnvelope []byte, trustedKeys map[string]ed25519.PublicKey) (Snapshot, error) {
	latest, _, err := VerifyLatest(latestData, latestEnvelope, trustedKeys)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot, _, err := VerifySnapshot(snapshotData, snapshotEnvelope, trustedKeys)
	if err != nil {
		return Snapshot{}, err
	}
	if latest.Sequence != snapshot.Sequence {
		return Snapshot{}, fmt.Errorf("latest sequence %d does not match snapshot sequence %d", latest.Sequence, snapshot.Sequence)
	}
	hash := sha256.Sum256(snapshotData)
	if !strings.EqualFold(latest.SnapshotSHA256, hex.EncodeToString(hash[:])) {
		return Snapshot{}, errors.New("latest snapshot SHA-256 does not match snapshot bytes")
	}
	return snapshot, nil
}

func decodeJSON(data []byte, dst any) error {
	if !utf8.Valid(data) {
		return errors.New("JSON is not valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func validateSnapshot(snapshot Snapshot) error {
	if snapshot.SchemaVersion != 1 {
		return fmt.Errorf("unsupported schema version %d", snapshot.SchemaVersion)
	}
	if snapshot.Sequence == 0 {
		return errors.New("sequence must be positive")
	}
	if err := validateTimestamp(snapshot.PublishedAt); err != nil {
		return fmt.Errorf("published_at: %w", err)
	}
	if !gitSHA.MatchString(snapshot.SourceCommit) {
		return errors.New("source_commit must be a complete 40-character Git SHA")
	}
	if err := validateText("min_client_version", snapshot.MinClientVersion, maxIdentifierBytes, true); err != nil {
		return err
	}
	if len(snapshot.Candidates) == 0 || len(snapshot.Candidates) > maxCandidates {
		return fmt.Errorf("candidate count must be between 1 and %d", maxCandidates)
	}

	candidateNames := make(map[string]bool, len(snapshot.Candidates))
	activeArtifacts := make(map[string]bool)
	selectorNames := make(map[string]bool)
	revokedArtifacts := make(map[string]bool)
	releaseCount, artifactCount := 0, 0
	for i, candidate := range snapshot.Candidates {
		if err := validateCandidate(candidate); err != nil {
			return fmt.Errorf("candidate %d: %w", i, err)
		}
		if candidateNames[candidate.Name] {
			return fmt.Errorf("duplicate candidate %q", candidate.Name)
		}
		candidateNames[candidate.Name] = true
		vendorNames := make(map[string]bool, len(candidate.Vendors))
		defaultVendorFound := false
		defaultVendorRelease := false
		for _, vendor := range candidate.Vendors {
			if err := validateVendor(vendor); err != nil {
				return fmt.Errorf("candidate %q vendor: %w", candidate.Name, err)
			}
			if vendorNames[vendor.Name] {
				return fmt.Errorf("candidate %q has duplicate vendor %q", candidate.Name, vendor.Name)
			}
			vendorNames[vendor.Name] = true
			if vendor.Name == candidate.DefaultVendor {
				defaultVendorFound = true
			}
			versionNames := make(map[string]bool, len(vendor.Releases))
			for _, release := range vendor.Releases {
				releaseCount++
				if releaseCount > maxReleases {
					return fmt.Errorf("release count exceeds %d", maxReleases)
				}
				if err := validateRelease(release); err != nil {
					return fmt.Errorf("%s/%s release: %w", candidate.Name, vendor.Name, err)
				}
				if versionNames[release.Version] {
					return fmt.Errorf("duplicate release version %q in %s/%s", release.Version, candidate.Name, vendor.Name)
				}
				versionNames[release.Version] = true
				if selectorNames[candidate.Name+"\x00"+release.Selector] {
					return fmt.Errorf("duplicate release selector %q in candidate %q", release.Selector, candidate.Name)
				}
				selectorNames[candidate.Name+"\x00"+release.Selector] = true
				if vendor.Name == candidate.DefaultVendor && len(release.Artifacts) > 0 {
					defaultVendorRelease = true
				}
				platforms := make(map[string]bool)
				for _, artifact := range release.Artifacts {
					artifactCount++
					if artifactCount > maxArtifacts {
						return fmt.Errorf("artifact count exceeds %d", maxArtifacts)
					}
					if activeArtifacts[artifact.ArtifactID] {
						return fmt.Errorf("duplicate artifact ID %q", artifact.ArtifactID)
					}
					if err := validateArtifact(artifact); err != nil {
						return fmt.Errorf("release %q artifact: %w", release.Selector, err)
					}
					activeArtifacts[artifact.ArtifactID] = true
					for _, platform := range artifact.Platforms {
						key := platform.OS + "/" + platform.Arch
						if platforms[key] {
							return fmt.Errorf("release %q has multiple artifacts for %s", release.Selector, key)
						}
						platforms[key] = true
					}
				}
			}
		}
		if !defaultVendorFound {
			return fmt.Errorf("candidate %q default vendor %q does not exist", candidate.Name, candidate.DefaultVendor)
		}
		if !defaultVendorRelease {
			return fmt.Errorf("candidate %q default vendor has no release", candidate.Name)
		}
	}

	for i, revocation := range snapshot.Revocations {
		if err := validateRevocation(revocation); err != nil {
			return fmt.Errorf("revocation %d: %w", i, err)
		}
		if revokedArtifacts[revocation.ArtifactID] {
			return fmt.Errorf("duplicate revocation for artifact %q", revocation.ArtifactID)
		}
		revokedArtifacts[revocation.ArtifactID] = true
		if activeArtifacts[revocation.ArtifactID] {
			return fmt.Errorf("revoked artifact %q is active", revocation.ArtifactID)
		}
	}
	for _, revocation := range snapshot.Revocations {
		if revocation.ReplacementArtifactID != "" && revokedArtifacts[revocation.ReplacementArtifactID] {
			return fmt.Errorf("replacement artifact %q is also revoked", revocation.ReplacementArtifactID)
		}
	}
	return nil
}

func validateCandidate(candidate SnapshotCandidate) error {
	if err := validateSegment("candidate name", candidate.Name); err != nil {
		return err
	}
	if err := validateText("display_name", candidate.DisplayName, maxDescriptionBytes, true); err != nil {
		return err
	}
	if err := validateText("description", candidate.Description, maxDescriptionBytes, true); err != nil {
		return err
	}
	if err := validateHTTPSURL("homepage", candidate.Homepage); err != nil {
		return err
	}
	if candidate.HomeEnv != "" {
		if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*_HOME$`).MatchString(candidate.HomeEnv) {
			return fmt.Errorf("home_env %q is not a safe *_HOME name", candidate.HomeEnv)
		}
		if map[string]bool{"HOME": true, "PATH": true, "JAVA_TOOL_OPTIONS": true}[candidate.HomeEnv] {
			return fmt.Errorf("home_env %q is reserved", candidate.HomeEnv)
		}
	}
	if err := validateSegment("default_vendor", candidate.DefaultVendor); err != nil {
		return err
	}
	if len(candidate.Vendors) == 0 {
		return errors.New("must contain at least one vendor")
	}
	return nil
}

func validateVendor(vendor Vendor) error {
	if err := validateSegment("vendor name", vendor.Name); err != nil {
		return err
	}
	if err := validateText("vendor display_name", vendor.DisplayName, maxDescriptionBytes, true); err != nil {
		return err
	}
	if len(vendor.Releases) == 0 {
		return errors.New("vendor must contain at least one release")
	}
	return nil
}

func validateRelease(release SnapshotRelease) error {
	if err := validateSegment("version", release.Version); err != nil {
		return err
	}
	if !stableVersion(release.Version) {
		return fmt.Errorf("version %q is not a stable release", release.Version)
	}
	if err := validateSegment("selector", release.Selector); err != nil {
		return err
	}
	if release.SupportTier != "core" && release.SupportTier != "beta" {
		return fmt.Errorf("unsupported support tier %q", release.SupportTier)
	}
	if len(release.Artifacts) == 0 {
		return errors.New("must contain at least one artifact")
	}
	return nil
}

func validateArtifact(artifact Artifact) error {
	if err := validateText("artifact_id", artifact.ArtifactID, maxIdentifierBytes, true); err != nil {
		return err
	}
	if artifact.ArchiveType != "zip" && artifact.ArchiveType != "tar.gz" && artifact.ArchiveType != "tgz" {
		return fmt.Errorf("unsupported archive type %q", artifact.ArchiveType)
	}
	if err := validateHTTPSURL("url", artifact.URL); err != nil {
		return err
	}
	if len(artifact.Platforms) == 0 {
		return errors.New("must declare at least one platform")
	}
	for i, platform := range artifact.Platforms {
		key := platform.OS + "/" + platform.Arch
		if !platformsV1[key] {
			return fmt.Errorf("platform %d %q is not supported by v1", i, key)
		}
	}
	if len(artifact.AllowedRedirectHosts) > maxRedirectHosts {
		return fmt.Errorf("allowed redirect hosts exceeds %d", maxRedirectHosts)
	}
	seenHosts := make(map[string]bool, len(artifact.AllowedRedirectHosts))
	for _, host := range artifact.AllowedRedirectHosts {
		if err := validateRedirectHost(host); err != nil {
			return err
		}
		normalized := strings.ToLower(host)
		if seenHosts[normalized] {
			return fmt.Errorf("duplicate redirect host %q", host)
		}
		seenHosts[normalized] = true
	}
	if artifact.Checksum != nil {
		if artifact.Checksum.Algorithm != "sha256" {
			return fmt.Errorf("unsupported checksum algorithm %q", artifact.Checksum.Algorithm)
		}
		if len(artifact.Checksum.Value) != sha256.Size*2 {
			return errors.New("checksum value must be 64 hexadecimal characters")
		}
		if _, err := hex.DecodeString(artifact.Checksum.Value); err != nil {
			return fmt.Errorf("checksum value is not hexadecimal: %w", err)
		}
		if artifact.Checksum.Source != "upstream" && artifact.Checksum.Source != "mirror" && artifact.Checksum.Source != "catalog-computed" {
			return fmt.Errorf("unsupported checksum source %q", artifact.Checksum.Source)
		}
		if artifact.Checksum.SourceURL != "" {
			if err := validateHTTPSURL("checksum source_url", artifact.Checksum.SourceURL); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateRevocation(revocation Revocation) error {
	if err := validateText("artifact_id", revocation.ArtifactID, maxIdentifierBytes, true); err != nil {
		return err
	}
	if err := validateText("reason", revocation.Reason, maxDescriptionBytes, true); err != nil {
		return err
	}
	if err := validateText("message", revocation.Message, maxDescriptionBytes, false); err != nil {
		return err
	}
	if err := validateTimestamp(revocation.RevokedAt); err != nil {
		return fmt.Errorf("revoked_at: %w", err)
	}
	if revocation.ReplacementArtifactID != "" {
		if err := validateText("replacement_artifact_id", revocation.ReplacementArtifactID, maxIdentifierBytes, true); err != nil {
			return err
		}
		if revocation.ReplacementArtifactID == revocation.ArtifactID {
			return errors.New("replacement artifact cannot equal revoked artifact")
		}
	}
	return nil
}

func validateLatest(latest Latest) error {
	if latest.SchemaVersion != 1 {
		return fmt.Errorf("unsupported schema version %d", latest.SchemaVersion)
	}
	if latest.Sequence == 0 || latest.Sequence > 999999 {
		return errors.New("sequence must fit six-digit release tag")
	}
	if len(latest.SnapshotSHA256) != sha256.Size*2 {
		return errors.New("snapshot_sha256 must be 64 hexadecimal characters")
	}
	if _, err := hex.DecodeString(latest.SnapshotSHA256); err != nil {
		return fmt.Errorf("snapshot_sha256 is not hexadecimal: %w", err)
	}
	if !releaseTagRE.MatchString(latest.ReleaseTag) {
		return errors.New("release_tag must match catalog-v1-NNNNNN")
	}
	tagSequence, _ := strconv.ParseUint(latest.ReleaseTag[len("catalog-v1-"):], 10, 64)
	if tagSequence != latest.Sequence {
		return fmt.Errorf("release_tag sequence %d does not match sequence %d", tagSequence, latest.Sequence)
	}
	if err := validateText("snapshot_asset", latest.SnapshotAsset, maxIdentifierBytes, true); err != nil {
		return err
	}
	if strings.ContainsAny(latest.SnapshotAsset, `/\\`) || !strings.HasSuffix(latest.SnapshotAsset, ".json") {
		return errors.New("snapshot_asset must be a JSON filename without a path")
	}
	return nil
}

func validateSignature(signature Signature) error {
	if err := validateText("key_id", signature.KeyID, maxIdentifierBytes, true); err != nil {
		return err
	}
	if signature.Algorithm != "ed25519" {
		return fmt.Errorf("unsupported signature algorithm %q", signature.Algorithm)
	}
	raw, err := base64.StdEncoding.DecodeString(signature.Signature)
	if err != nil || len(raw) != ed25519.SignatureSize {
		return errors.New("signature must be a base64-encoded Ed25519 signature")
	}
	return nil
}

func validateSegment(label, value string) error {
	if err := validateText(label, value, maxIdentifierBytes, true); err != nil {
		return err
	}
	if !identifierRE.MatchString(value) || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s contains unsafe characters", label)
	}
	return nil
}

func validateText(label, value string, maxBytes int, required bool) error {
	if value == "" {
		if required {
			return fmt.Errorf("%s is required", label)
		}
		return nil
	}
	if len(value) > maxBytes {
		return fmt.Errorf("%s exceeds %d bytes", label, maxBytes)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s is not valid UTF-8", label)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s contains a control character", label)
		}
	}
	return nil
}

func validateTimestamp(value string) error {
	if value == "" {
		return errors.New("timestamp is required")
	}
	if _, err := time.Parse(time.RFC3339, value); err != nil {
		return errors.New("timestamp must be RFC3339")
	}
	return nil
}

func validateHTTPSURL(label, rawURL string) error {
	if len(rawURL) > maxURLBytes {
		return fmt.Errorf("%s exceeds %d bytes", label, maxURLBytes)
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Hostname() == "" || u.User != nil {
		return fmt.Errorf("%s must be an HTTPS URL without userinfo", label)
	}
	if isPrivateHost(u.Hostname()) {
		return fmt.Errorf("%s must not target a private or local host", label)
	}
	return nil
}

func validateRedirectHost(host string) error {
	if len(host) > maxIdentifierBytes || host == "" || strings.TrimSpace(host) != host || strings.ContainsAny(host, `/\\@?#`) {
		return fmt.Errorf("redirect host %q is invalid", host)
	}
	u, err := url.Parse("https://" + host)
	if err != nil || u.Host != host || u.Hostname() == "" || isPrivateHost(u.Hostname()) {
		return fmt.Errorf("redirect host %q is invalid", host)
	}
	return nil
}

func isPrivateHost(host string) bool {
	lower := strings.ToLower(strings.TrimSuffix(host, "."))
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") || strings.HasSuffix(lower, ".local") {
		return true
	}
	ip := net.ParseIP(lower)
	return ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast())
}
