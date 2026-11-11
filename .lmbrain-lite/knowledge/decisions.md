---
title: Decisions
updated: 2026-11-11
---

# Decisions

The operator made these decisions on 2026-11-10. Each decision has a short reason.

## D1 — NidaOS is a layer on an existing distribution

NidaOS does not build a distribution from zero. It adds three parts to an existing base: the image recipe, the disk planner of the installer, and the NidaOS console with the private cloud. Reason: the base gives the kernel, the drivers and the security updates.

## D2 — The base is Fedora bootc, if the feasibility proof passes

The proposed base is Fedora in the image-based form (bootc). The operator accepts the change from Ubuntu. The feasibility must be proved in a virtual machine first. The fallback is Ubuntu Server with automatic installation.

## D3 — Two user interfaces

The main interface is a web console. A minimal graphical desktop is also available through the Remote Desktop Protocol (RDP). The desktop is mainly for monitoring.

## D4 — Reuse the hosting parts of GitStack

NidaOS reuses the application hosting and the managed databases of GitStack, if the reuse costs less than new code. Git hosting, issues and the internal continuous integration are removed. See Project Memory MEM-001.

## D5 — Access from the internet through a separate gateway

A small gateway gives the public address. The project develops it. It runs on a Raspberry Pi and is deployed with balena.io. The gateway does the authentication, as a single sign-on point. NidaOS opens no port on the home router.

## D6 — The installation erases all disks

The real installation keeps no data from the current system. The installer configures the computer from a clean state.

## D7 — Storage tiers

The installer puts the operating system on the fastest disk. The next solid-state disk is the `performance` hosting tier. The hard disks are the `standard` hosting tier.

## D8 — Metrics and logs are real-time in each interface

The operator gave this rule on 2026-11-10. Each interface shows the metrics and the logs in real time: the web console, the desktop, and the access through the gateway. The operator does not reload a page to see new data.

Consequences for the design:

- One live data service on NidaOS gives the metrics and the logs as a stream. All interfaces, the `nida` command and the agents read the same stream.
- Each page that shows a number or a log line shows the live value. A page shows clearly when the stream stops, and it shows the time of the last data.
- The stream must work through the gateway. Thus the gateway must pass long connections.
- The mockups in `design/` show this rule: `desktop`, `logs` and `console` have live values.

## D9 — The core service is in Go

The operator selected Go on 2026-11-10. GitStack core is in Go. Thus the project reuses its controllers for apps and databases with small changes.

## D10 — The web console uses React and Vite

The operator selected React and Vite on 2026-11-10. GitStack web uses them. Thus the project reuses parts of it.

## D11 — The desktop is a GNOME extension

The operator selected a GNOME extension on 2026-11-10. The lead recommended a web page in a full-screen window, because it shares the code with the console.

Consequences:

- The desktop code is separate from the console code. The monitor, the file program and the log program are GNOME programs.
- A GNOME update can break an extension. The image sets the GNOME version, and each update of the image tests the extension first.
- The desktop reads the same live stream as the console (D8).

## D12 — The installer is a custom program

The operator selected a custom program on 2026-11-10. The installation media starts the NidaOS installer in full screen, with the six steps of the mockup. The tool from the proof of M-01 (bootc) copies the system.

## D13 — Only PostgreSQL in the first version

The operator selected only PostgreSQL on 2026-11-10. Other database types can come later in a separate milestone.

## D14 — Logs in the web console, files only on the desktop

The operator made this choice on 2026-11-10. The web console has the full logs, with the filters and the search. The file program is only on the desktop: NidaOS does not give the files of the computer to a browser.

## D15 — Remote Desktop only in the home network

The operator made this choice on 2026-11-10. The gateway does not pass Remote Desktop. From the internet, the operator uses the web console.

## D16 — The owner signs in with a password

The operator selected a password on 2026-11-11. NidaOS has one owner. The core service keeps only a slow hash of the password (PBKDF2 with SHA-256, from the Go standard library). Reason: it is simple and enough inside the home network. From the internet, the gateway does its own sign-in (D5). A second factor can come later.

## D17 — Tokens for programs have two levels

The operator selected two levels on 2026-11-11. Each token has a name and a level: `read` gives the state, the metrics and the logs; `admin` gives all the operations. The owner makes and deletes the tokens. Reason: a script that only reads cannot change the computer. M-09 adds finer rules for the agents.

## D18 — The nida command on the computer uses a local socket

The operator selected a local socket on 2026-11-11. The core service also listens on a socket file that only `root` and the group `nida` can open. A request through this socket has the `admin` level, without a password or a token. Reason: the operator uses the `nida` command on the computer through SSH or the desktop, and the system permissions of the file already control the access.

## D19 — The apps have mDNS names in the home network

The operator selected mDNS on 2026-11-11. Each app has the address `<app>.nidaos.local`. The core service announces the names on the home network with multicast DNS, the same as a printer. Reason: the router needs no change. Risk: some old Android devices do not find `.local` names. The operator must examine one Windows computer, because the VM test cannot examine the Windows resolver for a name with two labels.

## D20 — NidaOS has its own certificate authority

The operator selected an own authority on 2026-11-11. The core service makes the authority at the first start and signs one certificate for `*.nidaos.local`. The operator installs the authority one time on each device. The API gives the certificate of the authority without a sign-in, because it is public data. The private key stays on the computer, in `/var/lib/nidaos`, and only `root` can read it.

## D21 — A deploy takes an image from a local registry

The operator selected a local registry on 2026-11-11. NidaOS runs a small image registry on the computer. A deploy takes an image from it. In M-03 the test pushes the image. In M-05 the GitHub runner on the computer builds the image, pushes it and starts the deploy. No image leaves the home network.

## D22 — The app controller uses client-go and a JSON file

The lead selected this on 2026-11-11 for M-03. The app controller of GitStack uses the Kubernetes client `client-go`. NidaOS keeps it, thus the reuse of D4 is possible. NidaOS has one owner and few apps. Thus a JSON file in `/var/lib/nidaos` keeps the apps, the same as the sign-in data, and NidaOS needs no database for its own data.

## D23 — PostgreSQL 17 and 18

The operator selected 17 and 18 on 2026-11-11. A new database uses 18 if the owner does not select a version. NidaOS updates a minor version only when the owner asks for it, and it makes a backup first. A major update (for example 17 to 18) is always a new database that NidaOS makes from a backup of the old database.

## D24 — The managed databases follow GitStack, with the changes for one owner

The lead selected this on 2026-11-11 for M-04, from GitStack `docs/adr/0003-database-gestiti.md`:

- Each database has its own namespace `nida-db-<name>`, a StatefulSet with one copy and a volume claim on the tier that the owner selects. The image is the official `postgres:<version>-alpine`. It runs as the user 70, not as root.
- The core service uses a superuser only through the local socket in the container. Each connected app gets its own login role, thus the disconnect of one app does not stop the other apps.
- The database accepts only TLS connections with a password, also from the apps. The network policy of the database accepts only the connected apps. The apps use `sslmode=require`: the certificate is for `nidaos.local`, thus an app cannot examine the name. The switch for the home network opens a port from 15432 to 15531 on the computer. A device of the home network uses `sslmode=verify-full` with the NidaOS authority (D20) and the host `nidaos.local`. Reason: k3s can show a connection from the home network with an address of the pod network, thus a rule "plain text only from the pods" is not safe.
- The backups are `pg_dump` files in `/var/nida/standard/backups` (the standard tier), mode 0600. NidaOS makes one each day at 03:00 and keeps the last 7. It also keeps the last 5 backups on demand. M-11 adds the encryption and a copy to an other computer.

## D25 — NidaOS uses its own GitHub App

The operator selected an own GitHub App on 2026-11-11 for M-05. The core service makes the app with the manifest flow of GitHub: the owner clicks one time in the browser and selects the repositories. The core service keeps the private key of the app in `/var/lib/nidaos` with the mode 0600. It makes installation tokens that are valid for one hour, only when it needs them. Reason: no personal token of the owner stays on the computer, and the access is only to the selected repositories.

## D26 — Each job runs in a new runner pod and BuildKit builds without root

The operator selected this on 2026-11-11 for M-05. Each job gets a new runner pod in k3s with a just-in-time registration, and the pod stops after the job. Rootless BuildKit in the pod builds the image without root and without privileges, and pushes it to the local registry (D21). The pod has CPU and memory limits. Reason: the code of a workflow comes from GitHub, thus it must not get control of the computer, and one job must not leave files for the next job.

## D27 — Runners only for private repositories

The operator selected this on 2026-11-11 for M-05. NidaOS connects a runner only to a private repository. Reason: in a public repository, a stranger can open a pull request that changes the workflow and runs code on the runner. GitHub gives the same advice for self-hosted runners.
