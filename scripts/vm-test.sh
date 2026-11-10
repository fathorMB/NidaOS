#!/usr/bin/env bash
# Install NidaOS in a QEMU virtual machine and examine the result.
#
# Usage: sudo vm-test.sh <installer.iso> <work directory>
#
# The virtual machine has UEFI firmware and three empty disks:
#   hdd    64 GiB, hard disk (rotation rate 7200)
#   ssd-b  32 GiB, solid-state disk
#   ssd-a  24 GiB, solid-state disk
# The hard disk is the first disk on the bus. Thus the test fails if the
# installer selects the disks by their sequence.
#
# Expected plan: system on ssd-a, "performance" on ssd-b, "standard" on hdd.

set -euo pipefail

ISO=$(realpath "$1")
WORK=$2
INSTALL_TIMEOUT=${INSTALL_TIMEOUT:-3000}
BOOT_TIMEOUT=${BOOT_TIMEOUT:-600}
VM_CPUS=${VM_CPUS:-2}
VM_MEMORY_MB=${VM_MEMORY_MB:-4096}

OVMF_CODE=/usr/share/OVMF/OVMF_CODE_4M.fd
OVMF_VARS=/usr/share/OVMF/OVMF_VARS_4M.fd

GIB=$((1024 * 1024 * 1024))
SIZE_SSD_A=$((24 * GIB))
SIZE_SSD_B=$((32 * GIB))
SIZE_HDD=$((64 * GIB))

mkdir -p "$WORK"
cd "$WORK"

cp "$OVMF_VARS" vars.fd
qemu-img create -q -f qcow2 hdd.qcow2 "$SIZE_HDD"
qemu-img create -q -f qcow2 ssd-b.qcow2 "$SIZE_SSD_B"
qemu-img create -q -f qcow2 ssd-a.qcow2 "$SIZE_SSD_A"

# The seed volume gives the SSH key of the test to the installer.
rm -f id_test id_test.pub
ssh-keygen -q -t ed25519 -N '' -C nida-vm-test -f id_test
mkdir -p seed
cp id_test.pub seed/authorized_keys
xorriso -as mkisofs -quiet -J -r -V NIDASEED -o seed.iso seed

QEMU=(
    qemu-system-x86_64
    -machine q35,accel=kvm -cpu host -smp "$VM_CPUS" -m "$VM_MEMORY_MB"
    -display none
    -drive "if=pflash,format=raw,readonly=on,file=$OVMF_CODE"
    -drive "if=pflash,format=raw,file=vars.fd"
    -drive "id=d0,if=none,format=qcow2,file=hdd.qcow2"
    -device "ide-hd,drive=d0,bus=ide.0,rotation_rate=7200"
    -drive "id=d1,if=none,format=qcow2,file=ssd-b.qcow2"
    -device "ide-hd,drive=d1,bus=ide.1,rotation_rate=1"
    -drive "id=d2,if=none,format=qcow2,file=ssd-a.qcow2"
    -device "ide-hd,drive=d2,bus=ide.2,rotation_rate=1"
    -netdev "user,id=n0,hostfwd=tcp:127.0.0.1:2222-:22"
    -device "virtio-net-pci,netdev=n0"
    -serial "file:serial.log"
    -monitor "unix:monitor.sock,server,nowait"
)

screenshot() {
    echo "screendump $PWD/$1 -f png" | socat - UNIX-CONNECT:monitor.sock > /dev/null 2>&1 || true
}

echo "==> Install from $ISO"
"${QEMU[@]}" \
    -drive "id=cd0,if=none,media=cdrom,readonly=on,format=raw,file=$ISO" \
    -device "ide-cd,drive=cd0,bus=ide.3,bootindex=0" \
    -drive "id=cd1,if=none,media=cdrom,readonly=on,format=raw,file=seed.iso" \
    -device "ide-cd,drive=cd1,bus=ide.4" \
    -no-reboot &
qemu_pid=$!

# The installer restarts the machine at the end. With -no-reboot, QEMU stops then.
waited=0
while kill -0 "$qemu_pid" 2>/dev/null; do
    if [ "$waited" -ge "$INSTALL_TIMEOUT" ]; then
        screenshot screen-install-timeout.png
        kill "$qemu_pid" || true
        echo "error: the installation did not complete in $INSTALL_TIMEOUT seconds" >&2
        exit 1
    fi
    sleep 30
    waited=$((waited + 30))
    screenshot screen-install.png
    [ $((waited % 300)) -eq 0 ] && echo "    installation in progress: $waited seconds"
done
wait "$qemu_pid" || true
echo "==> The installer stopped after $waited seconds"

echo "==> Start the installed system"
"${QEMU[@]}" &
qemu_pid=$!
trap 'kill "$qemu_pid" 2>/dev/null || true' EXIT

SSH=(ssh -i id_test -p 2222 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null
    -o ConnectTimeout=5 -o BatchMode=yes -o LogLevel=ERROR nida@127.0.0.1)

waited=0
until "${SSH[@]}" true 2>/dev/null; do
    if [ "$waited" -ge "$BOOT_TIMEOUT" ] || ! kill -0 "$qemu_pid" 2>/dev/null; then
        screenshot screen-boot-timeout.png
        echo "error: no SSH connection to the installed system" >&2
        exit 1
    fi
    sleep 10
    waited=$((waited + 10))
done
echo "==> SSH is available after $waited seconds"
screenshot screen-boot.png

{
    echo "## bootc status"
    "${SSH[@]}" sudo bootc status
    echo "## lsblk"
    "${SSH[@]}" lsblk -o NAME,SIZE,ROTA,TYPE,FSTYPE,LABEL,MOUNTPOINTS
    echo "## install plan"
    "${SSH[@]}" cat /etc/nidaos/install-plan.txt
    echo "## nidaos release"
    "${SSH[@]}" cat /usr/lib/nidaos/release
} | tee report.txt

# Print "<size in bytes> <rotational>" for the disk that contains a mount point.
disk_of() {
    "${SSH[@]}" "lsblk -bndo SIZE,ROTA /dev/\$(lsblk -ndo PKNAME \$(findmnt -no SOURCE $1))" | xargs
}

failures=0
check() {
    local label=$1 mount=$2 expected=$3 actual
    actual=$(disk_of "$mount")
    if [ "$actual" = "$expected" ]; then
        echo "PASS $label: $mount is on the disk '$actual'" | tee -a report.txt
    else
        echo "FAIL $label: $mount is on the disk '$actual', expected '$expected'" | tee -a report.txt
        failures=$((failures + 1))
    fi
}

# On a bootc system, /sysroot is the physical root file system.
check system /sysroot "$SIZE_SSD_A 0"
check performance /var/nida/performance "$SIZE_SSD_B 0"
check standard /var/nida/standard "$SIZE_HDD 1"

"${SSH[@]}" sudo systemctl poweroff || true
for _ in $(seq 1 30); do
    kill -0 "$qemu_pid" 2>/dev/null || break
    sleep 2
done

if [ "$failures" -ne 0 ]; then
    echo "error: $failures checks failed" >&2
    exit 1
fi
echo "==> All checks passed"
