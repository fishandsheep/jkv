[CmdletBinding()]
param(
  [string]$InstallDir = $(if ($env:JKV_DIR) { $env:JKV_DIR } else { Join-Path $HOME '.jkv' }),
  [switch]$NoModifyProfile,
  [switch]$Uninstall,
  [switch]$Purge,
  [switch]$Yes
)

$ErrorActionPreference = 'Stop'
$managedBegin = '# >>> jkv managed >>>'
$managedEnd = '# <<< jkv managed <<<'
$modifyProfile = -not $NoModifyProfile -and $env:JKV_MODIFY_PROFILE -ne '0'
if ($env:JKV_UNINSTALL -eq '1') { $Uninstall = $true }
if ($env:JKV_PURGE -eq '1') { $Uninstall = $true; $Purge = $true }
if ($env:JKV_ASSUME_YES -eq '1') { $Yes = $true }

function Remove-JkvProfileBlock {
  if (-not (Test-Path $PROFILE)) { return }
  $text = Get-Content $PROFILE -Raw
  $pattern = '(?ms)^' + [Regex]::Escape($managedBegin) + '\r?\n.*?^' +
    [Regex]::Escape($managedEnd) + '\r?\n?'
  $text = [Regex]::Replace($text, $pattern, '')
  $lines = @($text -split '\r?\n' | Where-Object { -not $_.Contains('# jkv init') })
  Set-Content -Encoding utf8 $PROFILE ($lines -join [Environment]::NewLine)
}

$fullInstallDir = [IO.Path]::GetFullPath($InstallDir)
$binDir = Join-Path $fullInstallDir 'bin'
$target = Join-Path $binDir 'jkv.exe'

if ($Uninstall) {
  Remove-JkvProfileBlock
  Remove-Item -Force -ErrorAction SilentlyContinue $target
  if ($Purge) {
    $homePath = [IO.Path]::GetFullPath($HOME)
    $rootPath = [IO.Path]::GetPathRoot($fullInstallDir)
    if (-not $fullInstallDir -or $fullInstallDir -eq $homePath -or $fullInstallDir -eq $rootPath) {
      throw "拒绝清理危险目录: $fullInstallDir"
    }
    if (-not $Yes) {
      if (-not [Environment]::UserInteractive) {
        throw '--purge 在非交互环境必须同时使用 --yes'
      }
      $answer = Read-Host "将永久删除 $fullInstallDir，继续? [y/N]"
      if ($answer -notin @('y', 'Y', 'yes', 'YES')) {
        Write-Host '已取消'
        return
      }
    }
    Remove-Item -Recurse -Force $fullInstallDir
    Write-Host "jkv 已彻底卸载: $fullInstallDir（不可恢复）"
  } else {
    Write-Host "jkv 已卸载；已安装工具和配置保留在: $fullInstallDir"
  }
  return
}

New-Item -ItemType Directory -Force -Path $binDir | Out-Null
$repo = if ($env:JKV_REPO) { $env:JKV_REPO } else { 'fishandsheep/jkv' }
$scriptPath = if ($MyInvocation.MyCommand.Name -eq 'install.ps1') {
  $MyInvocation.MyCommand.Path
} else {
  $null
}
$sourceRoot = if ($scriptPath) { Split-Path -Parent $scriptPath } else { $null }
$buildFromSource = -not $env:JKV_DOWNLOAD_BASE -and $sourceRoot -and
  (Test-Path (Join-Path $sourceRoot 'go.mod')) -and
  (Test-Path (Join-Path $sourceRoot 'cmd/jkv')) -and
  (Get-Command go -ErrorAction SilentlyContinue)

if ($buildFromSource) {
  Write-Host '从本地源码构建 jkv...'
  Push-Location $sourceRoot
  try {
    go build -trimpath -ldflags '-s -w' -o $target ./cmd/jkv
    if ($LASTEXITCODE -ne 0) { throw "go build 失败，退出码 $LASTEXITCODE" }
  } finally {
    Pop-Location
  }
} else {
  $nativeArch = if ($env:PROCESSOR_ARCHITEW6432) {
    $env:PROCESSOR_ARCHITEW6432
  } elseif ($env:PROCESSOR_ARCHITECTURE) {
    $env:PROCESSOR_ARCHITECTURE
  } else {
    [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
  }
  $arch = switch ($nativeArch.ToUpperInvariant()) {
    'AMD64' { 'amd64' }
    'X64' { 'amd64' }
    'ARM64' { 'arm64' }
    default { throw "不支持架构: $nativeArch" }
  }
  $asset = "jkv-windows-$arch.exe"
  $embeddedBase = '__JKV_CN_DOWNLOAD_BASE__'
  if ($embeddedBase.StartsWith('__JKV_')) { $embeddedBase = $null }
  $githubBase = if ($env:JKV_VERSION) {
    "https://github.com/$repo/releases/download/$($env:JKV_VERSION)"
  } else {
    "https://github.com/$repo/releases/latest/download"
  }
  $bases = @(
    $(if ($env:JKV_DOWNLOAD_BASE) { $env:JKV_DOWNLOAD_BASE } else { $embeddedBase }),
    $(if ($env:JKV_FALLBACK_BASE) { $env:JKV_FALLBACK_BASE } else { $githubBase })
  ) | Where-Object { $_ } | ForEach-Object { $_.TrimEnd('/') } | Select-Object -Unique

  $tmp = Join-Path $binDir ".$asset.$([Guid]::NewGuid().ToString('N')).tmp"
  $sumFile = "$tmp.sha256"
  $downloaded = $false
  try {
    foreach ($base in $bases) {
      $url = "$base/$asset"
      Write-Host "下载 ${asset}: $base"
      try {
        Invoke-WebRequest -UseBasicParsing $url -OutFile $tmp
        Invoke-WebRequest -UseBasicParsing "$url.sha256" -OutFile $sumFile
      } catch {
        Write-Warning "下载失败，尝试后备地址: $($_.Exception.Message)"
        continue
      }
      $expected = ((Get-Content $sumFile -Raw) -split '\s+')[0]
      $actual = (Get-FileHash -Algorithm SHA256 $tmp).Hash.ToLowerInvariant()
      if ($expected.ToLowerInvariant() -ne $actual) {
        throw 'SHA-256 校验失败；拒绝尝试其他来源'
      }
      $downloaded = $true
      break
    }
    if (-not $downloaded) { throw '所有 jkv 下载地址均不可用' }
    Move-Item -Force $tmp $target
  } finally {
    Remove-Item -Force -ErrorAction SilentlyContinue $tmp, $sumFile
  }
}

$env:JKV_DIR = $fullInstallDir
if (($env:Path -split [IO.Path]::PathSeparator) -notcontains $binDir) {
  $env:Path = "$binDir$([IO.Path]::PathSeparator)$env:Path"
}

if ($modifyProfile) {
  $profileDir = Split-Path -Parent $PROFILE
  New-Item -ItemType Directory -Force -Path $profileDir | Out-Null
  Remove-JkvProfileBlock
  $escapedDir = $fullInstallDir.Replace("'", "''")
  $block = @"
$managedBegin
`$env:JKV_DIR = '$escapedDir'
`$env:Path = (Join-Path `$env:JKV_DIR 'bin') + [IO.Path]::PathSeparator + `$env:Path
Invoke-Expression ((jkv init powershell) -join [Environment]::NewLine)
$managedEnd
"@
  Add-Content -Encoding utf8 $PROFILE $block
  Write-Host "已更新 PowerShell 配置: $PROFILE"
}

Write-Host "jkv 已安装: $target"
Write-Host '重开 PowerShell，或运行: Invoke-Expression ((jkv init powershell) -join "`n")'
