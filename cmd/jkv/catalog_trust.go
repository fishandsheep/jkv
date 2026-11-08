package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fishandsheep/jkv/internal/catalog"
	"github.com/fishandsheep/jkv/internal/store"
)

const catalogCacheTTL = 6 * time.Hour

var clientVersionRE = regexp.MustCompile(`^v?([0-9]+)\.([0-9]+)\.([0-9]+)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$`)

func loadReleases(ctx context.Context, s *store.Store, candidate string, refresh, quiet bool) ([]catalog.Release, error) {
	snapshot, err := loadSignedSnapshot(ctx, s, refresh, quiet)
	if err != nil {
		return nil, err
	}
	return releasesFromSnapshot(snapshot, candidate, catalog.CurrentPlatform())
}

func catalogConfiguredKeyID() string {
	if value := os.Getenv("JKV_CATALOG_KEY_ID"); value != "" {
		return value
	}
	return catalogKeyID
}

func catalogSettings() (map[string]ed25519.PublicKey, []catalog.Endpoint, error) {
	encodedKey := os.Getenv("JKV_CATALOG_PUBLIC_KEY")
	if encodedKey == "" {
		encodedKey = catalogPublicKeyBase64
	}
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil || len(key) != ed25519.PublicKeySize || catalogConfiguredKeyID() == "" {
		return nil, nil, stateErrorf("签名 catalog 未配置可信公钥")
	}
	primary := os.Getenv("JKV_CATALOG_ENDPOINT")
	if primary == "" {
		primary = catalogCNBDownloadBase
	}
	fallback := os.Getenv("JKV_CATALOG_FALLBACK_ENDPOINT")
	if fallback == "" && primary != catalogGitHubDownloadBase {
		fallback = catalogGitHubDownloadBase
	}
	endpoints := []catalog.Endpoint{catalog.ReleaseEndpoint("CNB", primary)}
	if fallback != "" {
		endpoints = append(endpoints, catalog.ReleaseEndpoint("GitHub", fallback))
	}
	return map[string]ed25519.PublicKey{catalogConfiguredKeyID(): ed25519.PublicKey(key)}, endpoints, nil
}

func loadSignedSnapshot(ctx context.Context, s *store.Store, refresh, quiet bool) (catalog.Snapshot, error) {
	keys, endpoints, settingsErr := catalogSettings()
	state, stateErr := s.LoadTrustedCatalog()
	trusted, trustedErr := verifiedTrustedSnapshot(state, keys)
	if !refresh && trustedErr == nil && time.Since(trusted.FetchedAt) < catalogCacheTTL {
		return compatibleCatalogSnapshot(trusted.Snapshot)
	}
	if settingsErr != nil {
		if trustedErr == nil {
			return compatibleCatalogSnapshot(trusted.Snapshot)
		}
		return catalog.Snapshot{}, settingsErr
	}
	fetched, err := (catalog.RemoteClient{Endpoints: endpoints, HTTP: catalogHTTPClient, TrustedKeys: keys}).FetchDocument(ctx)
	if err != nil {
		if trustedErr == nil {
			if !quiet {
				fmt.Fprintf(os.Stderr, "签名 catalog 更新失败，使用可信快照: %v\n", err)
			}
			return compatibleCatalogSnapshot(trusted.Snapshot)
		}
		return catalog.Snapshot{}, err
	}
	if stateErr != nil && !os.IsNotExist(stateErr) {
		return catalog.Snapshot{}, stateErrorf("读取可信 catalog 状态: %w", stateErr)
	}
	if err := saveFetchedCatalog(s, state, fetched); err != nil {
		if trustedErr == nil && !quiet {
			fmt.Fprintf(os.Stderr, "保存可信 catalog 失败，使用已有快照: %v\n", err)
			return trusted.Snapshot, nil
		}
		return catalog.Snapshot{}, err
	}
	return compatibleCatalogSnapshot(fetched.Snapshot)
}

func compatibleCatalogSnapshot(snapshot catalog.Snapshot) (catalog.Snapshot, error) {
	if version == "dev" || versionAtLeast(version, snapshot.MinClientVersion) {
		return snapshot, nil
	}
	return catalog.Snapshot{}, stateErrorf("catalog 要求 jkv >= %s，当前为 %s；请升级 jkv", snapshot.MinClientVersion, version)
}

func versionAtLeast(current, minimum string) bool {
	currentParts, currentOK := parseClientVersion(current)
	minimumParts, minimumOK := parseClientVersion(minimum)
	if !currentOK || !minimumOK {
		return false
	}
	for index := 0; index < 3; index++ {
		if currentParts.numbers[index] != minimumParts.numbers[index] {
			return currentParts.numbers[index] > minimumParts.numbers[index]
		}
	}
	return comparePrerelease(currentParts.prerelease, minimumParts.prerelease) >= 0
}

type parsedClientVersion struct {
	numbers    [3]int
	prerelease string
}

func parseClientVersion(value string) (parsedClientVersion, bool) {
	match := clientVersionRE.FindStringSubmatch(value)
	if match == nil {
		return parsedClientVersion{}, false
	}
	var result parsedClientVersion
	for index := range result.numbers {
		number, err := strconv.Atoi(match[index+1])
		if err != nil || number < 0 {
			return parsedClientVersion{}, false
		}
		result.numbers[index] = number
	}
	result.prerelease = match[4]
	return result, true
}

func comparePrerelease(left, right string) int {
	if left == right {
		return 0
	}
	if left == "" {
		return 1
	}
	if right == "" {
		return -1
	}
	leftParts, rightParts := strings.Split(left, "."), strings.Split(right, ".")
	for index := 0; index < len(leftParts) && index < len(rightParts); index++ {
		if leftParts[index] == rightParts[index] {
			continue
		}
		leftNumber, leftNumeric := prereleaseNumber(leftParts[index])
		rightNumber, rightNumeric := prereleaseNumber(rightParts[index])
		switch {
		case leftNumeric && rightNumeric:
			if leftNumber < rightNumber {
				return -1
			}
			return 1
		case leftNumeric:
			return -1
		case rightNumeric:
			return 1
		case leftParts[index] < rightParts[index]:
			return -1
		default:
			return 1
		}
	}
	if len(leftParts) < len(rightParts) {
		return -1
	}
	return 1
}

func prereleaseNumber(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, false
		}
	}
	number, err := strconv.Atoi(value)
	return number, err == nil
}

type verifiedSnapshot struct {
	store.TrustedCatalogSnapshot
	Snapshot catalog.Snapshot
}

func verifiedTrustedSnapshot(state store.TrustedCatalogState, keys map[string]ed25519.PublicKey) (verifiedSnapshot, error) {
	if len(state.Snapshots) == 0 {
		return verifiedSnapshot{}, os.ErrNotExist
	}
	if len(keys) == 0 {
		return verifiedSnapshot{}, stateErrorf("签名 catalog 未配置可信公钥")
	}
	current := state.Snapshots[0]
	for _, snapshot := range state.Snapshots[1:] {
		if snapshot.Sequence > current.Sequence {
			current = snapshot
		}
	}
	value, err := catalog.VerifyLatestSnapshot(current.Latest, current.LatestSignature, current.Snapshot, current.SnapshotSignature, keys)
	if err != nil {
		return verifiedSnapshot{}, stateErrorf("验证本地可信 catalog: %w", err)
	}
	if value.Sequence != current.Sequence || value.Sequence != state.HighestSequence {
		return verifiedSnapshot{}, stateErrorf("本地可信 catalog 序号不一致")
	}
	return verifiedSnapshot{TrustedCatalogSnapshot: current, Snapshot: value}, nil
}

func saveFetchedCatalog(s *store.Store, state store.TrustedCatalogState, fetched catalog.FetchedSnapshot) error {
	if fetched.Snapshot.Sequence < state.HighestSequence {
		return stateErrorf("拒绝 catalog 回滚：远端 sequence %d 低于已见 %d", fetched.Snapshot.Sequence, state.HighestSequence)
	}
	for index, known := range state.Snapshots {
		if known.Sequence != fetched.Snapshot.Sequence {
			continue
		}
		if !bytes.Equal(known.Snapshot, fetched.SnapshotBytes) ||
			!bytes.Equal(known.SnapshotSignature, fetched.SnapshotSignature) ||
			!bytes.Equal(known.Latest, fetched.LatestBytes) ||
			!bytes.Equal(known.LatestSignature, fetched.LatestSignature) {
			return stateErrorf("拒绝 catalog：sequence %d 对应不同字节", fetched.Snapshot.Sequence)
		}
		state.Snapshots[index].FetchedAt = time.Now().UTC()
		state.Snapshots[index].Endpoint = fetched.Endpoint
		state.Snapshots[index].KeyIDs = append([]string(nil), fetched.KeyIDs...)
		return s.SaveTrustedCatalog(state)
	}
	if fetched.Snapshot.Sequence == state.HighestSequence && state.HighestSequence != 0 {
		return stateErrorf("拒绝 catalog：sequence %d 缺少本地可信快照", fetched.Snapshot.Sequence)
	}
	state.HighestSequence = fetched.Snapshot.Sequence
	state.Snapshots = append(state.Snapshots, store.TrustedCatalogSnapshot{
		Sequence:          fetched.Snapshot.Sequence,
		Endpoint:          fetched.Endpoint,
		FetchedAt:         time.Now().UTC(),
		KeyIDs:            append([]string(nil), fetched.KeyIDs...),
		Snapshot:          append([]byte(nil), fetched.SnapshotBytes...),
		SnapshotSignature: append([]byte(nil), fetched.SnapshotSignature...),
		Latest:            append([]byte(nil), fetched.LatestBytes...),
		LatestSignature:   append([]byte(nil), fetched.LatestSignature...),
	})
	state.Revocations = mergeRevocations(state.Revocations, fetched.Snapshot.Revocations)
	return s.SaveTrustedCatalog(state)
}

func mergeRevocations(existing, incoming []catalog.Revocation) []catalog.Revocation {
	byID := make(map[string]catalog.Revocation, len(existing)+len(incoming))
	for _, item := range existing {
		byID[item.ArtifactID] = item
	}
	for _, item := range incoming {
		byID[item.ArtifactID] = item
	}
	ids := sortedKeys(byID)
	out := make([]catalog.Revocation, 0, len(ids))
	for _, id := range ids {
		out = append(out, byID[id])
	}
	return out
}

func releasesFromSnapshot(snapshot catalog.Snapshot, candidate string, platform catalog.Platform) ([]catalog.Release, error) {
	releases := snapshot.Releases(candidate, platform)
	if len(releases) == 0 {
		return nil, notFoundErrorf("签名 catalog 未包含 %s 的当前平台制品", candidate)
	}
	return releases, nil
}

func snapshotCandidateList(snapshot catalog.Snapshot) []catalog.Candidate {
	out := make([]catalog.Candidate, 0, len(snapshot.Candidates))
	for _, candidate := range snapshot.Candidates {
		out = append(out, catalog.Candidate{
			Name:        candidate.Name,
			Description: candidate.Description,
			Source:      "签名 Catalog",
			Platforms:   "按 Catalog",
			SupportTier: candidateTier(candidate),
		})
	}
	return out
}

func candidateTier(candidate catalog.SnapshotCandidate) string {
	for _, vendor := range candidate.Vendors {
		for _, release := range vendor.Releases {
			if release.SupportTier == "core" {
				return "core"
			}
		}
	}
	return "beta"
}
