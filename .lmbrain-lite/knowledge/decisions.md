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

## D28 — The runner pods get to the registry and the deploy only through the job endpoint

The lead selected this on 2026-11-11 for M-05. The local registry and the API of nida-core do not accept connections from the pods. nida-core opens a second HTTP server on the bridge of the pod network (`10.42.0.1:8471`). Each runner pod gets a job token, and the token gives only the registry paths of its app, the deploy of an image of its app, and the state of its app. Reason: the code of a workflow comes from GitHub, thus a job must not change the other apps or the settings of NidaOS. M-11 can add TLS to this endpoint.

## D29 — The console is at https://nidaos.local and its files are in nida-core

The operator selected this on 2026-11-11 for M-06. The browser opens the console at `https://nidaos.local`, through the same HTTPS entry as the apps, with a certificate of the NidaOS authority (D20). The built files of the console are in the nida-core program, thus one update changes the console and the API together. The console and the API have the same origin, thus the session cookie and the same-site check work without changes. Reason: the password and the session never go in plain text on the home network. Each device must trust the NidaOS authority one time, as for the apps.

## D30 — The console pages of the gateway and the agents come with their milestones

The operator selected this on 2026-11-11 for M-06. M-06 makes the runners page. M-10 makes the gateway page and M-09 makes the agents page, with their real data. The menu of the console shows a page only when its part of NidaOS exists. Reason: a page without data has no use, and its design can change when the part is built.

## D31 — The updates page uses bootc through the API

The lead selected this on 2026-11-11 for M-06. nida-core gives the system versions of bootc at `/api/v1/updates`, and starts `bootc upgrade --check`, `bootc upgrade`, `bootc rollback` and the restart. Only one operation runs at a time, and each operation needs the admin level. Reason: the console must not run commands on the computer, and bootc already does the update and the rollback in one step. M-11 can add the automatic checks and the signatures.

## D32 — The installer is a web page in a kiosk browser

The operator selected this on 2026-11-11 for M-07. A small Go program on the installation media measures the disks, makes the plan and runs the installation. The six steps of the mockup are a React page with the design tokens of the console. Firefox in its kiosk mode shows the page in full screen (the Fedora package cog is not the WPE browser). Reason: the installer shares the parts and the style of the console, and Playwright tests it as the console. The owner can also follow the installation from the browser of an other computer in the home network.

## D33 — The installer uses bootc install, without Anaconda

The operator selected this on 2026-11-11 for M-07. The installer makes the partitions of the plan, then runs `bootc install to-filesystem`. Reason: the installation media becomes much smaller, and the two corrections of the proof (the temporary files and the graphical target) are not necessary. The VM test examines the partitions with different sets of disks.

## D34 — One owner password at the installation

The operator selected this on 2026-11-11 for M-07. The access step asks for one owner password of 12 characters or more, two times. The installer sets it as the owner password of nida-core (D16) and as the Remote Desktop password. The SSH key of the administrator stays: from the volume `NIDASEED`, or pasted. The owner can change the two passwords separately later.

## D35 — The agents use the API through MCP, with the rules of their token

The lead selected this on 2026-11-12 for M-09, during the autonomous work of the night. **To review by the operator.** An agent token has the level `agent` and one rule for each class of operation (read, apps, databases, delete, updates, secrets): `allow`, `ask` or `deny`. The class `access` (tokens, password, GitHub App, approvals) is always refused. nida-core applies the rules to each request, thus each interface of the agent has the same limits. The MCP server is in nida-core at `POST /api/v1/mcp` (Streamable HTTP), and `nida mcp` gives the same tools over stdio. Reason: the agent needs no program on its computer, only the token and the NidaOS authority.

## D36 — An approval runs the request that the agent sent

The lead selected this on 2026-11-12 for M-09. **To review by the operator.** With the rule `ask`, nida-core keeps the full request (method, path and body) in `/var/lib/nidaos/agents.json` (mode 0600). When the owner approves it in the console, nida-core runs that request with the identity of the agent and keeps the response for the agent. Reason: the owner approves exactly the operation that the agent asked for, and the agent does not have to send it again. A request expires after 24 hours.

## D37 — The order of the milestones in the night of 2026-11-12

The lead selected this on 2026-11-12. **To review by the operator.** The operator asked the lead to continue all the approved milestones alone. The lead does M-09 (agents) and M-11 (updates and security) before M-08 (desktop), because a VM test can examine them fully. M-08 needs a visual check of the remote desktop, and M-10 and M-12 need hardware (the Raspberry Pi and the real computer).

## D38 — The desktop reads nida-core through the local socket

The lead selected this on 2026-11-12 for M-08. **To review by the operator.** The GNOME extension and the two programs (logs and files, GTK 4 in GJS) use the local socket of nida-core: the owner is in the group nida (decision D18), thus they need no token and no password. The file program opens the files with the permissions of the owner: a folder of an app that only the app can read shows a note. The folders of the storage tiers show the app or the database that uses them, from the names of the volumes of k3s. The dock button "Console" tells the address of the web console, because the image has no web browser.
