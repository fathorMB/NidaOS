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
    -netdev "user,id=n0,hostfwd=tcp:127.0.0.1:2222-:22,hostfwd=tcp:127.0.0.1:3389-:3389,hostfwd=tcp:127.0.0.1:8470-:8470,hostfwd=tcp:127.0.0.1:15432-:15432,hostfwd=tcp:127.0.0.1:8443-:443"
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
    sed -e 's/\[[0-9;?]*[A-Za-z]//g' serial.log | tr -d '
' | grep -v '^[[:space:]]*$' | tail -n "$1" | cut -c1-400
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
    # owner opens the socket without sudo. ssh joins its arguments into
    # one command line, thus the remote command is one quoted string.
    [ "$("${SSH[@]}" "stat -c '%U:%G %a' /run/nidaos/core.sock")" = "root:nida 660" ] &&
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

echo "==> Examine the app hosting (M-03)"
# The test builds the images of a small web app in the virtual machine and
# pushes them to the local registry (decision D21). The rules of the
# section "Rules for scripts/vm-test.sh" in knowledge/testing.md apply: a
# remote command with quotes is one string for ssh.
REG=localhost:5000
vm_ip=$("${SSH[@]}" "ip -4 -o route get 1.1.1.1 | sed -n 's/.* src \([0-9.]*\).*/\1/p'")

registry_ready() {
    "${SSH[@]}" "curl -sf --max-time 5 http://127.0.0.1:5000/v2/ > /dev/null"
}
build_images() {
    "${SSH[@]}" "mkdir -p /tmp/hello && cat > /tmp/hello/Containerfile" < "$REPO/tests/hello-app/Containerfile" &&
        "${SSH[@]}" "cd /tmp/hello &&
            sudo podman build -q -t $REG/hello:1 --build-arg VERSION=1 . &&
            sudo podman build -q -t $REG/hello:2 --build-arg VERSION=2 . &&
            sudo podman build -q -t $REG/hello:bad --build-arg VERSION=bad --build-arg BROKEN=yes . &&
            sudo podman build -q -t $REG/api:1 --build-arg VERSION=api . &&
            for i in hello:1 hello:2 hello:bad api:1; do sudo podman push -q --tls-verify=false $REG/\$i || exit 1; done" > app-images.log 2>&1
}
nida_app() {
    # A deploy waits for the health check. timeout stops a deploy that
    # hangs, thus the VM step cannot hang (knowledge/testing.md).
    "${SSH[@]}" "sudo timeout 400 nida app $*"
}
# page gives the page of an app at its https address, with the certificate
# of the NidaOS authority. --resolve gives the address that mDNS gives to a
# device of the home network. The certificate is in the home folder of the
# user: /tmp is empty after a restart of the system.
page() {
    local app=$1 path=${2:-/}
    "${SSH[@]}" "curl -sf --max-time 10 --cacert nida-ca.crt --resolve $app.nidaos.local:443:$vm_ip https://$app.nidaos.local$path"
}
page_is() {
    [ "$(page "$1" "$2")" = "$3" ]
}
app_added() {
    nida_app add hello --repo fathor/hello --port 8080 --health /healthz \
        --folder data:/data:standard:64M --env GREETING=hi > /dev/null &&
        nida_app "secret set hello TOKEN --value s3cret-vm" > /dev/null
}
ca_installed() {
    "${SSH[@]}" "sudo nida ca > nida-ca.crt && grep -q 'BEGIN CERTIFICATE' nida-ca.crt"
}
mdns_answers() {
    [ "$("${SSH[@]}" nida-core mdns-lookup hello.nidaos.local)" = "$vm_ip" ]
}
copy_env() {
    local out
    out=$("${SSH[@]}" "sudo k3s kubectl exec -n nida-app-hello deploy/app -- sh -c 'echo \$TOKEN \$GREETING \$NIDA_APP'") &&
        [ "$out" = "s3cret-vm hi hello" ]
}
broken_version_fails() {
    # The deploy fails, and nida gives the exit code 1.
    ! nida_app "deploy hello $REG/hello:bad --wait" > deploy-bad.log 2>&1 &&
        grep -q 'version 1 stays in operation' deploy-bad.log
}
app_version_active() {
    "${SSH[@]}" "sudo nida --json app show hello" | grep -q "\"active\": $1,"
}
internal_name() {
    nida_app add api --port 8080 > /dev/null &&
        nida_app "deploy api $REG/api:1 --wait" > /dev/null &&
        retry 60 internal_name_answers
}
internal_name_answers() {
    [ "$("${SSH[@]}" "sudo k3s kubectl exec -n nida-app-hello deploy/app -- wget -qO- -T 5 http://api/")" = "nida-hello-api" ]
}
folder_on() {
    "${SSH[@]}" "sudo sh -c 'ls -d /var/nida/$1/volumes/*nida-app-hello_folder-data-$1* > /dev/null 2>&1'"
}
app_removed() {
    nida_app "remove hello --yes" > /dev/null &&
        retry 120 namespace_gone nida-app-hello
}
namespace_gone() {
    ! "${SSH[@]}" "sudo k3s kubectl get namespace $1" > /dev/null 2>&1
}
alias_gone() {
    ! "${SSH[@]}" "sudo k3s kubectl get service -n nida-app-api hello" > /dev/null 2>&1
}

expect "the local registry is ready" retry 300 registry_ready
expect "the test builds the app images and pushes them to the local registry" build_images
expect "the owner adds an app with a folder, a variable and a secret" app_added
expect "the first version passes the health check" nida_app "deploy hello $REG/hello:1 --commit aaa111 --wait"
expect "nida ca gives the certificate of the NidaOS authority" ca_installed
expect "the app answers at its https address with the certificate of NidaOS" retry 60 page_is hello / nida-hello-1
expect "the mDNS responder gives the address of the app" retry 30 mdns_answers
expect "the copy gets the secret, the variable and the name of the app" copy_env
expect "a broken version fails and the version before stays" broken_version_fails
expect "the app still answers with version 1 after the failed deploy" retry 120 page_is hello / nida-hello-1
expect "version 2 passes the health check" nida_app "deploy hello $REG/hello:2 --wait"
expect "the app answers with version 2" retry 60 page_is hello / nida-hello-2
expect "the files of the folder stay after a deploy" page_is hello /first 1
expect "the rollback to version 1 passes the health check" nida_app "rollback hello 1 --wait"
expect "the app answers with version 1 after the rollback" retry 60 page_is hello / nida-hello-1
expect "the active version is the rollback" app_version_active 4
expect "an app reaches an other app by its name" internal_name
expect "the folder is on the standard disk" folder_on standard
expect "the move of the folder to the performance tier passes" nida_app "move hello data performance --wait"
expect "the folder is on the performance disk after the move" folder_on performance
# The storage provisioner deletes the old folder some seconds after the
# claim, with a helper pod.
no_folder_on() {
    ! folder_on "$1"
}
expect "the old folder on the standard disk is deleted" retry 60 no_folder_on standard
expect "the files of the folder stay after the move" retry 60 page_is hello /first 1
{
    echo "## nida app list"
    "${SSH[@]}" sudo nida app list || true
    echo "## nida app show hello"
    "${SSH[@]}" sudo nida app show hello || true
    echo "## the failed deploy"
    cat deploy-bad.log || true
    echo "## app objects"
    "${SSH[@]}" sudo k3s kubectl get all,pvc,ingressroute -n nida-app-hello || true
    "${SSH[@]}" sudo k3s kubectl get pods,pvc -n nida-system || true
} | tee -a report.txt
expect "the remove of an app deletes its namespace" app_removed
expect "the other apps lose the internal name of the removed app" retry 60 alias_gone

echo "==> Examine the managed databases (M-04)"
# The database starts with PostgreSQL 17.6. Then the test gives the service
# the image 17.7 (a minor update) and restores a backup into PostgreSQL 18
# (the major update). The SQL goes to psql through stdin, thus ssh does not
# change the quotes (knowledge/testing.md).
CORE_ENV=/etc/sysconfig/nida-core
set_db_images() {
    printf 'NIDA_CORE_DB_IMAGES=%s\n' "$1" | "${SSH[@]}" "sudo tee $CORE_ENV > /dev/null" &&
        "${SSH[@]}" sudo systemctl restart nida-core.service &&
        retry 60 "${SSH[@]}" "sudo nida db list > /dev/null 2>&1"
}
nida_db() {
    "${SSH[@]}" "sudo timeout 600 nida db $*"
}
# client_sql runs a shell script in the app dbclient.
client_sql() {
    "${SSH[@]}" "sudo k3s kubectl exec -i -n nida-app-dbclient deploy/app -- sh -s" <<< "$1"
}
server_sql() {
    local db=$1 sql=$2
    "${SSH[@]}" "sudo k3s kubectl exec -i -n nida-db-$db pg-0 -- psql -X -q -t -A -h /var/run/postgresql -U nida_admin -d ${db//-/_}" <<< "$sql"
}
db_client_app() {
    "${SSH[@]}" "mkdir -p /tmp/dbclient && cat > /tmp/dbclient/Containerfile" < "$REPO/tests/db-client/Containerfile" &&
        "${SSH[@]}" "cd /tmp/dbclient && sudo podman build -q -t $REG/dbclient:1 . && sudo podman push -q --tls-verify=false $REG/dbclient:1" > db-client-image.log 2>&1 &&
        nida_app add dbclient > /dev/null &&
        nida_app "deploy dbclient $REG/dbclient:1 --wait" > /dev/null
}
not_linked_refused() {
    # Before the link, the network policy of k3s rejects the packets of the
    # app: the connection stops before the password check ("Connection
    # refused", or the time limit).
    ! client_sql 'PGCONNECT_TIMEOUT=5 psql "postgresql://x:y@pg.nida-db-shop-db.svc.cluster.local:5432/shop_db?sslmode=require" -c "SELECT 1"' > db-not-linked.log 2>&1 &&
        grep -Eq 'Connection refused|timeout' db-not-linked.log && ! grep -Eq 'password|pg_hba' db-not-linked.log
}
app_writes() {
    client_sql 'psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -q -c "CREATE TABLE IF NOT EXISTS proof (x int); INSERT INTO proof VALUES (42);"'
}
plain_refused() {
    # The same connection without TLS: the server refuses it.
    ! client_sql 'psql "postgresql://$PGUSER:$PGPASSWORD@$PGHOST:$PGPORT/$PGDATABASE?sslmode=disable" -c "SELECT 1"' > db-plain.log 2>&1 &&
        grep -Eq 'pg_hba.conf|SSL' db-plain.log
}
# The runner is a device of the home network: QEMU forwards its port 15432
# to the VM. psql of the runner uses the name nidaos.local and the NidaOS
# authority (sslmode=verify-full).
lan_conn() {
    local pw
    pw=$("${SSH[@]}" "sudo nida --json db connection shop-db" | grep -o '"password": "[0-9a-f]*"' | grep -o '[0-9a-f]\{32\}')
    [ -n "$pw" ] && echo "host=nidaos.local hostaddr=127.0.0.1 port=15432 dbname=shop_db user=shop_db_owner password=$pw connect_timeout=10"
}
lan_verify_full() {
    local conn
    conn=$(lan_conn) && "${SSH[@]}" "sudo nida ca" > nida-ca.crt &&
        [ "$(psql "$conn sslmode=verify-full sslrootcert=$PWD/nida-ca.crt" -X -t -A -c 'SELECT x FROM proof')" = 42 ]
}
lan_plain_refused() {
    local conn
    conn=$(lan_conn) && ! psql "$conn sslmode=disable" -X -c 'SELECT 1' > db-lan-plain.log 2>&1
}
db_metrics() {
    "${SSH[@]}" "sudo nida --json metrics" | tr -d ' \n' | grep -q '"databases":\[{"name":"shop-db","state":"ready"'
}
backup_name() {
    "${SSH[@]}" "sudo nida --json db backups shop-db" | grep -o '"name": "[^"]*-manual-pg17.dump"' | head -n 1 | cut -d'"' -f4
}
restored_on_18() {
    local b
    b=$(backup_name)
    [ -n "$b" ] && nida_db "add shop-18 --version 18 --tier standard --restore shop-db/$b --wait" > db-restore.log 2>&1 &&
        [ "$(server_sql shop-18 'SELECT x FROM proof')" = 42 ] &&
        [ "$(server_sql shop-18 "SELECT tableowner FROM pg_tables WHERE tablename = 'proof'")" = shop_18_owner ] &&
        server_sql shop-18 'SHOW server_version' | grep -q '^18\.'
}
# update_offered: after the restart of the service, the database
# controller must connect to k3s again before it accepts the update.
update_offered() {
    "${SSH[@]}" "sudo nida --json db show shop-db" | grep -q '"update_available": *"[^"]'
}
db_updated() {
    nida_db "update shop-db --wait" > db-update.log 2>&1
}
minor_updated() {
    [ "$(server_sql shop-db 'SHOW server_version')" = 17.7 ] && [ "$(server_sql shop-db 'SELECT x FROM proof')" = 42 ]
}
runs_as_70() {
    [ "$("${SSH[@]}" "sudo k3s kubectl exec -n nida-db-shop-db pg-0 -- id -u")" = 70 ]
}
client_without_url() {
    [ -z "$(client_sql 'echo $DATABASE_URL')" ]
}
unlinked() {
    nida_db "unlink shop-db dbclient" > /dev/null && retry 120 client_without_url
}
db_removed() {
    nida_db "remove shop-db --yes" > /dev/null && nida_db "remove shop-18 --yes" > /dev/null &&
        retry 120 namespace_gone nida-db-shop-db && retry 120 namespace_gone nida-db-shop-18
}
backups_stay() {
    [ -n "$(backup_name)" ]
}

expect "the service gets the image PostgreSQL 17.6" set_db_images "17=docker.io/library/postgres:17.6-alpine,18=docker.io/library/postgres:18.1-alpine"
expect "the owner adds a PostgreSQL 17 database on the performance tier" nida_db "add shop-db --version 17 --tier performance --wait"
expect "the server of the database runs as the user 70" runs_as_70
expect "the data of the database are on the performance disk" \
    "${SSH[@]}" "sudo sh -c 'ls -d /var/nida/performance/volumes/*nida-db-shop-db_data-pg-0* > /dev/null'"
expect "an app without a port starts as the database client" db_client_app
expect "an app that is not connected cannot open the port of the database" retry 30 not_linked_refused
expect "the owner connects the app to the database" nida_db "link shop-db dbclient"
expect "the app writes in the database with DATABASE_URL" retry 120 app_writes
expect "the database refuses a connection without TLS" plain_refused
expect "the owner opens the database to the home network" nida_db "lan shop-db on"
command -v psql > /dev/null || apt-get install -y -qq postgresql-client > /dev/null 2>&1 || true
expect "a device of the home network connects with verify-full and the NidaOS authority" retry 60 lan_verify_full
expect "the port of the home network refuses a connection without TLS" lan_plain_refused
expect "the live metrics show the database" retry 60 db_metrics
expect "a backup on demand passes" nida_db "backup shop-db --wait"
expect "the service gets the image PostgreSQL 17.7" set_db_images "17=docker.io/library/postgres:17.7-alpine,18=docker.io/library/postgres:18.1-alpine"
expect "the database controller offers the update to 17.7" retry 120 update_offered
expect "the minor update to 17.7 passes with a backup before it" db_updated
expect "the server runs 17.7 and keeps the data" retry 30 minor_updated
expect "the backup restores into a new PostgreSQL 18 database" restored_on_18
expect "the disconnect removes the connection from the app" unlinked
{
    echo "## nida db list"
    "${SSH[@]}" sudo nida db list || true
    echo "## nida db show shop-db"
    "${SSH[@]}" sudo nida db show shop-db || true
    echo "## nida db backups shop-db"
    "${SSH[@]}" sudo nida db backups shop-db || true
    echo "## database objects"
    "${SSH[@]}" sudo k3s kubectl get all,pvc,networkpolicy -n nida-db-shop-db || true
    cat db-update.log db-restore.log db-plain.log db-not-linked.log db-lan-plain.log 2>/dev/null || true
} | tee -a report.txt
expect "the remove of the databases deletes their namespaces" db_removed
expect "the backups stay after the remove" backups_stay
"${SSH[@]}" "sudo rm -f $CORE_ENV && sudo systemctl restart nida-core.service" || true
nida_app "remove dbclient --yes" > /dev/null || true

echo "==> Examine the GitHub runners and the deploy flow (M-05)"
# The checks of the job endpoint need no GitHub. The flow from a push to an
# app in operation needs the GitHub App and the private repository of the
# test (scripts/github-test.sh). Each run uses its own app name and branch,
# thus two runs at the same time do not see the jobs of the other run.
source "$REPO/scripts/github-test.sh"
job_endpoint_code() {
    "${SSH[@]}" "curl -s -o /dev/null -w '%{http_code}' --max-time 5 http://10.42.0.1:8471$1"
}
job_endpoint_refuses() {
    [ "$(job_endpoint_code /v2/)" = 401 ] && [ "$(job_endpoint_code /api/v1/apps)" = 401 ]
}
job_endpoint_gives_nida() {
    "${SSH[@]}" "curl -sf --max-time 10 http://10.42.0.1:8471/tools/nida | head -c 4 | od -An -c" | grep -q 'E   L   F'
}
runner_policy() {
    "${SSH[@]}" "sudo k3s kubectl get networkpolicy -n nida-runners runner-isolation" > /dev/null 2>&1
}
expect "the job endpoint refuses a request without a job token" retry 120 job_endpoint_refuses
expect "the job endpoint gives the nida command to the runner pods" job_endpoint_gives_nida
expect "the runner namespace has its network policy" retry 60 runner_policy

if gh_configured; then
    RUN=${GITHUB_RUN_ID:-$$}
    CI_APP=ci-${RUN: -8}
    CI_BRANCH=nidaos-test-$RUN
    nida_runner() {
        "${SSH[@]}" "sudo timeout 120 nida $*"
    }
    github_imported() {
        "${SSH[@]}" "umask 077 && cat > github-app.pem" < "$(gh_key_file)" &&
            nida_runner "github import --app-id $NIDA_TEST_GITHUB_APP_ID --key-file github-app.pem" > github-import.log 2>&1
        local rc=$?
        "${SSH[@]}" "rm -f github-app.pem"
        return $rc
    }
    test_branch() {
        gh_clean_old || true
        gh_new_branch "$CI_BRANCH" &&
            gh_put_file "$CI_BRANCH" Containerfile "$REPO/tests/hello-app/Containerfile" "Add the app of the NidaOS test"
    }
    ci_app_added() {
        nida_app "add $CI_APP --port 8080 --health /healthz" > /dev/null
    }
    connected() {
        nida_runner "app connect $CI_APP $NIDA_TEST_GITHUB_REPO --branch $CI_BRANCH" > github-connect.log 2>&1 &&
            grep -q '/pull/' github-connect.log
    }
    pr_number() {
        gh_api GET "/pulls?state=open&base=$CI_BRANCH" | jq -r --arg h "nidaos/deploy-$CI_APP" '.[] | select(.head.ref == $h) | .number' | head -n 1
    }
    pr_has_workflow() {
        local n
        n=$(pr_number) && [ -n "$n" ] &&
            gh_api GET "/pulls/$n/files" | jq -r '.[].filename' | grep -qx ".github/workflows/nidaos-deploy-$CI_APP.yml"
    }
    pr_merged() {
        local n
        n=$(pr_number) && [ -n "$n" ] && gh_api PUT "/pulls/$n/merge" '{"merge_method":"squash"}' > /dev/null
    }
    branch_sha() {
        gh_api GET "/git/ref/heads/$CI_BRANCH" | jq -r .object.sha
    }
    # job_state N gives the state of the job N (1 is the newest).
    job_state() {
        "${SSH[@]}" "sudo nida --json app jobs $CI_APP" | jq -r ".jobs[$(($1 - 1))].state // empty"
    }
    job_count() {
        "${SSH[@]}" "sudo nida --json app jobs $CI_APP" | jq '.jobs | length'
    }
    job_started() { [ "$(job_count)" -ge "$1" ]; }
    job_final() {
        local s
        s=$(job_state 1)
        [ "$s" = succeeded ] || [ "$s" = failed ] || [ "$s" = cancelled ]
    }
    job_succeeded() { [ "$(job_state 1)" = succeeded ]; }
    active_image() {
        "${SSH[@]}" "sudo nida --json app show $CI_APP" | jq -r '.active as $a | .versions[] | select(.number == $a) | .image'
    }
    image_of_commit() {
        [ "$(active_image)" = "localhost:5000/$CI_APP:$(cut -c1-12 <<< "$1")" ]
    }
    job_log_has_build() {
        local id
        id=$("${SSH[@]}" "sudo nida --json app jobs $CI_APP" | jq -r '.jobs[0].id') &&
            "${SSH[@]}" "sudo nida app job-log $id" > github-job.log &&
            grep -q 'nida builds' github-job.log && grep -q 'is in operation' github-job.log
    }
    second_push() {
        sed 's/^ARG VERSION=1$/ARG VERSION=2/' "$REPO/tests/hello-app/Containerfile" > hello-v2.Containerfile &&
            gh_put_file "$CI_BRANCH" Containerfile hello-v2.Containerfile "Change the app of the NidaOS test to version 2"
    }
    no_runner_pods() {
        [ -z "$("${SSH[@]}" "sudo k3s kubectl get pods -n nida-runners -o name")" ]
    }
    no_github_runners() {
        [ "$(gh_api GET "/actions/runners?per_page=100" | jq --arg l "nidaos-$CI_APP" '[.runners[] | select(any(.labels[]; .name == $l))] | length')" = 0 ]
    }
    pod_was_isolated() {
        # The runner pods run as normal users: no privileged container.
        ! grep -q '"privileged": *true' runner-pod.json && grep -q '"runAsNonRoot": *true' runner-pod.json
    }
    save_runner_pod() {
        "${SSH[@]}" "sudo k3s kubectl get pods -n nida-runners -o json" > runner-pod.json &&
            [ "$(jq '.items | length' runner-pod.json)" -ge 1 ]
    }

    expect "NidaOS imports the GitHub App of the test" github_imported
    expect "the test branch of the private repository is ready" test_branch
    expect "the owner adds the app of the test" ci_app_added
    expect "the owner connects the app to the private repository" connected
    expect "NidaOS opens a pull request with the deploy workflow" retry 60 pr_has_workflow
    expect "the merge of the pull request passes" pr_merged
    SHA1=$(branch_sha)
    expect "the push starts a deploy job" retry 300 job_started 1
    expect "a runner pod runs the job" retry 300 save_runner_pod
    expect "the runner pod is not privileged and runs as a normal user" pod_was_isolated
    retry 1200 job_final || true
    expect "the deploy job passes" job_succeeded
    expect "the app runs the image of the commit" retry 60 image_of_commit "$SHA1"
    expect "the app built on NidaOS answers at its https address" retry 120 page_is "$CI_APP" / nida-hello-1
    expect "the log of the job has the build and the deploy" retry 60 job_log_has_build
    expect "a second push gives a new version" second_push
    SHA2=$(branch_sha)
    expect "the second push starts a second job" retry 300 job_started 2
    retry 900 job_final || true
    expect "the second deploy job passes" job_succeeded
    expect "the app answers with version 2" retry 120 page_is "$CI_APP" / nida-hello-2
    expect "the app runs the image of the second commit" image_of_commit "$SHA2"
    expect "the runner pods stop after the jobs" retry 120 no_runner_pods
    expect "GitHub has no runner of NidaOS after the jobs" retry 60 no_github_runners
    {
        echo "## nida runners"
        "${SSH[@]}" sudo nida runners || true
        echo "## nida app jobs $CI_APP"
        "${SSH[@]}" sudo nida app jobs "$CI_APP" || true
        cat github-connect.log github-import.log 2>/dev/null || true
    } | tee -a report.txt
    expect "the disconnect removes the connection" nida_runner "app disconnect $CI_APP"
    nida_app "remove $CI_APP --yes" > /dev/null || true
    "${SSH[@]}" "sudo nida github remove --yes" > /dev/null || true
    gh_delete_branch "$CI_BRANCH"
    gh_delete_branch "nidaos/deploy-$CI_APP"
else
    echo "SKIP the flow from a push to an app in operation: the GitHub App of the test is not set" | tee -a report.txt
fi

echo "==> Examine the web console (M-06)"
# The console is at https://nidaos.local (decision D29): Traefik gets the
# request with the certificate of the NidaOS authority and sends it to
# nida-core. The curl checks run in the VM with the authority. Then a real
# browser on the runner opens the console through the port 8443 of QEMU.
console_curl() {
    "${SSH[@]}" "curl -s --max-time 10 --cacert nida-ca.crt --resolve nidaos.local:443:$vm_ip --resolve nidaos.local:80:$vm_ip $*"
}
console_https() {
    "${SSH[@]}" "sudo nida ca > nida-ca.crt" &&
        console_curl -f https://nidaos.local/apps | grep -q 'id="root"'
}
console_api_needs_sign_in() {
    [ "$(console_curl -o /dev/null -w '%{http_code}' https://nidaos.local/api/v1/state)" = 401 ]
}
console_redirect() {
    local out
    out=$(console_curl -o /dev/null -w "'%{http_code} %{redirect_url}'" http://nidaos.local/logs)
    case "$out" in
    "301 https://nidaos.local/logs" | "308 https://nidaos.local/logs") return 0 ;;
    esac
    echo "redirect: $out" >&2
    return 1
}
console_secure_cookie() {
    console_curl -D - -o /dev/null -H "'Content-Type: application/json'" -H "'Origin: https://nidaos.local'" \
        -d "'{\"password\":\"$owner_password\"}'" https://nidaos.local/api/v1/auth/login |
        grep -i '^set-cookie: nida_session=' | grep -qi 'secure'
}
updates_api() {
    "${SSH[@]}" "sudo curl -s --max-time 30 --unix-socket /run/nidaos/core.sock http://nida/api/v1/updates" > updates.json &&
        grep -q '"booted"' updates.json
}
console_browser() {
    # The test runs as the user of the runner: the browser is in its home.
    local dir=$PWD/console-e2e
    mkdir -p "$dir" && chmod 0777 "$dir"
    (cd "$REPO/console" && sudo -u "${SUDO_USER:-runner}" -H env \
        NIDA_URL=https://nidaos.local:8443 NIDA_RESOLVE=127.0.0.1 NIDA_PASSWORD="$owner_password" NIDA_OUT="$dir" \
        timeout 900 npx playwright test) > console-e2e.log 2>&1
    local rc=$?
    cp "$dir"/console-*.png . 2>/dev/null || true
    return $rc
}
expect "the console answers at https://nidaos.local with the certificate of NidaOS" retry 120 console_https
expect "the API through the console address needs a sign-in" console_api_needs_sign_in
expect "http://nidaos.local sends the browser to https" console_redirect
expect "the session cookie of the console has the Secure flag" console_secure_cookie
expect "the updates API gives the system version in operation" updates_api
if [ -d "$REPO/console/node_modules/@playwright" ]; then
    expect "a browser signs in to the console and opens each page with live data" console_browser
else
    echo "SKIP the browser test of the console: Playwright is not installed" | tee -a report.txt
fi
{
    echo "## console browser test"
    tail -n 40 console-e2e.log 2>/dev/null || true
    echo "## updates"
    cat updates.json 2>/dev/null || true
    echo
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
    updates_staged() {
        "${SSH[@]}" "sudo curl -s --max-time 30 --unix-socket /run/nidaos/core.sock http://nida/api/v1/updates" | grep -q '"staged":{"image":"[^"]*nidaos:v2"'
    }
    expect "the updates API shows the staged version" retry 30 updates_staged
    expect "the system restarts after the update" reboot_vm
    expect "the system runs the new image version" \
        "${SSH[@]}" grep -qx "NIDAOS_BUILD=${BUILD_AFTER:-v2}" /usr/lib/nidaos/release
    expect "the core service runs the new image version" retry 60 core_version "${BUILD_AFTER:-v2}"
    updates_rollback() {
        "${SSH[@]}" "sudo curl -s --max-time 30 --unix-socket /run/nidaos/core.sock http://nida/api/v1/updates" | grep -q '"rollback":{"image"'
    }
    expect "the updates API shows the version before for a rollback" retry 30 updates_rollback
    expect "the log store keeps its lines after the update" store_search
    expect "the apps stay in operation after the update" retry 180 page_is api / nida-hello-api
    expect "the data on the storage tiers stays after the update" \
        "${SSH[@]}" "sudo sh -c 'grep -qx performance /var/nida/performance/volumes/*/proof.txt'"
    expect "bootc prepares the rollback" "${SSH[@]}" sudo bootc rollback
    expect "the system restarts after the rollback" reboot_vm
    expect "the system runs the first image version again" \
        "${SSH[@]}" grep -qx "NIDAOS_BUILD=${BUILD_BEFORE:-v1}" /usr/lib/nidaos/release
    expect "the apps stay in operation after the rollback" retry 180 page_is api / nida-hello-api
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
