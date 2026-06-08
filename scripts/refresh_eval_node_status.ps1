param(
    [int]$IntervalSeconds = 20,
    [switch]$Once
)

$ErrorActionPreference = "Continue"
$repoRoot = Resolve-Path (Join-Path $PSScriptRoot "..")
$runtimeDir = Join-Path $repoRoot ".tmp\live-eval-current"
New-Item -ItemType Directory -Force -Path $runtimeDir | Out-Null

do {
    $nodeList = kubectl get nodes -l kubesage.io/eval-node=true -o json 2>$null
    if ($LASTEXITCODE -eq 0 -and $nodeList) {
        $nodes = ($nodeList | ConvertFrom-Json).items
        foreach ($node in $nodes) {
            $name = $node.metadata.name
            $labels = $node.metadata.labels
            $now = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
            $conditions = @(
                @{
                    type = "Ready"
                    status = "False"
                    reason = "KubeletNotReady"
                    message = "Synthetic live evaluation node is intentionally NotReady"
                    lastHeartbeatTime = $now
                    lastTransitionTime = $now
                },
                @{
                    type = "MemoryPressure"
                    status = $(if ($labels.'kubesage.io/memory-pressure' -eq "true") { "True" } else { "False" })
                    reason = "KubeSageLiveEvaluation"
                    message = "MemoryPressure state for live evaluation"
                    lastHeartbeatTime = $now
                    lastTransitionTime = $now
                },
                @{
                    type = "DiskPressure"
                    status = $(if ($labels.'kubesage.io/disk-pressure' -eq "true") { "True" } else { "False" })
                    reason = "KubeSageLiveEvaluation"
                    message = "DiskPressure state for live evaluation"
                    lastHeartbeatTime = $now
                    lastTransitionTime = $now
                },
                @{
                    type = "PIDPressure"
                    status = $(if ($labels.'kubesage.io/pid-pressure' -eq "true") { "True" } else { "False" })
                    reason = "KubeSageLiveEvaluation"
                    message = "PIDPressure state for live evaluation"
                    lastHeartbeatTime = $now
                    lastTransitionTime = $now
                }
            )
            $patchPath = Join-Path $runtimeDir "$name-status.json"
            $patchJSON = @{ status = @{ conditions = $conditions } } | ConvertTo-Json -Depth 8
            [IO.File]::WriteAllText($patchPath, $patchJSON, (New-Object Text.UTF8Encoding($false)))
            kubectl patch node $name --subresource=status --type=merge --patch-file $patchPath *> $null
        }
    }

    if (-not $Once) {
        Start-Sleep -Seconds $IntervalSeconds
    }
} while (-not $Once)
