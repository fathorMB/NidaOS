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
REPO=$(cd "$(dirname "$0")/.." && pwd)
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
    -netdev "user,id=n0,hostfwd=tcp:127.0.0.1:2222-:22,hostfwd=tcp:127.0.0.1:3389-:3389"
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
    "${SSH[@]}" sudo cat /var/lib/nidaos/install-plan.txt || echo "(no plan file)"
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

expect() {
    local label=$1
    shift
    if "$@"; then
        echo "PASS $label" | tee -a report.txt
    else
        echo "FAIL $label" | tee -a report.txt
        failures=$((failures + 1))
    fi
}

expect "the installed system keeps the disk plan" \
    "${SSH[@]}" sudo grep -q '^os=' /var/lib/nidaos/install-plan.txt

echo "==> Examine k3s and the two storage tiers"
k3s_ready() {
    local waited=0
    until "${SSH[@]}" sudo k3s kubectl wait --for=condition=Ready node --all --timeout=10s > /dev/null 2>&1; do
        [ "$waited" -ge 600 ] && return 1
        sleep 10
        waited=$((waited + 10))
    done
}
storage_proof() {
    "${SSH[@]}" sudo k3s kubectl apply -f - < "$REPO/tests/storage-proof.yaml" &&
        "${SSH[@]}" sudo k3s kubectl wait --for=condition=Ready pod/nida-storage-proof --timeout=600s
}
expect "the k3s node is ready" k3s_ready
expect "one pod uses a volume of each storage tier" storage_proof
expect "the performance volume is on the performance disk" \
    "${SSH[@]}" "sudo sh -c 'grep -qx performance /var/nida/performance/volumes/*/proof.txt'"
expect "the standard volume is on the standard disk" \
    "${SSH[@]}" "sudo sh -c 'grep -qx standard /var/nida/standard/volumes/*/proof.txt'"
{
    echo "## k3s"
    "${SSH[@]}" sudo k3s kubectl get nodes,storageclass,pvc,pods -A -o wide || true
    "${SSH[@]}" sudo k3s kubectl describe pod nida-storage-proof | tail -n 15 || true
    echo "## failed units"
    "${SSH[@]}" systemctl --failed --no-pager || true
    echo "## selinux"
    "${SSH[@]}" getenforce || true
} | tee -a report.txt

echo "==> Examine the desktop and the RDP service"
rdp_password=$(head -c 18 /dev/urandom | base64 | tr -d '/+=')
rdp_listens() {
    local waited=0
    until "${SSH[@]}" "sudo ss -ltn | grep -q ':3389 '"; do
        [ "$waited" -ge 180 ] && return 1
        sleep 10
        waited=$((waited + 10))
    done
}
rdp_login() {
    xfreerdp3 /v:127.0.0.1:3389 /u:nida "/p:$rdp_password" /cert:ignore +auth-only > rdp-client.log 2>&1
}
expect "the display manager is active" "${SSH[@]}" systemctl is-active --quiet gdm.service
expect "the RDP setup completed" "${SSH[@]}" systemctl is-active --quiet nida-rdp-setup.service
expect "the administrator can set the RDP credentials" \
    "${SSH[@]}" "sudo grdctl --system rdp set-credentials nida $rdp_password && sudo systemctl restart gnome-remote-desktop.service"
expect "the RDP service listens on port 3389" rdp_listens
expect "an RDP client can log in to the RDP service" rdp_login
{
    echo "## rdp"
    "${SSH[@]}" sudo grdctl --system status || true
    "${SSH[@]}" systemctl status --no-pager -n 15 gnome-remote-desktop.service nida-rdp-setup.service || true
    tail -n 15 rdp-client.log || true
} | tee -a report.txt
screenshot screen-desktop.png

# Restart the virtual machine and wait for a new start of the system.
reboot_vm() {
    local old new waited=0
    old=$("${SSH[@]}" cat /proc/sys/kernel/random/boot_id)
    "${SSH[@]}" sudo systemctl reboot || true
    while true; do
        sleep 10
        waited=$((waited + 10))
        new=$("${SSH[@]}" cat /proc/sys/kernel/random/boot_id 2>/dev/null || true)
        [ -n "$new" ] && [ "$new" != "$old" ] && return 0
        [ "$waited" -ge "$BOOT_TIMEOUT" ] && return 1
    done
}

if [ -n "${UPDATE_REF:-}" ]; then
    echo "==> Update to $UPDATE_REF, then go back"
    registry=${UPDATE_REF%%/*}
    update_stage() {
        printf '[[registry]]\nlocation = "%s"\ninsecure = true\n' "$registry" |
            "${SSH[@]}" sudo tee /etc/containers/registries.conf.d/90-nida-test.conf > /dev/null &&
            "${SSH[@]}" sudo bootc switch "$UPDATE_REF"
    }
    expect "the system runs the first image version" \
        "${SSH[@]}" grep -qx "NIDAOS_BUILD=${BUILD_BEFORE:-v1}" /usr/lib/nidaos/release
    expect "bootc downloads and stages the new image" update_stage
    expect "the system restarts after the update" reboot_vm
    expect "the system runs the new image version" \
        "${SSH[@]}" grep -qx "NIDAOS_BUILD=${BUILD_AFTER:-v2}" /usr/lib/nidaos/release
    expect "the data on the storage tiers stays after the update" \
        "${SSH[@]}" "sudo sh -c 'grep -qx performance /var/nida/performance/volumes/*/proof.txt'"
    expect "bootc prepares the rollback" "${SSH[@]}" sudo bootc rollback
    expect "the system restarts after the rollback" reboot_vm
    expect "the system runs the first image version again" \
        "${SSH[@]}" grep -qx "NIDAOS_BUILD=${BUILD_BEFORE:-v1}" /usr/lib/nidaos/release
    {
        echo "## bootc status after the rollback"
        "${SSH[@]}" sudo bootc status || true
    } | tee -a report.txt
fi

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
