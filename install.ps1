param(
  [string]$InstallDir = $(if ($env:JKV_DIR) { $env:JKV_DIR } else { Join-Path $HOME '.jkv' })
)
$ErrorActionPreference = 'Stop'
$binDir = Join-Path $InstallDir 'bin'
New-Item -ItemType Directory -Force -Path $binDir | Out-Null
$target = Join-Path $binDir 'jkv.exe'
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
  # PROCESSOR_ARCHITEW6432 reports the native architecture when running
  # 32-bit Windows PowerShell on a 64-bit system.
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
  $downloadBase = if ($env:JKV_DOWNLOAD_BASE) {
    $env:JKV_DOWNLOAD_BASE.TrimEnd('/')
  } else {
    "https://github.com/$repo/releases/latest/download"
  }
  $asset = "jkv-windows-$arch.exe"
  $url = "$downloadBase/$asset"
  $tmp = Join-Path $binDir ".$asset.$([Guid]::NewGuid().ToString('N')).tmp"
  $sumFile = "$tmp.sha256"
  try {
    Write-Host "下载 $asset..."
    Invoke-WebRequest -UseBasicParsing $url -OutFile $tmp
    Invoke-WebRequest -UseBasicParsing "$url.sha256" -OutFile $sumFile
    $expected = ((Get-Content $sumFile -Raw) -split '\s+')[0]
    $actual = (Get-FileHash -Algorithm SHA256 $tmp).Hash.ToLowerInvariant()
    if ($expected.ToLowerInvariant() -ne $actual) { throw 'SHA-256 校验失败' }
    Move-Item -Force $tmp $target
  } finally {
    Remove-Item -Force -ErrorAction SilentlyContinue $tmp, $sumFile
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
