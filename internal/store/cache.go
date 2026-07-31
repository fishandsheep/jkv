package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fishandsheep/jkv/internal/catalog"
)

type CatalogCache struct {
	SchemaVersion int               `json:"schema_version"`
	FetchedAt     time.Time         `json:"fetched_at"`
	CheckedAt     time.Time         `json:"checked_at,omitempty"`
	Releases      []catalog.Release `json:"releases"`
}

// TrustedCatalogSnapshot stores exact signed bytes after client verification.
// The current snapshot and two predecessors remain available for offline use.
type TrustedCatalogSnapshot struct {
	Sequence          uint64    `json:"sequence"`
	Endpoint          string    `json:"endpoint"`
	FetchedAt         time.Time `json:"fetched_at"`
	KeyIDs            []string  `json:"key_ids"`
	Snapshot          []byte    `json:"snapshot"`
	SnapshotSignature []byte    `json:"snapshot_signature"`
	Latest            []byte    `json:"latest"`
	LatestSignature   []byte    `json:"latest_signature"`
}

// TrustedCatalogState is separate from disposable per-candidate catalog cache.
// HighestSequence and cumulative revocations survive cache cleanup.
type TrustedCatalogState struct {
	SchemaVersion   int                      `json:"schema_version"`
	HighestSequence uint64                   `json:"highest_sequence"`
	Snapshots       []TrustedCatalogSnapshot `json:"snapshots"`
	Revocations     []catalog.Revocation     `json:"revocations,omitempty"`
}

type archiveMetadata struct {
	SchemaVersion int             `json:"schema_version"`
	Release       catalog.Release `json:"release"`
	SHA256        string          `json:"sha256"`
}

type CleanResult struct {
	Files int
	Bytes int64
}

func (s *Store) cacheRoot() string { return filepath.Join(s.Root, "cache") }

func (s *Store) catalogCachePath(p catalog.Platform, candidate string) string {
	return filepath.Join(s.cacheRoot(), "catalog", p.OS+"-"+p.Arch, candidate+".json")
}

func (s *Store) trustedCatalogPath() string {
	return filepath.Join(s.cacheRoot(), "trusted-catalog.json")
}

func (s *Store) LoadTrustedCatalog() (TrustedCatalogState, error) {
	return s.loadTrustedCatalog()
}

func (s *Store) loadTrustedCatalog() (TrustedCatalogState, error) {
	b, err := os.ReadFile(s.trustedCatalogPath())
	if err != nil {
		return TrustedCatalogState{}, err
	}
	var state TrustedCatalogState
	if err := json.Unmarshal(b, &state); err != nil {
		return TrustedCatalogState{}, err
	}
	if state.SchemaVersion > stateSchemaVersion {
		return TrustedCatalogState{}, fmt.Errorf("不支持 trusted catalog schema %d", state.SchemaVersion)
	}
	if err := validateTrustedCatalogState(state); err != nil {
		return TrustedCatalogState{}, err
	}
	return state, nil
}

func (s *Store) SaveTrustedCatalog(state TrustedCatalogState) error {
	lock, err := s.acquireLock(context.Background(), "trusted-catalog")
	if err != nil {
		return err
	}
	defer lock.release()
	persisted, err := s.loadTrustedCatalog()
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	state, err = mergeTrustedCatalogState(persisted, state)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(s.trustedCatalogPath(), append(b, '\n'), 0o600)
}

func mergeTrustedCatalogState(persisted, incoming TrustedCatalogState) (TrustedCatalogState, error) {
	if incoming.HighestSequence < persisted.HighestSequence {
		return TrustedCatalogState{}, fmt.Errorf("拒绝 trusted catalog 回滚：sequence %d 低于已见 %d", incoming.HighestSequence, persisted.HighestSequence)
	}
	bySequence := make(map[uint64]TrustedCatalogSnapshot, len(persisted.Snapshots)+len(incoming.Snapshots))
	for _, snapshot := range persisted.Snapshots {
		bySequence[snapshot.Sequence] = snapshot
	}
	seenIncoming := make(map[uint64]bool, len(incoming.Snapshots))
	for _, snapshot := range incoming.Snapshots {
		if seenIncoming[snapshot.Sequence] {
			return TrustedCatalogState{}, fmt.Errorf("duplicate trusted catalog sequence %d", snapshot.Sequence)
		}
		seenIncoming[snapshot.Sequence] = true
		if known, exists := bySequence[snapshot.Sequence]; exists && !sameTrustedSnapshot(known, snapshot) {
			return TrustedCatalogState{}, fmt.Errorf("拒绝 trusted catalog：sequence %d 对应不同字节", snapshot.Sequence)
		}
		bySequence[snapshot.Sequence] = snapshot
	}
	state := TrustedCatalogState{SchemaVersion: stateSchemaVersion, HighestSequence: incoming.HighestSequence}
	if persisted.HighestSequence > state.HighestSequence {
		state.HighestSequence = persisted.HighestSequence
	}
	for _, snapshot := range bySequence {
		state.Snapshots = append(state.Snapshots, snapshot)
	}
	sort.Slice(state.Snapshots, func(i, j int) bool { return state.Snapshots[i].Sequence > state.Snapshots[j].Sequence })
	if len(state.Snapshots) > 3 {
		state.Snapshots = state.Snapshots[:3]
	}
	state.Revocations = mergeTrustedRevocations(persisted.Revocations, incoming.Revocations)
	if err := validateTrustedCatalogState(state); err != nil {
		return TrustedCatalogState{}, err
	}
	return state, nil
}

func sameTrustedSnapshot(left, right TrustedCatalogSnapshot) bool {
	return bytes.Equal(left.Snapshot, right.Snapshot) &&
		bytes.Equal(left.SnapshotSignature, right.SnapshotSignature) &&
		bytes.Equal(left.Latest, right.Latest) &&
		bytes.Equal(left.LatestSignature, right.LatestSignature)
}

func mergeTrustedRevocations(existing, incoming []catalog.Revocation) []catalog.Revocation {
	byID := make(map[string]catalog.Revocation, len(existing)+len(incoming))
	for _, item := range existing {
		byID[item.ArtifactID] = item
	}
	for _, item := range incoming {
		byID[item.ArtifactID] = item
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]catalog.Revocation, 0, len(ids))
	for _, id := range ids {
		out = append(out, byID[id])
	}
	return out
}

func (s *Store) ArtifactRevocation(artifactID string) (catalog.Revocation, bool, error) {
	if artifactID == "" {
		return catalog.Revocation{}, false, nil
	}
	state, err := s.LoadTrustedCatalog()
	if os.IsNotExist(err) {
		return catalog.Revocation{}, false, nil
	}
	if err != nil {
		return catalog.Revocation{}, false, err
	}
	for _, revocation := range state.Revocations {
		if revocation.ArtifactID == artifactID {
			return revocation, true, nil
		}
	}
	return catalog.Revocation{}, false, nil
}

func validateTrustedCatalogState(state TrustedCatalogState) error {
	if len(state.Snapshots) > 3 {
		return errors.New("trusted catalog stores at most three snapshots")
	}
	seen := map[uint64]bool{}
	var max uint64
	for _, snapshot := range state.Snapshots {
		if snapshot.Sequence == 0 || snapshot.Endpoint == "" || snapshot.FetchedAt.IsZero() ||
			len(snapshot.Snapshot) == 0 || len(snapshot.SnapshotSignature) == 0 || len(snapshot.Latest) == 0 || len(snapshot.LatestSignature) == 0 {
			return errors.New("invalid trusted catalog snapshot")
		}
		if seen[snapshot.Sequence] {
			return fmt.Errorf("duplicate trusted catalog sequence %d", snapshot.Sequence)
		}
		seen[snapshot.Sequence] = true
		if snapshot.Sequence > max {
			max = snapshot.Sequence
		}
	}
	if max > state.HighestSequence {
		return errors.New("trusted catalog highest sequence is lower than a stored snapshot")
	}
	return nil
}

func (s *Store) LoadCatalog(p catalog.Platform, candidate string) (CatalogCache, error) {
	b, err := os.ReadFile(s.catalogCachePath(p, candidate))
	if err != nil {
		return CatalogCache{}, err
	}
	var cached CatalogCache
	if err := json.Unmarshal(b, &cached); err != nil {
		return CatalogCache{}, err
	}
	if cached.SchemaVersion > stateSchemaVersion {
		return CatalogCache{}, fmt.Errorf("不支持 catalog schema %d", cached.SchemaVersion)
	}
	return cached, nil
}

func (s *Store) SaveCatalog(p catalog.Platform, candidate string, cached CatalogCache) error {
	lock, err := s.acquireLock(context.Background(), "cache")
	if err != nil {
		return err
	}
	defer lock.release()
	if cached.SchemaVersion == 0 {
		cached.SchemaVersion = stateSchemaVersion
	}
	b, err := json.MarshalIndent(cached, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(s.catalogCachePath(p, candidate), append(b, '\n'), 0o644)
}

func validSegment(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, `/\\`)
}

func (s *Store) archiveDir(candidate, version string) (string, bool) {
	if !validSegment(candidate) || !validSegment(version) {
		return "", false
	}
	return filepath.Join(s.cacheRoot(), "downloads", candidate, version), true
}

func (s *Store) archivePaths(candidate, version string) (string, string, bool) {
	dir, ok := s.archiveDir(candidate, version)
	if !ok {
		return "", "", false
	}
	return filepath.Join(dir, "archive"), filepath.Join(dir, "metadata.json"), true
}

func (s *Store) CachedRelease(candidate, version string) (catalog.Release, bool) {
	archive, metadataPath, ok := s.archivePaths(candidate, version)
	if !ok {
		return catalog.Release{}, false
	}
	if _, err := os.Stat(archive); err != nil {
		return catalog.Release{}, false
	}
	b, err := os.ReadFile(metadataPath)
	if err != nil {
		return catalog.Release{}, false
	}
	var metadata archiveMetadata
	if json.Unmarshal(b, &metadata) != nil || metadata.SchemaVersion > stateSchemaVersion ||
		metadata.Release.Candidate != candidate || metadata.Release.Version != version || metadata.Release.URL == "" {
		return catalog.Release{}, false
	}
	return metadata.Release, true
}

func (s *Store) CachedVersions(candidate string) []string {
	if !validSegment(candidate) {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(s.cacheRoot(), "downloads", candidate))
	if err != nil {
		return nil
	}
	var versions []string
	for _, entry := range entries {
		if entry.IsDir() {
			if _, ok := s.CachedRelease(candidate, entry.Name()); ok {
				versions = append(versions, entry.Name())
			}
		}
	}
	return versions
}

func (s *Store) validCachedArchive(r catalog.Release) (string, bool) {
	archive, metadataPath, ok := s.archivePaths(r.Candidate, r.Version)
	if !ok {
		return "", false
	}
	b, err := os.ReadFile(metadataPath)
	if err != nil {
		return "", false
	}
	var metadata archiveMetadata
	if json.Unmarshal(b, &metadata) != nil || metadata.SchemaVersion > stateSchemaVersion ||
		metadata.Release.URL != r.URL || metadata.SHA256 == "" {
		return "", false
	}
	sum, err := fileSHA256(archive)
	if err != nil || !strings.EqualFold(sum, metadata.SHA256) {
		return "", false
	}
	if r.ChecksumValue != "" && !strings.EqualFold(sum, r.ChecksumValue) {
		return "", false
	}
	return archive, true
}

func (s *Store) saveArchiveMetadata(r catalog.Release, sum string) error {
	_, path, ok := s.archivePaths(r.Candidate, r.Version)
	if !ok {
		return os.ErrInvalid
	}
	b, err := json.MarshalIndent(archiveMetadata{SchemaVersion: stateSchemaVersion, Release: r, SHA256: sum}, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(b, '\n'), 0o644)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (s *Store) CleanCache(kind, candidate, version string) (CleanResult, error) {
	lock, err := s.acquireLock(context.Background(), "cache")
	if err != nil {
		return CleanResult{}, err
	}
	defer lock.release()
	paths, err := s.cachePaths(kind, candidate, version)
	if err != nil {
		return CleanResult{}, err
	}
	var total CleanResult
	for _, path := range paths {
		result, err := removeMeasured(path)
		if err != nil {
			return total, err
		}
		total.Files += result.Files
		total.Bytes += result.Bytes
	}
	return total, nil
}

func (s *Store) InspectCache(kind, candidate, version string) (CleanResult, error) {
	paths, err := s.cachePaths(kind, candidate, version)
	if err != nil {
		return CleanResult{}, err
	}
	var total CleanResult
	for _, path := range paths {
		result, err := measurePath(path)
		if err != nil {
			return total, err
		}
		total.Files += result.Files
		total.Bytes += result.Bytes
	}
	return total, nil
}

func (s *Store) CleanCacheOlderThan(kind, candidate, version string, age time.Duration) (CleanResult, error) {
	lock, err := s.acquireLock(context.Background(), "cache")
	if err != nil {
		return CleanResult{}, err
	}
	defer lock.release()
	paths, err := s.olderCachePaths(kind, candidate, version, age)
	if err != nil {
		return CleanResult{}, err
	}
	var total CleanResult
	for _, path := range paths {
		result, err := removeMeasured(path)
		if err != nil {
			return total, err
		}
		total.Files += result.Files
		total.Bytes += result.Bytes
	}
	return total, nil
}

func (s *Store) InspectCacheOlderThan(kind, candidate, version string, age time.Duration) (CleanResult, error) {
	paths, err := s.olderCachePaths(kind, candidate, version, age)
	if err != nil {
		return CleanResult{}, err
	}
	var total CleanResult
	for _, path := range paths {
		result, err := measurePath(path)
		if err != nil {
			return total, err
		}
		total.Files += result.Files
		total.Bytes += result.Bytes
	}
	return total, nil
}

func (s *Store) CleanPartialsOlderThan(candidate, version string, age time.Duration) (CleanResult, error) {
	paths, err := s.olderPartialPaths(candidate, version, age)
	if err != nil {
		return CleanResult{}, err
	}
	var total CleanResult
	for _, path := range paths {
		candidateName := filepath.Base(filepath.Dir(path))
		versionName := filepath.Base(path)
		lock, err := s.acquireLock(context.Background(), "install-"+candidateName+"-"+versionName)
		if err != nil {
			return total, err
		}
		latest, exists, checkErr := newestModTime(path)
		if checkErr == nil && exists && latest.Before(time.Now().Add(-age)) {
			var result CleanResult
			result, checkErr = removeMeasured(path)
			total.Files += result.Files
			total.Bytes += result.Bytes
		}
		lock.release()
		if checkErr != nil {
			return total, checkErr
		}
	}
	return total, nil
}

func (s *Store) InspectPartialsOlderThan(candidate, version string, age time.Duration) (CleanResult, error) {
	paths, err := s.olderPartialPaths(candidate, version, age)
	if err != nil {
		return CleanResult{}, err
	}
	var total CleanResult
	for _, path := range paths {
		result, err := measurePath(path)
		if err != nil {
			return total, err
		}
		total.Files += result.Files
		total.Bytes += result.Bytes
	}
	return total, nil
}

func (s *Store) olderPartialPaths(candidate, version string, age time.Duration) ([]string, error) {
	if age <= 0 || candidate != "" && !validSegment(candidate) ||
		version != "" && (candidate == "" || !validSegment(version)) {
		return nil, os.ErrInvalid
	}
	root := filepath.Join(s.Root, "partials", "downloads")
	var paths []string
	if version != "" {
		paths = append(paths, filepath.Join(root, candidate, version))
	} else {
		candidates, err := os.ReadDir(root)
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		for _, candidateEntry := range candidates {
			if !candidateEntry.IsDir() || candidate != "" && candidateEntry.Name() != candidate {
				continue
			}
			versions, readErr := os.ReadDir(filepath.Join(root, candidateEntry.Name()))
			if readErr != nil {
				return nil, readErr
			}
			for _, versionEntry := range versions {
				if versionEntry.IsDir() {
					paths = append(paths, filepath.Join(root, candidateEntry.Name(), versionEntry.Name()))
				}
			}
		}
	}
	cutoff := time.Now().Add(-age)
	var old []string
	for _, path := range paths {
		latest, exists, err := newestModTime(path)
		if err != nil {
			return nil, err
		}
		if exists && latest.Before(cutoff) {
			old = append(old, path)
		}
	}
	return old, nil
}

func (s *Store) olderCachePaths(kind, candidate, version string, age time.Duration) ([]string, error) {
	if age <= 0 {
		return nil, os.ErrInvalid
	}
	if kind == "" {
		downloads, err := s.olderCachePaths("downloads", candidate, version, age)
		if err != nil {
			return nil, err
		}
		catalogs, err := s.olderCachePaths("catalog", candidate, "", age)
		return append(downloads, catalogs...), err
	}
	if kind != "downloads" && kind != "catalog" {
		return nil, os.ErrInvalid
	}
	if candidate != "" && !validSegment(candidate) {
		return nil, os.ErrInvalid
	}
	if version != "" && (kind != "downloads" || candidate == "" || !validSegment(version)) {
		return nil, os.ErrInvalid
	}
	var candidates []string
	if kind == "downloads" {
		root := filepath.Join(s.cacheRoot(), "downloads")
		if candidate != "" {
			root = filepath.Join(root, candidate)
		}
		if version != "" {
			candidates = append(candidates, filepath.Join(root, version))
		} else {
			if candidate == "" {
				entries, err := os.ReadDir(root)
				if os.IsNotExist(err) {
					return nil, nil
				}
				if err != nil {
					return nil, err
				}
				for _, candidateEntry := range entries {
					if !candidateEntry.IsDir() {
						continue
					}
					versionEntries, readErr := os.ReadDir(filepath.Join(root, candidateEntry.Name()))
					if readErr != nil {
						return nil, readErr
					}
					for _, versionEntry := range versionEntries {
						if versionEntry.IsDir() {
							candidates = append(candidates, filepath.Join(root, candidateEntry.Name(), versionEntry.Name()))
						}
					}
				}
			} else {
				entries, err := os.ReadDir(root)
				if os.IsNotExist(err) {
					return nil, nil
				}
				if err != nil {
					return nil, err
				}
				for _, entry := range entries {
					if entry.IsDir() {
						candidates = append(candidates, filepath.Join(root, entry.Name()))
					}
				}
			}
		}
	} else {
		root := filepath.Join(s.cacheRoot(), "catalog")
		platforms, err := os.ReadDir(root)
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		for _, platform := range platforms {
			if !platform.IsDir() {
				continue
			}
			dir := filepath.Join(root, platform.Name())
			if candidate != "" {
				candidates = append(candidates, filepath.Join(dir, candidate+".json"))
				continue
			}
			entries, readErr := os.ReadDir(dir)
			if readErr != nil {
				return nil, readErr
			}
			for _, entry := range entries {
				if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
					candidates = append(candidates, filepath.Join(dir, entry.Name()))
				}
			}
		}
	}
	cutoff := time.Now().Add(-age)
	var old []string
	for _, path := range candidates {
		latest, exists, err := newestModTime(path)
		if err != nil {
			return nil, err
		}
		if exists && latest.Before(cutoff) {
			old = append(old, path)
		}
	}
	return old, nil
}

func newestModTime(path string) (time.Time, bool, error) {
	var latest time.Time
	found := false
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		found = true
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
		return nil
	})
	return latest, found, err
}

func (s *Store) cachePaths(kind, candidate, version string) ([]string, error) {
	if kind == "" {
		return []string{s.cacheRoot()}, nil
	}
	if kind == "downloads" {
		path := filepath.Join(s.cacheRoot(), "downloads")
		if candidate != "" {
			if !validSegment(candidate) {
				return nil, os.ErrInvalid
			}
			path = filepath.Join(path, candidate)
		}
		if version != "" {
			if candidate == "" || !validSegment(version) {
				return nil, os.ErrInvalid
			}
			path = filepath.Join(path, version)
		}
		return []string{path}, nil
	}
	if kind == "catalog" {
		root := filepath.Join(s.cacheRoot(), "catalog")
		if candidate == "" {
			return []string{root}, nil
		}
		if !validSegment(candidate) {
			return nil, os.ErrInvalid
		}
		entries, err := os.ReadDir(root)
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		var paths []string
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			paths = append(paths, filepath.Join(root, entry.Name(), candidate+".json"))
		}
		return paths, nil
	}
	return nil, os.ErrInvalid
}

func measurePath(path string) (CleanResult, error) {
	var result CleanResult
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			result.Files++
			result.Bytes += info.Size()
		}
		return nil
	})
	if os.IsNotExist(err) {
		return CleanResult{}, nil
	}
	if err != nil {
		return CleanResult{}, err
	}
	return result, nil
}

func removeMeasured(path string) (CleanResult, error) {
	result, err := measurePath(path)
	if err != nil {
		return CleanResult{}, err
	}
	return result, os.RemoveAll(path)
}
