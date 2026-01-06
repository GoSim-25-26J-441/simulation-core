# Test script to verify simulation duration and SSE streaming
$scenarioYaml = Get-Content -Path "config/scenario.yaml" -Raw
$body = @{
    input = @{
        scenario_yaml = $scenarioYaml
        duration_ms = 15000  # 15 seconds for better testing
    }
} | ConvertTo-Json -Depth 10

Write-Host "=== Step 1: Creating run ===" -ForegroundColor Cyan
try {
    $createResponse = Invoke-RestMethod -Uri "http://localhost:8082/v1/runs" -Method POST -ContentType "application/json" -Body $body
    $runId = $createResponse.run.id
    Write-Host "SUCCESS: Run created: $runId" -ForegroundColor Green
    
    Write-Host "`n=== Step 2: Starting run ===" -ForegroundColor Cyan
    $startResponse = Invoke-RestMethod -Uri "http://localhost:8082/v1/runs/$runId" -Method POST -ContentType "application/json"
    Write-Host "SUCCESS: Run started" -ForegroundColor Green
    $startTime = Get-Date
    
    Write-Host "`n=== Step 3: Testing SSE Stream (will run for 5 seconds) ===" -ForegroundColor Cyan
    Write-Host "Connecting to: http://localhost:8082/v1/runs/$runId/metrics/stream" -ForegroundColor Gray
    
    # Start SSE stream in background
    $streamJob = Start-Job -ScriptBlock {
        param($url)
        try {
            $request = [System.Net.HttpWebRequest]::Create($url)
            $request.Method = "GET"
            $request.Timeout = 20000
            $response = $request.GetResponse()
            $stream = $response.GetResponseStream()
            $reader = New-Object System.IO.StreamReader($stream)
            
            $events = @()
            $startTime = Get-Date
            $endTime = $startTime.AddSeconds(5)
            
            while ((Get-Date) -lt $endTime) {
                $line = $reader.ReadLine()
                if ($line) {
                    $events += $line
                    if ($line -match "^event: (.+)$") {
                        $eventType = $matches[1]
                        $dataLine = $reader.ReadLine()
                        if ($dataLine -match "^data: (.+)$") {
                            $dataJson = $matches[1]
                            Write-Output "EVENT: $eventType | DATA: $dataJson"
                        }
                    }
                }
            }
            
            $reader.Close()
            $response.Close()
            return $events
        } catch {
            Write-Output "ERROR: $_"
            return @()
        }
    } -ArgumentList "http://localhost:8082/v1/runs/$runId/metrics/stream"
    
    # Wait and collect stream output
    Start-Sleep -Seconds 5
    $streamOutput = Receive-Job -Job $streamJob -Wait
    Stop-Job -Job $streamJob
    Remove-Job -Job $streamJob
    
    Write-Host "`nSSE Stream Events Received:" -ForegroundColor Cyan
    $eventCount = 0
    foreach ($line in $streamOutput) {
        if ($line -match "^EVENT:") {
            Write-Host $line -ForegroundColor Yellow
            $eventCount++
        } elseif ($line -match "^ERROR:") {
            Write-Host $line -ForegroundColor Red
        }
    }
    Write-Host "Total events captured: $eventCount" -ForegroundColor $(if ($eventCount -gt 0) { "Green" } else { "Yellow" })
    
    Write-Host "`n=== Step 4: Waiting for simulation completion ===" -ForegroundColor Cyan
    $maxWait = 20  # seconds
    $waited = 0
    while ($waited -lt $maxWait) {
        Start-Sleep -Seconds 1
        $waited++
        $status = Invoke-RestMethod -Uri "http://localhost:8082/v1/runs/$runId"
        if ($status.run.status -eq "RUN_STATUS_COMPLETED" -or $status.run.status -eq "RUN_STATUS_FAILED") {
            break
        }
        Write-Host "  Status: $($status.run.status) (waited $waited/$maxWait seconds)" -ForegroundColor Gray
    }
    
    $endTime = Get-Date
    $actualDuration = ($endTime - $startTime).TotalSeconds
    
    Write-Host "`n=== Step 5: Final Status Check ===" -ForegroundColor Cyan
    $finalStatus = Invoke-RestMethod -Uri "http://localhost:8082/v1/runs/$runId"
    Write-Host "Final Status: $($finalStatus.run.status)" -ForegroundColor $(if ($finalStatus.run.status -eq "RUN_STATUS_COMPLETED") { "Green" } else { "Red" })
    
    if ($finalStatus.run.error) {
        Write-Host "Error: $($finalStatus.run.error)" -ForegroundColor Red
    }
    
    Write-Host "`nDuration Analysis:" -ForegroundColor Cyan
    Write-Host "  Expected: 15 seconds" -ForegroundColor White
    Write-Host "  Actual Real Time: $([math]::Round($actualDuration, 2)) seconds" -ForegroundColor White
    Write-Host "  Started At: $($finalStatus.run.started_at_unix_ms)" -ForegroundColor Gray
    Write-Host "  Ended At: $($finalStatus.run.ended_at_unix_ms)" -ForegroundColor Gray
    if ($finalStatus.run.ended_at_unix_ms -gt 0 -and $finalStatus.run.started_at_unix_ms -gt 0) {
        $simDuration = ($finalStatus.run.ended_at_unix_ms - $finalStatus.run.started_at_unix_ms) / 1000.0
        Write-Host "  Simulation Duration: $([math]::Round($simDuration, 2)) seconds" -ForegroundColor $(if ($simDuration -ge 14 -and $simDuration -le 16) { "Green" } else { "Yellow" })
    }
    
    Write-Host "`n=== Step 6: Fetching Final Metrics ===" -ForegroundColor Cyan
    $metrics = Invoke-RestMethod -Uri "http://localhost:8082/v1/runs/$runId/metrics"
    Write-Host "Metrics Summary:" -ForegroundColor Cyan
    Write-Host "  Total Requests: $($metrics.metrics.total_requests)" -ForegroundColor White
    Write-Host "  Successful: $($metrics.metrics.successful_requests)" -ForegroundColor Green
    Write-Host "  Failed: $($metrics.metrics.failed_requests)" -ForegroundColor $(if ($metrics.metrics.failed_requests -gt 0) { "Red" } else { "Green" })
    Write-Host "  Throughput: $([math]::Round($metrics.metrics.throughput_rps, 2)) RPS" -ForegroundColor White
    Write-Host "  Mean Latency: $([math]::Round($metrics.metrics.latency_mean_ms, 2)) ms" -ForegroundColor White
    Write-Host "  P95 Latency: $([math]::Round($metrics.metrics.latency_p95_ms, 2)) ms" -ForegroundColor White
    
    Write-Host "`n=== Test Summary ===" -ForegroundColor Cyan
    $allGood = $true
    if ($finalStatus.run.status -ne "RUN_STATUS_COMPLETED") {
        Write-Host "FAILED: Simulation did not complete successfully" -ForegroundColor Red
        $allGood = $false
    }
    if ($eventCount -eq 0) {
        Write-Host "WARNING: No SSE events received (streaming may not be working)" -ForegroundColor Yellow
    } else {
        Write-Host "SUCCESS: SSE streaming is working ($eventCount events received)" -ForegroundColor Green
    }
    if ($allGood) {
        Write-Host "SUCCESS: All tests passed!" -ForegroundColor Green
    }
    
} catch {
    Write-Host "ERROR: $_" -ForegroundColor Red
    Write-Host $_.Exception.Message -ForegroundColor Red
    if ($_.Exception.Response) {
        $reader = New-Object System.IO.StreamReader($_.Exception.Response.GetResponseStream())
        $responseBody = $reader.ReadToEnd()
        Write-Host "Response: $responseBody" -ForegroundColor Red
    }
}

