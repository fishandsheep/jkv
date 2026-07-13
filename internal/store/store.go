package store

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"jkv/internal/catalog"
)

type Store struct {
	Root string
	HTTP *http.Client
}

type metadata struct {
	Release     catalog.Release `json:"release"`
	InstalledAt time.Time       `json:"installed_at"`
}

func DefaultRoot() string {
	if v := os.Getenv("JKV_DIR"); v != "" {
		return v
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".jkv")
}

func New(root string) *Store {
	return &Store{Root: root, HTTP: &http.Client{Timeout: 0}}
}

func (s *Store) CandidateDir(candidate, version string) string {
	return filepath.Join(s.Root, "candidates", candidate, version)
}

func (s *Store) Home(candidate, version string) (string, error) {
	d := s.CandidateDir(candidate, version)
	if st, err := os.Stat(d); err != nil || !st.IsDir() {
		return "", fmt.Errorf("未安装 %s %s", candidate, version)
	}
	if candidate == "java" {
		macHome := filepath.Join(d, "Contents", "Home")
		if st, err := os.Stat(macHome); err == nil && st.IsDir() {
			return macHome, nil
		}
	}
	return d, nil
}

func (s *Store) Installed(candidate string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.Root, "candidates", candidate))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

func (s *Store) Install(ctx context.Context, r catalog.Release, progress io.Writer) error {
	dest := s.CandidateDir(r.Candidate, r.Version)
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("已安装 %s %s", r.Candidate, r.Version)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmpRoot, err := os.MkdirTemp(filepath.Dir(dest), ".jkv-install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpRoot)
	archive := filepath.Join(tmpRoot, "download")
	sum, err := s.download(ctx, r.URL, archive, progress)
	if err != nil {
		return err
	}
	if r.ChecksumURL != "" {
		if err := s.verifyChecksum(ctx, r.ChecksumURL, sum); err != nil {
			return err
		}
	}
	extract := filepath.Join(tmpRoot, "extract")
	if err := os.MkdirAll(extract, 0o755); err != nil {
		return err
	}
	if err := unpack(archive, r.URL, extract); err != nil {
		return err
	}
	root, err := flattenedRoot(extract)
	if err != nil {
		return err
	}
	if root == extract {
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return err
		}
		entries, _ := os.ReadDir(extract)
		for _, e := range entries {
			if err := os.Rename(filepath.Join(extract, e.Name()), filepath.Join(dest, e.Name())); err != nil {
				return err
			}
		}
	} else if err := os.Rename(root, dest); err != nil {
		return err
	}
	m := metadata{Release: r, InstalledAt: time.Now()}
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(dest, ".jkv-release.json"), append(b, '\n'), 0o644); err != nil {
		return err
	}
	return nil
}

func (s *Store) download(ctx context.Context, rawURL, path string, progress io.Writer) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "jkv/0.1")
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("下载失败: HTTP %s", resp.Status)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	w := io.MultiWriter(f, h)
	reader := io.Reader(resp.Body)
	if progress != nil {
		reader = &progressReader{r: resp.Body, total: resp.ContentLength, out: progress}
	}
	if _, err := io.Copy(w, reader); err != nil {
		return "", err
	}
	if progress != nil {
		fmt.Fprintln(progress)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type progressReader struct {
	r     io.Reader
	total int64
	n     int64
	out   io.Writer
	last  int
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.n += int64(n)
	if p.total > 0 {
		pct := int(p.n * 100 / p.total)
		if pct >= p.last+2 || pct == 100 {
			fmt.Fprintf(p.out, "\r下载 %3d%%", pct)
			p.last = pct
		}
	} else if p.n/(10<<20) > int64(p.last) {
		p.last = int(p.n / (10 << 20))
		fmt.Fprintf(p.out, "\r下载 %d MiB", p.n>>20)
	}
	return n, err
}

func (s *Store) verifyChecksum(ctx context.Context, rawURL, got string) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("读取校验和失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("读取校验和失败: HTTP %s", resp.Status)
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	expected := strings.Fields(string(b))
	if len(expected) == 0 || len(expected[0]) != 64 {
		return errors.New("镜像返回无效 SHA-256")
	}
	if !strings.EqualFold(expected[0], got) {
		return fmt.Errorf("SHA-256 不匹配: expected %s, got %s", expected[0], got)
	}
	return nil
}

func unpack(path, rawURL, dest string) error {
	l := strings.ToLower(strings.Split(rawURL, "?")[0])
	switch {
	case strings.HasSuffix(l, ".zip"):
		return unzip(path, dest)
	case strings.HasSuffix(l, ".tar.gz"), strings.HasSuffix(l, ".tgz"):
		return untar(path, dest)
	default:
		return fmt.Errorf("不支持压缩格式: %s", rawURL)
	}
}

func safePath(dest, name string) (string, error) {
	name = filepath.FromSlash(name)
	target := filepath.Join(dest, name)
	cleanDest := filepath.Clean(dest) + string(os.PathSeparator)
	if target != filepath.Clean(dest) && !strings.HasPrefix(filepath.Clean(target), cleanDest) {
		return "", fmt.Errorf("压缩包包含越界路径: %s", name)
	}
	return target, nil
}

func unzip(path, dest string) error {
	r, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		target, err := safePath(dest, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, f.Mode().Perm()); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		mode := f.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
		if err == nil {
			_, err = io.Copy(out, rc)
		}
		rc.Close()
		if out != nil {
			out.Close()
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func untar(path, dest string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(bufio.NewReader(f))
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		target, err := safePath(dest, h.Name)
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(h.Mode).Perm()); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(h.Mode).Perm())
			if err == nil {
				_, err = io.Copy(out, tr)
			}
			if out != nil {
				out.Close()
			}
			if err != nil {
				return err
			}
		case tar.TypeSymlink:
			if runtime.GOOS == "windows" {
				continue
			}
			linkTarget := filepath.Clean(filepath.Join(filepath.Dir(target), filepath.FromSlash(h.Linkname)))
			cleanDest := filepath.Clean(dest) + string(os.PathSeparator)
			if linkTarget != filepath.Clean(dest) && !strings.HasPrefix(linkTarget, cleanDest) {
				return fmt.Errorf("压缩包包含越界链接: %s -> %s", h.Name, h.Linkname)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(h.Linkname, target); err != nil {
				return err
			}
		case tar.TypeLink:
			link, err := safePath(dest, h.Linkname)
			if err != nil {
				return err
			}
			if err := os.Link(link, target); err != nil {
				return err
			}
		}
	}
	return nil
}

func flattenedRoot(dest string) (string, error) {
	entries, err := os.ReadDir(dest)
	if err != nil {
		return "", err
	}
	if len(entries) == 1 && entries[0].IsDir() {
		return filepath.Join(dest, entries[0].Name()), nil
	}
	return dest, nil
}

func (s *Store) defaultsPath() string { return filepath.Join(s.Root, "defaults.json") }

func (s *Store) Defaults() (map[string]string, error) {
	b, err := os.ReadFile(s.defaultsPath())
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func (s *Store) SetDefault(candidate, version string) error {
	if _, err := s.Home(candidate, version); err != nil {
		return err
	}
	m, err := s.Defaults()
	if err != nil {
		return err
	}
	m[candidate] = version
	if err := os.MkdirAll(s.Root, 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	return atomicWrite(s.defaultsPath(), append(b, '\n'), 0o644)
}

func (s *Store) Remove(candidate, version string) error {
	home := s.CandidateDir(candidate, version)
	if _, err := os.Stat(home); err != nil {
		return fmt.Errorf("未安装 %s %s", candidate, version)
	}
	if err := os.RemoveAll(home); err != nil {
		return err
	}
	m, err := s.Defaults()
	if err == nil && m[candidate] == version {
		delete(m, candidate)
		b, _ := json.MarshalIndent(m, "", "  ")
		_ = atomicWrite(s.defaultsPath(), append(b, '\n'), 0o644)
	}
	return nil
}

func atomicWrite(path string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".jkv-write-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err == nil {
		err = f.Chmod(mode)
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}
