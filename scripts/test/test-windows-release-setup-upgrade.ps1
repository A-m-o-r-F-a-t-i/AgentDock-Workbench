#requires -Version 5.1
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string] $BaselineSetup,
    [Parameter(Mandatory = $true)][string] $CurrentSetup,
    [Parameter(Mandatory = $true)][ValidatePattern('^\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?$')][string] $BaselineVersion,
    [Parameter(Mandatory = $true)][ValidatePattern('^\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?$')][string] $CurrentVersion,
    [string] $TestRoot = '',
    [switch] $AllowFixedInstallerMutation
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

if (-not $AllowFixedInstallerMutation) {
    throw 'Published Setup compatibility mutates the fixed AgentDock AppId and task. Run only in an isolated Windows profile and pass -AllowFixedInstallerMutation.'
}

$BaselineSetup = (Resolve-Path -LiteralPath $BaselineSetup).Path
$CurrentSetup = (Resolve-Path -LiteralPath $CurrentSetup).Path
if ([string]::IsNullOrWhiteSpace($TestRoot)) {
    $base = if ([string]::IsNullOrWhiteSpace($env:RUNNER_TEMP)) { [IO.Path]::GetTempPath() } else { $env:RUNNER_TEMP }
    $TestRoot = Join-Path $base ('agentdock-release-upgrade-' + $BaselineVersion)
}
$TestRoot = [IO.Path]::GetFullPath($TestRoot)
$installRoot = Join-Path $TestRoot '安装目录 with spaces'
$homeRoot = Join-Path $TestRoot '用户数据'
$workspaceRoot = Join-Path $TestRoot '工作区'
$logRoot = Join-Path $TestRoot 'logs'
$reportPath = Join-Path $TestRoot 'report.json'
$runKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'
$productionRoot = Join-Path $env:LOCALAPPDATA 'AgentDock'
$initialUserPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$initialEnvironment = @{}
Get-ChildItem Env:AGENTDOCK_* -ErrorAction SilentlyContinue | ForEach-Object { $initialEnvironment[$_.Name] = $_.Value }
$startedAt = [DateTime]::UtcNow
$cases = [Collections.Generic.List[object]]::new()
$passed = $false
$failure = ''
$cleanupAllowed = $false
$utf8 = New-Object Text.UTF8Encoding($false)

function Normalize-Version([string] $Value) {
    return $Value.Trim().TrimStart('v')
}

function Save-Report {
    $report = [ordered]@{
        schema_version = 1
        passed = $passed
        baseline_version = $BaselineVersion
        current_version = $CurrentVersion
        started_at = $startedAt.ToString('o')
        completed_at = [DateTime]::UtcNow.ToString('o')
        install_root = $installRoot
        failure = $failure
        cases = @($cases.ToArray())
    }
    New-Item -ItemType Directory -Path $TestRoot -Force | Out-Null
    [IO.File]::WriteAllText($reportPath, ($report | ConvertTo-Json -Depth 8), $utf8)
}

function Record-Case([string] $Name, [hashtable] $Details = @{}) {
    $entry = [ordered]@{ name = $Name; passed = $true }
    foreach ($key in $Details.Keys) { $entry[$key] = $Details[$key] }
    $cases.Add([pscustomobject] $entry)
    Save-Report
    Write-Host "PASS: $Name"
}

function Get-RunValue([string] $Name) {
    try { return Get-ItemPropertyValue -LiteralPath $runKey -Name $Name -ErrorAction Stop } catch { return $null }
}

function Stop-TestProcesses {
    if (-not (Test-Path -LiteralPath $installRoot)) { return }
    $prefix = [IO.Path]::GetFullPath($installRoot).TrimEnd('\') + '\'
    Get-CimInstance Win32_Process -ErrorAction SilentlyContinue | ForEach-Object {
        try {
            if ($_.ExecutablePath -and $_.ExecutablePath.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) {
                Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue
            }
        } catch { }
    }
}

function Remove-FixedInstallerState {
    Stop-TestProcesses
    Stop-ScheduledTask -TaskName 'AgentDock' -TaskPath '\' -ErrorAction SilentlyContinue
    Unregister-ScheduledTask -TaskName 'AgentDock' -TaskPath '\' -Confirm:$false -ErrorAction SilentlyContinue
    Get-ScheduledTask -TaskName 'AgentDock Setup Native *' -ErrorAction SilentlyContinue | ForEach-Object {
        Stop-ScheduledTask -InputObject $_ -ErrorAction SilentlyContinue
        Unregister-ScheduledTask -InputObject $_ -Confirm:$false -ErrorAction SilentlyContinue
    }
    foreach ($name in @('AgentDock', 'AgentDockTray', 'AgentDockTunnel', 'AgentDockCloudflared')) {
        Remove-ItemProperty -LiteralPath $runKey -Name $name -ErrorAction SilentlyContinue
    }
}

function Invoke-ProcessChecked {
    param(
        [Parameter(Mandatory = $true)][string] $FilePath,
        [Parameter(Mandatory = $true)][string[]] $Arguments,
        [Parameter(Mandatory = $true)][string] $Label,
        [Parameter(Mandatory = $true)][string] $LogPath,
        [int] $TimeoutSeconds = 300
    )
    $timer = [Diagnostics.Stopwatch]::StartNew()
    $argumentLine = (($Arguments | ForEach-Object {
        if ($_.Contains('"')) { throw "$Label contains an unsupported quote in an argument." }
        '"' + $_ + '"'
    }) -join ' ')
    $process = Start-Process -FilePath $FilePath -ArgumentList $argumentLine -PassThru
    if (-not $process.WaitForExit($TimeoutSeconds * 1000)) {
        Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
        throw "$Label did not exit within $TimeoutSeconds seconds."
    }
    if ($process.ExitCode -ne 0) {
        throw "$Label failed with exit code $($process.ExitCode). Inspect $LogPath."
    }
    return $timer.ElapsedMilliseconds
}

function Invoke-Setup {
    param([string] $Path, [string] $Label, [int] $Port)
    $log = Join-Path $logRoot ($Label + '.inno.log')
    $arguments = @(
        '/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', '/SP-',
        "/DIR=$installRoot", "/PORT=$Port", '/MODE=local', '/AUTOSTART=1',
        '/ADMINMODE=standard', "/LOG=$log"
    )
    $elapsed = Invoke-ProcessChecked -FilePath $Path -Arguments $arguments -Label $Label -LogPath $log -TimeoutSeconds 360
    Record-Case $Label @{ elapsed_ms = $elapsed; log = $log }
}

function Wait-Health([string] $ExpectedVersion, [int] $Port, [string] $Label) {
    $expected = Normalize-Version $ExpectedVersion
    $deadline = [DateTime]::UtcNow.AddSeconds(60)
    $last = ''
    do {
        try {
            $health = Invoke-RestMethod -Uri "http://127.0.0.1:$Port/healthz" -TimeoutSec 2
            $last = [string] $health.version
            if ($health.ok -and (Normalize-Version $last) -eq $expected) {
                Record-Case $Label @{ version = $last }
                return
            }
        } catch { $last = $_.Exception.Message }
        Start-Sleep -Milliseconds 300
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "$Label failed: expected health version $ExpectedVersion; last=$last"
}

function Get-FileHashes([string] $Root, [string] $Filter) {
    $result = @{}
    Get-ChildItem -LiteralPath $Root -File -Filter $Filter -ErrorAction SilentlyContinue | ForEach-Object {
        $result[$_.Name] = (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash
    }
    return $result
}

function Assert-FileHashes([string] $Root, [hashtable] $Expected) {
    foreach ($name in $Expected.Keys) {
        $path = Join-Path $Root $name
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Protected file was removed: $name" }
        $actual = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash
        if ($actual -ne $Expected[$name]) { throw "Protected file changed: $name" }
    }
}

function Set-SystemOwner([string] $Path) {
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        [IO.File]::WriteAllText($Path, '', $utf8)
    }
    $systemSid = New-Object Security.Principal.SecurityIdentifier('S-1-5-18')
    $acl = Get-Acl -LiteralPath $Path
    $acl.SetOwner($systemSid)
    try {
        Set-Acl -LiteralPath $Path -AclObject $acl
    } catch {
        & icacls.exe $Path /setowner '*S-1-5-18' /C | Out-Null
        if ($LASTEXITCODE -ne 0) { throw "Could not assign SYSTEM as the compatibility fixture owner: $($_.Exception.Message)" }
    }
    $actual = (Get-Acl -LiteralPath $Path).GetOwner([Security.Principal.SecurityIdentifier]).Value
    if ($actual -ne 'S-1-5-18') { throw "Foreign-owner fixture was not established: $actual" }
    Record-Case 'Foreign owner fixture established' @{ path = $Path; owner_sid = $actual }
}

function Assert-CommittedCurrentState([string] $ExpectedVersion) {
    $transactionPath = Join-Path $installRoot 'install\transaction.json'
    $resultPath = Join-Path $installRoot 'install\result.json'
    $pointerPath = Join-Path $installRoot 'active-version.json'
    foreach ($path in @($transactionPath, $resultPath, $pointerPath)) {
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Current installer state is missing: $path" }
    }
    $transaction = Get-Content -LiteralPath $transactionPath -Raw -Encoding UTF8 | ConvertFrom-Json
    $result = Get-Content -LiteralPath $resultPath -Raw -Encoding UTF8 | ConvertFrom-Json
    $pointer = Get-Content -LiteralPath $pointerPath -Raw -Encoding UTF8 | ConvertFrom-Json
    $expected = Normalize-Version $ExpectedVersion
    if ($transaction.state -ne 'committed' -or $result.state -ne 'committed' -or -not $result.healthy) {
        throw "Current transaction is not healthy and committed: transaction=$($transaction.state); result=$($result.state); healthy=$($result.healthy)"
    }
    if ($pointer.state -ne 'committed' -or (Normalize-Version ([string] $pointer.active_version)) -ne $expected) {
        throw "Current generation pointer is invalid: state=$($pointer.state); version=$($pointer.active_version)"
    }
    $manifest = Get-Content -LiteralPath (Join-Path $installRoot 'runtime.json') -Raw -Encoding UTF8 | ConvertFrom-Json
    if ([IO.Path]::GetFullPath([string] $manifest.agentdock_home) -ne [IO.Path]::GetFullPath($homeRoot)) {
        throw "User data root changed: $($manifest.agentdock_home)"
    }
    if ([IO.Path]::GetFullPath([string] $manifest.agentdock_default_dir) -ne [IO.Path]::GetFullPath($workspaceRoot)) {
        throw "Workspace root changed: $($manifest.agentdock_default_dir)"
    }
    $leftovers = @(Get-ChildItem -LiteralPath (Join-Path $installRoot 'versions') -Directory -Filter '.repair-backup-*' -ErrorAction SilentlyContinue)
    if ($leftovers.Count -ne 0) { throw 'Installer left a repair backup generation.' }
}

try {
    if (Test-Path -LiteralPath $TestRoot) { Remove-Item -LiteralPath $TestRoot -Recurse -Force }
    New-Item -ItemType Directory -Path $TestRoot, $homeRoot, $workspaceRoot, $logRoot -Force | Out-Null
    if (Test-Path -LiteralPath $productionRoot) {
        throw "A production AgentDock runtime exists at $productionRoot. Use a clean Windows profile."
    }
    if ($null -ne (Get-ScheduledTask -TaskName 'AgentDock' -TaskPath '\' -ErrorAction SilentlyContinue)) {
        throw 'A production AgentDock scheduled task exists. Use a clean Windows profile.'
    }
    foreach ($name in @('AgentDock', 'AgentDockTray', 'AgentDockTunnel', 'AgentDockCloudflared')) {
        if ($null -ne (Get-RunValue $name)) { throw "A production startup entry exists: $name" }
    }
    $cleanupAllowed = $true

    foreach ($key in $initialEnvironment.Keys) { [Environment]::SetEnvironmentVariable($key, $null, 'Process') }
    $env:AGENTDOCK_HOME = $homeRoot
    $env:AGENTDOCK_DEFAULT_DIR = $workspaceRoot
    [IO.File]::WriteAllText((Join-Path $homeRoot 'history-user-sentinel.txt'), 'preserve-user-data', $utf8)
    [IO.File]::WriteAllText((Join-Path $workspaceRoot 'history-workspace-sentinel.txt'), 'preserve-workspace', $utf8)

    $listener = New-Object Net.Sockets.TcpListener([Net.IPAddress]::Loopback, 0)
    $listener.Start(); $port = $listener.LocalEndpoint.Port; $listener.Stop()

    Invoke-Setup -Path $BaselineSetup -Label ('baseline-' + $BaselineVersion) -Port $port
    Wait-Health -ExpectedVersion $BaselineVersion -Port $port -Label 'Baseline runtime healthy'
    $credentialHashes = Get-FileHashes -Root $installRoot -Filter '*.dpapi'
    if ($credentialHashes.Count -eq 0) { throw 'Baseline Setup did not create protected credential files.' }
    Set-SystemOwner -Path (Join-Path $installRoot 'server-url.txt')

    Invoke-Setup -Path $CurrentSetup -Label ('upgrade-to-' + $CurrentVersion) -Port $port
    Wait-Health -ExpectedVersion $CurrentVersion -Port $port -Label 'Upgraded runtime healthy'
    Assert-CommittedCurrentState -ExpectedVersion $CurrentVersion
    Assert-FileHashes -Root $installRoot -Expected $credentialHashes
    if ([IO.File]::ReadAllText((Join-Path $homeRoot 'history-user-sentinel.txt')) -ne 'preserve-user-data') { throw 'Upgrade changed user data.' }
    if ([IO.File]::ReadAllText((Join-Path $workspaceRoot 'history-workspace-sentinel.txt')) -ne 'preserve-workspace') { throw 'Upgrade changed workspace data.' }
    Record-Case 'Historical upgrade committed and preserved data' @{ credentials = $credentialHashes.Count }

    Invoke-Setup -Path $CurrentSetup -Label ('repair-' + $CurrentVersion) -Port $port
    Wait-Health -ExpectedVersion $CurrentVersion -Port $port -Label 'Same-version repair healthy'
    Assert-CommittedCurrentState -ExpectedVersion $CurrentVersion
    Assert-FileHashes -Root $installRoot -Expected $credentialHashes
    Record-Case 'Same-version repair committed without residue'

    $uninstaller = Join-Path $installRoot 'unins000.exe'
    if (-not (Test-Path -LiteralPath $uninstaller -PathType Leaf)) { throw "Current uninstaller is missing: $uninstaller" }
    $uninstallLog = Join-Path $logRoot 'uninstall.inno.log'
    $uninstallElapsed = Invoke-ProcessChecked -FilePath $uninstaller -Arguments @('/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', "/LOG=$uninstallLog") -Label 'uninstall' -LogPath $uninstallLog -TimeoutSeconds 300
    Record-Case 'Current uninstaller completed' @{ elapsed_ms = $uninstallElapsed; log = $uninstallLog }
    if ([IO.File]::ReadAllText((Join-Path $homeRoot 'history-user-sentinel.txt')) -ne 'preserve-user-data') { throw 'Uninstall removed user data.' }
    if ([IO.File]::ReadAllText((Join-Path $workspaceRoot 'history-workspace-sentinel.txt')) -ne 'preserve-workspace') { throw 'Uninstall removed workspace data.' }
    $passed = $true
} catch {
    $failure = $_.Exception.Message
    $cases.Add([pscustomobject]@{ name = 'failure'; passed = $false; message = $failure })
    throw
} finally {
    if ($cleanupAllowed) { Remove-FixedInstallerState }
    $currentPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if ($currentPath -ne $initialUserPath) { [Environment]::SetEnvironmentVariable('Path', $initialUserPath, 'User') }
    Get-ChildItem Env:AGENTDOCK_* -ErrorAction SilentlyContinue | ForEach-Object { [Environment]::SetEnvironmentVariable($_.Name, $null, 'Process') }
    foreach ($key in $initialEnvironment.Keys) { [Environment]::SetEnvironmentVariable($key, $initialEnvironment[$key], 'Process') }
    Save-Report
}
