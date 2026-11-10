#Requires -RunAsAdministrator
<#
.SYNOPSIS
    Remove the NidaOS test virtual machine and its files.

.EXAMPLE
    .\remove-vm.ps1 -VmPath F:\HyperV-VMs\nidaos-vm
#>
[CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'High')]
param(
    [string]$VmName = 'nidaos-vm',
    [string]$VmPath
)

$ErrorActionPreference = 'Stop'

if (-not $VmPath) { $VmPath = Join-Path 'C:\HyperV-VMs' $VmName }

if (-not $PSCmdlet.ShouldProcess("$VmName and the folder $VmPath", 'Remove')) { return }

$vm = Get-VM -Name $VmName -ErrorAction SilentlyContinue
if ($vm) {
    if ($vm.State -ne 'Off') { Stop-VM -Name $VmName -TurnOff -Force }
    Remove-VM -Name $VmName -Force
}
if (Test-Path -LiteralPath $VmPath) {
    Remove-Item -LiteralPath $VmPath -Recurse -Force
}
Write-Host "The virtual machine '$VmName' is removed."
