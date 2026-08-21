#requires -Version 5.1

[CmdletBinding()]
param(
    [string]$Context = "docker-desktop",
    [string]$GatewayImage = "",
    [string]$Timeout = "25m",
    [switch]$SkipBuild,
    [switch]$SkipPlatform,
    [switch]$KeepResources,
    [switch]$IgnoreNetworkPreflight
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

$root = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot "..\.."))
$mainLog = Join-Path $root "e2e-local.log"
$platformLog = Join-Path $root "e2e-platform.log"
$platformTest = Join-Path $root "build\bin\platform-e2e.test.exe"
$singBox = Join-Path $root "build\bin\sing-box.exe"
$helper = Join-Path $root "build\embedded\kubeloop-helper.exe"
$cache = Join-Path $root ".gocache-e2e"

if ([string]::IsNullOrWhiteSpace($GatewayImage)) {
    $GatewayImage = "kubeloop-gateway:e2e-local-$PID"
}

$mainPackages = @(
    "./e2e/dataplane",
    "./e2e/remotetun"
)

$mainExit = 1
$platformExit = if ($SkipPlatform) { 0 } else { 1 }
$setupFailed = $false
$imageBuilt = $false
$helperExisted = $null -ne (Get-Service -Name "KubeLoopHelperDev" -ErrorAction SilentlyContinue)
$oldLocation = Get-Location

function Invoke-Checked {
    param(
        [Parameter(Mandatory)]
        [string]$Command,
        [Parameter(ValueFromRemainingArguments)]
        [string[]]$Arguments
    )

    & $Command @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$Command exited with code $LASTEXITCODE"
    }
}

function Test-LocalDNSPort {
    $tcp = $null
    $udp = $null
    try {
        $address = [System.Net.IPAddress]::Parse("127.0.0.1")
        $tcp = [System.Net.Sockets.TcpListener]::new($address, 53)
        $tcp.Start()
        $udp = [System.Net.Sockets.UdpClient]::new(
            [System.Net.IPEndPoint]::new($address, 53)
        )
        return $true
    }
    catch {
        Write-Warning "127.0.0.1:53 cannot be used: $($_.Exception.Message)"
        return $false
    }
    finally {
        if ($udp) {
            $udp.Dispose()
        }
        if ($tcp) {
            $tcp.Stop()
        }
    }
}

function Show-NetworkRequirements {
    $nodesJSON = (& kubectl --context $Context get nodes -o json) -join "`n"
    if ($LASTEXITCODE -ne 0) {
        throw "could not read Kubernetes nodes"
    }
    $podCIDRs = @(
        ($nodesJSON | ConvertFrom-Json).items |
            ForEach-Object { $_.spec.podCIDRs } |
            Where-Object { -not [string]::IsNullOrWhiteSpace($_) }
    )
    $serviceIP = & kubectl --context $Context get service kubernetes `
        --namespace default `
        -o 'jsonpath={.spec.clusterIP}'

    Write-Host ""
    Write-Host "Mihomo/Clash preflight" -ForegroundColor Cyan
    Write-Host "  Pod CIDR(s): $($podCIDRs -join ', ')"
    Write-Host "  Kubernetes Service IP: $serviceIP"
    Write-Host "  Docker Desktop defaults to exclusions 10.244.0.0/16 and 10.96.0.0/16."
    Write-Host "  Disable TUN DNS hijack 'any:53' before running DNS E2E."
    Write-Host ""
}

function Get-TestResults {
    param([string[]]$Paths)

    $results = @()
    foreach ($path in $Paths) {
        if (-not (Test-Path -LiteralPath $path)) {
            continue
        }
        foreach ($line in Get-Content -LiteralPath $path) {
            if ($line -match '^--- (PASS|FAIL|SKIP): ([^ ]+)(?: \(([^)]+)\))?') {
                $results += [pscustomobject]@{
                    Test = $Matches[2]
                    Status = $Matches[1]
                    Duration = $Matches[3]
                }
            }
        }
    }
    return $results
}

function Uninstall-TemporaryHelper {
    if ($helperExisted -or -not (Test-Path -LiteralPath $helper)) {
        return
    }
    if ($null -eq (Get-Service -Name "KubeLoopHelperDev" -ErrorAction SilentlyContinue)) {
        return
    }
    $process = Start-Process `
        -FilePath $helper `
        -ArgumentList "uninstall" `
        -WorkingDirectory (Split-Path -Parent $helper) `
        -Verb RunAs `
        -WindowStyle Hidden `
        -Wait `
        -PassThru
    if ($process.ExitCode -ne 0) {
        Write-Warning "Helper uninstall exited with code $($process.ExitCode)"
    }
}

try {
    Set-Location $root
    $env:GOCACHE = $cache

    foreach ($command in @("go", "docker", "kubectl")) {
        if (-not (Get-Command $command -ErrorAction SilentlyContinue)) {
            throw "required command is missing: $command"
        }
    }

    Invoke-Checked docker version
    Invoke-Checked kubectl --context $Context cluster-info
    Show-NetworkRequirements

    if (-not (Test-LocalDNSPort) -and -not $IgnoreNetworkPreflight) {
        throw @"
DNS preflight failed. Disable Mihomo/Clash TUN DNS hijack (any:53), restart its
TUN, and run this script again. Use -IgnoreNetworkPreflight only when you want
to record the expected DNS failures.
"@
    }

    if (-not $SkipBuild) {
        Write-Host "==> Building Helper and sing-box" -ForegroundColor Cyan
        Invoke-Checked go run ./build/helper-prebuild.go windows/amd64
        Invoke-Checked go run ./e2e/scripts/ensure-singbox.go

        Write-Host "==> Installing privileged Helper (Windows UAC may appear)" `
            -ForegroundColor Cyan
        Invoke-Checked go run ./e2e/scripts/ensure-helper.go
        Invoke-Checked go run ./e2e/scripts/helper-ping.go
        Invoke-Checked go run ./e2e/scripts/stop-helper.go

        Write-Host "==> Building Gateway image $GatewayImage" -ForegroundColor Cyan
        $oldCGO = $env:CGO_ENABLED
        $oldGOOS = $env:GOOS
        $oldGOARCH = $env:GOARCH
        try {
            $env:CGO_ENABLED = "0"
            $env:GOOS = "linux"
            $env:GOARCH = (& go env GOARCH).Trim()
            Invoke-Checked -Command go -Arguments @(
                "build",
                "-trimpath",
                "-ldflags=-s -w",
                "-o",
                "build/bin/kubeloop-gateway",
                "./cmd/kubeloop-gateway"
            )
        }
        finally {
            $env:CGO_ENABLED = $oldCGO
            $env:GOOS = $oldGOOS
            $env:GOARCH = $oldGOARCH
        }
        Invoke-Checked docker build `
            -t $GatewayImage `
            -f build/gateway.e2e.Dockerfile .
        $imageBuilt = $true
    }
    elseif (-not (Test-Path -LiteralPath $singBox)) {
        throw "sing-box is missing: $singBox"
    }

    Write-Host "==> E2E test list" -ForegroundColor Cyan
    Invoke-Checked go test -tags=e2e ./e2e/... -list "^Test"

    $env:KUBELOOP_E2E = "1"
    $env:KUBELOOP_E2E_CONTEXT = $Context
    $env:KUBELOOP_GATEWAY_IMAGE = $GatewayImage
    $env:KUBELOOP_SINGBOX_PATH = $singBox
    $env:KUBELOOP_REMOTE_TUN_E2E = "1"

    Write-Host "==> Running TUN/Kubernetes E2E" -ForegroundColor Cyan
    & go test `
        -tags=e2e `
        @mainPackages `
        -count=1 `
        "-timeout=$Timeout" `
        -parallel=1 `
        -p=1 `
        -v 2>&1 | Tee-Object -FilePath $mainLog
    $mainExit = $LASTEXITCODE

    if (-not $SkipPlatform) {
        Write-Host "==> Building Windows platform E2E" -ForegroundColor Cyan
        Invoke-Checked -Command go -Arguments @(
            "test",
            "-tags=e2e",
            "-c",
            "-o",
            $platformTest,
            "./e2e/platform"
        )

        Write-Host "==> Running Windows platform E2E (Windows UAC may appear)" `
            -ForegroundColor Cyan
        $platformDirectory = Join-Path $root "e2e\platform"
        $commandLine = 'cd /d "' + $platformDirectory +
            '"&& set KUBELOOP_PLATFORM_E2E=1&& "' + $platformTest +
            '" -test.v -test.timeout=5m > "' + $platformLog + '" 2>&1'
        $process = Start-Process `
            -FilePath "$env:SystemRoot\System32\cmd.exe" `
            -ArgumentList @("/d", "/s", "/c", ('"' + $commandLine + '"')) `
            -Verb RunAs `
            -WindowStyle Hidden `
            -Wait `
            -PassThru
        $platformExit = $process.ExitCode
        if (Test-Path -LiteralPath $platformLog) {
            Get-Content -LiteralPath $platformLog
        }
    }
}
catch {
    $setupFailed = $true
    [Console]::Error.WriteLine("E2E setup/run error: $($_.Exception.Message)")
}
finally {
    if (-not $KeepResources) {
        Write-Host "==> Cleaning E2E resources" -ForegroundColor Cyan
        & go run ./e2e/scripts/stop-helper.go
        & kubectl --context $Context delete namespace kubeloop-e2e `
            --ignore-not-found=true `
            --wait=false
        & kubectl --context $Context --namespace kubeloop-system `
            delete deployment kubeloop-gateway `
            --ignore-not-found=true `
            --wait=false
        & kubectl --context $Context --namespace kubeloop-system `
            delete service kubeloop-gateway `
            --ignore-not-found=true `
            --wait=false
        if ($imageBuilt) {
            & docker image rm $GatewayImage --force
        }
        Uninstall-TemporaryHelper
    }
    Set-Location $oldLocation
}

$results = Get-TestResults @($mainLog, $platformLog)
Write-Host ""
Write-Host "==> E2E summary" -ForegroundColor Cyan
if ($results.Count -eq 0) {
    Write-Host "No completed tests were parsed."
}
else {
    $results | Format-Table -AutoSize
    $passed = @($results | Where-Object Status -eq "PASS").Count
    $failed = @($results | Where-Object Status -eq "FAIL").Count
    $skipped = @($results | Where-Object Status -eq "SKIP").Count
    Write-Host "PASS=$passed FAIL=$failed SKIP=$skipped TOTAL=$($results.Count)"
}
Write-Host "Main log: $mainLog"
if (-not $SkipPlatform) {
    Write-Host "Platform log: $platformLog"
}

if ($setupFailed -or $mainExit -ne 0 -or $platformExit -ne 0) {
    exit 1
}
exit 0
