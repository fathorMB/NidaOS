#Requires -RunAsAdministrator
<#
.SYNOPSIS
    Make a Hyper-V virtual machine that installs NidaOS from the installer ISO.

.DESCRIPTION
    The virtual machine is Generation 2 (UEFI) and has three empty disks:
    24 GB, 32 GB and 64 GB. A small fourth disk with the label NIDASEED gives
    your public SSH key to the installer. The installer makes the user "nida"
    with that key.

    Hyper-V cannot tell the guest that a virtual disk is a solid-state disk or
    a hard disk. Thus the installer cannot find a "performance" disk here. The
    reference test for the disk plan is the QEMU test (scripts/vm-test.sh).

    The script does not change a virtual machine that exists.

.EXAMPLE
    .\new-vm.ps1 -IsoPath F:\NidaOS\install.iso -VmPath F:\HyperV-VMs\nidaos-vm
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$IsoPath,
    [string]$VmName = 'nidaos-vm',
    [string]$VmPath,
    [string]$SshPublicKeyPath = (Join-Path $env:USERPROFILE '.ssh\id_ed25519.pub'),
    [string]$SwitchName = 'Default Switch',
    [ValidateRange(1, 64)][int]$CpuCount = 4,
    [ValidateRange(4, 1024)][int]$MemoryGB = 8,
    [int[]]$DiskSizesGB = @(24, 32, 64)
)

$ErrorActionPreference = 'Stop'

if (-not $VmPath) { $VmPath = Join-Path 'C:\HyperV-VMs' $VmName }
if (-not (Test-Path -LiteralPath $IsoPath -PathType Leaf)) { throw "The installer ISO does not exist: $IsoPath" }
if (-not (Test-Path -LiteralPath $SshPublicKeyPath -PathType Leaf)) { throw "The public SSH key does not exist: $SshPublicKeyPath" }
if (Get-VM -Name $VmName -ErrorAction SilentlyContinue) { throw "The virtual machine '$VmName' exists. Remove it with remove-vm.ps1 first." }
if (-not (Get-VMSwitch -Name $SwitchName -ErrorAction SilentlyContinue)) { throw "The virtual switch '$SwitchName' does not exist." }
if ((Test-Path -LiteralPath $VmPath) -and (Get-ChildItem -LiteralPath $VmPath)) { throw "The folder '$VmPath' is not empty." }

$publicKey = (Get-Content -LiteralPath $SshPublicKeyPath -TotalCount 1).Trim()
if ($publicKey -notmatch '^(ssh-|ecdsa-|sk-)') { throw "The file '$SshPublicKeyPath' does not contain a public SSH key." }

New-Item -ItemType Directory -Path $VmPath -Force | Out-Null

Write-Host "==> Make the seed disk with the public SSH key"
$seedPath = Join-Path $VmPath 'seed.vhdx'
New-VHD -Path $seedPath -SizeBytes 64MB -Dynamic | Out-Null
$seedDisk = Mount-VHD -Path $seedPath -Passthru | Get-Disk
try {
    Initialize-Disk -Number $seedDisk.Number -PartitionStyle MBR
    $partition = New-Partition -DiskNumber $seedDisk.Number -UseMaximumSize -AssignDriveLetter
    Format-Volume -Partition $partition -FileSystem FAT -NewFileSystemLabel 'NIDASEED' -Confirm:$false | Out-Null
    $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllText("$($partition.DriveLetter):\authorized_keys", "$publicKey`n", $utf8NoBom)
} finally {
    Dismount-VHD -Path $seedPath
}

Write-Host "==> Make the virtual machine '$VmName'"
New-VM -Name $VmName -Generation 2 -MemoryStartupBytes ([int64]$MemoryGB * 1GB) -NoVHD -Path $VmPath -SwitchName $SwitchName | Out-Null
Set-VMProcessor -VMName $VmName -Count $CpuCount
Set-VMMemory -VMName $VmName -DynamicMemoryEnabled $false
Set-VMFirmware -VMName $VmName -EnableSecureBoot Off
Set-VM -VMName $VmName -AutomaticCheckpointsEnabled $false

$index = 0
foreach ($sizeGB in $DiskSizesGB) {
    $diskPath = Join-Path $VmPath ("disk{0}-{1}gb.vhdx" -f $index, $sizeGB)
    New-VHD -Path $diskPath -SizeBytes ([int64]$sizeGB * 1GB) -Dynamic | Out-Null
    Add-VMHardDiskDrive -VMName $VmName -Path $diskPath
    $index++
}
Add-VMHardDiskDrive -VMName $VmName -Path $seedPath
Add-VMDvdDrive -VMName $VmName -Path $IsoPath

# The disks are first in the boot sequence. They are empty at the first start,
# thus the firmware starts the ISO. After the installation, the system disk starts.
$dataDisks = Get-VMHardDiskDrive -VMName $VmName | Where-Object { $_.Path -ne $seedPath }
$dvd = Get-VMDvdDrive -VMName $VmName
Set-VMFirmware -VMName $VmName -BootOrder (@($dataDisks) + @($dvd))

Start-VM -Name $VmName

Write-Host ""
Write-Host "The virtual machine '$VmName' installs NidaOS now. The installation takes some minutes."
Write-Host "Open the console:      vmconnect localhost $VmName"
Write-Host "Find the IP address:   (Get-VMNetworkAdapter -VMName $VmName).IPAddresses"
Write-Host "Connect after restart: ssh nida@<IP address>"
