# Local-dev aimux build with version injection identical to .goreleaser.yaml.
# Usage:
#   ./scripts/build.ps1                       # outputs aimux-dev-next.exe
#   ./scripts/build.ps1 -Output aimux.exe     # custom path
#   ./scripts/build.ps1 -Race                 # add -race detector

param(
    [string]$Output = "aimux-dev-next.exe",
    [switch]$Race
)

$ErrorActionPreference = "Stop"
Set-Location (Join-Path $PSScriptRoot "..")

$rawVersion = ((git describe --tags --always --dirty 2>$null) -join "")
$version = $rawVersion.Trim()
if ($version.StartsWith("aimux/")) { $version = $version.Substring("aimux/".Length) }
$version = $version.TrimStart('v')
$commit    = (git rev-parse --short HEAD 2>$null) -join ""
$buildDate = (Get-Date -Format "yyyy-MM-ddTHH:mm:ssZ" -AsUTC)

if (-not $version)   { $version   = "0.0.0-dev" }
if (-not $commit)    { $commit    = "unknown"  }

$pkg = "github.com/thebtf/aimux/pkg/build"
$ldflags = @(
    "-s",
    "-w",
    "-X $pkg.Version=$version",
    "-X $pkg.Commit=$commit",
    "-X $pkg.BuildDate=$buildDate"
) -join " "

Write-Host "Building $Output  (Version=$version  Commit=$commit  BuildDate=$buildDate)"

$args = @("build", "-ldflags", $ldflags)
if ($Race) { $args += "-race" }
$args += "-o", $Output, "./cmd/aimux/"

& go @args
if ($LASTEXITCODE -ne 0) {
    Write-Error "go build failed (rc=$LASTEXITCODE)"
    exit $LASTEXITCODE
}

Write-Host "OK: $Output ($([math]::Round((Get-Item $Output).Length / 1MB, 1)) MB)"
