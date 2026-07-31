package catalog

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Endpoint maps trusted fixed release URLs. Catalog data never controls either URL.
// LatestURL points to catalog-latest/latest.json; ReleaseBaseURL points to the
// directory containing immutable release tags.
type Endpoint struct {
	Name           string
	LatestURL      string
	ReleaseBaseURL string
}

// ReleaseEndpoint builds an adapter for GitHub/CNB Release download URLs.
func ReleaseEndpoint(name, baseURL string) Endpoint {
	baseURL = strings.TrimRight(baseURL, "/")
	return Endpoint{
		Name:           name,
		LatestURL:      baseURL + "/catalog-latest/latest.json",
		ReleaseBaseURL: baseURL,
	}
}

// FetchedSnapshot preserves verified original bytes for trusted local storage.
type FetchedSnapshot struct {
	Endpoint          string
	KeyIDs            []string
	Snapshot          Snapshot
	Latest            Latest
	SnapshotBytes     []byte
	SnapshotSignature []byte
	LatestBytes       []byte
	LatestSignature   []byte
}

// DownloadError permits endpoint fallback only for transport or HTTP failure.
// Signature, hash, schema, and endpoint-configuration errors are never safe to
// hide by trying another endpoint.
type DownloadError struct {
	Endpoint string
	URL      string
	Err      error
}

func (err *DownloadError) Error() string {
	return fmt.Sprintf("download catalog from %s: %v", err.Endpoint, err.Err)
}

func (err *DownloadError) Unwrap() error { return err.Err }

// RemoteClient downloads only trusted fixed Catalog v1 paths, then verifies
// original bytes. Endpoints are attempted in order. Endpoint is retained for
// backward-compatible single-root test deployments.
type RemoteClient struct {
	Endpoint    string
	Endpoints   []Endpoint
	HTTP        *http.Client
	TrustedKeys map[string]ed25519.PublicKey
}

func (client RemoteClient) Fetch(ctx context.Context) (Snapshot, error) {
	fetched, err := client.FetchDocument(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	return fetched.Snapshot, nil
}

// FetchDocument returns one verified Snapshot and its original signed bytes.
func (client RemoteClient) FetchDocument(ctx context.Context) (FetchedSnapshot, error) {
	if len(client.TrustedKeys) == 0 {
		return FetchedSnapshot{}, fmt.Errorf("catalog trust root is not configured")
	}
	endpoints := append([]Endpoint(nil), client.Endpoints...)
	if client.Endpoint != "" {
		endpoints = append(endpoints, rootEndpoint(client.Endpoint))
	}
	if len(endpoints) == 0 {
		return FetchedSnapshot{}, fmt.Errorf("catalog endpoint is not configured")
	}
	if client.HTTP == nil {
		client.HTTP = http.DefaultClient
	}
	var failures []error
	for _, endpoint := range endpoints {
		fetched, err := client.fetchEndpoint(ctx, endpoint)
		if err == nil {
			return fetched, nil
		}
		var download *DownloadError
		if !errors.As(err, &download) {
			return FetchedSnapshot{}, err
		}
		failures = append(failures, err)
	}
	return FetchedSnapshot{}, fmt.Errorf("download catalog from every endpoint: %w", errors.Join(failures...))
}

func rootEndpoint(baseURL string) Endpoint {
	baseURL = strings.TrimRight(baseURL, "/")
	return Endpoint{
		Name:           baseURL,
		LatestURL:      baseURL + "/latest.json",
		ReleaseBaseURL: baseURL + "/releases",
	}
}

func (client RemoteClient) fetchEndpoint(ctx context.Context, endpoint Endpoint) (FetchedSnapshot, error) {
	latestURL, err := trustedURL(endpoint.LatestURL)
	if err != nil {
		return FetchedSnapshot{}, fmt.Errorf("invalid catalog endpoint %q: %w", endpoint.Name, err)
	}
	releaseBase, err := trustedURL(endpoint.ReleaseBaseURL)
	if err != nil {
		return FetchedSnapshot{}, fmt.Errorf("invalid catalog endpoint %q: %w", endpoint.Name, err)
	}
	name := endpoint.Name
	if name == "" {
		name = latestURL.Host
	}
	latest, err := client.getURL(ctx, name, latestURL, maxLatestBytes)
	if err != nil {
		return FetchedSnapshot{}, err
	}
	latestSig, err := client.getURL(ctx, name, appendSuffix(latestURL, ".sig"), maxEnvelopeBytes)
	if err != nil {
		return FetchedSnapshot{}, err
	}
	pointer, verification, err := VerifyLatest(latest, latestSig, client.TrustedKeys)
	if err != nil {
		return FetchedSnapshot{}, fmt.Errorf("verify latest from %s: %w", name, err)
	}
	snapshotURL := appendPath(releaseBase, "/"+pointer.ReleaseTag+"/"+pointer.SnapshotAsset)
	snapshot, err := client.getURL(ctx, name, snapshotURL, maxSnapshotBytes)
	if err != nil {
		return FetchedSnapshot{}, err
	}
	snapshotSig, err := client.getURL(ctx, name, appendSuffix(snapshotURL, ".sig"), maxEnvelopeBytes)
	if err != nil {
		return FetchedSnapshot{}, err
	}
	value, err := VerifyLatestSnapshot(latest, latestSig, snapshot, snapshotSig, client.TrustedKeys)
	if err != nil {
		return FetchedSnapshot{}, fmt.Errorf("verify snapshot from %s: %w", name, err)
	}
	return FetchedSnapshot{
		Endpoint:          name,
		KeyIDs:            verification.KeyIDs,
		Snapshot:          value,
		Latest:            pointer,
		SnapshotBytes:     snapshot,
		SnapshotSignature: snapshotSig,
		LatestBytes:       latest,
		LatestSignature:   latestSig,
	}, nil
}

func trustedURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("must be an absolute HTTPS URL without credentials, query, or fragment")
	}
	return parsed, nil
}

func appendPath(base *url.URL, suffix string) *url.URL {
	target := *base
	target.Path = strings.TrimRight(base.Path, "/") + "/" + strings.TrimLeft(suffix, "/")
	return &target
}

func appendSuffix(base *url.URL, suffix string) *url.URL {
	target := *base
	target.Path += suffix
	return &target
}

func (client RemoteClient) getURL(ctx context.Context, endpoint string, target *url.URL, limit int) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	response, err := client.HTTP.Do(request)
	if err != nil {
		return nil, &DownloadError{Endpoint: endpoint, URL: target.String(), Err: err}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, &DownloadError{Endpoint: endpoint, URL: target.String(), Err: fmt.Errorf("HTTP %s", response.Status)}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(body) > limit {
		return nil, fmt.Errorf("catalog response exceeds %d bytes", limit)
	}
	return body, nil
}

// Releases selects exact Artifact platform matches without applying URL heuristics.
func (snapshot Snapshot) Releases(candidate string, platform Platform) []Release {
	var out []Release
	for _, item := range snapshot.Candidates {
		if item.Name != candidate {
			continue
		}
		for _, vendor := range item.Vendors {
			for _, release := range vendor.Releases {
				for _, artifact := range release.Artifacts {
					for _, applicable := range artifact.Platforms {
						if applicable == platform {
							integrity := "https-only"
							checksumURL := ""
							if artifact.Checksum != nil {
								integrity = "checksum"
								checksumURL = artifact.Checksum.SourceURL
							}
							checksumValue := ""
							if artifact.Checksum != nil {
								checksumValue = artifact.Checksum.Value
							}
							out = append(out, Release{Candidate: candidate, Version: release.Selector, Vendor: vendor.Name, ArtifactID: artifact.ArtifactID, SupportTier: release.SupportTier, IntegrityLevel: integrity, URL: artifact.URL, AllowedRedirectHosts: append([]string(nil), artifact.AllowedRedirectHosts...), ChecksumURL: checksumURL, ChecksumValue: checksumValue, Available: true, AvailabilityKnown: true, AvailabilityStatus: "catalog"})
							break
						}
					}
				}
			}
		}
	}
	return out
}
