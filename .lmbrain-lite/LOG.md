# Log

One line per action. Format: `timestamp | by:<who> | <target or -> | <kind> | <message>`.

2026-11-10T18:07 | by:lead | MEM-001 | memory | created project memory
2026-11-10T18:07 | by:lead | - | note | The lead wrote PROJECT.md from the first request of the operator and gave a first technical analysis; the base distribution, the user interface type and the reuse of GitStack are open decisions.
2026-11-10T18:13 | by:lead | - | decision | The operator made decisions D1 to D7; they are in knowledge/decisions.md.
2026-11-10T18:13 | by:lead | M-01 | milestone | created 'Feasibility proof of the Fedora bootc base' with 9 tasks (proposed)
2026-11-10T18:13 | by:operator | M-01 | status | proposed -> approved
2026-11-10T18:14 | by:lead | M-01 | status | approved -> active
2026-11-10T18:18 | by:lead | M-01 | commit | Commit on branch m01-feasibility: image recipe, installer disk plan, and a workflow that tests the installation in QEMU with three disks.
2026-11-10T18:18 | by:lead | M-01/T-10 | task | added 'Write an automatic installation test in QEMU with one hard disk and two solid-state disks'
2026-11-10T18:37 | by:lead | M-01/T-03 | finding | bootc-image-builder is deprecated and fails on Fedora 45 (no enabled repositories); the installer ISO now comes from image-builder with the type bootc-generic-iso and an installer container.
2026-11-10T19:02 | by:lead | M-01/T-04 | test | GitHub Actions run 42866480399: the ISO installed NidaOS in QEMU without operator input, the system started, and the installer put the system on the small solid-state disk, the performance tier on the second one and the standard tier on the hard disk.
2026-11-10T19:33 | by:lead | M-01/T-02 | task | done — Write the minimal image recipe (Containerfile) from Fedora bootc
2026-11-10T19:33 | by:lead | M-01/T-03 | task | done — Build the image and an installer ISO in GitHub Actions
2026-11-10T19:33 | by:lead | M-01/T-04 | task | done: Proved in QEMU on GitHub Actions (run 42870718127). The Hyper-V installation is part of T-01. — Install the image in the virtual machine and start it
2026-11-10T19:33 | by:lead | M-01/T-05 | task | done — Add k3s to the image and run a test pod on two storage classes on two disks
2026-11-10T19:33 | by:lead | M-01/T-08 | task | done — Prove that a script in the installer can select the target disks from a computed plan
2026-11-10T19:33 | by:lead | M-01/T-10 | task | done — Write an automatic installation test in QEMU with one hard disk and two solid-state disks
2026-11-10T19:33 | by:lead | M-01 | test | GitHub Actions run 42870718127 passed all checks: disk plan, k3s node ready, and one pod with a volume on each storage tier. Finding: SELinux is disabled in the installed system.
2026-11-10T20:53 | by:lead | M-01 | test | Run 5 on GitHub failed: with the desktop in the image, the installation did not complete in 3000 seconds, and the log gave no cause.
2026-11-10T20:53 | by:lead | M-01 | finding | The GitHub Actions budget of the account is used up, thus the tests now run on the operator PC in WSL 2 with KVM (scripts/local-test.sh).
2026-11-10T21:56 | by:lead | - | note | The lead added two design mockups for the operator: design/installer (six installer steps) and design/desktop (monitoring desktop).
2026-11-10T22:04 | by:lead | - | note | The lead added three more design mockups: design/files (file program), design/logs (log program) and design/console (web console), and a power dialog in design/desktop.
2026-11-10T22:10 | by:lead | - | note | The lead extended design/console: app page with six tabs, "Add an app" form, database page with five tabs and "Add a database" form.
2026-11-10T22:12 | by:operator | - | decision | Each interface shows the metrics and the logs in real time, with each type of access (decision D8 in knowledge/decisions.md).
2026-11-10T22:13 | by:lead | - | note | The lead changed design/console for decision D8: live metrics and a live log on the overview, on each app page and on each database page, and a Logs page.
2026-11-10T22:15 | by:lead | M-02 | milestone | created 'Platform core: one service for state, live metrics and live logs' with 9 tasks (proposed)
2026-11-10T22:15 | by:lead | M-03 | milestone | created 'App hosting from GitHub repositories' with 9 tasks (proposed)
2026-11-10T22:15 | by:lead | M-04 | milestone | created 'Managed databases' with 10 tasks (proposed)
2026-11-10T22:15 | by:lead | M-05 | milestone | created 'GitHub runners and the deploy flow' with 7 tasks (proposed)
2026-11-10T22:15 | by:lead | M-06 | milestone | created 'Web console' with 10 tasks (proposed)
2026-11-10T22:15 | by:lead | M-07 | milestone | created 'Installer with the disk planner' with 8 tasks (proposed)
2026-11-10T22:15 | by:lead | M-08 | milestone | created 'Monitoring desktop' with 7 tasks (proposed)
2026-11-10T22:15 | by:lead | M-09 | milestone | created 'Agent access' with 6 tasks (proposed)
2026-11-10T22:15 | by:lead | M-10 | milestone | created 'Internet gateway on a Raspberry Pi' with 8 tasks (proposed)
2026-11-10T22:15 | by:lead | M-11 | milestone | created 'Updates, rollback and security' with 7 tasks (proposed)
2026-11-10T22:15 | by:lead | M-12 | milestone | created 'Installation on the real computer' with 6 tasks (proposed)
2026-11-10T22:21 | by:operator | - | decision | The operator answered seven open choices: decisions D9 to D15 in knowledge/decisions.md, with changes to M-02, M-04, M-06, M-07, M-08 and M-10.
2026-11-10T22:33 | by:lead | M-01/T-06 | finding | Run 6 showed the cause of the installation stop: the 3.8 GB image filled /var/tmp in the memory of the installer; the fix puts /var/tmp on a storage tier (run 7).
2026-11-10T23:02 | by:lead | M-01/T-07 | task | done: Run 7 passed: bootc switch to version v2, restart, data stays on the tiers, rollback, restart, version v1 again. — Publish a new image version, update the virtual machine, then go back to the old version
2026-11-10T23:02 | by:lead | M-01/T-06 | test | Run 7: the installation of the 3.8 GB image passed, but the display manager and the RDP setup did not start; run 8 tests a fix.
2026-11-10T23:27 | by:lead | M-01/T-09 | task | done: The report is in docs/feasibility-report.md. The result is a go. — Write the feasibility report with the results, the problems and the go or no-go decision
2026-11-10T23:27 | by:lead | M-01 | test | Run 8 (42910372253) passed all 21 checks: installation, disk plan, k3s on two tiers, desktop with RDP authentication, update and rollback.
2026-11-10T23:27 | by:lead | M-07/T-09 | task | added 'Keep the two corrections of the proof in the custom installer: temporary files on a disk and the graphical target'
2026-11-10T23:27 | by:lead | M-11/T-08 | task | added 'Make a local build and test cycle that is shorter than the 30 minutes of the full cycle'
2026-11-10T23:30 | by:lead | MEM-001 | memory | updated project memory
2026-11-10T23:30 | by:lead | - | note | End of the session: the lead updated PROJECT.md, added knowledge/testing.md, and committed the brain and the report to the branch m01-feasibility.
2026-11-10T23:31 | by:operator | M-02 | status | proposed -> approved
2026-11-10T23:32 | by:lead | M-01/T-01 | task | blocked: The script vm/new-vm.ps1 is ready. The operator must run it as administrator on the Windows computer. — Write the scripts that make a UEFI virtual machine with three virtual disks on Hyper-V
2026-11-10T23:32 | by:lead | M-01/T-06 | task | blocked: Run 8 in QEMU proved the desktop and the RDP sign-in. The operator must connect from Windows with Remote Desktop. — Add a minimal desktop and connect to it from Windows with Remote Desktop
2026-11-10T23:32 | by:lead | M-02 | status | approved -> active: The operator approved M-02. M-01 stays open with two tasks blocked until the operator does the manual checks.
2026-11-10T23:36 | by:lead | M-02/T-01 | task | done: The Go module core/ has the structure of GitStack core: configuration from environment variables, JSON log, clean stop, health paths and a JSON error format. go vet, go test and the Linux build pass. The workflow core.yml runs the same checks. — Make the Go project of the core service from the structure of GitStack core
2026-11-10T23:36 | by:lead | M-02/T-01 | decision | The core service uses only the Go standard library and runs on the host as a systemd service, not in a container, because it must read /proc, the disks and the journal; NATS and PostgreSQL of GitStack core are not necessary.
2026-11-10T23:36 | by:lead | M-02/T-01 | test | Local run on Windows: gofmt, go vet and go test ./... pass, and the build for Linux passes.
2026-11-10T23:37 | by:lead | M-02/T-01 | commit | Commit 9fbe3f9 on the new branch m02-core adds the core service project and the workflow core.yml.
2026-11-10T23:44 | by:lead | M-02/T-02 | task | done: The paths /api/v1/state, /system, /disks and /tiers give the host, the disks with their role in the plan, and the two tiers with their state and usage. The tests pass on Windows and on the Linux runner. — Write the core service with the API for the state of the computer, the disks and the storage tiers
2026-11-10T23:44 | by:lead | M-02/T-02 | test | The workflow core failed on Linux because the fixture had no mount point for statfs; after the fix the run passed, with the test on the real files of the runner.
2026-11-10T23:44 | by:lead | M-02/T-02 | commit | Commits 0529e1a and the fixture fix on m02-core add the state API and the package internal/system.
2026-11-10T23:51 | by:lead | M-02/T-03 | task | done: GET /api/v1/metrics/stream sends one snapshot at each interval with Server-Sent Events. The tests pass on Windows and on the Linux runner with the race detector. T-08 must examine the pod metrics with a real k3s. — Add the live stream of the metrics for the computer and for each workload
2026-11-10T23:51 | by:lead | M-02/T-03 | decision | The live streams use Server-Sent Events. A browser reads them without a library, and the gateway can pass them as one long HTTP response.
2026-11-10T23:51 | by:lead | M-02/T-03 | decision | The service finds the pods in the cgroups and reads their names from /var/log/pods. Thus it needs no access to the Kubernetes API for the metrics.
2026-11-10T23:58 | by:lead | M-02/T-04 | task | done: GET /api/v1/logs/stream sends the lines of the journal and of the pod log files, with the source and the level. It accepts filters and Last-Event-ID. The tests pass on the runner with the real journal. T-08 must examine the pod logs with a real k3s. — Add the live stream of the logs with the source and the level of each line
2026-11-10T23:58 | by:lead | M-02/T-04 | test | The workflow core passed. On the runner, a line from logger came through the real journal, and the sampler found 20 real systemd services.
2026-11-10T23:58 | by:lead | M-02/T-04 | decision | The service reads the journal with the JSON output of journalctl, because the binary format of the journal is internal to systemd.
2026-11-11T00:06 | by:lead | M-02/T-05 | task | done: The log store keeps one file for each day for 30 days, with a size limit of 10 GiB. GET /api/v1/logs/search finds old lines with filters, a time range and pages. A test with 1 million lines searched them in less than 1 second. — Keep the logs for 30 days and give a search in the old lines
2026-11-11T00:06 | by:lead | M-02/T-05 | decision | The log store uses files of JSON lines for each day, compressed with gzip, and no database. Thus the service stays one static Go program with no dependencies.
2026-11-11T00:06 | by:lead | M-02/T-05 | finding | After a restart of the service, the journal lines of the stop time come back through the saved cursor, but the pod lines of the stop time are not in the store.
2026-11-11T00:08 | by:operator | M-02/T-06 | decision | The operator answered three choices for the sign-in: decisions D16, D17 and D18 in knowledge/decisions.md.
2026-11-11T00:16 | by:lead | M-02/T-06 | task | done: Each path except the health and the sign-in needs an identity: the owner session, a token with the level read or admin, or the local socket. The tests pass on Windows and on the Linux runner. — Add the sign-in for the owner and the tokens for programs
2026-11-11T00:16 | by:lead | M-02/T-06 | test | The workflow core passed with the race detector. A test of the full service examines the TCP access, the local socket, a token and set-password through the socket.
