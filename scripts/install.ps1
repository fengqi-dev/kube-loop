# Download the latest KubeLoop desktop release for Windows.
# Usage:
#   irm https://raw.githubusercontent.com/fengqi-dev/kube-loop/main/scripts/install.ps1 | iex
#   .\scripts\install.ps1 -Version v1.1.0
param(
  [string]$Version = $env:VERSION,
  [string]$Repo = $(if ($env:REPO) { $env:REPO } else { "fengqi-dev/kube-loop" }),
  [string]$Dest = (Get-Location).Path,
  [ValidateSet("installer", "portable")]
  [string]$Package = "installer"
)

$ErrorActionPreference = "Stop"

$arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }

if ($Version) {
  $rel = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/tags/$Version"
} else {
  $rel = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest"
}

$tag = $rel.tag_name
$ver = $tag.TrimStart("v")
$names = if ($Package -eq "installer") {
  @("kubeloop-desktop-$ver-windows-$arch-installer.exe", "kubeloop-$ver-windows-$arch-installer.exe")
} else {
  @("kubeloop-desktop-$ver-windows-$arch.zip", "kubeloop-$ver-windows-$arch.zip")
}

$asset = $null
foreach ($name in $names) {
  $asset = $rel.assets | Where-Object { $_.name -eq $name } | Select-Object -First 1
  if ($asset) { break }
}
if (-not $asset) {
  throw "missing one of $($names -join ', ') in $tag"
}

New-Item -ItemType Directory -Force -Path $Dest | Out-Null
$out = Join-Path $Dest $asset.name
Write-Host "Downloading $($asset.name) ($tag)..."
Invoke-WebRequest -Uri $asset.browser_download_url -OutFile $out

if ($Package -eq "installer") {
  Write-Host "Starting installer: $out"
  Start-Process -FilePath $out
} else {
  $extract = Join-Path $Dest "kubeloop"
  New-Item -ItemType Directory -Force -Path $extract | Out-Null
  Expand-Archive -Path $out -DestinationPath $extract -Force
  Write-Host "Extracted portable build to $extract"
  Write-Host "Run KubeLoop.exe from that folder."
}
