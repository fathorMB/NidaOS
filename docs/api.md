# The API of nida-core (version 1)

This document is for the persons that make an interface for NidaOS: the web console, the desktop applications, the scripts and the agents. It tells all the paths of the API, the sign-in, the live streams, the errors and the shape of each JSON body.

`nida-core` is the only source of the state, the metrics and the logs of the computer (decision D8). An interface must not read `/proc`, the journal or the files of the pods itself.

## 1. Connection

| Connection | Address | Identity |
| --- | --- | --- |
| Network (HTTP) | `http://<computer>:8470` | A session cookie or a token (section 2). |
| Local socket | `/run/nidaos/core.sock` (Unix socket) | `local`, with the level `admin`. Only `root` and the members of the group `nida` can open it. |

- All the paths start with `/api/v1/`, except the probes `/healthz` and `/readyz`.
- The service does not use TLS. When an interface goes through the network of an other computer, put a proxy with TLS in front of the service. The proxy must send `X-Forwarded-Proto: https`, thus the session cookie gets the `Secure` flag.
- A path that has no data source in the configuration does not exist (404). For example, `/api/v1/logs/search` does not exist when `NIDA_CORE_LOG_DIR=off`.

Example with the local socket:

```sh
curl --unix-socket /run/nidaos/core.sock http://nida/api/v1/state
```

## 2. Sign-in and access

### 2.1 The identities

| Kind | How to get it | Level |
| --- | --- | --- |
| `owner` | `POST /api/v1/auth/login` with the owner password. The response sets the cookie `nida_session`. | `admin` |
| `token` | The header `Authorization: Bearer nida_...`. An `admin` makes the token with `POST /api/v1/tokens`. | `read` or `admin` |
| `local` | A request through the local socket. It needs no secret. | `admin` |

- A browser interface uses the cookie. It does not keep a secret in JavaScript: the cookie is `HttpOnly` and `SameSite=Strict`.
- A script, a program or an agent uses a token.
- A session closes after 30 days, or after 7 days without use. A change of the owner password closes all the sessions.

### 2.2 The rules of the paths

| Paths | Access |
| --- | --- |
| `/healthz`, `/readyz`, `/api/v1/health`, `/api/v1/auth/status`, `/api/v1/auth/login` | Open. No identity is necessary. |
| `/api/v1/auth/logout`, `/api/v1/auth/me` | Each identity. |
| `/api/v1/tokens`, `/api/v1/tokens/{id}`, `/api/v1/auth/password` | The level `admin`. |
| Each other `GET` | The level `read` or `admin`. |
| Each other method | The level `admin`. |

### 2.3 The protection against other sites

When a request uses the session cookie and its method is not `GET`, `HEAD` or `OPTIONS`, the header `Origin` must be the host of the request. If not, the response is `403 cross_site`. A request without `Origin` (not from a browser) is accepted.

### 2.4 Too many wrong passwords

After 5 wrong passwords from one address, the address must wait. The wait doubles after each new failure, to a maximum of 15 minutes. A correct password resets the count. While the address must wait, the service answers `429 too_many_attempts` with the header `Retry-After` (seconds).

## 3. The general rules of the responses

- Each JSON response has `Content-Type: application/json` and `Cache-Control: no-store`.
- The times are in RFC 3339 format, in UTC (for example `2026-11-11T09:30:00.123456789Z`).
- The sizes are in bytes. The fields have the unit in the name: `_bytes`, `_seconds`, `_percent`, `_per_second`, `_c` (degrees Celsius).
- A list is always in an object (for example `{"disks": [...]}`), thus the API can add fields later.
- **The API can add new fields in version 1.** An interface must ignore the fields that it does not know. A field with `omitempty` in this document is not in the body when it has no value.
- A body of a request must have only the known fields. An unknown field gives `400 bad_body`. The maximum size of a body is 4 KiB.

### 3.1 The errors

Each error has this body:

```json
{"code": "sign_in_required", "message": "Sign in, or give a token in the Authorization header."}
```

- Use `code` in the program. It does not change.
- `message` is for a person. Its text can change.

| Status | `code` | Cause |
| --- | --- | --- |
| 400 | `bad_body` | The body is not a JSON object with the correct fields. |
| 400 | `bad_level` | The `level` parameter of the logs, or the level of a new token, is not valid. |
| 400 | `bad_tail` | `tail` is not a number from 0 to 5000. |
| 400 | `bad_since`, `bad_until`, `bad_range` | The time range of a search is not valid. |
| 400 | `bad_limit` | `limit` is not a number from 1 to 1000. |
| 400 | `bad_cursor` | The search cursor is not valid. Start the search again without a cursor. |
| 400 | `bad_name` | The name of a new token is not valid. |
| 400 | `weak_password` | The new password has fewer than 12 characters. |
| 401 | `sign_in_required` | The request has no valid session or token. The header `WWW-Authenticate: Bearer realm="nidaos"` is in the response. |
| 401 | `wrong_password` | The password is not correct. |
| 403 | `forbidden` | The level of the identity does not give this operation. |
| 403 | `cross_site` | The request comes from an other site (section 2.3). |
| 404 | `not_found` | The path does not exist. |
| 404 | `token_not_found` | The token does not exist. |
| 405 | `method_not_allowed` | The path does not accept this method. The header `Allow` gives the accepted methods. |
| 409 | `owner_not_set` | The owner password is not set. Set it on the computer with `nida-core set-password`. |
| 429 | `too_many_attempts` | Too many wrong passwords (section 2.4). |
| 500 | `sign_in_failed`, `password_failed`, `token_failed`, `search_failed`, `stream_unsupported` | An internal problem. The cause is in the log of the service. |
| 503 | `state_unavailable` | The service cannot read the state of the computer. |
| 503 | `metrics_unavailable` | The first metric snapshot is not ready. Try again after some seconds. |

## 4. The paths

| Method and path | Section |
| --- | --- |
| `GET /healthz`, `GET /readyz`, `GET /api/v1/health` | 4.1 |
| `GET /api/v1/state`, `/system`, `/disks`, `/tiers` | 4.2 |
| `GET /api/v1/metrics` | 4.3 |
| `GET /api/v1/metrics/stream` | 5.2 |
| `GET /api/v1/logs` | 4.4 |
| `GET /api/v1/logs/stream` | 5.3 |
| `GET /api/v1/logs/search` | 4.5 |
| `GET /api/v1/auth/status`, `POST /api/v1/auth/login`, `POST /api/v1/auth/logout`, `GET /api/v1/auth/me`, `PUT /api/v1/auth/password` | 4.6 |
| `GET /api/v1/tokens`, `POST /api/v1/tokens`, `DELETE /api/v1/tokens/{id}` | 4.7 |

### 4.1 Health

`GET /healthz`, `GET /readyz` and `GET /api/v1/health` give the same body. They need no identity.

```json
{"status": "ok", "version": "45.20261111.0", "uptime": "2h13m5s"}
```

- `version` is the version of the NidaOS image.
- `uptime` is the time since the service started, in the Go duration format.

### 4.2 The state of the computer

`GET /api/v1/state` gives all the state in one response. The other three paths give one part of it.

```json
{
  "time": "2026-11-11T09:30:00Z",
  "system": { ... },
  "disks": [ ... ],
  "tiers": [ ... ]
}
```

#### `GET /api/v1/system`

```json
{
  "time": "2026-11-11T09:30:00Z",
  "hostname": "nida",
  "os": {"name": "Fedora Linux", "version": "45", "pretty_name": "NidaOS 45", "image_version": "45.20261111.0"},
  "kernel": "6.17.4-300.fc45.x86_64",
  "uptime_seconds": 8000.5,
  "cpu": {"model": "Intel(R) Core(TM) i5-8500T", "threads": 6},
  "memory": {"total_bytes": 16624558080, "available_bytes": 12001234944},
  "load": {"one": 0.42, "five": 0.35, "fifteen": 0.3}
}
```

`os.image_version` is `omitempty`.

#### `GET /api/v1/disks`

```json
{
  "disks": [
    {
      "name": "nvme0n1",
      "model": "Samsung SSD 980 1TB",
      "size_bytes": 1000204886016,
      "kind": "nvme",
      "transport": "nvme",
      "removable": false,
      "read_only": false,
      "role": "performance",
      "partitions": [
        {"name": "nvme0n1p1", "size_bytes": 1000203837440, "mount_point": "/var/nida/performance", "fs_type": "xfs"}
      ]
    }
  ]
}
```

| Field | Values |
| --- | --- |
| `kind` | `nvme`, `ssd`, `hdd` |
| `transport` | `nvme`, `sata`, `usb`, `virtio`, `other` |
| `role` | `os`, `performance`, `standard`, `unused`, or `""` when the disk plan does not name the disk |
| `partitions[].mount_point`, `partitions[].fs_type` | `omitempty`: the partition is not mounted |

#### `GET /api/v1/tiers`

```json
{
  "tiers": [
    {
      "name": "performance",
      "storage_class": "nida-performance",
      "path": "/var/nida/performance",
      "disk": "nvme0n1",
      "device": "nvme0n1p1",
      "fs_type": "xfs",
      "state": "ready",
      "usage": {"total_bytes": 999653638144, "used_bytes": 7254491136, "free_bytes": 992399147008}
    }
  ]
}
```

| Field | Values |
| --- | --- |
| `name` | `performance`, `standard` (decision D7) |
| `state` | `ready`: the disk of the tier is mounted. `not_mounted`: the plan gives a disk, but it is not mounted on `path`. `no_disk`: the plan gives no disk for the tier. |
| `device`, `fs_type` | `omitempty`: there is no mount on `path`. |
| `usage` | `omitempty`: the tier is not `ready`. |

### 4.3 The last metric snapshot

`GET /api/v1/metrics` gives the last snapshot. Use it for a single view (for example `nida status`). For a live view, use the stream (section 5.2). The service makes a snapshot each 2 seconds (`NIDA_CORE_METRICS_INTERVAL`).

```json
{
  "seq": 4012,
  "time": "2026-11-11T09:30:00Z",
  "interval_seconds": 2,
  "host": {
    "cpu": {"usage_percent": 12.5, "threads": [10.2, 15.1, 8, 20.4, 9.9, 11.3], "temperature_c": 48},
    "memory": {"total_bytes": 16624558080, "used_bytes": 4623323136, "available_bytes": 12001234944, "used_percent": 27.8},
    "load": [0.42, 0.35, 0.3],
    "disks": [
      {"name": "nvme0n1", "read_bytes_per_second": 0, "write_bytes_per_second": 40960, "busy_percent": 0.4}
    ],
    "network": [
      {"name": "enp1s0", "receive_bytes_per_second": 1520.5, "transmit_bytes_per_second": 880}
    ]
  },
  "workloads": [
    {"kind": "pod", "namespace": "apps", "name": "photos-7d9f", "id": "3f2a9c1e-...", "cpu_cores": 0.25, "memory_bytes": 157286400},
    {"kind": "service", "name": "k3s", "id": "k3s.service", "cpu_cores": 0.12, "memory_bytes": 612368384}
  ]
}
```

- `seq` increases by one for each snapshot. It starts again at 1 when the service starts again.
- The rates and the percents are for the interval `interval_seconds` before `time`.
- `host.cpu.threads` gives the usage of each processor thread, in percent.
- `host.cpu.temperature_c` is `omitempty`: the computer has no temperature sensor that the service can read.
- `host.load` is the load average of 1, 5 and 15 minutes.
- `host.disks` has only the physical disks. `host.network` has the network interfaces, without the loopback (`lo`) and the pod ends (`veth...`).
- `workloads[].kind` is `pod` (a pod of k3s) or `service` (a systemd service).
  - For a pod, `id` is the UID of the pod. `namespace` and `name` come from the log directory of the pod. For a new pod, `name` can be the UID during some seconds.
  - For a service, `id` is the unit name and `namespace` is not in the body.
- `cpu_cores` is the processor usage in cores: `1.0` is one full thread.

### 4.4 The last log lines

`GET /api/v1/logs` gives the last lines that are in the memory of the service (the last 5000 lines, `NIDA_CORE_LOG_BUFFER_LINES`), the oldest first.

| Parameter | Value |
| --- | --- |
| `source` | A prefix of the source id. `service/k3s` selects `service/k3s`, but not `service/k3s-agent`. `pod/apps` selects all the pods of the namespace `apps`. |
| `level` | The minimum level: `debug`, `info`, `warning` or `error`. The usual synonyms are accepted, for example `warn` or `err`. |
| `q` | A text in the message. The case of the letters is not important. |
| `tail` | The maximum number of lines, from 0 to 5000. The default is 200. |

```json
{
  "lines": [
    {
      "seq": 90215,
      "time": "2026-11-11T09:30:01.5Z",
      "source": {"kind": "pod", "id": "pod/apps/photos-7d9f/web", "name": "photos-7d9f", "namespace": "apps", "container": "web"},
      "level": "warning",
      "message": "level=warn msg=\"disk is slow\""
    },
    {
      "seq": 90216,
      "time": "2026-11-11T09:30:02Z",
      "source": {"kind": "service", "id": "service/k3s", "name": "k3s"},
      "level": "info",
      "message": "Started the k3s agent"
    }
  ]
}
```

| Field | Values |
| --- | --- |
| `seq` | Increases by one for each line. It starts again at 1 when the service starts again. |
| `source.kind` | `service`: a unit or a program of the systemd journal. `pod`: a container of a pod of k3s. |
| `source.id` | `service/<unit>` or `pod/<namespace>/<pod>/<container>`. |
| `source.name` | The short name for a view: the unit, or the pod. |
| `source.namespace`, `source.container` | Only for a pod. |
| `level` | `debug`, `info`, `warning`, `error`. For the journal, the service uses the priority of the line. For a pod, it reads the level in the text (JSON fields, `level=`, a prefix such as `WARN`, or the klog prefix). Without a level in the text, the line is `info`. A line on stderr is not an error: many programs write all their log on stderr. |

### 4.5 The log search

`GET /api/v1/logs/search` finds the lines on the disk: the last 30 days, to a maximum of 10 GiB. It gives the newest line first.

| Parameter | Value |
| --- | --- |
| `source`, `level`, `q` | The same as in section 4.4. |
| `since`, `until` | A time in RFC 3339, or a duration before now: for example `90m`, `2h`, `7d`. |
| `limit` | The maximum number of lines in one page, from 1 to 1000. The default is 100. |
| `cursor` | The `next_cursor` of the previous page. |

```json
{"lines": [ ... ], "next_cursor": "2026-11-10:48213", "partial": true}
```

- `lines` has the same shape as in section 4.4. The `seq` of an old line is the `seq` at the time of the line: it is not unique across the restarts of the service.
- `next_cursor` is `omitempty`: there are no more lines. To get the next page (older lines), send the same parameters and `cursor`. Do not make a cursor yourself: its format can change.
- `partial` is `omitempty`. It is `true` when the search stopped after 10 seconds. Then the page has fewer lines than `limit`, but `next_cursor` continues the search.

### 4.6 Sign-in

#### `GET /api/v1/auth/status` (open)

```json
{"owner_set": true}
```

When `owner_set` is `false`, the sign-in page tells the person to set the password on the computer with `nida-core set-password`.

#### `POST /api/v1/auth/login` (open)

Request: `{"password": "..."}`

Response `200`, and the cookie `nida_session`:

```json
{"expires": "2026-12-11T09:30:00Z"}
```

Errors: `401 wrong_password`, `409 owner_not_set`, `429 too_many_attempts`.

#### `POST /api/v1/auth/logout` (each identity)

It closes the session of the cookie and removes the cookie. Response: `204` without a body.

#### `GET /api/v1/auth/me` (each identity)

```json
{"kind": "token", "name": "home-assistant", "level": "read", "token_id": "k7q2mxa4bcdz"}
```

- `kind` is `owner`, `token` or `local`.
- `token_id` is only for a token.

Use this path to show or hide the commands for the `admin` level.

#### `PUT /api/v1/auth/password` (`admin`, not a token)

Request: `{"current": "...", "new": "..."}`

- A session must give the `current` password. Through the local socket, `current` is not necessary: the owner can set a new password when they forgot it.
- The new password must have 12 characters or more.
- After the change, all the sessions are closed. A browser must sign in again.

Response: `204` without a body. Errors: `400 weak_password`, `401 wrong_password`, `403 forbidden` (a token), `429 too_many_attempts`.

### 4.7 Tokens (`admin`)

#### `GET /api/v1/tokens`

```json
{
  "tokens": [
    {"id": "k7q2mxa4bcdz", "name": "home-assistant", "level": "read", "hint": "nida_xk3p", "created": "2026-11-01T10:00:00Z", "last_used": "2026-11-11T09:29:58Z"}
  ]
}
```

- `hint` is the start of the token, to find it in a list.
- `last_used` is `omitempty`: nobody used the token.
- The secret of a token is never in this list.

#### `POST /api/v1/tokens`

Request: `{"name": "home-assistant", "level": "read"}`

- `name`: 1 to 64 characters: letters, digits, `-`, `_`, `.` or space.
- `level`: `read` or `admin`.

Response `201`: the fields of a token, and `secret`.

```json
{"id": "k7q2mxa4bcdz", "name": "home-assistant", "level": "read", "hint": "nida_xk3p", "created": "2026-11-11T09:30:00Z", "secret": "nida_xk3p..."}
```

**This response is the only place where the secret is visible.** The service keeps only a hash of it. Show the secret one time, and tell the person to copy it.

#### `DELETE /api/v1/tokens/{id}`

Response: `204` without a body. Error: `404 token_not_found`. The token stops at once.

## 5. The live streams

### 5.1 The format

The streams use Server-Sent Events (`Content-Type: text/event-stream`). Each event has this format:

```text
id: 4012
event: metrics
data: {"seq":4012,"time":"2026-11-11T09:30:00Z",...}

```

- `data` is always one line of JSON.
- When there is no data, the service sends a comment each 15 seconds: `: heartbeat`. The comment keeps the connection open through the proxies. An interface can use it to see that the stream is alive. If no data and no comment come during 30 seconds, close the connection and connect again.
- The stream does not stop by itself. It stops when the client closes the connection, or when the service stops.

In a browser, use `EventSource`. The session cookie goes with the request:

```js
const metrics = new EventSource("/api/v1/metrics/stream");
metrics.addEventListener("metrics", (e) => {
  const snapshot = JSON.parse(e.data);
  // Show snapshot.host.cpu.usage_percent, ...
});
```

`EventSource` cannot send the header `Authorization`. A program with a token must read the stream with an HTTP client (for example `curl -N -H "Authorization: Bearer nida_..."`).

### 5.2 `GET /api/v1/metrics/stream`

| Event | `data` |
| --- | --- |
| `metrics` | One snapshot, as in section 4.3. The `id` is the `seq` of the snapshot. |

- The first event is the last snapshot, thus the interface shows data at once.
- Then the service sends each new snapshot, each `interval_seconds`.
- After a reconnection, the stream starts again with the last snapshot. The snapshots between are not sent again.

### 5.3 `GET /api/v1/logs/stream`

The parameters `source`, `level`, `q` and `tail` are the same as in section 4.4.

| Event | `data` |
| --- | --- |
| `log` | One line, as in section 4.4. The `id` is the `seq` of the line. |
| `dropped` | `{"count": 37}`: the stream did not send `count` lines, because the client was too slow to read them. |

- First, the stream sends the last `tail` lines that the filter selects (the oldest first). Then it sends each new line.
- **Resume.** A client that connects again sends the header `Last-Event-ID` with the last `id` that it got. `EventSource` does this automatically. Then the stream sends the lines after that `id` that are still in the memory of the service (5000 lines at most), and `tail` is not used.
- When the service started again, the numbers start again at 1. Then the stream sends only the lines that come after the reconnection.
- A line that the stream did not send (after `dropped`, or older than the memory) is in the log store. Use the search (section 4.5) to get it.

## 6. Examples

Sign in from a script and read the state:

```sh
curl -c cookies.txt -H 'Content-Type: application/json' \
  -d '{"password":"..."}' http://nida:8470/api/v1/auth/login
curl -b cookies.txt http://nida:8470/api/v1/state
```

Make a token through the local socket, then use it from an other computer:

```sh
curl --unix-socket /run/nidaos/core.sock -H 'Content-Type: application/json' \
  -d '{"name":"agent","level":"read"}' http://nida/api/v1/tokens
curl -H 'Authorization: Bearer nida_...' http://nida:8470/api/v1/metrics
```

Follow the warnings and the errors of the namespace `apps`:

```sh
curl -N -H 'Authorization: Bearer nida_...' \
  'http://nida:8470/api/v1/logs/stream?source=pod/apps&level=warning&tail=50'
```

Find the errors of k3s of the last 2 days:

```sh
curl -H 'Authorization: Bearer nida_...' \
  'http://nida:8470/api/v1/logs/search?source=service/k3s&level=error&since=2d&limit=200'
```

## 7. Changes to this API

- In version 1, the API only adds paths, parameters, fields, event names and error codes. It does not remove or change them.
- An interface must ignore an unknown field and an unknown event name.
- A change that removes or changes a thing goes into a new version, `/api/v2/`.
