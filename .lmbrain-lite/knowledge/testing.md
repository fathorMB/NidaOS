---
title: How to build and test NidaOS
updated: 2026-11-10
---

# How to build and test NidaOS

## The test cycle

The workflow `.github/workflows/image.yml` does the full cycle in approximately 30 minutes:

1. It builds the image, the installer image and the installer ISO.
2. It builds a second image version and puts it in a local registry.
3. It runs `scripts/vm-test.sh`: the script installs the ISO in a QEMU virtual machine with three disks, then it examines the installed system through SSH.

The script prints one `PASS` or `FAIL` line for each check. The report of the feasibility proof is `docs/feasibility-report.md`.

## How to start a cycle

- A push to `main` or to a branch `m01-*` starts it, if the push changes `image/`, `installer/`, `tests/`, `scripts/` or the workflow.
- `gh workflow run image --ref <branch> -f upload_iso=true` starts it and keeps the ISO as an artifact for 3 days.
- Read the result with `gh run view <id> --log`. The artifact `vm-test-results` has the screenshots of the virtual machine and the serial console.

## Limits

- GitHub Actions is free only while the repository is public. On 2026-11-10 the Actions budget of the account was used up, and a private repository cannot start a run.
- When the installation stops, the cause is on the screen of the virtual machine, not on the serial console. Read `screen-install-timeout.png` in the artifact.
- `scripts/local-test.sh` does the same cycle on a local Linux computer. On the WSL 2 of the operator it stops at the ISO build: WSL uses cgroups version 1, and the builder accepts only version 2. The correction is one line in `.wslconfig`. The operator did not approve this change.
- Docker Desktop is not in operation on the Windows computer, and the agent has no administrator rights. Thus the agent cannot use Hyper-V.

## The Hyper-V virtual machine for manual checks

`vm/new-vm.ps1` makes a virtual machine with three disks and a seed volume with the SSH key of the operator. Nobody ran the script. It needs administrator rights and the installer ISO on the Windows computer.
