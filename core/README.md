# nida-core

`nida-core` is the core service of NidaOS (milestone M-02). It gives one API for the state of the computer, and the metrics and the logs as a live stream. The web console, the desktop, the `nida` command and the agents read from it (decision D8).

The structure comes from GitStack core (decisions D4 and D9):

| Part | Path |
| --- | --- |
| Entry point and commands | `main.go` |
| Configuration from environment variables | `internal/config` |
| HTTP router and the error format | `internal/httpserver` |
| Version of the service | `internal/version` |

The service uses only the Go standard library at this time.

## Commands

```bash
go run . serve     # start the service (the default command)
go run . version   # print the version
go test ./...      # run the tests
```

## Configuration

| Variable | Default | Usage |
| --- | --- | --- |
| `NIDA_CORE_ADDR` | `127.0.0.1:8470` | The HTTP listen address. |
| `NIDA_CORE_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. |
| `NIDA_CORE_SHUTDOWN_TIMEOUT` | `10s` | The maximum time for the open requests when the service stops. |
| `NIDA_CORE_ROOT` | `/` | The start of the paths of `/proc`, `/sys` and `/etc`. |
| `NIDA_CORE_PLAN_FILE` | `/var/lib/nidaos/install-plan.txt` | The disk plan of the installer. |

To run the service on a development computer, use the copy of a NidaOS host in the tests:

```bash
NIDA_CORE_ROOT=internal/system/testdata/host go run .
```

## Paths

| Path | Usage |
| --- | --- |
| `GET /healthz` | The service is alive. |
| `GET /readyz` | The service can accept requests. |
| `GET /api/v1/health` | The health, the version and the uptime, for the interfaces. |
| `GET /api/v1/state` | The host, the disks and the tiers in one response. |
| `GET /api/v1/system` | The host: name, operating system, kernel, uptime, processor, memory and load. |
| `GET /api/v1/disks` | The physical disks, their partitions and their role in the disk plan. |
| `GET /api/v1/tiers` | The two storage tiers (`performance`, `standard`), their state and their usage. |

A tier has one of three states:

| State | Meaning |
| --- | --- |
| `ready` | The file system of the tier is mounted on `/var/nida/<tier>`. |
| `not_mounted` | The disk plan gives a disk, but the file system is not mounted. |
| `no_disk` | The disk plan gives no disk for the tier. |

Each error has the body `{"code": "...", "message": "..."}`. When the service cannot read the state, the status is 503 and the code is `state_unavailable`.

The package `internal/system` reads only files (`/proc`, `/sys`, `/etc/os-release` and the disk plan). It does not start other programs.
