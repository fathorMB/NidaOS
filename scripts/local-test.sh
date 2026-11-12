#!/usr/bin/env bash
# Build NidaOS and test it on a local Linux computer with KVM.
# The steps are the same as in .github/workflows/image.yml.
#
# Usage: sudo local-test.sh [work directory]
#
# Necessary packages (Ubuntu 24.04):
#   podman qemu-system-x86 qemu-utils ovmf xorriso socat freerdp3-x11 openssh-client
#
# On Windows, run the script in WSL 2. Use a work directory in the Linux
# file system, not below /mnt: the build is much faster there.
#
# The ISO builder accepts only cgroups version 2. For WSL 2, add this line to
# the [wsl2] section of %USERPROFILE%\.wslconfig, then run "wsl --shutdown":
#   kernelCommandLine = cgroup_no_v1=all

set -euo pipefail

if [ "$(stat -fc %T /sys/fs/cgroup)" != cgroup2fs ]; then
    echo "error: this computer does not use cgroups version 2 only" >&2
    exit 1
fi

REPO=$(cd "$(dirname "$0")/.." && pwd)
WORK=${1:-/var/tmp/nidaos-test}
IMAGE=${IMAGE:-ghcr.io/fathormb/nidaos}
IMAGE_TAG=${IMAGE_TAG:-proof}

# Copy the sources. Thus the build does not read the slow Windows file system.
mkdir -p "$WORK"
rm -rf "$WORK/src" "$WORK/out"
mkdir -p "$WORK/src" "$WORK/out"
mkdir -p "$WORK/src/console"
(cd "$REPO/console" && tar --exclude=node_modules --exclude=test-results -cf - .) | (cd "$WORK/src/console" && tar -xf -)
cp -r "$REPO/core" "$REPO/desktop" "$REPO/image" "$REPO/installer" "$REPO/tests" "$REPO/scripts" "$WORK/src/"
cd "$WORK/src"

echo "==> Build the image"
podman build -t "$IMAGE:$IMAGE_TAG" --build-arg NIDAOS_BUILD=v1 --build-context core=core --build-context console=console --build-context desktop=desktop -f image/Containerfile image

echo "==> Build the installer image"
podman build -t localhost/nidaos-installer:proof \
    --build-arg "PAYLOAD_REF=$IMAGE:$IMAGE_TAG" --build-arg NIDAOS_BUILD=v1 \
    --build-context core=core --build-context console=console \
    -f installer/Containerfile installer

echo "==> Build the installer ISO"
podman run --rm --privileged --pull=newer \
    --security-opt label=type:unconfined_t \
    -v "$WORK/out:/output" \
    -v /var/lib/containers/storage:/var/lib/containers/storage \
    ghcr.io/osbuild/image-builder-cli:latest \
    build \
    --bootc-ref localhost/nidaos-installer:proof \
    --bootc-installer-payload-ref "$IMAGE:$IMAGE_TAG" \
    --bootc-default-fs xfs \
    bootc-generic-iso
iso=$(find "$WORK/out" -name '*.iso' | head -n1)
ls -lh "$iso"

echo "==> Build the second image version for the update test"
# The virtual machine connects to this registry as 10.0.2.2:5000.
podman rm -f nidaos-test-registry > /dev/null 2>&1 || true
podman run -d --name nidaos-test-registry -p 5000:5000 docker.io/library/registry:2
trap 'podman rm -f nidaos-test-registry > /dev/null 2>&1 || true' EXIT
podman build -t localhost:5000/nidaos:v2 --build-arg NIDAOS_BUILD=v2 --build-context core=core --build-context console=console --build-context desktop=desktop -f image/Containerfile image
podman push --tls-verify=false localhost:5000/nidaos:v2

echo "==> Install and test in a virtual machine"
rm -rf "$WORK/vm"
UPDATE_REF=10.0.2.2:5000/nidaos:v2 bash scripts/vm-test.sh "$iso" "$WORK/vm"
