# Graceful upgrade of aimux-dev daemon — workaround for issue #170 (hot-swap .old lock).
#
# Until muxcore drain protocol lands, hot-swap fails when previous .old is held by
# lingering shim processes. This script does a clean kill+replace cycle.
#
# Usage:
#   ./scripts/graceful-upgrade-dev.ps1                       # build new + replace + restart
#   ./scripts/graceful-upgrade-dev.ps1 -SkipBuild            # use existing aimux-dev-next.exe
#   ./scripts/graceful-upgrade-dev.ps1 -CommitMessage "..."  # post-build commit dev binary metadata
#
# After running, CC will reconnect to aimux-dev on next mcp__aimux-dev__* tool call.

param(
    [switch]$SkipBuild,
    [switch]$Verbose
)

$ErrorActionPreference = "Stop"
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
Set-Location $repoRoot

# Deploy targets — both prod (aimux.exe) and dev (aimux-dev.exe) live under bin/.
# graceful-upgrade-dev only refreshes the dev binary; aimux.exe is reserved for
# tagged releases installed via the release flow.
$binDir = "bin"
$targetDev = Join-Path $binDir "aimux-dev.exe"
$stageNext = Join-Path $binDir "aimux-dev-next.exe"

# Step 1 — build (unless skipped)
if (-not $SkipBuild) {
    Write-Host "[1/5] building $stageNext with version injection..."
    New-Item -ItemType Directory -Path $binDir -Force | Out-Null
    & ./scripts/build.ps1 -Output $stageNext
    if ($LASTEXITCODE -ne 0) { throw "build failed (rc=$LASTEXITCODE)" }
}

if (-not (Test-Path $stageNext)) {
    throw "$stageNext not found — run without -SkipBuild or build manually"
}

$newSize = (Get-Item $stageNext).Length
$newVersion = & $stageNext --version
if ($Verbose) { Write-Host "  new binary: $newSize bytes, version: $newVersion" }

# Step 2 — kill all aimux-dev processes (daemon + shims)
Write-Host "[2/5] killing aimux-dev processes (daemon + shims)..."
$killed = 0
$processes = Get-Process aimux-dev -ErrorAction SilentlyContinue
foreach ($p in $processes) {
    try {
        Stop-Process -Id $p.Id -Force -ErrorAction Stop
        $killed++
    } catch {
        Write-Warning "could not stop PID $($p.Id): $_"
    }
}
Write-Host "  killed $killed process(es)"

# Brief pause for OS to release file handles
Start-Sleep -Milliseconds 1500

# Step 3 — clean stale rotation files
Write-Host "[3/5] cleaning stale rotation files..."
$stalePaths = @(
    (Join-Path $binDir "aimux-dev.exe.old"),
    (Join-Path $binDir "aimux-dev.exe~"),
    (Join-Path $binDir "aimux-dev-next.exe~")
)
foreach ($path in $stalePaths) {
    if (Test-Path $path) {
        try {
            Remove-Item $path -Force -ErrorAction Stop
            if ($Verbose) { Write-Host "  removed $path" }
        } catch {
            Write-Warning "could not remove $path : $_"
        }
    }
}

# Step 4 — replace + restore -next (so MCP config that points at -next finds a fresh binary)
Write-Host "[4/5] replacing $targetDev..."
Move-Item $stageNext $targetDev -Force
Copy-Item $targetDev $stageNext

# Step 5 — verify
Write-Host "[5/5] verifying new binary..."
$verifyVersion = & $targetDev --version
Write-Host "  $verifyVersion"

if ($verifyVersion -ne $newVersion) {
    throw "version mismatch after replace: expected '$newVersion', got '$verifyVersion'"
}

Write-Host ""
Write-Host "OK: dev binary upgraded. CC will reconnect on next mcp__aimux-dev__* tool call."
Write-Host "    Old shim sessions are gone (kill was destructive)."
Write-Host "    Verify by calling: mcp__aimux-dev__sessions(action=list)"
