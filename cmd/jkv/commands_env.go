package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/fishandsheep/jkv/internal/catalog"
	"github.com/fishandsheep/jkv/internal/store"
)

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
	case "defaults":
		d, err := s.Defaults()
		if err != nil {
			return err
		}
		return printEnv(s, d, shell, true)
	case "clear":
		return printClearEnv(s, shell)
	case "init":
		if _, err := os.Stat(".jkvrc"); err == nil {
			return stateErrorf(".jkvrc 已存在")
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
		return usageErrorf("未知 env 动作 %q", action)
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
			return nil, usageErrorf("%s:%d 格式错误", path, n+1)
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
			return usageErrorf("不支持 shell %q", shell)
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

// printClearEnv prints the inverse of printEnv: unset the *_HOME and
// JKV_CURRENT_* variables and remove jkv-managed bin directories from PATH.
// JKV_DIR stays set because the PowerShell wrapper function depends on it.
func printClearEnv(s *store.Store, shell string) error {
	binDirs := []string{filepath.Join(s.Root, "bin")}
	for _, candidate := range sortedKeys(homeVars) {
		versions, err := s.Installed(candidate)
		if err != nil {
			continue
		}
		for _, version := range versions {
			if home, homeErr := s.Home(candidate, version); homeErr == nil {
				binDirs = append(binDirs, filepath.Join(home, "bin"))
			}
		}
	}
	var names []string
	for _, candidate := range sortedKeys(homeVars) {
		names = append(names, homeVars[candidate], currentVar(candidate))
	}
	switch shell {
	case "bash", "zsh", "sh":
		fmt.Printf("unset %s\n", strings.Join(names, " "))
		quoted := make([]string, 0, len(binDirs))
		for _, dir := range binDirs {
			quoted = append(quoted, shQuote(dir))
		}
		fmt.Printf("for _d in %s; do PATH=\":$PATH:\"; PATH=\"${PATH//:\"$_d\":/:}\"; PATH=\"${PATH#:}\"; PATH=\"${PATH%%:}\"; done; unset _d\n", strings.Join(quoted, " "))
	case "powershell", "pwsh":
		for _, name := range names {
			fmt.Printf("Remove-Item Env:%s -ErrorAction SilentlyContinue\n", name)
		}
		for _, dir := range binDirs {
			fmt.Printf("$env:Path = ($env:Path -split [IO.Path]::PathSeparator | Where-Object { $_ -ne %s }) -join [IO.Path]::PathSeparator\n", psQuote(dir))
		}
	case "fish":
		for _, name := range names {
			fmt.Printf("set -e %s\n", name)
		}
		for _, dir := range binDirs {
			fmt.Printf("set PATH (string match -v -- %s $PATH)\n", fishQuote(dir))
		}
	default:
		return usageErrorf("不支持 shell %q", shell)
	}
	return nil
}

func cmdInit(args []string) error {
	if len(args) != 1 {
		return usageErrorf("用法: jkv init <bash|zsh|fish|powershell>")
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
		return usageErrorf("不支持 shell %q", args[0])
	}
	return nil
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
