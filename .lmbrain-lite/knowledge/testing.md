---
title: How to build and test NidaOS
updated: 2026-11-11
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

## Rules for scripts/vm-test.sh

- `ssh` joins its arguments into one command line, and the remote shell reads that line again. Thus quotes inside the arguments are lost. Give a remote command with quotes as one string: `"${SSH[@]}" "stat -c '%U:%G %a' /run/x"`. Send a pod or a script with a `sh -c` part as a manifest on stdin (`kubectl apply -f - < tests/x.yaml`).
- k3s can be ready before it makes the service account `default`. Wait for it before a pod of the namespace `default`.
- Give `curl` a time limit (`--max-time`): a port that drops the packets keeps it open with no limit.
- The log of a running step is not visible on GitHub. When the step "Install and test in a virtual machine" takes more than 30 minutes, cancel the run and read the log. Before M-03 the limit was 15 minutes. The app checks of M-03 add some minutes: the registry image, four image builds and five deploys.
- A command that waits in the VM (for example `nida app deploy --wait`) has `timeout` in front of it, thus a check cannot hang.
- Do not keep a file of the test in `/tmp` of the VM when a check after a restart uses it: `/tmp` is empty after each start.
- A deleted volume claim of k3s leaves its folder for some seconds: the provisioner deletes it with a helper pod. Use `retry` for a check of the folder.

## The core service in the VM

The full cycle also examines `nida-core` (milestone M-02): the version, the tiers, the metrics and the logs of real pods, the journal, the log search, the access rules and the update. The port 8470 of the VM is forwarded to `127.0.0.1:8470` of the runner.

## The app hosting in the VM

The full cycle also examines the app hosting (milestone M-03). The test builds the images of `tests/hello-app` in the VM with `podman` and pushes them to the local registry `localhost:5000`. Then it examines:

- the deploy with the health check, and the page at `https://hello.nidaos.local` with the certificate of the NidaOS authority (`curl --resolve` to the address of the VM);
- the answer of the mDNS responder (`nida-core mdns-lookup`);
- the secret, the variable and the system variables in the copy;
- a broken version: the deploy fails and version 1 stays in operation;
- a new version, a rollback, and the files of the folder after each step;
- the internal name: the app `hello` gets the page of the app `api` at `http://api/`;
- the move of the folder from the standard disk to the performance disk;
- the remove of the app, and of its internal name in the other apps;
- the app `api` after the update of the system and after the rollback.

The VM test cannot examine the mDNS answer on a different device, because the QEMU network of the runner has no multicast. The operator must examine one Windows computer and one phone in the home network (decision D19).

## The managed databases in the VM

The full cycle also examines the databases (milestone M-04). The test gives the service the image PostgreSQL 17.6 through `/etc/sysconfig/nida-core`, then 17.7, thus it can examine a minor update. It examines:

- a PostgreSQL 17 database on the performance disk, with the server as the user 70;
- an app without a port (`tests/db-client`, with `psql`) that cannot open the port before the link, and that writes with `DATABASE_URL` after the link;
- a connection without TLS: the server refuses it;
- the home network: QEMU forwards the port 15432 to the runner, and `psql` of the runner connects with `sslmode=verify-full`, the name `nidaos.local` and the NidaOS authority;
- the databases in the live metrics;
- a backup on demand, the minor update with its backup, and the restore of the backup into a new PostgreSQL 18 database (the major update);
- the disconnect, the remove, and the backups that stay.

Rule: send SQL and scripts to a pod through stdin (`kubectl exec -i ... -- sh -s <<< "$script"`). Thus ssh does not change the quotes.

## The GitHub runners in the VM

The full cycle also examines the deploy flow (milestone M-05). Without GitHub, the test examines the job endpoint: it refuses a request without a job token, it gives the `nida` command, and the runner namespace has its network policy.

The flow from a push to an app in operation needs a real GitHub App and a real private repository. The operator makes them one time:

1. Make a private repository, for example `<owner>/nidaos-ci`, with a README. The test adds the other files.
2. Make a GitHub App on the GitHub account (Settings, Developer settings, GitHub Apps, New GitHub App). Turn off the webhook. Give the repository permissions: Administration "Read and write", Actions "Read-only", Contents "Read and write", Pull requests "Read and write", Workflows "Read and write". Metadata is "Read-only" automatically.
3. Make a private key of the app, and install the app only on the test repository.
4. In the NidaOS repository on GitHub (Settings, Secrets and variables, Actions), add the secrets `NIDA_TEST_GITHUB_APP_ID` (the App ID) and `NIDA_TEST_GITHUB_APP_KEY` (all the text of the `.pem` file), and the variable `NIDA_TEST_GITHUB_REPO` (`<owner>/nidaos-ci`).

Then the test imports the app (`nida github import`), and for each run it uses its own app `ci-<run>` and its own branch `nidaos-test-<run>`. It examines:

- the connection to the private repository and the pull request with the deploy workflow;
- the merge, which starts a job on a new runner pod that is not privileged;
- the build with rootless BuildKit, the deploy of the image of the commit, and the page of the app at its https address;
- the log of the job, and a second push that gives version 2;
- no runner pod and no GitHub runner after the jobs.

At the end, the test removes its app, its branches and the GitHub App from NidaOS. A run that stops leaves its branches; the next run removes the branches that are older than one day. The secrets are not in the artifact of the test.

## The web console in the VM

- QEMU forwards the port 443 of the VM to `127.0.0.1:8443` on the runner. The curl checks run in the VM with the NidaOS authority: the certificate of `https://nidaos.local`, the 401 of the API without a sign-in, the redirect from `http://`, and the `Secure` flag of the session cookie.
- The workflow installs Playwright and Chromium in `console/` before the VM step. The VM test runs `npx playwright test` as the user of the runner (`sudo -u $SUDO_USER -H`), with `NIDA_URL=https://nidaos.local:8443` and `NIDA_RESOLVE=127.0.0.1`. The screenshots `console-*.png` go into the test results.
- Without Playwright, the test writes a SKIP line for the browser test.
- On the Windows computer, `node dev/mock.mjs 8480` in `console/` gives a fake API with sample data. The same Playwright tests run against it: `NIDA_URL=http://127.0.0.1:8480 npm run e2e`.
