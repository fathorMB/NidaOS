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
| `NIDA_CORE_POD_LOG_DIR` | `/var/log/pods` | The pod logs of the kubelet. The metrics read the pod names from it. |
| `NIDA_CORE_METRICS_INTERVAL` | `2s` | The time between two metric snapshots. The minimum is `250ms`. |
| `NIDA_CORE_JOURNALCTL` | `journalctl` | The command that reads the systemd journal. `off` stops the journal source. |
| `NIDA_CORE_LOG_BUFFER_LINES` | `5000` | The number of log lines in memory, for the new clients and the reconnections. |

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
| `GET /api/v1/metrics` | The last metric snapshot. |
| `GET /api/v1/metrics/stream` | The live stream of the metric snapshots (Server-Sent Events). |
| `GET /api/v1/logs` | The last log lines in memory. |
| `GET /api/v1/logs/stream` | The live stream of the log lines (Server-Sent Events). |

## Live metrics

One sampler reads the counters of the kernel at each interval and sends the same snapshot to all the clients (decision D8). A snapshot has:

- the host: the processor usage (total and for each thread), the processor temperature when a sensor is available, the memory, the load, the input and output of each disk, and the traffic of each network interface;
- the workloads: each pod of k3s and each systemd service, with the processor usage in cores and the memory.

The service finds the pods in the cgroups (version 2) and reads their names from the directories in `/var/log/pods`. Thus it needs no access to the Kubernetes API.

The stream uses Server-Sent Events. A browser reads it with `EventSource`:

```js
const metrics = new EventSource("/api/v1/metrics/stream");
metrics.addEventListener("metrics", (e) => show(JSON.parse(e.data)));
```

The stream sends the last snapshot first, then each new snapshot as the event `metrics`. The `id` of an event is the `seq` of its snapshot. With no new data for 15 seconds, the stream sends a comment line. Each snapshot has its `time`: an interface shows this time when the stream stops. A slow client does not stop the other clients: it does not get the snapshots that it cannot receive.

A tier has one of three states:

| State | Meaning |
| --- | --- |
| `ready` | The file system of the tier is mounted on `/var/nida/<tier>`. |
| `not_mounted` | The disk plan gives a disk, but the file system is not mounted. |
| `no_disk` | The disk plan gives no disk for the tier. |

Each error has the body `{"code": "...", "message": "..."}`. When the service cannot read the state, the status is 503 and the code is `state_unavailable`.

The package `internal/system` reads only files (`/proc`, `/sys`, `/etc/os-release` and the disk plan). It does not start other programs.

## Live logs

Two sources give the log lines:

- the systemd journal, for the services of the host (with `journalctl --follow --output=json`);
- the files of the kubelet in `/var/log/pods`, for the containers of the pods.

Each line has a sequence number (`seq`), a time, a source, a level and a message. The source `id` has the form `service/<unit>` or `pod/<namespace>/<pod>/<container>`. The level is `debug`, `info`, `warning` or `error`. For the journal, the level comes from the priority. For the pods, the service finds the level in the text: a JSON field `level`, a field `level=...`, a word at the start of the line, or the prefix of klog.

Both log paths accept these parameters:

| Parameter | Usage |
| --- | --- |
| `source` | A prefix of the source `id`, for example `pod/apps` or `service/k3s`. |
| `level` | The minimum level. `warning` gives the warnings and the errors. |
| `q` | A text in the message. The comparison ignores the case. |
| `tail` | The number of old lines first, from 0 to 5000. The default is 200. |

The stream sends the old lines first, then each new line as the event `log`. The `id` of an event is the `seq` of its line. When a browser connects again, it sends the header `Last-Event-ID`. Then the stream sends the lines that the client did not get, if they are still in memory. When a client is too slow and does not get some lines, the stream sends the event `dropped` with the number of lost lines.

The memory keeps only the last lines. The storage for 30 days and the search in the old lines come in task T-05.
