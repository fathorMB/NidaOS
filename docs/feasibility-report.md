# Feasibility report: Fedora bootc as the base of NidaOS

Milestone M-01. Date: 2026-11-10.

## Result

**Go.** Fedora bootc 45 can be the base of NidaOS. An automatic test installed
the system in a virtual machine and proved each technical requirement of the
proof. Two checks need the operator, because they need a Windows computer.

The evidence is run 42910372253 of the workflow `image` on the branch
`m01-feasibility`. All 21 checks of `scripts/vm-test.sh` passed.

## What the test does

1. It builds the NidaOS image from `image/Containerfile`.
2. It builds an installer ISO that contains the image.
3. It starts a QEMU virtual machine with UEFI firmware and three empty disks:
   a 64 GiB hard disk, a 32 GiB solid-state disk and a 24 GiB solid-state disk.
   The hard disk is the first disk on the bus.
4. The installer runs without questions. Then the test starts the installed
   system and examines it through SSH.

## Result for each requirement

| Requirement | Result | Evidence |
| --- | --- | --- |
| Image-based system from a recipe | Go | The image builds in 2 minutes. `bootc container lint` passes. |
| Installer ISO for an installation without a network | Go | `image-builder` makes a `bootc-generic-iso` with the image inside. |
| Installer selects the disks from a computed plan | Go | System on the 24 GiB solid-state disk, performance tier on the 32 GiB solid-state disk, standard tier on the hard disk. The sequence of the disks on the bus has no effect. |
| No default password | Go | The administrator comes from a seed volume with an SSH key. The password is locked. |
| Private cloud with two storage tiers | Go | k3s v1.37.2 runs. One pod writes on a volume of each tier, and each volume is on the correct disk. |
| Minimal desktop with Remote Desktop | Go, with one open check | GDM and gnome-remote-desktop run in system mode. An RDP client completes the authentication on port 3389. The full session from Windows is not tested. |
| Update and rollback | Go | `bootc switch` to a second image, restart, the data on the tiers stays, `bootc rollback`, restart, the first image runs again. |
| Development in virtual machines first | Go | The full test runs in GitHub Actions in approximately 30 minutes. |

## Checks that need the operator

- **Remote Desktop from Windows.** Start the virtual machine on Hyper-V with
  `vm/new-vm.ps1`, then connect with the Remote Desktop program of Windows.
  The scripts are written but nobody ran them: they need administrator rights.
- **The real computer.** The proof does not test the MSI B85M-G43 hardware
  (UEFI from 2014, Intel graphics, Realtek network). This is the work of M-12.

## Problems found and their solutions

| Problem | Solution |
| --- | --- |
| `bootc-image-builder` cannot build an ISO for Fedora 45. | Use `image-builder` with an installer image (`installer/Containerfile`). |
| The installer saved the plan in a path that does not stay. | Save it in `/var/lib/nidaos` of the installed system. |
| The image with the desktop is 3.8 GB. The installer kept the temporary files of the copy in the memory and stopped with "no space left on device". | The installer puts `/var/tmp` on a new storage tier for the time of the installation. |
| The installer in text mode leaves the text target as the default. The desktop did not start. | The image enables GDM. The installer sets the graphical target. |

## Risks that stay open

| Risk | Effect | Where it is solved |
| --- | --- | --- |
| SELinux is off in the installer and in the installed system. | Less protection between the system and the containers. | M-11 |
| The administrators use sudo without a password. | Acceptable only for the proof. | M-11 |
| The update test uses a local registry without TLS. | The path with a real registry and a signature is not proven. | M-11 |
| The planner uses the type of the disk, not a speed measurement. | Two solid-state disks of different speed can get the wrong roles. | M-07 |
| The ISO is larger than 2 GB. | The download and the copy to a USB key are slow. | M-07 |
| A build and test cycle is 30 minutes. | Slow corrections. A local test on WSL 2 needs cgroups version 2 (`scripts/local-test.sh`). | Not planned |
| Hyper-V cannot show a virtual disk as a hard disk. | The planner test with a hard disk runs only in QEMU. | Accepted |
| The desktop will be a GNOME extension (decision D11). | A GNOME update can break it. | M-08 |

## Cost of the alternative

The fallback was Ubuntu Server with autoinstall (decision D2). It is not
necessary. Fedora bootc gives the update and the rollback of the full system
as one step, and the proof shows that the installer accepts a custom disk plan.

## Recommendation

Use Fedora bootc. Start M-02 (platform core) after the operator approves it.
Do the two operator checks in parallel: they do not stop M-02.
