# Run tests with coverage and verify against threshold (matches CI).
# Run locally before pushing to avoid coverage failures in CI.
#
# Usage: .\scripts\check-coverage.ps1 [-Threshold 80.0] [-SkipRace]
#   -SkipRace: omit -race flag (use on Windows when CGO is disabled)
param(
    [double]$Threshold = 80.0,
    [switch]$SkipRace
)

$ErrorActionPreference = "Stop"
Push-Location $PSScriptRoot\..

try {
    Write-Host "Running tests with coverage (excluding cmd/, gen/)..."
    $pkgs = @(go list ./... | Where-Object { $_ -notmatch '/cmd/' -and $_ -notmatch '/gen/' })
    $testArgs = @('-coverprofile=coverage.out', '-covermode=atomic') + $pkgs
    if (-not $SkipRace) { $testArgs = @('-race') + $testArgs }
    & go test @testArgs

    if (-not (Test-Path coverage.out)) {
        Write-Error "coverage.out was not created"
    }

    Write-Host ""
    Write-Host "Coverage report:"
    go tool cover -func coverage.out | Select-Object -Last 5

    $coverOut = go tool cover -func coverage.out 2>&1 | Out-String
    $totalLine = $coverOut -split "`n" | Where-Object { $_ -match 'total:' } | Select-Object -First 1
    if (-not $totalLine) {
        Write-Error "Could not find total line in coverage output"
    }
    # Format: "total:      (statements)    79.4%"
    if ($totalLine -match '([\d.]+)%\s*$') {
        $pct = [double]$Matches[1]
    } else {
        Write-Error "Could not parse coverage percentage from: $totalLine"
    }

    Write-Host ""
    Write-Host "Total coverage: $pct%"
    Write-Host "Threshold: $Threshold%"

    if ($pct -lt $Threshold) {
        Write-Host "❌ Coverage $pct% is below threshold $Threshold%"
        exit 1
    } else {
        Write-Host "✅ Coverage $pct% meets threshold $Threshold%"
        exit 0
    }
} finally {
    Pop-Location
}
