package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/fishandsheep/jkv/internal/catalog"
	"github.com/fishandsheep/jkv/internal/store"
)

var version = "dev"

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(exitCodeFor(err))
	}
}

const (
	exitUsage     = 2
	exitNotFound  = 3
	exitNetwork   = 4
	exitIntegrity = 5
	exitState     = 6
)

func exitCodeFor(err error) int {
	message := err.Error()
	switch {
	case errors.Is(err, store.ErrIntegrity):
		return exitIntegrity
	case errors.Is(err, store.ErrNetwork), errors.Is(err, catalog.ErrNetwork):
		return exitNetwork
	case strings.HasPrefix(message, "用法:"),
		strings.HasPrefix(message, "未知命令"),
		strings.HasPrefix(message, "不支持选项"),
		strings.HasPrefix(message, "不支持 candidate"):
		return exitUsage
	case strings.Contains(message, "SHA-256"),
		strings.Contains(message, "校验和"),
		strings.Contains(message, "压缩包包含"),
		strings.Contains(message, "归档"):
		return exitIntegrity
	case strings.Contains(message, "HTTP "),
		strings.Contains(message, "下载失败"),
		strings.Contains(message, "镜像"),
		strings.Contains(message, "网络"):
		return exitNetwork
	case strings.HasPrefix(message, "未安装"),
		strings.Contains(message, "未找到版本"),
		strings.Contains(message, "未设置默认版本"),
		strings.Contains(message, "暂无稳定"):
		return exitNotFound
	case strings.HasPrefix(message, "已安装"),
		strings.Contains(message, "已存在"),
		strings.Contains(message, "拒绝覆盖"),
		strings.Contains(message, "锁"):
		return exitState
	default:
		return 1
	}
}

func run(ctx context.Context, args []string) error {
	options, args, err := parseGlobalOptions(args)
	if err != nil {
		return err
	}
	ctx = context.WithValue(ctx, cliOptionsKey{}, options)
	if len(args) == 0 {
		usage()
		return nil
	}
	s := store.New(store.DefaultRoot())
	switch args[0] {
	case "list", "ls":
		return cmdList(ctx, s, args[1:])
	case "install", "i":
		return cmdInstall(ctx, s, args[1:])
	case "repair":
		return cmdRepair(ctx, s, args[1:])
	case "use", "u":
		return cmdUse(s, args[1:])
	case "default", "d":
		return cmdDefault(s, args[1:])
	case "current", "c":
		return cmdCurrent(ctx, s, args[1:])
	case "uninstall", "rm":
		return cmdUninstall(s, args[1:])
	case "home", "h":
		return cmdHome(ctx, s, args[1:])
	case "env", "e":
		return cmdEnv(s, args[1:])
	case "init", "in":
		return cmdInit(args[1:])
	case "mirror", "m":
		return cmdMirror(ctx, args[1:])
	case "clean", "cl":
		return cmdClean(ctx, s, args[1:])
	case "doctor":
		return cmdDoctor(ctx, s, args[1:])
	case "version", "v", "--version", "-v":
		if options.JSON {
			return writeJSON(map[string]string{"name": "jkv", "version": version})
		}
		fmt.Println("jkv", version)
		return nil
	case "help", "-h", "--help":
		usage()
		return nil
	case "__complete":
		return cmdComplete(ctx, s, args[1:])
	default:
		return fmt.Errorf("未知命令 %q；运行 jkv help", args[0])
	}
}

type cliOptionsKey struct{}

type cliOptions struct {
	JSON    bool
	Quiet   bool
	Verbose bool
}

func parseGlobalOptions(args []string) (cliOptions, []string, error) {
	var options cliOptions
	out := make([]string, 0, len(args))
	for _, arg := range args {
		switch arg {
		case "--json":
			options.JSON = true
		case "--quiet":
			options.Quiet = true
		case "--verbose":
			options.Verbose = true
		default:
			out = append(out, arg)
		}
	}
	return options, out, nil
}

func optionsFromContext(ctx context.Context) cliOptions {
	options, _ := ctx.Value(cliOptionsKey{}).(cliOptions)
	return options
}

func usage() {
	fmt.Print(`jkv - 中国网络友好、跨平台 JVM 工具版本管理器

用法:
  jkv list|ls [candidate] [--refresh]  列出工具或在线版本
  jkv install|i <candidate> [version]  安装版本；支持 21-tem 等别名
  jkv repair <candidate> <version>     原子修复已安装版本
  jkv use|u <candidate> <version>      当前终端切换（需 shell hook）
  jkv default|d <candidate> <version>  设置默认版本
  jkv current|c [candidate]            显示当前生效版本
  jkv uninstall|rm <candidate> <ver>   卸载版本
  jkv home|h <candidate> [version]     输出安装目录
  jkv env|e [init|apply|clear]          项目 .jkvrc 环境
  jkv init|in <bash|zsh|fish|powershell> 输出 shell hook 和补全
  jkv mirror|m <maven|gradle|status>    配置国内依赖镜像
  jkv clean|cl [downloads|catalog|partials] 清理本地缓存
  jkv doctor                            检查本机配置与运行环境
  jkv version|v                         显示版本
  jkv help                              显示帮助

例:
  jkv list java
  jkv install java 21-tem
  jkv install java 17-dragonwell
  jkv install maven
  jkv mirror maven --apply
`)
}

const catalogCacheTTL = 6 * time.Hour

func cmdList(ctx context.Context, s *store.Store, args []string) error {
	refresh := false
	var positional []string
	for _, arg := range args {
		if arg == "--refresh" {
			refresh = true
		} else {
			positional = append(positional, arg)
		}
	}
	if len(positional) == 0 {
		if optionsFromContext(ctx).JSON {
			return writeJSON(catalog.Candidates)
		}
		fmt.Printf("%s %s %s %s\n", padRight("CANDIDATE", 12), padRight("说明", 36), padRight("国内源", 22), "平台")
		for _, c := range catalog.Candidates {
			fmt.Printf("%s %s %s %s\n", padRight(c.Name, 12), padRight(c.Description, 36), padRight(c.Source, 22), c.Platforms)
		}
		return nil
	}
	if len(positional) != 1 {
		return errors.New("用法: jkv list [candidate] [--refresh]")
	}
	candidate := positional[0]
	if !catalog.IsCandidate(candidate) {
		return fmt.Errorf("不支持 candidate %q", candidate)
	}
	releases, err := loadReleases(ctx, s, candidate, refresh, true, false)
	if err != nil {
		return err
	}
	installed, _ := s.Installed(candidate)
	defaults, _ := s.Defaults()
	installedSet := map[string]bool{}
	for _, v := range installed {
		installedSet[v] = true
	}
	if len(releases) == 0 {
		return fmt.Errorf("当前平台 %s/%s 暂无稳定国内源", runtime.GOOS, runtime.GOARCH)
	}
	if optionsFromContext(ctx).JSON {
		type listedRelease struct {
			catalog.Release
			Installed bool `json:"installed"`
			Default   bool `json:"default"`
			Current   bool `json:"current"`
		}
		out := make([]listedRelease, 0, len(releases))
		for _, release := range releases {
			out = append(out, listedRelease{
				Release:   release,
				Installed: installedSet[release.Version],
				Default:   defaults[candidate] == release.Version,
				Current:   os.Getenv(currentVar(candidate)) == release.Version,
			})
		}
		return writeJSON(out)
	}
	groups := releaseGroups(candidate, releases)
	for groupIndex, group := range groups {
		if groupIndex > 0 {
			fmt.Println()
		}
		fmt.Println(vendorDisplay(group.vendor))
		fmt.Println(strings.Repeat("-", 100))
		fmt.Printf("%-32s %-11s %-8s %-11s %-10s %s\n", "VERSION", "STATUS", "TIER", "INTEGRITY", "AVAILABLE", "SOURCE")
		for _, r := range group.releases {
			status := ""
			if installedSet[r.Version] {
				status = "installed"
			}
			if defaults[candidate] == r.Version {
				status = "default"
			}
			if os.Getenv(currentVar(candidate)) == r.Version {
				status = "current"
			}
			available := "×"
			if r.Available {
				available = "√"
			}
			fmt.Printf("%-32s %-11s %-8s %-11s %-10s %s\n",
				r.Version, status, r.SupportTier, r.IntegrityLevel, available, hostOf(r.URL))
		}
	}
	return nil
}

type releaseGroup struct {
	vendor   string
	releases []catalog.Release
}

func releaseGroups(candidate string, releases []catalog.Release) []releaseGroup {
	byVendor := map[string][]catalog.Release{}
	for _, release := range releases {
		byVendor[release.Vendor] = append(byVendor[release.Vendor], release)
	}
	var order []string
	if candidate == "java" {
		order = []string{"temurin", "dragonwell", "bisheng"}
		known := map[string]bool{"temurin": true, "dragonwell": true, "bisheng": true}
		for _, vendor := range sortedKeys(byVendor) {
			if !known[vendor] {
				order = append(order, vendor)
			}
		}
	} else {
		order = sortedKeys(byVendor)
	}
	var groups []releaseGroup
	for _, vendor := range order {
		if len(byVendor[vendor]) > 0 {
			groups = append(groups, releaseGroup{vendor: vendor, releases: byVendor[vendor]})
		}
	}
	return groups
}

func vendorDisplay(vendor string) string {
	if display := map[string]string{"temurin": "Temurin", "dragonwell": "Alibaba Dragonwell", "bisheng": "Huawei BiSheng"}[vendor]; display != "" {
		return display
	}
	return vendor
}

func loadReleases(ctx context.Context, s *store.Store, candidate string, refresh, check, quiet bool) ([]catalog.Release, error) {
	platform := catalog.CurrentPlatform()
	now := time.Now()
	cached, cacheErr := s.LoadCatalog(platform, candidate)
	hasCache := cacheErr == nil && len(cached.Releases) > 0
	options := optionsFromContext(ctx)
	if options.Verbose && !options.Quiet {
		if hasCache {
			fmt.Fprintf(os.Stderr, "catalog 缓存: %d 个版本，年龄 %s\n", len(cached.Releases), now.Sub(cached.FetchedAt).Round(time.Second))
		} else {
			fmt.Fprintf(os.Stderr, "catalog 缓存不可用: %v\n", cacheErr)
		}
	}
	client := catalog.NewClient()
	refreshFailed := false
	if refresh || !hasCache || now.Sub(cached.FetchedAt) >= catalogCacheTTL {
		if !quiet {
			fmt.Fprintln(os.Stderr, "读取国内镜像目录...")
		}
		releases, err := client.List(ctx, candidate, platform)
		if err != nil {
			if len(releases) > 0 && !hasCache {
				cached = store.CatalogCache{FetchedAt: now, Releases: releases}
				hasCache = true
				refreshFailed = true
				if !quiet {
					fmt.Fprintf(os.Stderr, "部分 provider 刷新失败，使用可用结果: %v\n", err)
				}
			} else if !hasCache {
				return nil, err
			} else {
				refreshFailed = true
				if !quiet {
					fmt.Fprintf(os.Stderr, "刷新失败，使用本地缓存: %v\n", err)
				}
			}
		} else {
			cached = store.CatalogCache{FetchedAt: now, Releases: releases}
			hasCache = true
		}
	}
	if !hasCache {
		return nil, errors.New("无可用版本缓存")
	}
	needsCheck := check && (refresh || cached.CheckedAt.IsZero() || now.Sub(cached.CheckedAt) >= catalogCacheTTL)
	if needsCheck && (!refreshFailed || releasesNeedCheck(cached.Releases)) {
		if !quiet {
			fmt.Fprintln(os.Stderr, "检查下载地址...")
		}
		cached.Releases = client.CheckAvailability(ctx, cached.Releases)
		cached.CheckedAt = now
	}
	if !refreshFailed || needsCheck {
		if err := s.SaveCatalog(platform, candidate, cached); err != nil && !quiet {
			fmt.Fprintf(os.Stderr, "写入版本缓存失败: %v\n", err)
		}
	}
	return cached.Releases, nil
}

func releasesNeedCheck(releases []catalog.Release) bool {
	for _, release := range releases {
		if !release.AvailabilityKnown {
			return true
		}
	}
	return false
}

func hostOf(raw string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		return s[:i]
	}
	return s
}

func cmdInstall(ctx context.Context, s *store.Store, args []string) error {
	setDefault := false
	requireChecksum := envEnabled("JKV_REQUIRE_CHECKSUM")
	var pos []string
	for _, arg := range args {
		if arg == "--default" {
			setDefault = true
			continue
		}
		if arg == "--require-checksum" {
			requireChecksum = true
			continue
		}
		if strings.HasPrefix(arg, "--") {
			return fmt.Errorf("install 不支持选项 %q", arg)
		}
		pos = append(pos, arg)
	}
	if len(pos) < 1 || len(pos) > 2 {
		return errors.New("用法: jkv install <candidate> [version] [--default]")
	}
	candidate := pos[0]
	if !catalog.IsCandidate(candidate) {
		return fmt.Errorf("不支持 candidate %q", candidate)
	}
	want := ""
	if len(pos) == 2 {
		want = pos[1]
	}
	var r catalog.Release
	var err error
	foundCached := false
	if want != "" && want != "latest" {
		r, foundCached = s.CachedRelease(candidate, want)
	}
	if !foundCached {
		fmt.Fprintln(os.Stderr, "解析国内镜像版本...")
		releases, loadErr := loadReleases(ctx, s, candidate, false, true, false)
		if loadErr != nil {
			return loadErr
		}
		r, err = selectRelease(releases, want)
		if err != nil {
			return err
		}
	}
	options := optionsFromContext(ctx)
	if r.ChecksumURL == "" {
		fmt.Fprintln(os.Stderr, "警告: 此镜像未提供同源 SHA-256；下载仅由 HTTPS 保护。")
	}
	if !options.Quiet {
		fmt.Fprintf(os.Stderr, "安装 %s %s，来源 %s\n", candidate, r.Version, hostOf(r.URL))
	}
	var progress io.Writer = os.Stderr
	if options.Quiet || !isTerminal(os.Stderr) {
		progress = nil
	}
	if err := s.InstallWithOptions(ctx, r, progress, store.InstallOptions{RequireChecksum: requireChecksum}); err != nil {
		return err
	}
	defaults, _ := s.Defaults()
	if setDefault || defaults[candidate] == "" {
		if err := s.SetDefault(candidate, r.Version); err != nil {
			return err
		}
		fmt.Printf("已安装并设为默认: %s %s\n", candidate, r.Version)
	} else {
		fmt.Printf("已安装: %s %s\n", candidate, r.Version)
	}
	return nil
}

func envEnabled(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func cmdRepair(ctx context.Context, s *store.Store, args []string) error {
	if len(args) != 2 {
		return errors.New("用法: jkv repair <candidate> <version>")
	}
	version, err := installedMatch(s, args[0], args[1])
	if err != nil {
		return err
	}
	release, err := s.InstalledRelease(args[0], version)
	if err != nil {
		return err
	}
	var progress io.Writer = os.Stderr
	if optionsFromContext(ctx).Quiet || !isTerminal(os.Stderr) {
		progress = nil
	}
	if err := s.InstallWithOptions(ctx, release, progress, store.InstallOptions{
		Repair:          true,
		RequireChecksum: envEnabled("JKV_REQUIRE_CHECKSUM"),
	}); err != nil {
		return err
	}
	fmt.Printf("已修复: %s %s\n", args[0], version)
	return nil
}

func selectRelease(releases []catalog.Release, want string) (catalog.Release, error) {
	available := make([]catalog.Release, 0, len(releases))
	for _, release := range releases {
		if !release.AvailabilityKnown || release.Available {
			available = append(available, release)
		}
	}
	releases = available
	if len(releases) == 0 {
		return catalog.Release{}, errors.New("当前平台无可用稳定版本")
	}
	if want == "" || want == "latest" {
		for _, r := range releases {
			if r.Candidate != "java" || r.Vendor == "temurin" {
				return r, nil
			}
		}
		return releases[0], nil
	}
	for _, r := range releases {
		if r.Version == want {
			return r, nil
		}
	}
	vendor := ""
	prefix := want
	aliases := map[string]string{"tem": "temurin", "temurin": "temurin", "dragonwell": "dragonwell", "albba": "dragonwell", "bisheng": "bisheng"}
	for suffix, v := range aliases {
		if strings.HasSuffix(want, "-"+suffix) {
			vendor = v
			prefix = strings.TrimSuffix(want, "-"+suffix)
			break
		}
	}
	for _, r := range releases {
		base := strings.Split(r.Version, "-")[0]
		if (base == prefix || strings.HasPrefix(base, prefix+".")) && (vendor == "" || r.Vendor == vendor) {
			return r, nil
		}
	}
	return catalog.Release{}, fmt.Errorf("未找到版本 %q；先运行 jkv list %s", want, releases[0].Candidate)
}

func shellFlag(args []string) (string, []string, error) {
	shell := ""
	var out []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--shell" {
			if i+1 >= len(args) {
				return "", nil, errors.New("--shell 缺少值")
			}
			shell = args[i+1]
			i++
		} else {
			out = append(out, args[i])
		}
	}
	return shell, out, nil
}

func cmdUse(s *store.Store, args []string) error {
	shell, args, err := shellFlag(args)
	if err != nil {
		return err
	}
	if len(args) != 2 {
		return errors.New("用法: jkv use <candidate> <version>")
	}
	v, err := installedMatch(s, args[0], args[1])
	if err != nil {
		return err
	}
	if shell == "" {
		return fmt.Errorf("二进制无法修改父进程环境；先加载: %s", shellInitHint(guessedShell()))
	}
	fmt.Fprintf(os.Stderr, "已切换当前终端: %s %s\n", args[0], v)
	return printEnv(s, map[string]string{args[0]: v}, shell, false)
}

func cmdDefault(s *store.Store, args []string) error {
	shell, args, err := shellFlag(args)
	if err != nil {
		return err
	}
	if len(args) != 2 {
		return errors.New("用法: jkv default <candidate> <version>")
	}
	v, err := installedMatch(s, args[0], args[1])
	if err != nil {
		return err
	}
	if err := s.SetDefault(args[0], v); err != nil {
		return err
	}
	if shell != "" {
		fmt.Fprintf(os.Stderr, "已设置默认版本: %s %s\n", args[0], v)
		return printEnv(s, map[string]string{args[0]: v}, shell, false)
	}
	fmt.Printf("已设置默认版本: %s %s\n", args[0], v)
	return nil
}

func installedMatch(s *store.Store, candidate, want string) (string, error) {
	if !catalog.IsCandidate(candidate) {
		return "", fmt.Errorf("不支持 candidate %q", candidate)
	}
	versions, err := s.Installed(candidate)
	if err != nil {
		return "", err
	}
	for _, v := range versions {
		if v == want {
			return v, nil
		}
	}
	var matches []string
	for _, v := range versions {
		base := strings.Split(v, "-")[0]
		if base == want || strings.HasPrefix(base, want+".") || strings.HasSuffix(v, "-"+strings.TrimPrefix(want, strings.Split(want, "-")[0]+"-")) && strings.HasPrefix(base, strings.Split(want, "-")[0]) {
			matches = append(matches, v)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	return "", fmt.Errorf("未安装 %s %s", candidate, want)
}

func cmdCurrent(ctx context.Context, s *store.Store, args []string) error {
	d, err := s.Defaults()
	if err != nil {
		return err
	}
	if len(args) == 1 {
		v := os.Getenv(currentVar(args[0]))
		if v == "" {
			v = d[args[0]]
		}
		if v == "" {
			return fmt.Errorf("%s 未设置默认版本", args[0])
		}
		if optionsFromContext(ctx).JSON {
			return writeJSON(map[string]string{"candidate": args[0], "version": v})
		}
		fmt.Println(v)
		return nil
	}
	for _, c := range catalog.Candidates {
		if v := os.Getenv(currentVar(c.Name)); v != "" {
			d[c.Name] = v
		}
	}
	keys := sortedKeys(d)
	if len(keys) == 0 {
		if optionsFromContext(ctx).JSON {
			return writeJSON(map[string]string{})
		}
		fmt.Println("尚未设置默认版本")
		return nil
	}
	if optionsFromContext(ctx).JSON {
		return writeJSON(d)
	}
	for _, k := range keys {
		fmt.Printf("%-12s %s\n", k, d[k])
	}
	return nil
}

func writeJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func cmdUninstall(s *store.Store, args []string) error {
	if len(args) != 2 {
		return errors.New("用法: jkv uninstall <candidate> <version>")
	}
	v, err := installedMatch(s, args[0], args[1])
	if err != nil {
		return err
	}
	if err := s.Remove(args[0], v); err != nil {
		return err
	}
	fmt.Printf("已卸载: %s %s\n", args[0], v)
	return nil
}

func cmdHome(ctx context.Context, s *store.Store, args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return errors.New("用法: jkv home <candidate> [version]")
	}
	v := ""
	if len(args) == 2 {
		v = args[1]
	} else {
		d, _ := s.Defaults()
		v = d[args[0]]
	}
	if v == "" {
		return fmt.Errorf("%s 未设置默认版本", args[0])
	}
	v, err := installedMatch(s, args[0], v)
	if err != nil {
		return err
	}
	h, err := s.Home(args[0], v)
	if err == nil {
		if optionsFromContext(ctx).JSON {
			return writeJSON(map[string]string{"candidate": args[0], "version": v, "home": h})
		}
		fmt.Println(h)
	}
	return err
}

type doctorResult struct {
	Root       string               `json:"root"`
	OS         string               `json:"os"`
	Arch       string               `json:"arch"`
	Writable   bool                 `json:"writable"`
	HTTPSProxy bool                 `json:"https_proxy"`
	HTTPProxy  bool                 `json:"http_proxy"`
	NoProxy    bool                 `json:"no_proxy"`
	Catalog    map[string]string    `json:"catalog"`
	Installed  map[string]int       `json:"installed"`
	Mirrors    []doctorMirrorResult `json:"mirrors"`
}

type doctorMirrorResult struct {
	Name      string `json:"name"`
	Host      string `json:"host"`
	Reachable bool   `json:"reachable"`
	Status    int    `json:"status,omitempty"`
}

var doctorHTTPClient = &http.Client{Timeout: 4 * time.Second}

func cmdDoctor(ctx context.Context, s *store.Store, args []string) error {
	if len(args) != 0 {
		return errors.New("用法: jkv doctor")
	}
	result := doctorResult{
		Root:       "<JKV_DIR>",
		OS:         runtime.GOOS,
		Arch:       runtime.GOARCH,
		HTTPSProxy: os.Getenv("HTTPS_PROXY") != "" || os.Getenv("https_proxy") != "",
		HTTPProxy:  os.Getenv("HTTP_PROXY") != "" || os.Getenv("http_proxy") != "",
		NoProxy:    os.Getenv("NO_PROXY") != "" || os.Getenv("no_proxy") != "",
		Catalog:    map[string]string{},
		Installed:  map[string]int{},
	}
	if err := os.MkdirAll(s.Root, 0o755); err == nil {
		if f, createErr := os.CreateTemp(s.Root, ".jkv-doctor-"); createErr == nil {
			result.Writable = true
			name := f.Name()
			_ = f.Close()
			_ = os.Remove(name)
		}
	}
	platform := catalog.CurrentPlatform()
	for _, candidate := range []string{"java", "maven", "gradle"} {
		if cached, err := s.LoadCatalog(platform, candidate); err == nil && len(cached.Releases) > 0 {
			result.Catalog[candidate] = fmt.Sprintf("cached:%d", len(cached.Releases))
		} else {
			result.Catalog[candidate] = "missing"
		}
		if versions, err := s.Installed(candidate); err == nil {
			result.Installed[candidate] = len(versions)
		}
	}
	endpoints := []struct {
		name string
		url  string
	}{
		{"temurin", "https://mirrors.tuna.tsinghua.edu.cn/Adoptium/"},
		{"maven", "https://mirrors.aliyun.com/apache/maven/maven-3/"},
		{"gradle", "https://mirrors.cloud.tencent.com/gradle/"},
	}
	type mirrorCheck struct {
		index  int
		result doctorMirrorResult
	}
	checks := make(chan mirrorCheck, len(endpoints))
	for index, endpoint := range endpoints {
		go func() {
			checkCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(checkCtx, http.MethodHead, endpoint.url, nil)
			result := doctorMirrorResult{Name: endpoint.name, Host: hostOf(endpoint.url)}
			if err == nil {
				resp, requestErr := doctorHTTPClient.Do(req)
				if requestErr == nil {
					result.Status = resp.StatusCode
					result.Reachable = resp.StatusCode >= 200 && resp.StatusCode < 400
					_ = resp.Body.Close()
				}
			}
			checks <- mirrorCheck{index: index, result: result}
		}()
	}
	result.Mirrors = make([]doctorMirrorResult, len(endpoints))
	for range endpoints {
		check := <-checks
		result.Mirrors[check.index] = check.result
	}
	if optionsFromContext(ctx).JSON {
		if err := writeJSON(result); err != nil {
			return err
		}
		if !result.Writable {
			return errors.New("JKV_DIR 不可写: <JKV_DIR>")
		}
		return nil
	}
	fmt.Printf("jkv doctor\n目录: %s\n平台: %s/%s\n可写: %t\n", result.Root, result.OS, result.Arch, result.Writable)
	for _, mirror := range result.Mirrors {
		fmt.Printf("镜像 %-8s %-38s 可达: %t", mirror.Name, mirror.Host, mirror.Reachable)
		if mirror.Status != 0 {
			fmt.Printf(" (HTTP %d)", mirror.Status)
		}
		fmt.Println()
	}
	if !result.Writable {
		return errors.New("JKV_DIR 不可写: <JKV_DIR>")
	}
	return nil
}

func isTerminal(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func cmdEnv(s *store.Store, args []string) error {
	shell, args, err := shellFlag(args)
	if err != nil {
		return err
	}
	if shell == "" {
		shell = guessedShell()
	}
	action := "defaults"
	if len(args) > 0 {
		action = args[0]
	}
	switch action {
	case "defaults", "clear":
		d, err := s.Defaults()
		if err != nil {
			return err
		}
		return printEnv(s, d, shell, true)
	case "init":
		if _, err := os.Stat(".jkvrc"); err == nil {
			return errors.New(".jkvrc 已存在")
		}
		d, err := s.Defaults()
		if err != nil {
			return err
		}
		var b strings.Builder
		b.WriteString("# jkv project versions\n")
		for _, k := range sortedKeys(d) {
			fmt.Fprintf(&b, "%s=%s\n", k, d[k])
		}
		if err := os.WriteFile(".jkvrc", []byte(b.String()), 0o644); err != nil {
			return err
		}
		fmt.Println("已创建 .jkvrc")
		return nil
	case "apply":
		d, err := readEnvFile(".jkvrc")
		if err != nil {
			return err
		}
		return printEnv(s, d, shell, true)
	default:
		return fmt.Errorf("未知 env 动作 %q", action)
	}
}

func readEnvFile(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for n, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 || !catalog.IsCandidate(strings.TrimSpace(parts[0])) || !validVersionValue(strings.TrimSpace(parts[1])) {
			return nil, fmt.Errorf("%s:%d 格式错误", path, n+1)
		}
		m[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	return m, nil
}

var versionValueRE = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+_-]*$`)

func validVersionValue(value string) bool {
	return versionValueRE.MatchString(value) && !strings.ContainsAny(value, `/\`)
}

var homeVars = map[string]string{
	"java": "JAVA_HOME", "maven": "MAVEN_HOME", "gradle": "GRADLE_HOME", "ant": "ANT_HOME",
	"groovy": "GROOVY_HOME", "jmeter": "JMETER_HOME", "tomcat": "CATALINA_HOME", "springboot": "SPRING_HOME",
}

func printEnv(s *store.Store, versions map[string]string, shell string, includeBin bool) error {
	for _, candidate := range sortedKeys(versions) {
		version := versions[candidate]
		home, err := s.Home(candidate, version)
		if err != nil {
			return err
		}
		name := homeVars[candidate]
		switch shell {
		case "bash", "zsh", "sh":
			fmt.Printf("export %s=%s\n", name, shQuote(home))
			fmt.Printf("export %s=%s\n", currentVar(candidate), shQuote(version))
			fmt.Printf("export PATH=%s:$PATH\n", shQuote(filepath.Join(home, "bin")))
		case "powershell", "pwsh":
			fmt.Printf("$env:%s = %s\n", name, psQuote(home))
			fmt.Printf("$env:%s = %s\n", currentVar(candidate), psQuote(version))
			fmt.Printf("$env:Path = %s + [IO.Path]::PathSeparator + $env:Path\n", psQuote(filepath.Join(home, "bin")))
		case "fish":
			fmt.Printf("set -gx %s %s\n", name, fishQuote(home))
			fmt.Printf("set -gx %s %s\n", currentVar(candidate), fishQuote(version))
			fmt.Printf("set -gx PATH %s $PATH\n", fishQuote(filepath.Join(home, "bin")))
		default:
			return fmt.Errorf("不支持 shell %q", shell)
		}
	}
	if includeBin {
		bin := filepath.Join(s.Root, "bin")
		if shell == "powershell" || shell == "pwsh" {
			fmt.Printf("$env:JKV_DIR = %s\n$env:Path = %s + [IO.Path]::PathSeparator + $env:Path\n", psQuote(s.Root), psQuote(bin))
		} else if shell == "fish" {
			fmt.Printf("set -gx JKV_DIR %s\nset -gx PATH %s $PATH\n", fishQuote(s.Root), fishQuote(bin))
		} else {
			fmt.Printf("export JKV_DIR=%s\nexport PATH=%s:$PATH\n", shQuote(s.Root), shQuote(bin))
		}
	}
	return nil
}

func currentVar(candidate string) string {
	return "JKV_CURRENT_" + strings.ToUpper(strings.ReplaceAll(candidate, "-", "_"))
}

func cmdInit(args []string) error {
	if len(args) != 1 {
		return errors.New("用法: jkv init <bash|zsh|fish|powershell>")
	}
	switch args[0] {
	case "bash":
		fmt.Printf(`jkv() {
  case "$1" in
    use|u|default|d) eval "$(command jkv "$@" --shell bash)" ;;
    env|e)
      if [ "${2:-}" = "init" ]; then command jkv "$@"; else eval "$(command jkv "$@" --shell %s)"; fi ;;
    *) command jkv "$@" ;;
  esac
}
_jkv_complete() {
  local -a words
  words=( "${COMP_WORDS[@]:1:COMP_CWORD}" )
  COMPREPLY=()
  while IFS= read -r item; do COMPREPLY+=( "$item" ); done < <(command jkv __complete "${words[@]}")
}
complete -F _jkv_complete jkv
eval "$(command jkv env --shell bash)"
`, args[0])
	case "zsh":
		fmt.Print(`jkv() {
  case "$1" in
    use|u|default|d) eval "$(command jkv "$@" --shell zsh)" ;;
    env|e)
      if [ "${2:-}" = "init" ]; then command jkv "$@"; else eval "$(command jkv "$@" --shell zsh)"; fi ;;
    *) command jkv "$@" ;;
  esac
}
_jkv_complete() {
  local -a replies
  replies=("${(@f)$(command jkv __complete "${(@)words[2,CURRENT]}")}")
  compadd -- "${replies[@]}"
}
if (( ! $+functions[compdef] )); then
  autoload -Uz compinit && compinit
fi
compdef _jkv_complete jkv
eval "$(command jkv env --shell zsh)"
`)
	case "fish":
		fmt.Print(`function jkv
  switch $argv[1]
    case use u default d
      command jkv $argv --shell fish | source
    case env e
      if test "$argv[2]" = init
        command jkv $argv
      else
        command jkv $argv --shell fish | source
      end
    case '*'
      command jkv $argv
  end
end
function __jkv_complete
  set -l words (commandline -opc)
  set -e words[1]
  command jkv __complete $words (commandline -ct)
end
complete --command jkv --no-files --arguments '(__jkv_complete)'
command jkv env --shell fish | source
`)
	case "powershell", "pwsh":
		fmt.Printf(`function jkv {
  if ($args[0] -in @('use','u','default','d') -or ($args[0] -in @('env','e') -and $args[1] -ne 'init')) {
    $code = & (Join-Path $env:JKV_DIR 'bin/jkv.exe') @args --shell powershell
    if ($LASTEXITCODE -eq 0) { Invoke-Expression ($code -join "%cn") }
  } else { & (Join-Path $env:JKV_DIR 'bin/jkv.exe') @args }
}
Register-ArgumentCompleter -CommandName jkv -ScriptBlock {
  param($commandName, $wordToComplete, $cursorPosition, $commandAst, $fakeBoundParameters)
  $tokens = @($commandAst.CommandElements | Select-Object -Skip 1 | ForEach-Object { $_.Extent.Text })
  if ($commandAst.Extent.Text.EndsWith(' ')) { $tokens += '' }
  & (Join-Path $env:JKV_DIR 'bin/jkv.exe') __complete @tokens | ForEach-Object {
    [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_)
  }
}
Invoke-Expression ((& (Join-Path $env:JKV_DIR 'bin/jkv.exe') env --shell powershell) -join "%cn")
`, '`', '`')
	default:
		return fmt.Errorf("不支持 shell %q", args[0])
	}
	return nil
}

func cmdMirror(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("用法: jkv mirror <maven|gradle|status> [--apply]")
	}
	apply := len(args) > 1 && args[1] == "--apply"
	home, _ := os.UserHomeDir()
	switch args[0] {
	case "maven":
		path := filepath.Join(home, ".m2", "settings-jkv.xml")
		content := `<?xml version="1.0" encoding="UTF-8"?>
<settings xmlns="http://maven.apache.org/SETTINGS/1.0.0">
  <mirrors><mirror><id>jkv-aliyun</id><name>Aliyun public mirror</name><url>https://maven.aliyun.com/repository/public</url><mirrorOf>*</mirrorOf></mirror></mirrors>
</settings>
`
		if err := writeConfig(path, content); err != nil {
			return err
		}
		if apply {
			active := filepath.Join(home, ".m2", "settings.xml")
			if _, err := os.Stat(active); err == nil {
				return fmt.Errorf("%s 已存在，拒绝覆盖；使用 mvn -s %s", active, path)
			}
			if err := copyFile(path, active); err != nil {
				return err
			}
			fmt.Println("已启用 Maven 阿里云公共仓库:", active)
		} else {
			fmt.Println("已生成:", path, "\n启用命令: mvn -s", path)
		}
	case "gradle":
		path := filepath.Join(home, ".gradle", "init.d", "jkv-mirrors.gradle")
		content := `// Generated by jkv. Delete this file to disable.
settingsEvaluated { settings ->
  settings.pluginManagement.repositories {
    maven { url 'https://maven.aliyun.com/repository/gradle-plugin/' }
    maven { url 'https://maven.aliyun.com/repository/public/' }
    gradlePluginPortal()
  }
}
allprojects {
  repositories {
    maven { url 'https://maven.aliyun.com/repository/public/' }
    maven { url 'https://maven.aliyun.com/repository/google/' }
    mavenCentral()
  }
}
`
		if !apply {
			fmt.Println(content)
			fmt.Println("启用: jkv mirror gradle --apply")
			return nil
		}
		if err := writeConfig(path, content); err != nil {
			return err
		}
		fmt.Println("已启用 Gradle 阿里云依赖镜像:", path)
	case "status":
		status := []map[string]any{
			{"tool": "maven", "path": filepath.Join(home, ".m2", "settings-jkv.xml")},
			{"tool": "gradle", "path": filepath.Join(home, ".gradle", "init.d", "jkv-mirrors.gradle")},
		}
		for _, item := range status {
			_, err := os.Stat(item["path"].(string))
			item["configured"] = err == nil
		}
		if optionsFromContext(ctx).JSON {
			return writeJSON(status)
		}
		for _, item := range status {
			if item["configured"].(bool) {
				fmt.Println("存在", item["path"])
			} else {
				fmt.Println("未配置", item["path"])
			}
		}
	default:
		return fmt.Errorf("不支持镜像配置 %q", args[0])
	}
	return nil
}

func cmdClean(ctx context.Context, s *store.Store, args []string) error {
	dryRun := false
	var olderThan time.Duration
	var positional []string
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--dry-run":
			dryRun = true
		case arg == "--older-than":
			if index+1 >= len(args) {
				return errors.New("--older-than 缺少时长")
			}
			index++
			parsed, err := time.ParseDuration(args[index])
			if err != nil || parsed <= 0 {
				return fmt.Errorf("无效缓存年龄 %q", args[index])
			}
			olderThan = parsed
		case strings.HasPrefix(arg, "--older-than="):
			parsed, err := time.ParseDuration(strings.TrimPrefix(arg, "--older-than="))
			if err != nil || parsed <= 0 {
				return fmt.Errorf("无效缓存年龄 %q", strings.TrimPrefix(arg, "--older-than="))
			}
			olderThan = parsed
		default:
			positional = append(positional, arg)
		}
	}
	args = positional
	if len(args) > 3 {
		return errors.New("用法: jkv clean [downloads|catalog|partials] [candidate] [version] [--dry-run] [--older-than 24h]")
	}
	kind, candidate, version := "", "", ""
	if len(args) > 0 {
		kind = args[0]
	}
	if kind != "" && kind != "downloads" && kind != "catalog" && kind != "partials" {
		return fmt.Errorf("不支持缓存类型 %q", kind)
	}
	if len(args) > 1 {
		candidate = args[1]
		if !catalog.IsCandidate(candidate) {
			return fmt.Errorf("不支持 candidate %q", candidate)
		}
	}
	if len(args) > 2 {
		if kind != "downloads" && kind != "partials" {
			return errors.New("此缓存类型不支持 version 参数")
		}
		version = args[2]
	}
	if kind == "partials" && olderThan <= 0 {
		return errors.New("清理中断下载必须指定 --older-than")
	}
	var result store.CleanResult
	var err error
	if kind == "partials" && dryRun {
		result, err = s.InspectPartialsOlderThan(candidate, version, olderThan)
	} else if kind == "partials" {
		result, err = s.CleanPartialsOlderThan(candidate, version, olderThan)
	} else if dryRun && olderThan > 0 {
		result, err = s.InspectCacheOlderThan(kind, candidate, version, olderThan)
	} else if dryRun {
		result, err = s.InspectCache(kind, candidate, version)
	} else if olderThan > 0 {
		result, err = s.CleanCacheOlderThan(kind, candidate, version, olderThan)
	} else {
		result, err = s.CleanCache(kind, candidate, version)
	}
	if err != nil {
		return err
	}
	if optionsFromContext(ctx).JSON {
		return writeJSON(map[string]any{"dry_run": dryRun, "older_than": olderThan.String(), "files": result.Files, "bytes": result.Bytes})
	}
	if dryRun {
		fmt.Printf("将清理 %d 个文件，释放 %s\n", result.Files, formatBytes(result.Bytes))
		return nil
	}
	fmt.Printf("已清理 %d 个文件，释放 %s\n", result.Files, formatBytes(result.Bytes))
	return nil
}

func formatBytes(n int64) string {
	const unit = int64(1024)
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	for _, suffix := range units {
		value /= 1024
		if value < 1024 || suffix == units[len(units)-1] {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%d B", n)
}

func padRight(value string, width int) string {
	display := 0
	for _, r := range value {
		switch {
		case unicode.Is(unicode.Mn, r):
		case r >= 0x1100 && (r <= 0x115f || r == 0x2329 || r == 0x232a ||
			r >= 0x2e80 && r <= 0xa4cf && r != 0x303f ||
			r >= 0xac00 && r <= 0xd7a3 ||
			r >= 0xf900 && r <= 0xfaff ||
			r >= 0xfe10 && r <= 0xfe19 ||
			r >= 0xfe30 && r <= 0xfe6f ||
			r >= 0xff00 && r <= 0xff60 ||
			r >= 0xffe0 && r <= 0xffe6):
			display += 2
		default:
			display++
		}
	}
	if display >= width {
		return value
	}
	return value + strings.Repeat(" ", width-display)
}

func writeConfig(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if b, err := os.ReadFile(path); err == nil && string(b) != content {
		return fmt.Errorf("%s 已存在且内容不同，拒绝覆盖", path)
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func guessedShell() string {
	if runtime.GOOS == "windows" {
		return "powershell"
	}
	s := filepath.Base(os.Getenv("SHELL"))
	if s == "zsh" || s == "fish" {
		return s
	}
	return "bash"
}

func shellInitHint(shell string) string {
	if shell == "powershell" || shell == "pwsh" {
		return `Invoke-Expression ((jkv init powershell) -join [Environment]::NewLine)`
	}
	if shell == "fish" {
		return "jkv init fish | source"
	}
	return fmt.Sprintf(`eval "$(jkv init %s)"`, shell)
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func fishQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return "'" + strings.ReplaceAll(s, "'", `\'`) + "'"
}
