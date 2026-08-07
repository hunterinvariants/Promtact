# Installs the Promtact agent as a Windows service.
#
# The token is written to a file rather than passed on the command line: a
# service's arguments are readable by every local administrator through
# `sc.exe qc` and appear in the process list. The file is locked to SYSTEM and
# Administrators, which is the same audience but without the accidental
# exposure.
#
# Run from an elevated PowerShell:
#   .\install-agent-service.ps1 -Url https://app.promtact.com -LogName System
# It will prompt for the token; it is never taken as a parameter, so it does not
# land in the PowerShell history.

param(
  [string]$BinaryPath = "C:\Program Files\Promtact\promtactl.exe",
  [Parameter(Mandatory = $true)][string]$Url,
  [string]$Source = "windows-eventlog",
  [string]$LogName = "System",
  [string]$PollInterval = "10s",
  [string]$DataDir = "C:\ProgramData\Promtact",
  [string]$ServiceName = "PromtactAgent"
)

$ErrorActionPreference = "Stop"

if (-not ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()
      ).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
  throw "Run this from an elevated PowerShell. Installing a service requires it."
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

# Only SYSTEM (which runs the service) and Administrators may read it.
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
$binPath = "`"$BinaryPath`" $arguments"

if (Get-Service -Name $ServiceName -ErrorAction SilentlyContinue) {
  Write-Host "Replacing the existing $ServiceName service."
  sc.exe stop $ServiceName | Out-Null
  Start-Sleep -Seconds 2
  sc.exe delete $ServiceName | Out-Null
  Start-Sleep -Seconds 2
}

sc.exe create $ServiceName binPath= $binPath start= auto DisplayName= "Promtact Agent" | Out-Null
sc.exe description $ServiceName "Forwards selected Windows event log records to Promtact." | Out-Null

# Restart on failure rather than giving up: a collector that stops collecting
# silently is worse than one that is plainly absent.
sc.exe failure $ServiceName reset= 86400 actions= restart/10000/restart/30000/restart/60000 | Out-Null

sc.exe start $ServiceName | Out-Null
Start-Sleep -Seconds 3

$service = Get-Service -Name $ServiceName
Write-Host ""
Write-Host "Service $ServiceName is $($service.Status)."
Write-Host "Source:  $Source ($LogName)"
Write-Host "Sending: $Url"
Write-Host ""
Write-Host "Check it is sending:  Get-Service $ServiceName"
Write-Host "Stop it:              sc.exe stop $ServiceName"
Write-Host "Remove it entirely:   sc.exe stop $ServiceName; sc.exe delete $ServiceName"
