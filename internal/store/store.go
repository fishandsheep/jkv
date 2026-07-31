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
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/fishandsheep/jkv/internal/catalog"
)

type Store struct {
	Root      string
	HTTP      *http.Client
	RetryMax  int
	RetryBase time.Duration
	// NoProgressTimeout cancels a response body that yields no bytes for this long.
	NoProgressTimeout time.Duration
}

var (
	ErrIntegrity = errors.New("完整性校验失败")
	ErrNetwork   = errors.New("下载网络错误")
)

type InstallOptions struct {
	RequireChecksum bool
	Repair          bool
}

type metadata struct {
	SchemaVersion int             `json:"schema_version"`
	Release       catalog.Release `json:"release"`
	InstalledAt   time.Time       `json:"installed_at"`
}

const stateSchemaVersion = 1

func DefaultRoot() string {
	if v := os.Getenv("JKV_DIR"); v != "" {
		return v
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".jkv")
}

func New(root string) *Store {
	return &Store{
		Root:              root,
		HTTP:              newHTTPClient(),
		RetryMax:          3,
		RetryBase:         100 * time.Millisecond,
		NoProgressTimeout: 60 * time.Second,
	}
}

func newHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.ExpectContinueTimeout = time.Second
	return &http.Client{Transport: transport}
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
	return s.InstallWithOptions(ctx, r, progress, InstallOptions{})
}

func (s *Store) Repair(ctx context.Context, r catalog.Release, progress io.Writer) error {
	return s.InstallWithOptions(ctx, r, progress, InstallOptions{Repair: true})
}

func (s *Store) InstallWithOptions(ctx context.Context, r catalog.Release, progress io.Writer, options InstallOptions) error {
	if options.RequireChecksum && r.ChecksumURL == "" && r.ChecksumValue == "" {
		return fmt.Errorf("%w: 严格模式要求 SHA-256，当前源未提供", ErrIntegrity)
	}
	if !validSegment(r.Candidate) || !validSegment(r.Version) {
		return errors.New("无效 candidate 或版本")
	}
	lock, err := s.acquireLock(ctx, "install-"+r.Candidate+"-"+r.Version)
	if err != nil {
		return err
	}
	defer lock.release()
	dest := s.CandidateDir(r.Candidate, r.Version)
	_, statErr := os.Stat(dest)
	exists := statErr == nil
	if options.Repair && !exists {
		return fmt.Errorf("未安装 %s %s", r.Candidate, r.Version)
	}
	if exists && !options.Repair {
		if s.installationMatches(dest, r) {
			if progress != nil {
				fmt.Fprintln(progress, "已安装，无需重复操作")
			}
			return nil
		}
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
	archive, err := s.obtainArchive(ctx, r, filepath.Join(tmpRoot, "archive"), progress)
	if err != nil {
		return err
	}
	extract := filepath.Join(tmpRoot, "extract")
	if err := os.MkdirAll(extract, 0o755); err != nil {
		return err
	}
	if err := unpack(archive, r.URL, extract); err != nil {
		return fmt.Errorf("%w: %v", ErrIntegrity, err)
	}
	root, err := flattenedRoot(extract)
	if err != nil {
		return err
	}
	m := metadata{SchemaVersion: stateSchemaVersion, Release: r, InstalledAt: time.Now()}
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(root, ".jkv-release.json"), append(b, '\n'), 0o644); err != nil {
		return err
	}
	if options.Repair {
		backup := filepath.Join(tmpRoot, "previous")
		if err := os.Rename(dest, backup); err != nil {
			return err
		}
		if err := os.Rename(root, dest); err != nil {
			if restoreErr := os.Rename(backup, dest); restoreErr != nil {
				return fmt.Errorf("修复替换失败: %v；恢复旧版本失败: %w", err, restoreErr)
			}
			return err
		}
	} else if err := os.Rename(root, dest); err != nil {
		return err
	}
	return nil
}

type fileLock struct {
	path string
}

func (s *Store) acquireLock(ctx context.Context, name string) (*fileLock, error) {
	if !validSegment(name) {
		return nil, errors.New("无效锁名称")
	}
	dir := filepath.Join(s.Root, "locks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, name+".lock")
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintf(f, "pid=%d\ncreated=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
			if closeErr := f.Close(); closeErr != nil {
				_ = os.Remove(path)
				return nil, closeErr
			}
			return &fileLock{path: path}, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > 6*time.Hour {
			_ = os.Remove(path)
			continue
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("等待锁失败: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func (l *fileLock) release() {
	_ = os.Remove(l.path)
}

func (s *Store) installationMatches(dest string, release catalog.Release) bool {
	b, err := os.ReadFile(filepath.Join(dest, ".jkv-release.json"))
	if err != nil {
		return false
	}
	var installed metadata
	return json.Unmarshal(b, &installed) == nil &&
		installed.SchemaVersion <= stateSchemaVersion &&
		installed.Release.Candidate == release.Candidate &&
		installed.Release.Version == release.Version
}

func (s *Store) InstalledRelease(candidate, version string) (catalog.Release, error) {
	if !validSegment(candidate) || !validSegment(version) {
		return catalog.Release{}, errors.New("无效 candidate 或版本")
	}
	path := filepath.Join(s.CandidateDir(candidate, version), ".jkv-release.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return catalog.Release{}, fmt.Errorf("读取安装元数据失败: %w", err)
	}
	var installed metadata
	if err := json.Unmarshal(b, &installed); err != nil {
		return catalog.Release{}, fmt.Errorf("读取安装元数据失败: %w", err)
	}
	if installed.SchemaVersion > stateSchemaVersion {
		return catalog.Release{}, fmt.Errorf("不支持安装 schema %d", installed.SchemaVersion)
	}
	if installed.Release.Candidate != candidate || installed.Release.Version != version || installed.Release.URL == "" {
		return catalog.Release{}, errors.New("安装元数据无效")
	}
	return installed.Release, nil
}

func (s *Store) obtainArchive(ctx context.Context, r catalog.Release, staging string, progress io.Writer) (string, error) {
	cacheLock, err := s.acquireLock(ctx, "cache")
	if err != nil {
		return "", err
	}
	archive, cached := s.validCachedArchive(r)
	if cached {
		err := copyPath(archive, staging)
		cacheLock.release()
		if err != nil {
			return "", err
		}
		if progress != nil {
			fmt.Fprintln(progress, "使用本地下载缓存")
		}
		return staging, nil
	}
	cacheLock.release()
	archive, _, ok := s.archivePaths(r.Candidate, r.Version)
	if !ok {
		return "", errors.New("无效 candidate 或版本")
	}
	if err := os.MkdirAll(filepath.Dir(archive), 0o755); err != nil {
		return "", err
	}
	partialDir := filepath.Join(s.Root, "partials", "downloads", r.Candidate, r.Version)
	if err := os.MkdirAll(partialDir, 0o755); err != nil {
		return "", err
	}
	tmpPath := filepath.Join(partialDir, "archive.partial")
	sum, err := s.download(ctx, r.URL, tmpPath, progress)
	if err != nil {
		return "", err
	}
	if r.ChecksumValue != "" {
		if !strings.EqualFold(sum, r.ChecksumValue) {
			_ = os.Remove(tmpPath)
			return "", fmt.Errorf("%w: Catalog SHA-256 不匹配", ErrIntegrity)
		}
	} else if r.ChecksumURL != "" {
		if err := s.verifyChecksum(ctx, r.ChecksumURL, sum); err != nil {
			_ = os.Remove(tmpPath)
			return "", err
		}
	}
	cacheLock, err = s.acquireLock(ctx, "cache")
	if err != nil {
		return "", err
	}
	defer cacheLock.release()
	if err := os.MkdirAll(filepath.Dir(archive), 0o755); err != nil {
		return "", err
	}
	if err := os.Remove(archive); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err := os.Rename(tmpPath, archive); err != nil {
		return "", err
	}
	if err := s.saveArchiveMetadata(r, sum); err != nil {
		return "", err
	}
	if err := copyPath(archive, staging); err != nil {
		return "", err
	}
	_ = os.RemoveAll(partialDir)
	return staging, nil
}

func copyPath(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func (s *Store) download(ctx context.Context, rawURL, path string, progress io.Writer) (string, error) {
	attempts := s.RetryMax
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		sum, err := s.downloadAttempt(ctx, rawURL, path, progress)
		if err == nil {
			return sum, nil
		}
		lastErr = err
		if !retryableDownload(err) || attempt+1 == attempts {
			break
		}
		delay := s.RetryBase << attempt
		if delay <= 0 {
			continue
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
	}
	return "", lastErr
}

type downloadStatusError struct {
	status string
	code   int
}

func (e *downloadStatusError) Error() string {
	return fmt.Sprintf("下载失败: HTTP %s", e.status)
}

func (e *downloadStatusError) Unwrap() error { return ErrNetwork }

func retryableDownload(err error) bool {
	var status *downloadStatusError
	if errors.As(err, &status) {
		return status.code == http.StatusRequestTimeout ||
			status.code == http.StatusTooManyRequests ||
			status.code >= 500
	}
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

func (s *Store) downloadAttempt(ctx context.Context, rawURL, path string, progress io.Writer) (string, error) {
	offset := int64(0)
	if info, err := os.Stat(path); err == nil {
		offset = info.Size()
	} else if !os.IsNotExist(err) {
		return "", err
	}
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "jkv/0.2")
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNetwork, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &downloadStatusError{status: resp.Status, code: resp.StatusCode}
	}
	appendResponse := offset > 0 && resp.StatusCode == http.StatusPartialContent
	if !appendResponse {
		offset = 0
	}
	flags := os.O_CREATE | os.O_WRONLY
	if appendResponse {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if appendResponse {
		existing, err := os.Open(path)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(h, existing); err != nil {
			_ = existing.Close()
			return "", err
		}
		if err := existing.Close(); err != nil {
			return "", err
		}
	}
	w := io.MultiWriter(f, h)
	activity := &activityReader{r: resp.Body}
	activity.last.Store(time.Now().UnixNano())
	reader := io.Reader(activity)
	if progress != nil {
		total := resp.ContentLength
		if total >= 0 {
			total += offset
		}
		reader = &progressReader{r: activity, total: total, n: offset, out: progress, started: time.Now()}
	}
	stalled := make(chan struct{}, 1)
	done := make(chan struct{})
	if s.NoProgressTimeout > 0 {
		go watchDownloadProgress(activity, s.NoProgressTimeout, cancel, stalled, done)
	}
	_, copyErr := io.Copy(w, reader)
	close(done)
	select {
	case <-stalled:
		return "", fmt.Errorf("%w: 无下载进度，已取消", ErrNetwork)
	default:
	}
	if copyErr != nil {
		return "", fmt.Errorf("%w: %v", ErrNetwork, copyErr)
	}
	if progress != nil {
		fmt.Fprintln(progress)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type activityReader struct {
	r    io.Reader
	last atomic.Int64
}

func (r *activityReader) Read(b []byte) (int, error) {
	n, err := r.r.Read(b)
	if n > 0 {
		r.last.Store(time.Now().UnixNano())
	}
	return n, err
}

func watchDownloadProgress(reader *activityReader, timeout time.Duration, cancel context.CancelFunc, stalled chan<- struct{}, done <-chan struct{}) {
	interval := timeout / 4
	if interval < 10*time.Millisecond {
		interval = 10 * time.Millisecond
	}
	timer := time.NewTicker(interval)
	defer timer.Stop()
	for {
		select {
		case <-done:
			return
		case now := <-timer.C:
			last := time.Unix(0, reader.last.Load())
			if now.Sub(last) >= timeout {
				stalled <- struct{}{}
				cancel()
				return
			}
		}
	}
}

type progressReader struct {
	r       io.Reader
	total   int64
	n       int64
	out     io.Writer
	last    int
	started time.Time
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.n += int64(n)
	elapsed := time.Since(p.started).Seconds()
	rate := float64(p.n) / (1 << 20)
	if elapsed > 0 {
		rate /= elapsed
	}
	if p.total > 0 {
		pct := int(p.n * 100 / p.total)
		if pct >= p.last+2 || pct == 100 {
			fmt.Fprintf(p.out, "\r下载 %3d%% %.1f MiB/s", pct, rate)
			p.last = pct
		}
	} else if p.n/(10<<20) > int64(p.last) {
		p.last = int(p.n / (10 << 20))
		fmt.Fprintf(p.out, "\r下载 %d MiB %.1f MiB/s", p.n>>20, rate)
	}
	return n, err
}

func (s *Store) verifyChecksum(ctx context.Context, rawURL, got string) error {
	attempts := s.RetryMax
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		lastErr = s.verifyChecksumAttempt(ctx, rawURL, got)
		if lastErr == nil || !errors.Is(lastErr, ErrNetwork) {
			return lastErr
		}
		if attempt+1 < attempts {
			timer := time.NewTimer(s.RetryBase << attempt)
			select {
			case <-ctx.Done():
				timer.Stop()
				return fmt.Errorf("%w: %v", ErrNetwork, ctx.Err())
			case <-timer.C:
			}
		}
	}
	return lastErr
}

func (s *Store) verifyChecksumAttempt(ctx context.Context, rawURL, got string) error {
	requestCtx := ctx
	cancel := func() {}
	if s.NoProgressTimeout > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, s.NoProgressTimeout)
	}
	defer cancel()
	req, _ := http.NewRequestWithContext(requestCtx, http.MethodGet, rawURL, nil)
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%w: 读取校验和失败: %v", ErrNetwork, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: 读取校验和失败: HTTP %s", ErrNetwork, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return fmt.Errorf("%w: 读取校验和失败: %v", ErrNetwork, err)
	}
	expected := strings.Fields(string(b))
	if len(expected) == 0 || len(expected[0]) != 64 {
		return fmt.Errorf("%w: 镜像返回无效 SHA-256", ErrIntegrity)
	}
	if _, err := hex.DecodeString(expected[0]); err != nil {
		return fmt.Errorf("%w: 镜像返回无效 SHA-256", ErrIntegrity)
	}
	if !strings.EqualFold(expected[0], got) {
		return fmt.Errorf("%w: SHA-256 不匹配: expected %s, got %s", ErrIntegrity, expected[0], got)
	}
	return nil
}

func unpack(path, rawURL, dest string) error {
	l := strings.ToLower(strings.Split(rawURL, "?")[0])
	switch {
	case strings.HasSuffix(l, ".zip"):
		return unzipWithLimits(path, dest, defaultArchiveLimits)
	case strings.HasSuffix(l, ".tar.gz"), strings.HasSuffix(l, ".tgz"):
		return untarWithLimits(path, dest, defaultArchiveLimits)
	default:
		return fmt.Errorf("不支持压缩格式: %s", rawURL)
	}
}

type archiveLimits struct {
	MaxFiles      int
	MaxFileBytes  int64
	MaxTotalBytes int64
}

var defaultArchiveLimits = archiveLimits{
	MaxFiles:      200_000,
	MaxFileBytes:  8 << 30,
	MaxTotalBytes: 32 << 30,
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
	return unzipWithLimits(path, dest, defaultArchiveLimits)
}

func unzipWithLimits(path, dest string, limits archiveLimits) error {
	r, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer r.Close()
	var total int64
	for index, f := range r.File {
		if index+1 > limits.MaxFiles {
			return fmt.Errorf("压缩包文件数超过展开上限 %d", limits.MaxFiles)
		}
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
		if !f.Mode().IsRegular() {
			return fmt.Errorf("压缩包包含不支持的特殊文件: %s", f.Name)
		}
		size := int64(f.UncompressedSize64)
		if size > limits.MaxFileBytes || size > limits.MaxTotalBytes-total {
			return fmt.Errorf("压缩包内容超过展开上限: %s", f.Name)
		}
		total += size
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
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err == nil {
			var written int64
			written, err = io.Copy(out, io.LimitReader(rc, limits.MaxFileBytes+1))
			if err == nil && written > limits.MaxFileBytes {
				err = fmt.Errorf("压缩包内容超过展开上限: %s", f.Name)
			}
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
	return untarWithLimits(path, dest, defaultArchiveLimits)
}

func untarWithLimits(path, dest string, limits archiveLimits) error {
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
	var files int
	var total int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		files++
		if files > limits.MaxFiles {
			return fmt.Errorf("压缩包文件数超过展开上限 %d", limits.MaxFiles)
		}
		target, err := safePath(dest, h.Name)
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := ensureDirectoryPath(dest, target, os.FileMode(h.Mode).Perm()); err != nil {
				return err
			}
		case tar.TypeReg, byte(0):
			if h.Size < 0 || h.Size > limits.MaxFileBytes || h.Size > limits.MaxTotalBytes-total {
				return fmt.Errorf("压缩包内容超过展开上限: %s", h.Name)
			}
			total += h.Size
			if err := ensureDirectoryPath(dest, filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(h.Mode).Perm())
			if err == nil {
				var written int64
				written, err = io.Copy(out, io.LimitReader(tr, limits.MaxFileBytes+1))
				if err == nil && written != h.Size {
					err = fmt.Errorf("压缩包文件大小无效: %s", h.Name)
				}
			}
			if out != nil {
				out.Close()
			}
			if err != nil {
				return err
			}
		case tar.TypeSymlink:
			linkTarget := filepath.Clean(filepath.Join(filepath.Dir(target), filepath.FromSlash(h.Linkname)))
			cleanDest := filepath.Clean(dest) + string(os.PathSeparator)
			if linkTarget != filepath.Clean(dest) && !strings.HasPrefix(linkTarget, cleanDest) {
				return fmt.Errorf("压缩包包含越界链接: %s -> %s", h.Name, h.Linkname)
			}
			if err := ensureDirectoryPath(dest, filepath.Dir(target), 0o755); err != nil {
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
			info, err := os.Lstat(link)
			if err != nil || !info.Mode().IsRegular() {
				return fmt.Errorf("压缩包包含无效硬链接: %s -> %s", h.Name, h.Linkname)
			}
			if err := ensureDirectoryPath(dest, filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Link(link, target); err != nil {
				return err
			}
		default:
			return fmt.Errorf("压缩包包含不支持的特殊文件: %s", h.Name)
		}
	}
	return nil
}

func ensureDirectoryPath(root, dir string, mode os.FileMode) error {
	root = filepath.Clean(root)
	dir = filepath.Clean(dir)
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("压缩包包含越界目录: %s", dir)
	}
	current := root
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	if rel == "." {
		return nil
	}
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if err := os.Mkdir(current, mode); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("压缩包目录经过链接: %s", current)
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

type defaultsDocument struct {
	SchemaVersion int               `json:"schema_version"`
	Defaults      map[string]string `json:"defaults"`
}

func (s *Store) Defaults() (map[string]string, error) {
	b, err := os.ReadFile(s.defaultsPath())
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var document defaultsDocument
	if err := json.Unmarshal(b, &document); err == nil && document.SchemaVersion > 0 {
		if document.SchemaVersion > stateSchemaVersion {
			return nil, fmt.Errorf("不支持 defaults schema %d", document.SchemaVersion)
		}
		if document.Defaults == nil {
			document.Defaults = map[string]string{}
		}
		return document.Defaults, nil
	}
	legacy := map[string]string{}
	if err := json.Unmarshal(b, &legacy); err != nil {
		return nil, err
	}
	return legacy, nil
}

func (s *Store) SetDefault(candidate, version string) error {
	lock, err := s.acquireLock(context.Background(), "defaults")
	if err != nil {
		return err
	}
	defer lock.release()
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
	return s.writeDefaults(m)
}

func (s *Store) Remove(candidate, version string) error {
	if !validSegment(candidate) || !validSegment(version) {
		return errors.New("无效 candidate 或版本")
	}
	lock, err := s.acquireLock(context.Background(), "install-"+candidate+"-"+version)
	if err != nil {
		return err
	}
	defer lock.release()
	home := s.CandidateDir(candidate, version)
	if _, err := os.Stat(home); err != nil {
		return fmt.Errorf("未安装 %s %s", candidate, version)
	}
	if err := os.RemoveAll(home); err != nil {
		return err
	}
	defaultsLock, lockErr := s.acquireLock(context.Background(), "defaults")
	if lockErr != nil {
		return lockErr
	}
	defer defaultsLock.release()
	m, defaultsErr := s.Defaults()
	if defaultsErr == nil && m[candidate] == version {
		delete(m, candidate)
		if writeErr := s.writeDefaults(m); writeErr != nil {
			return writeErr
		}
	}
	return nil
}

func (s *Store) writeDefaults(defaults map[string]string) error {
	path := s.defaultsPath()
	if current, err := os.ReadFile(path); err == nil {
		var probe map[string]json.RawMessage
		if json.Unmarshal(current, &probe) == nil {
			if _, versioned := probe["schema_version"]; !versioned {
				backup := path + ".v0.backup"
				if _, statErr := os.Stat(backup); os.IsNotExist(statErr) {
					if err := atomicWrite(backup, current, 0o600); err != nil {
						return err
					}
				}
			}
		}
	}
	document := defaultsDocument{SchemaVersion: stateSchemaVersion, Defaults: defaults}
	b, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(b, '\n'), 0o644)
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
