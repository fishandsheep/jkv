param(
  [string]$InstallDir = $(if ($env:JKV_DIR) { $env:JKV_DIR } else { Join-Path $HOME '.jkv' })
)
$ErrorActionPreference = 'Stop'
$binDir = Join-Path $InstallDir 'bin'
New-Item -ItemType Directory -Force -Path $binDir | Out-Null
$target = Join-Path $binDir 'jkv.exe'

if ((Test-Path 'go.mod') -and (Test-Path 'cmd/jkv') -and (Get-Command go -ErrorAction SilentlyContinue)) {
  Write-Host '从本地源码构建 jkv...'
  go build -trimpath -ldflags '-s -w' -o $target ./cmd/jkv
} else {
  if (-not $env:JKV_REPO) { throw '请设置发布仓库，例如: $env:JKV_REPO="owner/jkv"' }
  $arch = switch ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture) {
    'X64' { 'amd64' }
    'Arm64' { 'arm64' }
    default { throw "不支持架构: $_" }
  }
  $base = "https://github.com/$($env:JKV_REPO)/releases/latest/download/jkv-windows-$arch.exe"
  $tmp = [IO.Path]::GetTempFileName()
  try {
    Invoke-WebRequest -UseBasicParsing $base -OutFile $tmp
    Invoke-WebRequest -UseBasicParsing "$base.sha256" -OutFile "$tmp.sha256"
    $expected = ((Get-Content "$tmp.sha256" -Raw) -split '\s+')[0]
    $actual = (Get-FileHash -Algorithm SHA256 $tmp).Hash.ToLowerInvariant()
    if ($expected.ToLowerInvariant() -ne $actual) { throw 'SHA-256 校验失败' }
    Move-Item -Force $tmp $target
  } finally {
    Remove-Item -Force -ErrorAction SilentlyContinue $tmp, "$tmp.sha256"
  }
}

$env:JKV_DIR = $InstallDir
if (($env:Path -split [IO.Path]::PathSeparator) -notcontains $binDir) {
  $env:Path = "$binDir$([IO.Path]::PathSeparator)$env:Path"
}
$profileDir = Split-Path -Parent $PROFILE
New-Item -ItemType Directory -Force -Path $profileDir | Out-Null
$marker = '# jkv init'
$profileText = if (Test-Path $PROFILE) { Get-Content $PROFILE -Raw } else { '' }
if (-not $profileText.Contains($marker)) {
  Add-Content $PROFILE "`n`$env:JKV_DIR = Join-Path `$HOME '.jkv'; `$env:Path = (Join-Path `$env:JKV_DIR 'bin') + [IO.Path]::PathSeparator + `$env:Path; Invoke-Expression ((jkv init powershell) -join [Environment]::NewLine) $marker"
}
Write-Host "jkv 已安装: $target"
Write-Host '重开 PowerShell，或运行: Invoke-Expression ((jkv init powershell) -join "`n")'
