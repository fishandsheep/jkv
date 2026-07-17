package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"jkv/internal/catalog"
)

type CatalogCache struct {
	FetchedAt time.Time         `json:"fetched_at"`
	CheckedAt time.Time         `json:"checked_at,omitempty"`
	Releases  []catalog.Release `json:"releases"`
}

type archiveMetadata struct {
	Release catalog.Release `json:"release"`
	SHA256  string          `json:"sha256"`
}

type CleanResult struct {
	Files int
	Bytes int64
}

func (s *Store) cacheRoot() string { return filepath.Join(s.Root, "cache") }

func (s *Store) catalogCachePath(p catalog.Platform, candidate string) string {
	return filepath.Join(s.cacheRoot(), "catalog", p.OS+"-"+p.Arch, candidate+".json")
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
	return cached, nil
}

func (s *Store) SaveCatalog(p catalog.Platform, candidate string, cached CatalogCache) error {
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
	if json.Unmarshal(b, &metadata) != nil || metadata.Release.Candidate != candidate || metadata.Release.Version != version || metadata.Release.URL == "" {
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
	if json.Unmarshal(b, &metadata) != nil || metadata.Release.URL != r.URL || metadata.SHA256 == "" {
		return "", false
	}
	sum, err := fileSHA256(archive)
	return archive, err == nil && strings.EqualFold(sum, metadata.SHA256)
}

func (s *Store) saveArchiveMetadata(r catalog.Release, sum string) error {
	_, path, ok := s.archivePaths(r.Candidate, r.Version)
	if !ok {
		return os.ErrInvalid
	}
	b, err := json.MarshalIndent(archiveMetadata{Release: r, SHA256: sum}, "", "  ")
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
	if kind == "" {
		return removeMeasured(s.cacheRoot())
	}
	if kind == "downloads" {
		path := filepath.Join(s.cacheRoot(), "downloads")
		if candidate != "" {
			if !validSegment(candidate) {
				return CleanResult{}, os.ErrInvalid
			}
			path = filepath.Join(path, candidate)
		}
		if version != "" {
			if candidate == "" || !validSegment(version) {
				return CleanResult{}, os.ErrInvalid
			}
			path = filepath.Join(path, version)
		}
		return removeMeasured(path)
	}
	if kind == "catalog" {
		root := filepath.Join(s.cacheRoot(), "catalog")
		if candidate == "" {
			return removeMeasured(root)
		}
		if !validSegment(candidate) {
			return CleanResult{}, os.ErrInvalid
		}
		entries, err := os.ReadDir(root)
		if os.IsNotExist(err) {
			return CleanResult{}, nil
		}
		if err != nil {
			return CleanResult{}, err
		}
		var total CleanResult
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			result, err := removeMeasured(filepath.Join(root, entry.Name(), candidate+".json"))
			if err != nil {
				return total, err
			}
			total.Files += result.Files
			total.Bytes += result.Bytes
		}
		return total, nil
	}
	return CleanResult{}, os.ErrInvalid
}

func removeMeasured(path string) (CleanResult, error) {
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
	return result, os.RemoveAll(path)
}
