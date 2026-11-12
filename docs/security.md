# The security of NidaOS

This document tells the protections of NidaOS, the services that listen on the network, and the points that are open (milestone M-11).

## 1. The services on the network

| Port | Service | Who can open it | Protection |
| --- | --- | --- | --- |
| 22/tcp | SSH | The home network | Only the SSH key of the owner (the installer sets no password login for SSH). |
| 80/tcp, 443/tcp | Traefik: the apps and the web console | The home network | HTTPS with the NidaOS authority. Port 80 sends the browser to HTTPS for the console. |
| 3389/tcp | Remote Desktop (GNOME) | The home network | The owner password (decision D34) and TLS. The gateway does not pass it (decision D15). |
| 8470/tcp | The API of nida-core | The home network | Each path except the health needs a session or a token (decisions D16 and D17). |
| 15432-15531/tcp | The databases that the owner opens to the home network | The home network | TLS and a password only (decision D24). Closed by default. |
| 5353/udp | mDNS: the names `*.nidaos.local` | The home network | Read only. |
| 6443, 10250, 10257, 10259/tcp, 8472/udp | k3s | **Only the computer and the pods** | The table `inet nidaos` of nftables (`/usr/share/nidaos/nftables.nft`, service `nida-firewall`) drops these ports from the home network. |
| 127.0.0.1:5000/tcp | The local image registry | Only the computer | It listens only on the loopback address. The runner pods use the job endpoint. |
| 10.42.0.1:8471/tcp | The job endpoint of the runner pods | Only the pods | A job token for one app (decision D28). |

## 2. The accounts

- The owner signs in to the console, Remote Desktop and `sudo` with the one owner password of the installer (decision D34). nida-core keeps only a slow hash of it (PBKDF2, decision D16).
- `sudo` asks for the password (`/etc/sudoers.d/10-nida-wheel`). The feasibility proof had `sudo` without a password: M-11 removed it. The `nida` command needs no `sudo`: the members of the group `nida` use the local socket (decision D18).
- The tokens of programs have the level `read` or `admin`. The agent tokens have rules (decision D35).

## 3. The secrets on the disk

| File | Content | Mode |
| --- | --- | --- |
| `/var/lib/nidaos/auth.json` | The hash of the owner password, the hashes of the sessions and the tokens | 0600 |
| `/var/lib/nidaos/ca/` | The key of the NidaOS authority | 0600 |
| `/var/lib/nidaos/runners.json` | The private key of the GitHub App | 0600 |
| `/var/lib/nidaos/agents.json` | The requests of the agents that wait, with their body | 0600 |
| `/var/lib/nidaos/firstboot.json` | The choices of the installer with the owner password, until the first start | 0600, deleted at the first start |
| `/var/nida/standard/backups/` | The database backups and the daily backup of the settings | 0700, root only |

## 4. The updates

- nida-core asks the registry for a new version each day. It downloads and installs nothing without the owner (the Updates page of the console).
- An update replaces the full system in one step, and the version before stays for a rollback.

## 5. The open points

| Point | Risk | Plan |
| --- | --- | --- |
| SELinux is off in the installer and in the installed system (the installer runs `bootc install --disable-selinux`). | Less protection between the containers and the system. | M-11 task T-04: k3s-selinux and the labels of the storage tiers. |
| The images are not signed, and the computer does not verify a signature. | An attacker with access to the registry can give a changed image. | M-11 task T-01: cosign at the publication and a policy of the container tools. |
| The job endpoint and the API on 8470 use plain HTTP. | A device of the home network can read the traffic. | The console uses HTTPS (decision D29). TLS for 8470 can come later. |
| The seed volume can have `install.json` with the owner password in plain text. | A person with the USB key reads the password. | Keep the seed volume in a safe place. Delete the file after the installation. |
