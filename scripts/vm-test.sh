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
INSTALL_TIMEOUT=${INSTALL_TIMEOUT:-1500}
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
    -netdev "user,id=n0,hostfwd=tcp:127.0.0.1:2222-:22,hostfwd=tcp:127.0.0.1:3389-:3389,hostfwd=tcp:127.0.0.1:8470-:8470"
    -device "virtio-net-pci,netdev=n0"
    -serial "file:serial.log"
    -monitor "unix:monitor.sock,server,nowait"
)

screenshot() {
    echo "screendump $PWD/$1 -f png" | socat - UNIX-CONNECT:monitor.sock > /dev/null 2>&1 || true
}

# Print the last lines of the serial console without the control characters.
show_serial() {
    echo "---- last $1 lines of the serial console ----"
    sed -e 's/\[[0-9;?]*[A-Za-z]//g' serial.log | tr -d '' | grep -v '^[[:space:]]*$' | tail -n "$1" | cut -c1-400
    echo "---- end of the serial console ----"
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
        show_serial 150
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
    until "${SSH[@]}" sudo k3s kubectl wait --for=condition=Ready node --all --timeout=10s > /dev/null 2>&1 &&
        "${SSH[@]}" sudo k3s kubectl get serviceaccount default > /dev/null 2>&1; do
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

echo "==> Examine the core service and the nida command (M-02)"
CORE=http://127.0.0.1:8470
journal_marker="nida-journal-proof-$(head -c 6 /dev/urandom | od -An -tx1 | tr -d ' \n')"
owner_password=$(head -c 18 /dev/urandom | base64 | tr -d '/+=')

# Run a check again until it passes, for a maximum of $1 seconds.
retry() {
    local limit=$1 waited=0
    shift
    until "$@"; do
        [ "$waited" -ge "$limit" ] && return 1
        sleep 5
        waited=$((waited + 5))
    done
}
core_version() {
    "${SSH[@]}" sudo nida version | grep -q "nida-core  $1 "
}
core_status() {
    local out
    out=$("${SSH[@]}" sudo nida status) || return 1
    echo "$out" | grep -Eq 'performance +ready' && echo "$out" | grep -Eq 'standard +ready'
}
pod_metrics() {
    "${SSH[@]}" sudo nida --json metrics | grep -q '"name": "nida-storage-proof"'
}
log_pod() {
    "${SSH[@]}" sudo k3s kubectl apply -f - < "$REPO/tests/log-proof.yaml" > /dev/null &&
        "${SSH[@]}" sudo k3s kubectl wait --for=condition=Ready pod/nida-log-proof --timeout=300s > /dev/null
}
pod_logs() {
    "${SSH[@]}" sudo nida logs --source pod/default/nida-log-proof -n 5 |
        grep -q 'WARN   pod/default/nida-log-proof/writer  level=warn msg=nida-log-proof-'
}
journal_logs() {
    "${SSH[@]}" sudo nida logs --source service/nida-vm-test -n 5 | grep -q "ERROR  service/nida-vm-test  $journal_marker"
}
store_search() {
    "${SSH[@]}" sudo nida logs --since 1h -q "$journal_marker" | grep -q "$journal_marker"
}
http_code() {
    curl -s --max-time 15 -o /dev/null -w '%{http_code}' "$@"
}
tcp_needs_sign_in() {
    [ "$(http_code "$CORE/api/v1/state")" = 401 ] && [ "$(http_code "$CORE/healthz")" = 200 ]
}
token_access() {
    local token
    token=$("${SSH[@]}" sudo nida --json token create vm-test --level read |
        sed -n 's/.*"secret": "\(nida_[a-z0-9]*\)".*/\1/p')
    [ -n "$token" ] &&
        [ "$(http_code -H "Authorization: Bearer $token" "$CORE/api/v1/metrics")" = 200 ] &&
        [ "$(http_code -H "Authorization: Bearer $token" "$CORE/api/v1/tokens")" = 403 ]
}
owner_sign_in() {
    printf '%s\n' "$owner_password" | "${SSH[@]}" sudo nida-core set-password > /dev/null &&
        [ "$(http_code -X POST -H 'Content-Type: application/json' \
            -d "{\"password\":\"$owner_password\"}" "$CORE/api/v1/auth/login")" = 200 ]
}
owner_uses_socket() {
    # The installer makes the user nida with its own group nida. Thus the
    # owner opens the socket without sudo.
    [ "$("${SSH[@]}" stat -c '%U:%G %a' /run/nidaos/core.sock)" = "root:nida 660" ] &&
        "${SSH[@]}" nida status > nida-owner.log 2>&1
}
other_user_no_socket() {
    # A user outside the group nida cannot open the socket.
    "${SSH[@]}" "sudo useradd -M -s /sbin/nologin nida-other 2>/dev/null; true" &&
        ! "${SSH[@]}" "sudo -u nida-other nida status" > nida-no-group.log 2>&1 &&
        grep -q 'group nida' nida-no-group.log
}
metrics_stream() {
    # The live stream sends a new snapshot each 2 seconds.
    "${SSH[@]}" "sudo timeout 7 nida --json metrics --watch" > metrics-stream.log 2>&1 || true
    [ "$(grep -c '"seq"' metrics-stream.log)" -ge 3 ]
}

expect "the core service is active" retry 60 "${SSH[@]}" systemctl is-active --quiet nida-core.service
expect "the core service has the version of the image" retry 30 core_version "${BUILD_BEFORE:-v1}"
expect "nida status shows the two storage tiers ready" retry 60 core_status
expect "the metrics show the pod of the storage proof" retry 60 pod_metrics
expect "the live metric stream sends a snapshot each 2 seconds" metrics_stream
expect "a pod that writes log lines starts" log_pod
expect "the logs show the lines of the pod with the source and the level" retry 60 pod_logs
"${SSH[@]}" "logger -t nida-vm-test -p user.err $journal_marker" || true
expect "the logs show a line of the journal with the source and the level" retry 30 journal_logs
expect "the log search finds the line in the store" retry 30 store_search
expect "through the network, the API needs a sign-in" tcp_needs_sign_in
expect "a read token gives the metrics but not the tokens" token_access
expect "the owner signs in with the password from nida-core set-password" owner_sign_in
expect "the owner uses nida through the local socket without sudo" owner_uses_socket
expect "a user outside the group nida cannot open the local socket" other_user_no_socket
{
    echo "## nida status"
    "${SSH[@]}" sudo nida status || true
    echo "## nida logs (pods)"
    "${SSH[@]}" sudo nida logs --source pod -n 10 || true
    echo "## nida-core"
    "${SSH[@]}" systemctl status --no-pager -n 20 nida-core.service || true
    "${SSH[@]}" sudo ls -l /run/nidaos /var/lib/nidaos /var/lib/nidaos/logs || true
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
    echo "default target: $("${SSH[@]}" systemctl get-default)"
    "${SSH[@]}" systemctl status --no-pager -n 15 gdm.service || true
    "${SSH[@]}" "sudo cat /var/lib/nidaos/install-post.log" || true
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
    expect "the core service runs the new image version" retry 60 core_version "${BUILD_AFTER:-v2}"
    expect "the log store keeps its lines after the update" store_search
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
