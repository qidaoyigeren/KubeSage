param(
    [switch]$SkipGenerate,
    [switch]$SkipPortForwards
)

$ErrorActionPreference = "Stop"
$repoRoot = Resolve-Path (Join-Path $PSScriptRoot "..")
$runtimeDir = Join-Path $repoRoot ".tmp\live-eval-current"
New-Item -ItemType Directory -Force -Path $runtimeDir | Out-Null

Push-Location $repoRoot
try {
    if (-not $SkipGenerate) {
        go run ./scripts/generate_eval_fault_bank.go
        if ($LASTEXITCODE -ne 0) {
            throw "failed to generate live evaluation manifests"
        }
    }

    kubectl apply -k deployments/observability
    if ($LASTEXITCODE -ne 0) {
        throw "failed to apply observability stack"
    }
    kubectl delete pods -n eval-bank -l kubesage.io/fault-type=nodenotready `
        --ignore-not-found --grace-period=0 --force --wait=true
    if ($LASTEXITCODE -ne 0) {
        throw "failed to replace immutable NodeNotReady scenario pods"
    }
    kubectl apply -k demo/eval-suite
    if ($LASTEXITCODE -ne 0) {
        throw "failed to apply live evaluation suite"
    }

    kubectl -n kubesage-observability rollout status deployment/prometheus --timeout=180s
    kubectl -n kubesage-observability rollout status deployment/loki --timeout=180s
    kubectl -n kubesage-observability rollout status daemonset/promtail --timeout=180s

    & (Join-Path $PSScriptRoot "refresh_eval_node_status.ps1") -Once
    if ($LASTEXITCODE -ne 0) {
        throw "failed to initialize synthetic node status"
    }
    $nodes = (kubectl get nodes -l kubesage.io/eval-node=true -o json | ConvertFrom-Json).items

    $refresherStatePath = Join-Path $runtimeDir "node-status-refresher.json"
    if (Test-Path $refresherStatePath) {
        try {
            $oldRefresher = Get-Content $refresherStatePath -Raw | ConvertFrom-Json
            Stop-Process -Id $oldRefresher.node_status_refresher_pid -Force -ErrorAction SilentlyContinue
        } catch {}
    }

    if ($nodes.Count -gt 0) {
        $refresher = Start-Process powershell -ArgumentList @(
            "-NoProfile", "-ExecutionPolicy", "Bypass",
            "-File", (Join-Path $PSScriptRoot "refresh_eval_node_status.ps1"),
            "-IntervalSeconds", "15"
        ) -WindowStyle Hidden -RedirectStandardOutput (Join-Path $runtimeDir "node-status-refresher.out.log") `
            -RedirectStandardError (Join-Path $runtimeDir "node-status-refresher.err.log") -PassThru
        @{ node_status_refresher_pid = $refresher.Id } |
            ConvertTo-Json |
            Set-Content -Encoding utf8 $refresherStatePath
    }

    if (-not $SkipPortForwards) {
        $portForwardStatePath = Join-Path $runtimeDir "port-forwards.json"
        if (Test-Path $portForwardStatePath) {
            try {
                $oldPortForwards = Get-Content $portForwardStatePath -Raw | ConvertFrom-Json
                Stop-Process -Id $oldPortForwards.prometheus_pid -Force -ErrorAction SilentlyContinue
                Stop-Process -Id $oldPortForwards.loki_pid -Force -ErrorAction SilentlyContinue
            } catch {}
        }

        $prometheus = Start-Process kubectl -ArgumentList @(
            "-n", "kubesage-observability", "port-forward", "--address", "127.0.0.1",
            "svc/prometheus", "9091:9090"
        ) -WindowStyle Hidden -RedirectStandardOutput (Join-Path $runtimeDir "prometheus.out.log") `
            -RedirectStandardError (Join-Path $runtimeDir "prometheus.err.log") -PassThru
        $loki = Start-Process kubectl -ArgumentList @(
            "-n", "kubesage-observability", "port-forward", "--address", "127.0.0.1",
            "svc/loki", "3102:3100"
        ) -WindowStyle Hidden -RedirectStandardOutput (Join-Path $runtimeDir "loki.out.log") `
            -RedirectStandardError (Join-Path $runtimeDir "loki.err.log") -PassThru

        @{
            prometheus_pid = $prometheus.Id
            loki_pid = $loki.Id
        } | ConvertTo-Json | Set-Content -Encoding utf8 $portForwardStatePath

        $deadline = (Get-Date).AddMinutes(2)
        do {
            Start-Sleep -Seconds 2
            $prometheusReady = $false
            $lokiReady = $false
            try {
                $prometheusReady = (Invoke-WebRequest -UseBasicParsing "http://127.0.0.1:9091/-/ready" -TimeoutSec 3).StatusCode -eq 200
            } catch {}
            try {
                $lokiReady = (Invoke-WebRequest -UseBasicParsing "http://127.0.0.1:3102/ready" -TimeoutSec 3).StatusCode -eq 200
            } catch {}
        } while ((-not $prometheusReady -or -not $lokiReady) -and (Get-Date) -lt $deadline)

        if (-not $prometheusReady -or -not $lokiReady) {
            throw "Prometheus or Loki port-forward did not become ready"
        }
    }

    $scenarioCount = (kubectl get pods -n eval-bank -l kubesage.io/fault-bank=true -o name).Count
    Write-Host "Live evaluation environment ready: $scenarioCount scenario pods, $($nodes.Count) Ready=False node objects."
} finally {
    Pop-Location
}
