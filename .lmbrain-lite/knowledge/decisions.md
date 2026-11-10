---
title: Decisions
updated: 2026-11-10
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
