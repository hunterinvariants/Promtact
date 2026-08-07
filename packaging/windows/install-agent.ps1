# Installs the Promtact agent so it runs at boot, as SYSTEM, unattended.
#
# It registers a scheduled task rather than a Windows service. A service has to
# speak the Service Control Manager protocol and report itself started within
# thirty seconds; a console program does not, and Windows kills it. Registering
# an ordinary executable as a service produces exactly one symptom — "the
# service did not respond in a timely fashion" — which says nothing about the
# cause.
#
# A scheduled task needs no such protocol, is native, and restarts on failure.
# A real Windows service is the better long-term answer and would need the agent
# to be built for it; this is honest about being the simpler one.
#
# Run from an elevated PowerShell:
#   .\install-agent.ps1 -Url https://app.promtact.example -LogName System

param(
  [string]$BinaryPath = "C:\Program Files\Promtact\promtactl.exe",
  [Parameter(Mandatory = $true)][string]$Url,
  [string]$Source = "windows-eventlog",
  [string]$LogName = "System",
  [string]$PollInterval = "10s",
  [string]$DataDir = "C:\ProgramData\Promtact",
  [string]$TaskName = "PromtactAgent"
)

$ErrorActionPreference = "Stop"

if (-not ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()
      ).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
  throw "Run this from an elevated PowerShell."
}

if (-not (Test-Path -LiteralPath $BinaryPath)) {
  throw "Agent binary not found at $BinaryPath. Copy promtactl.exe there first, or pass -BinaryPath."
}

New-Item -ItemType Directory -Force -Path $DataDir | Out-Null

# Read the token without echoing it, and without it reaching the history.
$secure = Read-Host -Prompt "Paste the agent API key" -AsSecureString
$plain = [Runtime.InteropServices.Marshal]::PtrToStringAuto(
  [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure))
if ([string]::IsNullOrWhiteSpace($plain)) { throw "No token given." }

$tokenPath = Join-Path $DataDir "agent.token"
[IO.File]::WriteAllText($tokenPath, $plain.Trim())
$plain = $null

# Only SYSTEM (which runs the task) and Administrators may read it. The token is
# in a file rather than on the command line because a task's arguments are
# readable by any local administrator, and show in the process list.
$acl = Get-Acl -LiteralPath $tokenPath
$acl.SetAccessRuleProtection($true, $false)
$acl.Access | ForEach-Object { $acl.RemoveAccessRule($_) | Out-Null }
foreach ($who in @("NT AUTHORITY\SYSTEM", "BUILTIN\Administrators")) {
  $acl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule(
    $who, "FullControl", "Allow")))
}
Set-Acl -LiteralPath $tokenPath -AclObject $acl
Write-Host "Token written to $tokenPath (SYSTEM and Administrators only)."

$statePath = Join-Path $DataDir "agent.state"
$arguments = "agent --source $Source --log-name `"$LogName`" --url $Url " +
             "--token-file `"$tokenPath`" --state-file `"$statePath`" --poll-interval $PollInterval"

if (Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue) {
  Write-Host "Replacing the existing $TaskName task."
  Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
}

$action = New-ScheduledTaskAction -Execute $BinaryPath -Argument $arguments -WorkingDirectory $DataDir
$trigger = New-ScheduledTaskTrigger -AtStartup
$principal = New-ScheduledTaskPrincipal -UserId "SYSTEM" -LogonType ServiceAccount -RunLevel Highest

# No execution time limit: this is a long-running collector, and the default
# would stop it after three days without saying so. Restart on failure, and
# start late if the machine was busy at boot rather than skipping the run.
$settings = New-ScheduledTaskSettingsSet `
  -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries `
  -StartWhenAvailable -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) `
  -ExecutionTimeLimit (New-TimeSpan -Seconds 0) -MultipleInstances IgnoreNew

Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger `
  -Principal $principal -Settings $settings `
  -Description "Forwards selected Windows event log records to Promtact." | Out-Null

Start-ScheduledTask -TaskName $TaskName
Start-Sleep -Seconds 5

$task = Get-ScheduledTask -TaskName $TaskName
$info = Get-ScheduledTaskInfo -TaskName $TaskName

Write-Host ""
Write-Host "Task:    $TaskName"
Write-Host "State:   $($task.State)"
Write-Host "Source:  $Source ($LogName)"
Write-Host "Sending: $Url"
Write-Host ""

if ($task.State -eq "Running") {
  Write-Host "The agent is running. Check the console for this machine under Assets."
} else {
  Write-Host "The task is not running. Last result: $($info.LastTaskResult)" -ForegroundColor Yellow
  Write-Host "Try the same arguments by hand to see the error:"
  Write-Host "  & `"$BinaryPath`" $arguments"
}

Write-Host ""
Write-Host "Check it:    Get-ScheduledTask -TaskName $TaskName"
Write-Host "Stop it:     Stop-ScheduledTask -TaskName $TaskName"
Write-Host "Remove it:   Unregister-ScheduledTask -TaskName $TaskName -Confirm:`$false"
