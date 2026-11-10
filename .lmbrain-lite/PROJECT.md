---
title: Project overview
updated: 2026-11-10
---

# Project

## What it is

NidaOS is an operating system for a home server. It is a Linux distribution with a custom installer, a private cloud for containers and managed databases, and one modern user interface. The name comes from Nidavellir.

The feasibility proof (M-01) gave a go for Fedora bootc. The development milestones M-02 to M-12 are proposed. The decisions are in `knowledge/decisions.md`. The mockups of each interface are in `design/`.

## Who uses it and why

The operator has one desktop computer at home (Intel Core i7-4790K, 32 GB of memory, two SATA solid-state disks, one hard disk, one Ethernet port, no Wi-Fi). The computer runs Ubuntu 24.04 with k3s and GitStack now. The operator wants a system that does the hosting part of GitStack without the source control part. The source code of the applications stays on github.com.

## Requirements from the operator

- An installer that examines the disks and makes the storage plan automatically: the fastest disk for the operating system, the next solid-state disk for the "performance" hosting tier, the hard disks for the "standard" hosting tier.
- Remote desktop connection from a Windows computer.
- A private cloud: managed databases and client-server web applications in containers.
- Local network and internet connections.
- One modern user interface.
- Metrics and logs in real time in each interface, with each type of access.
- Preparation for agents (for example Hermes or Claude Code) that configure and manage the computer.
- GitHub self-hosted runners that deploy the applications.

## Stack and how to run it

- Base: Fedora bootc 45, an image-based system. The recipe is `image/Containerfile`.
- Installer: an ISO from `image-builder` with an installer image (`installer/`).
- Containers: k3s, with the storage classes `nida-performance` and `nida-standard`.
- Core service: Go, with parts of GitStack core (D9). Web console: React and Vite (D10).
- Desktop: GNOME with Remote Desktop, later a GNOME extension (D11).

Development and tests use virtual machines before the real computer. See `knowledge/testing.md` for the test cycle.

## Constraints

- The target computer has an old UEFI firmware (2014) and an Intel integrated graphics adapter.
- The installer erases disks. It must show the plan and get a confirmation before it writes.

## Out of scope

- Git hosting, issues and an internal continuous integration system (GitStack has these).
