package selfmanage

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const (
	ReceiptName  = "jkv-install.json"
	ManagedBegin = "# >>> jkv managed >>>"
	ManagedEnd   = "# <<< jkv managed <<<"
	githubAPI    = "https://api.github.com/repos/fishandsheep/jkv/releases/latest"
	cnbBadge     = "https://cnb.cool/fishandsheep/jkv/-/badge/release"
	cnbBase      = "https://cnb.cool/fishandsheep/jkv/-/releases/download"
	githubBase   = "https://github.com/fishandsheep/jkv/releases/download"
)

var (
	ErrNetwork   = errors.New("自身管理网络错误")
	ErrIntegrity = errors.New("自身管理完整性校验失败")
	ErrState     = errors.New("自身管理状态错误")
	versionRE    = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.([0-9]+)$`)
	badgeTagRE   = regexp.MustCompile(`(?i)>\s*(v[0-9]+\.[0-9]+\.[0-9]+)\s*</text>`)
	shaLineRE    = regexp.MustCompile(`(?i)^([0-9a-f]{64})[ \t]+[*]?([^ \t\r\n]+)[ \t]*$`)
)

type Receipt struct {
	SchemaVersion int      `json:"schema_version"`
	Root          string   `json:"root"`
	Binary        string   `json:"binary"`
	Profiles      []string `json:"managed_profiles,omitempty"`
}

type Config struct {
	Root               string
	CurrentVersion     string
	Executable         string
	HTTP               *http.Client
	CNBLatestURL       string
	GitHubLatestURL    string
	CNBDownloadBase    string
	GitHubDownloadBase string
	Stdin              io.Reader
	StdinTerminal      bool
	Stdout             io.Writer
	GOOS               string
	GOARCH             string
}

type Manager struct{ config Config }

func New(config Config) *Manager {
	if config.HTTP == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.ResponseHeaderTimeout = 30 * time.Second
		transport.TLSHandshakeTimeout = 10 * time.Second
		config.HTTP = &http.Client{Transport: transport, Timeout: 30 * time.Minute}
	}
	if config.CNBLatestURL == "" {
		config.CNBLatestURL = cnbBadge
	}
	if config.GitHubLatestURL == "" {
		config.GitHubLatestURL = githubAPI
	}
	if config.CNBDownloadBase == "" {
		config.CNBDownloadBase = cnbBase
	}
	if config.GitHubDownloadBase == "" {
		config.GitHubDownloadBase = githubBase
	}
	if config.Stdin == nil {
		config.Stdin = os.Stdin
	}
	if config.Stdout == nil {
		config.Stdout = os.Stdout
	}
	if config.GOOS == "" {
		config.GOOS = runtime.GOOS
	}
	if config.GOARCH == "" {
		config.GOARCH = runtime.GOARCH
	}
	return &Manager{config: config}
}

// SaveReceipt persists installer ownership using canonical paths.
func SaveReceipt(root, binary string, profiles []string) error {
	canonicalRoot, err := canonicalPath(root)
	if err != nil {
		return err
	}
	canonicalBinary, err := canonicalPath(binary)
	if err != nil {
		return err
	}
	receipt := Receipt{SchemaVersion: 1, Root: canonicalRoot, Binary: canonicalBinary, Profiles: uniquePaths(profiles)}
	body, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(canonicalRoot, ReceiptName), append(body, '\n'), 0o600)
}

func (m *Manager) Update(ctx context.Context) (string, bool, error) {
	root, target, err := m.managedTarget()
	if err != nil {
		return "", false, err
	}
	if _, err := loadOwnedReceipt(root, target); err != nil {
		if os.IsNotExist(err) {
			return "", false, fmt.Errorf("%w: 缺少安装收据；请先运行当前版本安装器", ErrState)
		}
		return "", false, err
	}
	if m.config.CurrentVersion == "dev" || versionRE.FindStringSubmatch(m.config.CurrentVersion) == nil {
		return "", false, fmt.Errorf("%w: 开发版或非稳定版不能自行更新", ErrState)
	}
	lock, err := acquireLock(ctx, filepath.Join(root, "locks", "self-update.lock"))
	if err != nil {
		return "", false, err
	}
	defer lock.release()

	latest, err := m.discoverLatest(ctx)
	if err != nil {
		return "", false, err
	}
	comparison := compareVersions(latest, m.config.CurrentVersion)
	if comparison == 0 {
		return latest, false, nil
	}
	if comparison < 0 {
		return "", false, fmt.Errorf("%w: 拒绝回滚：远端 %s 低于当前 %s", ErrState, latest, m.config.CurrentVersion)
	}
	asset, err := assetName(m.config.GOOS, m.config.GOARCH)
	if err != nil {
		return "", false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".jkv-update-")
	if err != nil {
		return "", false, err
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", false, err
	}
	keepStaged := false
	defer func() {
		if !keepStaged {
			_ = os.Remove(tmpPath)
		}
	}()

	bases := []string{m.config.CNBDownloadBase, m.config.GitHubDownloadBase}
	downloaded := false
	for index, base := range bases {
		if base == "" || (index > 0 && strings.TrimRight(base, "/") == strings.TrimRight(bases[0], "/")) {
			continue
		}
		assetURL := strings.TrimRight(base, "/") + "/" + latest + "/" + asset
		checksum, checksumErr := m.downloadPair(ctx, assetURL, asset, tmpPath)
		if checksumErr == nil {
			if err := verifyFile(tmpPath, checksum); err != nil {
				return "", false, err
			}
			downloaded = true
			break
		}
		if !errors.Is(checksumErr, ErrNetwork) {
			return "", false, checksumErr
		}
		if index+1 == len(bases) {
			return "", false, checksumErr
		}
	}
	if !downloaded {
		return "", false, fmt.Errorf("%w: 所有 jkv 下载地址均不可用", ErrNetwork)
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return "", false, err
	}
	if err := replaceExecutable(target, tmpPath); err != nil {
		return "", false, err
	}
	keepStaged = runtime.GOOS == "windows"
	return latest, true, nil
}

func (m *Manager) Uninstall(purge, yes bool) error {
	root, target, err := m.managedTarget()
	if err != nil {
		return err
	}
	if purge {
		if err := safePurgeRoot(root); err != nil {
			return err
		}
		if !yes {
			if !m.config.StdinTerminal {
				return fmt.Errorf("用法: jkv self uninstall --purge 在非交互环境必须同时使用 --yes")
			}
			fmt.Fprintf(m.config.Stdout, "将永久删除 %s，继续? [y/N] ", root)
			line, _ := bufio.NewReader(m.config.Stdin).ReadString('\n')
			switch strings.ToLower(strings.TrimSpace(line)) {
			case "y", "yes":
			default:
				fmt.Fprintln(m.config.Stdout, "已取消")
				return nil
			}
		}
	}
	profiles, err := m.ownedProfiles(root, target)
	if err != nil {
		return err
	}
	updates := map[string][]byte{}
	for _, profile := range profiles {
		updated, changed, err := validateAndRemoveBlock(profile, root)
		if err != nil {
			return err
		}
		if changed {
			updates[profile] = updated
		}
	}
	for profile, updated := range updates {
		mode := os.FileMode(0o600)
		if info, statErr := os.Stat(profile); statErr == nil {
			mode = info.Mode().Perm()
		}
		if err := atomicWrite(profile, updated, mode); err != nil {
			return fmt.Errorf("%w: 更新 shell 配置 %s: %v", ErrState, profile, err)
		}
	}
	if purge {
		if runtime.GOOS == "windows" {
			return removeManagedExecutable(target, root)
		}
		if err := os.RemoveAll(root); err != nil {
			return err
		}
		return nil
	}
	return removeManagedExecutable(target, "")
}

func (m *Manager) managedTarget() (string, string, error) {
	root, err := canonicalPath(m.config.Root)
	if err != nil {
		return "", "", fmt.Errorf("%w: 规范化 JKV_DIR: %v", ErrState, err)
	}
	executable := m.config.Executable
	if executable == "" {
		executable, err = os.Executable()
		if err != nil {
			return "", "", fmt.Errorf("%w: 查找当前二进制: %v", ErrState, err)
		}
	}
	executable, err = canonicalPath(executable)
	if err != nil {
		return "", "", fmt.Errorf("%w: 规范化当前二进制: %v", ErrState, err)
	}
	name := "jkv"
	if m.config.GOOS == "windows" {
		name += ".exe"
	}
	target, err := canonicalPath(filepath.Join(root, "bin", name))
	if err != nil {
		return "", "", fmt.Errorf("%w: 规范化受管二进制: %v", ErrState, err)
	}
	if !samePath(executable, target) {
		return "", "", fmt.Errorf("%w: 当前二进制不由 JKV_DIR 管理: %s", ErrState, executable)
	}
	return root, target, nil
}

func (m *Manager) discoverLatest(ctx context.Context) (string, error) {
	if m.config.CNBLatestURL != "" {
		body, err := m.get(ctx, m.config.CNBLatestURL, 1<<20)
		if err == nil {
			if match := badgeTagRE.FindSubmatch(body); len(match) == 2 {
				return string(match[1]), nil
			}
		}
	}
	body, err := m.get(ctx, m.config.GitHubLatestURL, 1<<20)
	if err != nil {
		return "", err
	}
	var release struct {
		TagName    string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.Unmarshal(body, &release); err != nil || release.Draft || release.Prerelease || versionRE.FindStringSubmatch(release.TagName) == nil {
		return "", fmt.Errorf("%w: GitHub Latest Release 响应无效", ErrNetwork)
	}
	return release.TagName, nil
}

func (m *Manager) downloadPair(ctx context.Context, rawURL, asset, path string) (string, error) {
	if err := m.download(ctx, rawURL, path, 256<<20); err != nil {
		return "", err
	}
	body, err := m.get(ctx, rawURL+".sha256", 4096)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(body))
	match := shaLineRE.FindStringSubmatch(line)
	if len(match) != 3 || match[2] != asset {
		return "", fmt.Errorf("%w: SHA-256 文件未严格引用 %s", ErrIntegrity, asset)
	}
	return strings.ToLower(match[1]), nil
}

func (m *Manager) get(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNetwork, err)
	}
	req.Header.Set("Accept", "application/vnd.github+json, image/svg+xml, application/json")
	req.Header.Set("User-Agent", "jkv-self-update")
	resp, err := m.config.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNetwork, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: HTTP %s", ErrNetwork, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(body)) > limit {
		return nil, fmt.Errorf("%w: 读取响应失败", ErrNetwork)
	}
	return body, nil
}

func (m *Manager) download(ctx context.Context, rawURL, path string, limit int64) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("%w: 下载地址无效", ErrNetwork)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNetwork, err)
	}
	req.Header.Set("User-Agent", "jkv-self-update")
	resp, err := m.config.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNetwork, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: HTTP %s", ErrNetwork, resp.Status)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(file, io.LimitReader(resp.Body, limit+1))
	closeErr := file.Close()
	if copyErr != nil || written > limit {
		return fmt.Errorf("%w: 下载失败", ErrNetwork)
	}
	return closeErr
}

func verifyFile(path, expected string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != strings.ToLower(expected) {
		return fmt.Errorf("%w: SHA-256 不匹配", ErrIntegrity)
	}
	return nil
}

func assetName(goos, goarch string) (string, error) {
	if goos != "linux" && goos != "darwin" && goos != "windows" || goarch != "amd64" && goarch != "arm64" {
		return "", fmt.Errorf("%w: 不支持自身更新平台 %s/%s", ErrState, goos, goarch)
	}
	asset := "jkv-" + goos + "-" + goarch
	if goos == "windows" {
		asset += ".exe"
	}
	return asset, nil
}

func compareVersions(left, right string) int {
	a, b := versionRE.FindStringSubmatch(left), versionRE.FindStringSubmatch(right)
	if a == nil || b == nil {
		return strings.Compare(left, right)
	}
	for index := 1; index <= 3; index++ {
		if len(a[index]) != len(b[index]) {
			if len(a[index]) < len(b[index]) {
				return -1
			}
			return 1
		}
		if a[index] < b[index] {
			return -1
		}
		if a[index] > b[index] {
			return 1
		}
	}
	return 0
}

func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolved, evalErr := filepath.EvalSymlinks(abs); evalErr == nil {
		return filepath.Clean(resolved), nil
	}
	parent, evalErr := filepath.EvalSymlinks(filepath.Dir(abs))
	if evalErr != nil {
		return "", evalErr
	}
	return filepath.Join(parent, filepath.Base(abs)), nil
}

func samePath(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func safePurgeRoot(root string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("%w: 查找 HOME: %v", ErrState, err)
	}
	home, err = canonicalPath(home)
	if err != nil {
		return fmt.Errorf("%w: 规范化 HOME: %v", ErrState, err)
	}
	volume := filepath.VolumeName(root) + string(os.PathSeparator)
	if samePath(root, volume) || samePath(root, home) || pathContains(root, home) {
		return fmt.Errorf("%w: 拒绝清理危险目录: %s", ErrState, root)
	}
	return nil
}

func pathContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) && !filepath.IsAbs(relative)
}

func (m *Manager) ownedProfiles(root, target string) ([]string, error) {
	receipt, err := loadOwnedReceipt(root, target)
	if err == nil {
		return uniquePaths(receipt.Profiles), nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("%w: 读取安装收据: %v", ErrState, err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	profiles := []string{
		filepath.Join(home, ".bashrc"), filepath.Join(home, ".bash_profile"), filepath.Join(home, ".profile"),
		filepath.Join(home, ".zshrc"), filepath.Join(home, ".config", "fish", "config.fish"),
		filepath.Join(home, "Documents", "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1"),
		filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1"),
	}
	return uniquePaths(profiles), nil
}

func loadOwnedReceipt(root, target string) (Receipt, error) {
	receiptPath := filepath.Join(root, ReceiptName)
	body, err := os.ReadFile(receiptPath)
	if err != nil {
		return Receipt{}, err
	}
	var receipt Receipt
	if json.Unmarshal(bytesWithoutBOM(body), &receipt) != nil || receipt.SchemaVersion != 1 {
		return Receipt{}, fmt.Errorf("%w: 安装收据无效: %s", ErrState, receiptPath)
	}
	receiptRoot, rootErr := canonicalPath(receipt.Root)
	receiptBinary, binaryErr := canonicalPath(receipt.Binary)
	if rootErr != nil || binaryErr != nil || !samePath(receiptRoot, root) || !samePath(receiptBinary, target) {
		return Receipt{}, fmt.Errorf("%w: 安装收据不属于当前 JKV_DIR", ErrState)
	}
	return receipt, nil
}

func bytesWithoutBOM(body []byte) []byte {
	if len(body) >= 3 && body[0] == 0xef && body[1] == 0xbb && body[2] == 0xbf {
		return body[3:]
	}
	return body
}

func uniquePaths(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, path := range paths {
		if path == "" {
			continue
		}
		clean := filepath.Clean(path)
		key := clean
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if !seen[key] {
			seen[key] = true
			out = append(out, clean)
		}
	}
	return out
}

func validateAndRemoveBlock(path, root string) ([]byte, bool, error) {
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("%w: 读取 shell 配置 %s: %v", ErrState, path, err)
	}
	text := strings.ReplaceAll(string(body), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	begin, end := -1, -1
	for index, line := range lines {
		switch strings.TrimSuffix(line, "\r") {
		case ManagedBegin:
			if begin != -1 {
				return nil, false, fmt.Errorf("%w: shell 配置包含重复 managed block: %s", ErrState, path)
			}
			begin = index
		case ManagedEnd:
			if end != -1 {
				return nil, false, fmt.Errorf("%w: shell 配置包含重复 managed block: %s", ErrState, path)
			}
			end = index
		}
	}
	if begin == -1 && end == -1 {
		return nil, false, nil
	}
	if begin < 0 || end <= begin {
		return nil, false, fmt.Errorf("%w: shell 配置中的 managed block 不完整: %s", ErrState, path)
	}
	block := strings.Join(lines[begin:end+1], "\n")
	if !blockOwnsRoot(block, root) {
		return nil, false, fmt.Errorf("%w: shell 配置中的 managed block 指向其他 JKV_DIR: %s", ErrState, path)
	}
	updated := append([]string(nil), lines[:begin]...)
	updated = append(updated, lines[end+1:]...)
	for len(updated) > 1 && updated[len(updated)-1] == "" && updated[len(updated)-2] == "" {
		updated = updated[:len(updated)-1]
	}
	return []byte(strings.Join(updated, "\n")), true, nil
}

func blockOwnsRoot(block, root string) bool {
	normalized := filepath.ToSlash(root)
	variants := []string{root, normalized, strings.ReplaceAll(root, `\`, `\\`)}
	for _, value := range variants {
		if value != "" && strings.Contains(block, value) {
			return true
		}
	}
	// Installers may write the lexical JKV_DIR path while receipt validation
	// canonicalizes through a platform symlink (notably macOS /var -> /private/var).
	// Resolve only explicit JKV_DIR assignments; arbitrary text must not establish
	// ownership of a managed block.
	for _, line := range strings.Split(block, "\n") {
		marker := strings.Index(line, "JKV_DIR")
		if marker < 0 {
			continue
		}
		value := line[marker+len("JKV_DIR"):]
		if equal := strings.IndexByte(value, '='); equal >= 0 {
			value = value[equal+1:]
		}
		value = strings.TrimSpace(value)
		value = strings.Trim(value, "'\"")
		value = strings.TrimSpace(strings.TrimSuffix(value, ";"))
		if value == "" {
			continue
		}
		if canonical, err := canonicalPath(value); err == nil && samePath(canonical, root) {
			return true
		}
	}
	return false
}

func atomicWrite(path string, body []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".jkv-profile-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(body); err == nil {
		err = tmp.Chmod(mode)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

func removeManagedExecutable(target, purgeRoot string) error {
	if runtime.GOOS == "windows" {
		helper, err := writeWindowsHelper(filepath.Dir(target), os.Getpid(), target, "", true, purgeRoot)
		if err != nil {
			return err
		}
		command := helperCommand(helper)
		if err := configureDetached(command); err != nil {
			return err
		}
		if err := command.Start(); err != nil {
			return fmt.Errorf("%w: 启动 Windows 卸载 helper: %v", ErrState, err)
		}
		return nil
	}
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("%w: 删除 jkv 二进制: %v", ErrState, err)
	}
	return nil
}

type fileLock struct{ path string }

func acquireLock(ctx context.Context, path string) (*fileLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	for {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintf(file, "pid=%d\n", os.Getpid())
			_ = file.Close()
			return &fileLock{path: path}, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("%w: 创建自身更新锁: %v", ErrState, err)
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > 6*time.Hour {
			_ = os.Remove(path)
			continue
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("%w: 等待自身更新锁: %v", ErrState, ctx.Err())
		case <-timer.C:
		}
	}
}

func (lock *fileLock) release() { _ = os.Remove(lock.path) }
