package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

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
	case "use":
		return cmdUse(s, args[1:])
	case "default":
		return cmdDefault(s, args[1:])
	case "current":
		return cmdCurrent(s, args[1:])
	case "uninstall", "rm":
		return cmdUninstall(s, args[1:])
	case "home":
		return cmdHome(s, args[1:])
	case "env":
		return cmdEnv(s, args[1:])
	case "init":
		return cmdInit(args[1:])
	case "mirror":
		return cmdMirror(args[1:])
	case "version", "--version", "-v":
		fmt.Println("jkv", version)
		return nil
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		return fmt.Errorf("未知命令 %q；运行 jkv help", args[0])
	}
}

func usage() {
	fmt.Print(`jkv - 中国网络友好、跨平台 JVM 工具版本管理器

用法:
  jkv list [candidate]              列出候选工具或在线版本
  jkv install <candidate> [version] 安装版本；支持 21-tem 等别名
  jkv use <candidate> <version>     当前终端切换（需加载 shell hook）
  jkv default <candidate> <version> 设置默认版本
  jkv current                       显示当前生效版本
  jkv uninstall <candidate> <ver>   卸载版本
  jkv home <candidate> [version]    输出安装目录
  jkv env [init|apply|clear]        项目 .jkvrc 环境
  jkv init <bash|zsh|powershell>    输出 shell hook
  jkv mirror <maven|gradle|status>  配置国内依赖镜像

例:
  jkv list java
  jkv install java 21-tem
  jkv install java 17-dragonwell
  jkv install maven
  jkv mirror maven --apply
`)
}

func cmdList(ctx context.Context, s *store.Store, args []string) error {
	if len(args) == 0 {
		fmt.Printf("%-12s %-36s %-22s %s\n", "CANDIDATE", "说明", "国内源", "平台")
		for _, c := range catalog.Candidates {
			fmt.Printf("%-12s %-36s %-22s %s\n", c.Name, c.Description, c.Source, c.Platforms)
		}
		return nil
	}
	candidate := args[0]
	if !catalog.IsCandidate(candidate) {
		return fmt.Errorf("不支持 candidate %q", candidate)
	}
	fmt.Fprintf(os.Stderr, "读取国内镜像目录...\n")
	releases, err := catalog.NewClient().List(ctx, candidate, catalog.CurrentPlatform())
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
	fmt.Printf("%-32s %-14s %-10s %s\n", "VERSION", "VENDOR", "STATUS", "SOURCE")
	for _, r := range releases {
		status := ""
		if installedSet[r.Version] {
			status = "installed"
		}
		if defaults[candidate] == r.Version {
			status = "default"
		}
		fmt.Printf("%-32s %-14s %-10s %s\n", r.Version, r.Vendor, status, hostOf(r.URL))
	}
	return nil
}

func hostOf(raw string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		return s[:i]
	}
	return s
}

func cmdInstall(ctx context.Context, s *store.Store, args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	setDefault := fs.Bool("default", false, "设为默认")
	if err := fs.Parse(args); err != nil {
		return err
	}
	pos := fs.Args()
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
	fmt.Fprintln(os.Stderr, "解析国内镜像版本...")
	releases, err := catalog.NewClient().List(ctx, candidate, catalog.CurrentPlatform())
	if err != nil {
		return err
	}
	r, err := selectRelease(releases, want)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "安装 %s %s，来源 %s\n", candidate, r.Version, hostOf(r.URL))
	if err := s.Install(ctx, r, os.Stderr); err != nil {
		return err
	}
	defaults, _ := s.Defaults()
	if *setDefault || defaults[candidate] == "" {
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
		return printEnv(s, map[string]string{args[0]: v}, shell, false)
	}
	fmt.Printf("默认版本: %s %s\n", args[0], v)
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
	case "bash", "zsh":
		fmt.Printf(`jkv() {
  case "$1" in
    use|default) eval "$(command jkv "$@" --shell %s)" ;;
    env)
      if [ "${2:-}" = "init" ]; then command jkv "$@"; else eval "$(command jkv "$@" --shell %s)"; fi ;;
    *) command jkv "$@" ;;
  esac
}
eval "$(command jkv env --shell %s)"
`, args[0], args[0], args[0])
	case "powershell", "pwsh":
		fmt.Printf(`function jkv {
  if ($args[0] -in @('use','default') -or ($args[0] -eq 'env' -and $args[1] -ne 'init')) {
    $code = & (Join-Path $env:JKV_DIR 'bin/jkv.exe') @args --shell powershell
    if ($LASTEXITCODE -eq 0) { Invoke-Expression ($code -join "%cn") }
  } else { & (Join-Path $env:JKV_DIR 'bin/jkv.exe') @args }
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
