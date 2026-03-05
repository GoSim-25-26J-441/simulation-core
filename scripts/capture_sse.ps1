# Capture SSE (Server-Sent Events) output from simd metrics stream to a text file.
# Use this to inspect the stream format for frontend integration (e.g. EventSource).
#
# Prerequisites: simd must be running (e.g. go run ./cmd/simd)
# Usage: .\scripts\capture_sse.ps1 [-BaseUrl "http://localhost:8080"] [-OutputFile "sse_output.txt"] [-DurationSeconds 8] [-IntervalMs 500]

param(
    [string]$BaseUrl = "http://localhost:8080",
    [string]$OutputFile = "sse_output.txt",
    [int]$DurationSeconds = 8,
    [int]$IntervalMs = 500,
    [switch]$Online,
    [int]$TargetP95Ms = 50,
    [int]$ControlIntervalMs = 500
)

$ErrorActionPreference = "Stop"

$scenarioYaml = @"
hosts:
  - id: host-1
    cores: 2
services:
  - id: svc1
    replicas: 1
    model: cpu
    endpoints:
      - path: /test
        mean_cpu_ms: 10
        cpu_sigma_ms: 2
        downstream: []
        net_latency_ms: {mean: 1, sigma: 0.5}
workload:
  - from: client
    to: svc1:/test
    arrival: {type: poisson, rate_rps: 10}
"@

Write-Host "Checking server at $BaseUrl..."
try {
    $health = Invoke-RestMethod -Uri "$BaseUrl/healthz" -Method Get -TimeoutSec 3
} catch {
    Write-Error "Cannot reach simd at $BaseUrl. Start it with: go run ./cmd/simd"
}

Write-Host "Creating run..."
if ($Online) {
    Write-Host "Using ONLINE optimization mode (controller updates config automatically)..."
    $optConfig = @{
        objective              = "p95_latency_ms"
        online                 = $true
        target_p95_latency_ms  = $TargetP95Ms
        control_interval_ms    = $ControlIntervalMs
    }
    $input = @{
        scenario_yaml   = $scenarioYaml
        duration_ms     = 0
        real_time_mode  = $true
        optimization    = $optConfig
    }
} else {
    $input = @{
        scenario_yaml = $scenarioYaml
        duration_ms   = ($DurationSeconds * 1000)
    }
}

$createBody = @{ input = $input } | ConvertTo-Json -Depth 5
$createResp = Invoke-RestMethod -Uri "$BaseUrl/v1/runs" -Method Post -Body $createBody -ContentType "application/json"
$runId = $createResp.run.id
if (-not $runId) { Write-Error "Create run failed: no run id in response" }
Write-Host "Run ID: $runId"

Write-Host "Starting run..."
Invoke-RestMethod -Uri "$BaseUrl/v1/runs/$runId" -Method Post | Out-Null

if ($Online) {
    # Apply a sample dynamic config change so the SSE stream shows user-driven updates
    Write-Host "Applying sample dynamic config update (scale svc1 to 3 replicas)..."
    $patchBody = @{
        services = @(
            @{
                id       = "svc1"
                replicas = 3
            }
        )
    } | ConvertTo-Json -Depth 5

    try {
        Invoke-RestMethod -Uri "$BaseUrl/v1/runs/$runId/configuration" -Method Patch -Body $patchBody -ContentType "application/json" | Out-Null
        Write-Host "Dynamic config PATCH applied."
    } catch {
        Write-Host "Warning: dynamic config PATCH failed: $($_.Exception.Message)"
    }
}

Write-Host "Streaming SSE for ${DurationSeconds}s to $OutputFile (interval_ms=$IntervalMs)..."
$streamUrl = "$BaseUrl/v1/runs/$runId/metrics/stream?interval_ms=$IntervalMs"

# Write header first
$header = @"
# SSE stream sample from simd (GET /v1/runs/{id}/metrics/stream)
# Format: event: <type> followed by data: <json> (one JSON object per event).
# Frontend: use EventSource(url); addEventListener('status_change'|'metric_update'|'complete'|...) and use event.data (JSON).
# Mode: $(if ($Online) { 'ONLINE (controller + dynamic config)' } else { 'STANDARD' })
# Captured: $(Get-Date -Format 'yyyy-MM-dd HH:mm:ss') | Run: $runId | Interval: ${IntervalMs}ms
# ---
"@
Set-Content -Path $OutputFile -Value ($header + "`n") -Encoding UTF8
# Append curl output (stream for N seconds then stop). curl writes raw SSE to stdout.
$curlArgs = @("-s", "-N", "-H", "Accept: text/event-stream", "--max-time", $DurationSeconds, $streamUrl)
& curl.exe @curlArgs | Add-Content -Path $OutputFile -Encoding UTF8
Write-Host "Done. Output written to $OutputFile"

# Optionally stop the run so it does not keep running
try {
    Invoke-RestMethod -Uri "$BaseUrl/v1/runs/${runId}:stop" -Method Post -TimeoutSec 2 | Out-Null
    Write-Host "Run stopped."
} catch {
    Write-Host "Note: run may still be running; stop via POST /v1/runs/$runId`:stop"
}
