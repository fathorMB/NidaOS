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
printf '%s\n' "$password" | go run . set-password   # set the owner password
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
| `NIDA_CORE_LOG_DIR` | `/var/lib/nidaos/logs` | The directory of the log store. `off` keeps the logs only in memory. |
| `NIDA_CORE_LOG_RETENTION` | `30d` | The time that the store keeps the log lines (for example `30d` or `720h`). |
| `NIDA_CORE_AUTH_FILE` | `/var/lib/nidaos/auth.json` | The owner password hash, the sessions and the tokens. |
| `NIDA_CORE_SOCKET` | `/run/nidaos/core.sock` | The local socket for the `nida` command on the computer. `off` closes it. |
| `NIDA_CORE_SOCKET_GROUP` | `nida` | The group that can open the local socket. |
| `NIDA_CORE_LOG_MAX_SIZE` | `10G` | The maximum size of the log store. The store deletes the oldest days first. `0` is no limit. |

To run the service on a development computer, use the copy of a NidaOS host in the tests:

```bash
NIDA_CORE_ROOT=internal/system/testdata/host go run .
```

## Paths

The full description of the API, for the persons that make an interface, is in [docs/api.md](../docs/api.md).

| Path | Usage |
| --- | --- |
| `GET /healthz` | The service is alive. No sign-in. |
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
| `GET /api/v1/logs/search` | A search in the log lines of the last 30 days. |

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

## Log store and search

The store keeps each log line on the disk for 30 days. It has one file for each day (UTC), with one JSON object on each line. At the end of a day, the store compresses the file with gzip. Each hour, the store deletes the days that are older than the retention. When the files are larger than the maximum size, it also deletes the oldest days. The store never deletes the file of today.

The journal reader keeps the cursor of its last entry in `journal.cursor` in the log directory. After a restart of the service, the reader continues after this entry. Thus the store also gets the lines of the journal from the stop time. The pod files are read from their end after a restart: the lines of the pods from the stop time are not in the store.

`GET /api/v1/logs/search` accepts `source`, `level` and `q` as the stream, and also:

| Parameter | Usage |
| --- | --- |
| `since`, `until` | A time in RFC 3339, or a duration before now, for example `90m`, `2h` or `7d`. |
| `limit` | The maximum number of lines, from 1 to 1000. The default is 100. |
| `cursor` | The `next_cursor` of the previous page. |

The result gives the lines, the newest first, and `next_cursor` for the next page (older lines). A cursor stays valid when new lines come. When the search reads files for more than 10 seconds, it stops, sets `partial` to `true` and gives a cursor to continue.

## Sign-in and access

Only these paths are open without a sign-in: `/healthz`, `/readyz`, `/api/v1/health`, `/api/v1/auth/status` and `/api/v1/auth/login`. Each other path needs one of these identities (decisions D16, D17 and D18):

| Identity | How | Level |
| --- | --- | --- |
| The owner | `POST /api/v1/auth/login` with `{"password": "..."}` gives the cookie `nida_session`. | `admin` |
| A program | The header `Authorization: Bearer nida_...` with a token. | `read` or `admin`, as the token |
| The computer | A request through the local socket. | `admin` |

The `read` level gives the `GET` paths of the state, the metrics and the logs. The `admin` level gives all the paths. Only `admin` can list, make and delete the tokens.

| Path | Usage |
| --- | --- |
| `GET /api/v1/auth/status` | `{"owner_set": true}` when the owner password is set. |
| `POST /api/v1/auth/login` | Sign in. A session closes after 30 days, or after 7 days without use. |
| `POST /api/v1/auth/logout` | Close the session of the cookie. |
| `GET /api/v1/auth/me` | The identity of the caller. |
| `PUT /api/v1/auth/password` | `{"current": "...", "new": "..."}`. A new password closes all the sessions. Through the local socket, `current` is not necessary. A token cannot change the password. |
| `GET /api/v1/tokens` | The tokens, without their secrets. |
| `POST /api/v1/tokens` | `{"name": "laptop", "level": "read"}`. The response gives the `secret` only one time. |
| `DELETE /api/v1/tokens/{id}` | Delete a token. Its secret stops at once. |

Protections:

- The service keeps only hashes: PBKDF2 with SHA-256 and 600 000 iterations for the password, SHA-256 for the sessions and the tokens. The file has the mode `0600`.
- After 5 wrong passwords from one address, the address must wait 1 second, then 2, 4, and more, to a maximum of 15 minutes.
- The cookie has `HttpOnly` and `SameSite=Strict`. A change with the cookie must come from the same site (the header `Origin`).
- The local socket has the mode `0660` and the group `nida`. When the group does not exist, only `root` can open it.

`nida-core set-password` reads the password from the first line of the standard input. When the service runs, the command sends the password through the local socket. When the service does not run, the command writes the auth file. The minimum length is 12 characters.

## The nida command

`cmd/nida` is the command for the operator, the scripts and the agents. It reads the API of `nida-core`.

```bash
go build -o nida ./cmd/nida
```

On the NidaOS computer, `nida` uses the local socket and needs no password. From an other computer, give the address of the service and a token:

```bash
export NIDA_SERVER=http://nida.local:8470 NIDA_TOKEN=nida_...
```

| Command | Output |
| --- | --- |
| `nida status` | The host, the disks, the storage tiers, the network and the busiest workloads. |
| `nida metrics` | One line with the last metrics. `--watch` (`-w`) adds one line for each new snapshot. |
| `nida logs` | The last 50 log lines. `-n` gives an other number. |
| `nida logs -f` | The last lines, then each new line. |
| `nida logs --since 2h` | A search in the store of 30 days. `--until` gives the end. |
| `nida token list` | The tokens. |
| `nida token create NAME --level read` | Make a token. The output shows the secret one time. |
| `nida token delete ID` | Delete a token. |
| `nida version` | The version of `nida` and of `nida-core`. |

The log commands accept `--source` (for example `pod/apps`), `--level` (`warning` gives the warnings and the errors) and `-q` (a text). `--json` before the command writes the JSON of the API: for the live commands, one JSON object on each line.

When a live stream stops, `nida` writes the time of the stop and the time of the last data on the standard error, then it connects again (decision D8). For the logs, it gives the number of the last line to the service, thus it does not lose lines that the service still has in memory.
