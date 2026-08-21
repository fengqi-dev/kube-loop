# Install the KubeLoop terminal client for Windows.
# Usage:
#   irm https://raw.githubusercontent.com/fengqi-dev/kube-loop/main/scripts/install-tui.ps1 | iex
#   .\scripts\install-tui.ps1 -Version v2.1.2
param(
  [string]$Version = $env:VERSION,
  [string]$Repo = $(if ($env:REPO) { $env:REPO } else { "fengqi-dev/kube-loop" }),
  [string]$Dest = $(if ($env:DEST) { $env:DEST } else { Join-Path $env:LOCALAPPDATA "Programs\KubeLoop\bin" })
)

$ErrorActionPreference = "Stop"

$arch = switch ([Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()) {
  "Arm64" { "arm64" }
  "X64" { "amd64" }
  default { throw "unsupported architecture: $([Runtime.InteropServices.RuntimeInformation]::OSArchitecture)" }
}

if ($Version -and -not $Version.StartsWith("v")) {
  $Version = "v$Version"
}
if ($Version) {
  $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/tags/$Version"
} else {
  $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest"
}

$tag = $release.tag_name
$releaseVersion = $tag.TrimStart("v")
$assetName = "kubeloop-tui-$releaseVersion-windows-$arch.tar.gz"
$asset = $release.assets | Where-Object { $_.name -eq $assetName } | Select-Object -First 1
$checksums = $release.assets | Where-Object { $_.name -eq "SHA256SUMS" } | Select-Object -First 1
if (-not $asset) { throw "missing $assetName in $tag" }
if (-not $checksums) { throw "missing SHA256SUMS in $tag" }
if (-not (Get-Command tar.exe -ErrorAction SilentlyContinue)) { throw "tar.exe is required" }

$temporary = Join-Path ([IO.Path]::GetTempPath()) ("kubeloop-tui-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $temporary | Out-Null
try {
  $archivePath = Join-Path $temporary $assetName
  $checksumPath = Join-Path $temporary "SHA256SUMS"
  Write-Host "Downloading $assetName ($tag)..."
  Invoke-WebRequest -Uri $asset.browser_download_url -OutFile $archivePath
  Invoke-WebRequest -Uri $checksums.browser_download_url -OutFile $checksumPath

  $assetPattern = [Regex]::Escape($assetName)
  $checksumLine = Get-Content $checksumPath | Where-Object {
    $_ -match "^([0-9A-Fa-f]{64})\s+\*?(?:\./)?$assetPattern$"
  } | Select-Object -First 1
  if (-not $checksumLine) { throw "missing checksum for $assetName in $tag" }
  $expected = ([Regex]::Match($checksumLine, "^[0-9A-Fa-f]{64}")).Value.ToLowerInvariant()
  $actual = (Get-FileHash -Algorithm SHA256 -Path $archivePath).Hash.ToLowerInvariant()
  if ($actual -ne $expected) { throw "checksum mismatch for $assetName`: got $actual, want $expected" }

  $entries = @(tar.exe -tzf $archivePath | ForEach-Object { $_ -replace '^\./', '' } | Where-Object { $_ })
  if ($LASTEXITCODE -ne 0 -or $entries.Count -ne 1 -or $entries[0] -ne "kubeloop.exe") {
    throw "unexpected files in $assetName`: $($entries -join ', ')"
  }
  tar.exe -xzf $archivePath -C $temporary kubeloop.exe
  if ($LASTEXITCODE -ne 0) { throw "extract $assetName failed" }

  New-Item -ItemType Directory -Force -Path $Dest | Out-Null
  $target = Join-Path $Dest "kubeloop.exe"
  Copy-Item -Force (Join-Path $temporary "kubeloop.exe") $target
  Write-Host "Installed $target"
  & $target version

  $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
  $pathEntries = @($userPath -split ';' | Where-Object { $_ })
  if (-not ($pathEntries | Where-Object { $_.TrimEnd('\') -ieq $Dest.TrimEnd('\') })) {
    $newPath = (@($pathEntries) + $Dest) -join ';'
    [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
    Write-Host "Added $Dest to the user PATH. Open a new terminal to run kubeloop."
  }
} finally {
  Remove-Item -Recurse -Force $temporary -ErrorAction SilentlyContinue
}
