package catalog

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// RemoteClient downloads only fixed Catalog v1 paths, then verifies original bytes.
// Endpoint must be a trusted distribution base URL, never data-controlled.
type RemoteClient struct {
	Endpoint    string
	HTTP        *http.Client
	TrustedKeys map[string]ed25519.PublicKey
}

func (client RemoteClient) Fetch(ctx context.Context) (Snapshot, error) {
	if len(client.TrustedKeys) == 0 {
		return Snapshot{}, fmt.Errorf("catalog trust root is not configured")
	}
	base, err := url.Parse(client.Endpoint)
	if err != nil || base.Scheme != "https" || base.Host == "" {
		return Snapshot{}, fmt.Errorf("invalid catalog endpoint")
	}
	if client.HTTP == nil {
		client.HTTP = http.DefaultClient
	}
	latest, err := client.get(ctx, base, "latest.json", maxLatestBytes)
	if err != nil {
		return Snapshot{}, err
	}
	latestSig, err := client.get(ctx, base, "latest.json.sig", maxEnvelopeBytes)
	if err != nil {
		return Snapshot{}, err
	}
	pointer, _, err := VerifyLatest(latest, latestSig, client.TrustedKeys)
	if err != nil {
		return Snapshot{}, fmt.Errorf("verify latest: %w", err)
	}
	prefix := "releases/" + pointer.ReleaseTag + "/"
	snapshot, err := client.get(ctx, base, prefix+pointer.SnapshotAsset, maxSnapshotBytes)
	if err != nil {
		return Snapshot{}, err
	}
	snapshotSig, err := client.get(ctx, base, prefix+pointer.SnapshotAsset+".sig", maxEnvelopeBytes)
	if err != nil {
		return Snapshot{}, err
	}
	value, err := VerifyLatestSnapshot(latest, latestSig, snapshot, snapshotSig, client.TrustedKeys)
	if err != nil {
		return Snapshot{}, fmt.Errorf("verify snapshot: %w", err)
	}
	return value, nil
}

func (client RemoteClient) get(ctx context.Context, base *url.URL, path string, limit int) ([]byte, error) {
	target := *base
	target.Path = strings.TrimRight(base.Path, "/") + "/" + path
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	response, err := client.HTTP.Do(request)
	if err != nil {
		return nil, fmt.Errorf("download catalog: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download catalog: HTTP %s", response.Status)
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
							out = append(out, Release{Candidate: candidate, Version: release.Selector, Vendor: vendor.Name, SupportTier: release.SupportTier, IntegrityLevel: "https-only", URL: artifact.URL, Available: true, AvailabilityKnown: true, AvailabilityStatus: "catalog"})
							break
						}
					}
				}
			}
		}
	}
	return out
}
