# The NidaOS installer

This document is for the owner and for the persons that change the installer. The installer is in `core/internal/installer`, `core/cmd/nida-installer`, `console/src/installer` and `installer/` (milestone M-07, decisions D12, D32, D33 and D34).

## 1. How the installer works

1. The computer starts from the installation media (a USB key or an ISO). The media is a live Fedora bootc system without Anaconda.
2. `nida-installer.service` starts the installer program. It reads the disks with `lsblk`, reads the seed volume `NIDASEED`, and measures the read speed of each disk for 3 seconds. The test only reads: it does not change a disk.
3. The installer proposes the disk plan (decision D7):
   - The fastest disk gets the system. A solid-state disk is always before a hard disk. Two disks with a speed difference of less than 25% have "the same speed": then the smaller disk gets the system, and the larger disk stays for the data.
   - The next solid-state disk is the performance tier.
   - The largest hard disk is the standard tier. Without a hard disk, the largest disk that is not used is the standard tier.
   - The other disks are not used. The installer does not change them.
   - The installer does not use the installation media, the seed volume, removable, read-only and USB disks, and disks smaller than 8 GB.
4. `nida-kiosk.service` shows the installer page in full screen (cage and Firefox in its kiosk mode). The page has the six steps of the mockup `design/installer`. The owner can change the role of each disk.
5. On the review step, the owner writes the computer name. Then the installer:
   1. erases the disks of the plan and makes the partitions (system disk: EFI 600 MB, `/boot` 1 GB, root; tier disk: one XFS partition),
   2. installs the NidaOS image with `bootc install to-filesystem` from the container storage of the media. During the copy, `/var/tmp` is on a storage tier,
   3. adds the storage tiers to `/etc/fstab` with `nofail`,
   4. writes the computer name, the plan (`/var/lib/nidaos/install-plan.txt`), the log (`/var/lib/nidaos/install.log`) and the first-boot file,
   5. examines the boot loader and unmounts the disks.
6. At the first start of NidaOS, `nida-firstboot.service` runs `nida-core firstboot`. It makes the owner in the groups `wheel` and `nida`, writes the SSH key, sets the one owner password for nida-core and Remote Desktop (decision D34), the keyboard, the time zone and the fixed address. Then it deletes the first-boot file.

## 2. The page from an other computer

The installer page is on the port 80 of the computer: `http://<address>/`. The text console of the computer shows the address. A browser of an other computer can follow the installation, but only the screen of the computer can change the choices and start the installation. The page never shows the password.

## 3. The seed volume NIDASEED

The seed volume is a small volume with the label `NIDASEED`, for example a second USB key or an ISO file. The installer reads two files:

| File | Content |
| --- | --- |
| `authorized_keys` | The SSH public key of the owner. The access step proposes it. |
| `install.json` | Optional: the answers of an automatic installation without questions. |

`install.json` for an automatic installation (the VM test uses it):

```json
{
  "erase": "yes",
  "plan": "proposed",
  "after": "poweroff",
  "hostname": "nidaos",
  "user": "nida",
  "password": "an owner password of 12 characters or more",
  "keyboard": "it",
  "timezone": "Europe/Rome",
  "network": {"mode": "dhcp"}
}
```

| Field | Values |
| --- | --- |
| `erase` | Must be `"yes"`. Without it, the installer refuses the file. |
| `plan` | `"proposed"`: the plan of the installer. Else give `roles`, for example `{"sdb": "system", "sdc": "performance", "sda": "standard"}`. |
| `after` | `"poweroff"` (default), `"reboot"` or `"stay"`. |
| `plan_only` | `true`: the installer only makes the plan and installs nothing. The VM test reads the plan with other sets of disks. |
| `ssh_key` | Optional: a public key. The default is the key of `authorized_keys`. |
| `network` | `{"mode": "dhcp"}`, or `{"mode": "static", "interface": "enp1s0", "address": "192.168.1.50/24", "gateway": "192.168.1.1", "dns": ["1.1.1.1"]}`. |

**Security:** `install.json` has the owner password in plain text. Keep the seed volume in a safe place, and delete the file after the installation.

## 4. The API of the installer page

| Method and path | Access | Result |
| --- | --- | --- |
| `GET /api/state` | Each browser | The phase, the computer, the disks with their speed, the plan with the reasons, the steps of the installation and the last 200 log lines. `control` is `true` only for the screen of the computer. |
| `GET /api/log` | Each browser | All the log lines as text. |
| `POST /api/measure` | The screen | Measure the disks again. |
| `POST /api/install` | The screen | Start the installation with the answers. `400 bad_field` names the field that is not correct. |
| `POST /api/restart` | The screen | Restart the computer after the installation. |

## 5. Development

- `go run ./cmd/nida-installer --demo --addr 127.0.0.1:8090` (in `core/`) starts the installer with the disks of the mockup and a simulated installation. It changes no disk.
- `npx vite build --config vite.installer.config.ts` (in `console/`) builds the page into `core/internal/installerui/dist`. Then build nida-installer again.
- `NIDA_INSTALLER_URL=http://127.0.0.1:8090 npx playwright test -c playwright.installer.config.ts` runs the browser test of the six steps.
