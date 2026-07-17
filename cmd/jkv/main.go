package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"jkv/internal/catalog"
	"jkv/internal/store"
)

var version = "dev"

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
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
	case "use", "u":
		return cmdUse(s, args[1:])
	case "default", "d":
		return cmdDefault(s, args[1:])
	case "current", "c":
		return cmdCurrent(s, args[1:])
	case "uninstall", "rm":
		return cmdUninstall(s, args[1:])
	case "home", "h":
		return cmdHome(s, args[1:])
	case "env", "e":
		return cmdEnv(s, args[1:])
	case "init", "in":
		return cmdInit(args[1:])
	case "mirror", "m":
		return cmdMirror(args[1:])
	case "clean", "cl":
		return cmdClean(s, args[1:])
	case "version", "v", "--version", "-v":
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

func usage() {
	fmt.Print(`jkv - 中国网络友好、跨平台 JVM 工具版本管理器

用法:
  jkv list|ls [candidate] [--refresh]  列出工具或在线版本
  jkv install|i <candidate> [version]  安装版本；支持 21-tem 等别名
  jkv use|u <candidate> <version>      当前终端切换（需 shell hook）
  jkv default|d <candidate> <version>  设置默认版本
  jkv current|c [candidate]            显示当前生效版本
  jkv uninstall|rm <candidate> <ver>   卸载版本
  jkv home|h <candidate> [version]     输出安装目录
  jkv env|e [init|apply|clear]          项目 .jkvrc 环境
  jkv init|in <bash|zsh|powershell>     输出 shell hook 和补全
  jkv mirror|m <maven|gradle|status>    配置国内依赖镜像
  jkv clean|cl [downloads|catalog]      清理本地缓存
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
		fmt.Printf("%-12s %-36s %-22s %s\n", "CANDIDATE", "说明", "国内源", "平台")
		for _, c := range catalog.Candidates {
			fmt.Printf("%-12s %-36s %-22s %s\n", c.Name, c.Description, c.Source, c.Platforms)
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
	groups := releaseGroups(candidate, releases)
	for groupIndex, group := range groups {
		if groupIndex > 0 {
			fmt.Println()
		}
		fmt.Println(vendorDisplay(group.vendor))
		fmt.Println(strings.Repeat("-", 78))
		fmt.Printf("%-32s %-11s %-10s %s\n", "VERSION", "STATUS", "AVAILABLE", "SOURCE")
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
			fmt.Printf("%-32s %-11s %-10s %s\n", r.Version, status, available, hostOf(r.URL))
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
	client := catalog.NewClient()
	refreshFailed := false
	if refresh || !hasCache || now.Sub(cached.FetchedAt) >= catalogCacheTTL {
		if !quiet {
			fmt.Fprintln(os.Stderr, "读取国内镜像目录...")
		}
		releases, err := client.List(ctx, candidate, platform)
		if err != nil {
			if !hasCache {
				return nil, err
			}
			refreshFailed = true
			if !quiet {
				fmt.Fprintf(os.Stderr, "刷新失败，使用本地缓存: %v\n", err)
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
	var pos []string
	for _, arg := range args {
		if arg == "--default" {
			setDefault = true
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
		releases, loadErr := loadReleases(ctx, s, candidate, false, false, false)
		if loadErr != nil {
			return loadErr
		}
		r, err = selectRelease(releases, want)
		if err != nil {
			return err
		}
	}
	fmt.Fprintf(os.Stderr, "安装 %s %s，来源 %s\n", candidate, r.Version, hostOf(r.URL))
	if err := s.Install(ctx, r, os.Stderr); err != nil {
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
	if r.ChecksumURL == "" {
		fmt.Fprintln(os.Stderr, "提示: 此镜像未提供同源 SHA-256；下载由 HTTPS 保护。")
	}
	return nil
}

func selectRelease(releases []catalog.Release, want string) (catalog.Release, error) {
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
		return fmt.Errorf("二进制无法修改父进程环境；先加载: eval \"$(jkv init %s)\"", guessedShell())
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

func cmdCurrent(s *store.Store, args []string) error {
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
		fmt.Println("尚未设置默认版本")
		return nil
	}
	for _, k := range keys {
		fmt.Printf("%-12s %s\n", k, d[k])
	}
	return nil
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

func cmdHome(s *store.Store, args []string) error {
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
		fmt.Println(h)
	}
	return err
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
		if len(parts) != 2 || !catalog.IsCandidate(strings.TrimSpace(parts[0])) {
			return nil, fmt.Errorf("%s:%d 格式错误", path, n+1)
		}
		m[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	return m, nil
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
		default:
			return fmt.Errorf("不支持 shell %q", shell)
		}
	}
	if includeBin {
		bin := filepath.Join(s.Root, "bin")
		if shell == "powershell" || shell == "pwsh" {
			fmt.Printf("$env:JKV_DIR = %s\n$env:Path = %s + [IO.Path]::PathSeparator + $env:Path\n", psQuote(s.Root), psQuote(bin))
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
		return errors.New("用法: jkv init <bash|zsh|powershell>")
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

func cmdMirror(args []string) error {
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
		for _, p := range []string{filepath.Join(home, ".m2", "settings-jkv.xml"), filepath.Join(home, ".gradle", "init.d", "jkv-mirrors.gradle")} {
			if _, err := os.Stat(p); err == nil {
				fmt.Println("存在", p)
			} else {
				fmt.Println("未配置", p)
			}
		}
	default:
		return fmt.Errorf("不支持镜像配置 %q", args[0])
	}
	return nil
}

func cmdClean(s *store.Store, args []string) error {
	if len(args) > 3 {
		return errors.New("用法: jkv clean [downloads [candidate [version]]|catalog [candidate]]")
	}
	kind, candidate, version := "", "", ""
	if len(args) > 0 {
		kind = args[0]
	}
	if kind != "" && kind != "downloads" && kind != "catalog" {
		return fmt.Errorf("不支持缓存类型 %q", kind)
	}
	if len(args) > 1 {
		candidate = args[1]
		if !catalog.IsCandidate(candidate) {
			return fmt.Errorf("不支持 candidate %q", candidate)
		}
	}
	if len(args) > 2 {
		if kind != "downloads" {
			return errors.New("catalog 清理不支持 version 参数")
		}
		version = args[2]
	}
	result, err := s.CleanCache(kind, candidate, version)
	if err != nil {
		return err
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
	if s == "zsh" {
		return "zsh"
	}
	return "bash"
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
