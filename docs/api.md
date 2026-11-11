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
| `/healthz`, `/readyz`, `/api/v1/health`, `/api/v1/auth/status`, `/api/v1/auth/login`, `/api/v1/ca.crt` | Open. No identity is necessary. |
| `/api/v1/auth/logout`, `/api/v1/auth/me` | Each identity. |
| `/api/v1/tokens`, `/api/v1/tokens/{id}`, `/api/v1/auth/password`, `GET /api/v1/databases/{name}/connection` | The level `admin`. |
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
| 400 | `bad_field` | A field of an app request is not valid. `message` names the field. |
| 400 | `image_not_local` | The image of a deploy does not come from the local registry. |
| 404 | `app_not_found`, `version_not_found`, `folder_not_found`, `secret_not_found` | The app, the version, the folder or the secret does not exist. |
| 409 | `app_exists` | An app with this name exists. |
| 409 | `app_busy` | A deploy of the app is in progress. |
| 409 | `app_moving` | A move of a folder of the app is in progress. |
| 409 | `no_rollback` | The target of the rollback is not a version in the state `replaced`. |
| 409 | `no_version` | The app has no version in operation (for a restart). |
| 503 | `apps_not_ready` | k3s is not ready. Try again after some seconds. |
| 500 | `app_failed` | An internal problem of an app operation. |
| 404 | `database_not_found`, `link_not_found`, `backup_not_found` | The database, the link or the backup does not exist. |
| 409 | `database_exists` | A database with this name exists. |
| 409 | `database_busy` | The database is not ready, or an operation on it is in progress. |
| 409 | `app_linked`, `name_in_use` | The app is connected already, or it uses a name of the connection. |
| 409 | `no_port`, `no_update`, `sessions_remain` | No free port for the home network, no newer minor version, or open sessions of the app. |
| 503 | `databases_not_ready` | k3s is not ready. Try again after some seconds. |
| 500 | `database_failed` | An internal problem of a database operation. |
| 404 | `repository_not_connected`, `job_not_found`, `log_not_available` | The app has no repository, the job does not exist, or its log is not there yet. |
| 404 | `setup_expired` | The key of the setup of the GitHub App is not valid or is too old. |
| 404 | `github_not_found` | GitHub does not have the object. |
| 409 | `github_not_configured`, `github_not_installed` | NidaOS has no GitHub App, or the app is not installed on the repository. |
| 409 | `repository_public` | The repository is public (decision D27). |
| 409 | `repository_connected`, `repository_in_use` | The app has a repository already, or an other app uses the repository. |
| 502 | `github_error`, `github_unavailable` | GitHub gave an error, or GitHub is not available. |
| 401 | `job_token_required` | The job endpoint (section 4.11.6) got no valid job token. |

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
| `/api/v1/apps`, `/api/v1/apps/{name}/...` | 4.8 |
| `GET /api/v1/ca.crt`, `GET /api/v1/ca` | 4.9 |
| `/api/v1/databases`, `/api/v1/databases/{name}/...` | 4.10 |
| `/api/v1/github/...`, `GET /api/v1/runners`, `/api/v1/apps/{name}/repository`, `/api/v1/apps/{name}/jobs`, `/api/v1/jobs/{id}` | 4.11 |

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

### 4.8 Apps (milestone M-03)

An app is a program of the owner that NidaOS keeps in operation on k3s. Each deploy makes a new **version**. The image of a version comes from the local registry `localhost:5000` (decision D21). The GitHub runner of M-05 builds and pushes the images. Before M-05, push them with `podman push --tls-verify=false`.

| Method and path | Access | Result |
| --- | --- | --- |
| `GET /api/v1/apps` | `read` | `{"apps": [App, ...]}`, sorted by name. |
| `POST /api/v1/apps` | `admin` | `201` and the new App. The app has no version. |
| `GET /api/v1/apps/{name}` | `read` | One App. |
| `PATCH /api/v1/apps/{name}` | `admin` | `200`: the App and `deploy` (section 4.8.3). |
| `DELETE /api/v1/apps/{name}` | `admin` | `204`. It deletes the app, its versions, its secrets and **the files of its folders**. |
| `POST /api/v1/apps/{name}/deploys` | `admin` | `202` and the new Version (section 4.8.4). |
| `POST /api/v1/apps/{name}/rollback` | `admin` | `202` and the new Version (section 4.8.5). |
| `POST /api/v1/apps/{name}/restart` | `admin` | `204`. All the copies start again. |
| `PUT /api/v1/apps/{name}/secrets/{key}` | `admin` | `204`. Body: `{"value": "..."}`. |
| `DELETE /api/v1/apps/{name}/secrets/{key}` | `admin` | `204`. |
| `POST /api/v1/apps/{name}/folders/{folder}/move` | `admin` | `202` and the App. Body: `{"tier": "performance"}` (section 4.8.6). |

#### 4.8.1 The App object

```json
{
  "name": "blog",
  "repository": "fathor/blog",
  "branch": "main",
  "config": {
    "port": 8080,
    "health_check": {"path": "/healthz", "interval_seconds": 5, "timeout_seconds": 2},
    "variables": {"MODE": "prod"},
    "limits": {"cpu_millis": 500, "memory_bytes": 268435456}
  },
  "copies": 1,
  "folders": [
    {"name": "data", "path": "/var/lib/blog", "tier": "standard", "size_bytes": 10737418240}
  ],
  "active": 3,
  "versions": [ ... ],
  "created": "2026-11-11T09:30:00Z",
  "updated": "2026-11-11T10:02:00Z",
  "state": "running",
  "address": "https://blog.nidaos.local",
  "secrets": ["DB_PASSWORD"],
  "status": {"copies": 1, "ready": 1, "version": 3, "observed": "2026-11-11T10:02:05Z"}
}
```

| Field | Values |
| --- | --- |
| `name` | 1 to 32 characters: lowercase letters, digits and `-`. It starts with a letter. It does not change. |
| `config` | The config of the **next** version. Each version keeps its own copy. |
| `config.port` | The TCP port where the app listens for HTTP. Without a port, the app has no address and no health check on the network (for example a worker). |
| `config.health_check` | `omitempty`. A response from 200 to 399 on `path` is healthy. Without it, the check only opens the port. |
| `config.variables` | The environment variables. The names that start with `NIDA_`, and `PORT`, are for NidaOS. |
| `config.limits` | `0` is no limit. The minimum memory limit is 16 MiB. |
| `copies` | 0 to 10. `0` stops the app. An app with folders has 1 copy at most. |
| `folders` | The folders that stay after a deploy, on a storage tier (decision D7). All the other files of the app go away at each deploy. A folder can have `moving_to` (a move in progress) and `move_error` (the last move failed). |
| `active` | The version in operation. `0`: no version passed the health check. |
| `versions` | The last 5 versions, the oldest first, and also the active version. |
| `state` | A summary for the lists: `new`, `deploying`, `moving`, `running`, `starting`, `stopped` or `failed`. |
| `address` | `omitempty`: the app has no port. |
| `secrets` | The names of the secrets. **The API never gives a value.** |
| `status` | `omitempty`: the controller did not read the copies yet (for example before k3s is ready). |

Each copy gets the variables, the secrets, and also `NIDA_APP`, `NIDA_VERSION`, `NIDA_COMMIT` and `PORT`.

#### 4.8.2 The Version object

```json
{
  "number": 3,
  "image": "localhost:5000/blog:1.4",
  "commit": "7c1d9a2",
  "summary": "Try the new cache",
  "rollback_of": 1,
  "config": { ... },
  "state": "serving",
  "reason": "the container stops again and again (exit code 1, Error)",
  "created": "2026-11-11T10:00:00Z",
  "finished": "2026-11-11T10:00:41Z"
}
```

| `state` | Meaning |
| --- | --- |
| `deploying` | The controller applies the version and waits for the health check. |
| `serving` | The version is in operation. |
| `failed` | The version did not pass the health check in 120 seconds, or its container stops again and again. `reason` tells why. **The version before stays in operation.** |
| `replaced` | The version was in operation, and a newer version replaced it. A rollback can go back to it. |

#### 4.8.3 Change the settings

`PATCH /api/v1/apps/{name}` with one or more of these fields. A missing field does not change.

```json
{"repository": "fathor/blog", "branch": "main", "config": { ... }, "copies": 2, "add_folders": [ ... ]}
```

- `config` replaces the full config.
- `add_folders` adds new folders. A change cannot remove a folder.
- When `config` or `add_folders` changes and the app has a version in operation, the service makes a new version with the same image. The response has `"deploy": <number>`. Then follow that version as a deploy.
- A change of `copies` only applies at once, without a new version.

#### 4.8.4 Deploy

Request: `{"image": "localhost:5000/blog:1.4", "commit": "7c1d9a2", "summary": "Try the new cache"}`. Only `image` is necessary.

The response is `202`: the deploy runs in the service. To know the result, read the app until the state of the version is not `deploying`. The new copy starts before the old copy stops. For an app with folders, the old copy stops first.

#### 4.8.5 Rollback

Request: `{"version": 1}`. The target must be a version in the state `replaced`. The service makes a new version with the image and the config of the target, and deploys it as each other version: a rollback also has the health check.

#### 4.8.6 Move a folder

Request: `{"tier": "performance"}`. The app **stops during the copy**. The service copies the files to a new volume on the other tier, starts the app on it, and then deletes the old volume. When the copy fails, the app starts again on the old tier, with the old files, and `move_error` tells why. While `moving_to` is set, a deploy, a rollback or a change of the config gives `409 app_moving`.

#### 4.8.7 The address of an app

Each app with a port has the address `https://<name>.nidaos.local` in the home network (decision D19). The service announces the names with multicast DNS. The certificate comes from the NidaOS authority (section 4.9). In an other app, use `http://<name>` (port 80) or `http://<name>:<port>`.

### 4.9 The certificate authority

| Method and path | Access | Result |
| --- | --- | --- |
| `GET /api/v1/ca.crt` | Open | The certificate of the NidaOS authority in PEM (`application/x-x509-ca-cert`). Install it on a device, one time. |
| `GET /api/v1/ca` | `read` | `{"fingerprint_sha256": "AB:CD:...", "download": "/api/v1/ca.crt"}`. Show the fingerprint, thus the person can compare it before the installation. |

The authority can sign only names in `nidaos.local` (decision D20).

### 4.10 Databases (milestone M-04)

A managed database is a PostgreSQL server for the apps of the owner (decisions D13, D23 and D24). NidaOS makes it, makes the backups and connects it to the apps.

| Method and path | Access | Result |
| --- | --- | --- |
| `GET /api/v1/databases` | `read` | `{"databases": [Database, ...], "versions": [17, 18]}`. |
| `POST /api/v1/databases` | `admin` | `202` and the new Database in the state `creating` (section 4.10.2). |
| `GET /api/v1/databases/{name}` | `read` | One Database. |
| `DELETE /api/v1/databases/{name}` | `admin` | `204`. It disconnects the apps and deletes the database and **its data**. The backups stay. |
| `GET /api/v1/databases/{name}/connection` | `admin` | The connection of the owner role, **with its password** (section 4.10.4). |
| `POST /api/v1/databases/{name}/links` | `admin` | `200` and the Database. Body: `{"app": "shop", "mode": "rw"}` (section 4.10.3). |
| `DELETE /api/v1/databases/{name}/links/{app}` | `admin` | `200` and the Database. |
| `PUT /api/v1/databases/{name}/lan` | `admin` | `200` and the Database. Body: `{"on": true}` (section 4.10.4). |
| `GET /api/v1/databases/{name}/backups` | `read` | `{"backups": [Backup, ...]}`, the newest first. |
| `POST /api/v1/databases/{name}/backups` | `admin` | `202`. A backup on demand starts. |
| `DELETE /api/v1/databases/{name}/backups/{backup}` | `admin` | `204`. |
| `POST /api/v1/databases/{name}/update` | `admin` | `202` and the Database in the state `updating` (section 4.10.6). |

#### 4.10.1 The Database object

```json
{
  "name": "shop-db",
  "version": 18,
  "image": "docker.io/library/postgres:18.1-alpine",
  "tier": "performance",
  "size_bytes": 1073741824,
  "cpu_millis": 100,
  "memory_bytes": 268435456,
  "state": "ready",
  "lan": true,
  "lan_port": 15432,
  "links": [{"app": "shop-api", "mode": "rw", "role": "l_shop_api", "created": "2026-11-11T10:00:00Z"}],
  "created": "2026-11-11T09:30:00Z",
  "updated": "2026-11-11T10:00:00Z",
  "update_available": "docker.io/library/postgres:18.2-alpine",
  "address": "nidaos.local:15432",
  "live": {"size_bytes": 8192000, "connections": 3, "transactions_per_second": 12.5, "observed": "2026-11-11T10:02:00Z"}
}
```

| Field | Values |
| --- | --- |
| `state` | `creating`, `restoring`, `updating`, `ready` or `failed`. `reason` tells why the last operation failed. A failed update or backup keeps the state `ready` and writes `reason`. |
| `size_bytes` | The declared size. The volume does not apply it: it is a soft limit. `live.size_bytes` is the real size. |
| `links` | The connected apps. Each link has its own login role. |
| `restore`, `update_to` | `omitempty`: the backup of a restore or the image of an update in progress. |
| `update_available`, `address`, `live` | `omitempty`. |

The live metrics (section 5.2) also have the databases: `databases: [{"name", "state", "size_bytes", "connections", "transactions_per_second", "observed"}]`. The service reads them each 10 seconds.

#### 4.10.2 Add a database

```json
{"name": "shop-db", "version": 18, "tier": "performance", "size_bytes": 10737418240, "memory_bytes": 536870912, "cpu_millis": 250}
```

- Only `name` is necessary. The defaults: version 18, tier `performance`, 1 GiB, 256 MiB of memory, 0.1 processor.
- `version` is 17 or 18.
- To restore a backup into the new database, add `"restore_from": {"database": "shop-db", "backup": "20261111T030000Z-daily-pg17.dump"}`. The state is then `restoring`. The version must be the version of the backup or newer: this is the major update, for example from 17 to 18.
- The response is `202`: read the database until its state is `ready` or `failed`.

#### 4.10.3 Connect an app

The app gets these secret variables: `DATABASE_URL`, `PGHOST`, `PGPORT`, `PGDATABASE`, `PGUSER`, `PGPASSWORD` and `PGSSLMODE`. Its copies start again. `DATABASE_URL` is for example:

```text
postgresql://l_shop_api:<password>@pg.nida-db-shop-db.svc.cluster.local:5432/shop_db?sslmode=require
```

- `mode` is `rw` (the default) or `ro`. With `rw`, each object that the app makes belongs to the owner role of the database.
- The connection uses TLS. The app must not use `sslmode=verify-full`: the certificate is for `nidaos.local`.
- An app can have one database. A variable or a secret of the app with one of these names gives `409 name_in_use`.
- The app must try the connection again at its start: the network policy can need some seconds for a new copy.
- A disconnect closes the open sessions of the app first.

#### 4.10.4 The home network

`PUT .../lan` with `{"on": true}` opens a port from 15432 to 15531 on the computer. The port accepts only TLS with a password. `GET .../connection` gives the connection of the owner role:

```json
{"host": "nidaos.local", "port": 15432, "database": "shop_db", "user": "shop_db_owner", "password": "...", "sslmode": "verify-full",
 "url": "postgresql://shop_db_owner:...@nidaos.local:15432/shop_db?sslmode=verify-full", "lan": true, "note": "..."}
```

The client must have the certificate of the NidaOS authority (section 4.9) as its root certificate.

#### 4.10.5 Backups

```json
{"database": "shop-db", "name": "20261111T030000Z-daily-pg18.dump", "kind": "daily", "created": "2026-11-11T03:00:00Z", "version": 18, "size_bytes": 81920}
```

- `kind` is `daily`, `manual` (on demand) or `update` (before an update).
- NidaOS makes one `daily` backup each day after 03:00 and keeps the last 7. It keeps the last 5 `manual` and `update` backups.
- A backup is a `pg_dump` file in `/var/nida/standard/backups/<database>` on the computer. Only root can read it.
- The backups of a removed database stay. Delete them with `DELETE .../backups/{backup}`.
- A backup on demand runs in the service. When it fails, `reason` of the database tells why.

#### 4.10.6 Update the minor version

`POST .../update` starts when `update_available` is set. NidaOS makes an `update` backup first, and stops when the backup fails. Then it changes the image. When the server does not start with the new image, the old image comes back and `reason` tells why.

### 4.11 GitHub runners and the deploy flow (milestone M-05)

A push to a connected GitHub repository builds the app on the NidaOS computer and deploys it. The source stays on GitHub (decisions D25, D26 and D27):

- NidaOS uses its own GitHub App. The app has no webhook: NidaOS reads the queued jobs from GitHub each 15 seconds, and it opens no port to the internet.
- For each queued job with the label `nidaos-<app>`, NidaOS starts a new runner pod with a registration for one job. The pod builds the image with rootless BuildKit, pushes it to the local registry through the job endpoint (section 4.11.6), deploys it and stops.
- NidaOS connects only private repositories. When a connected repository becomes public, NidaOS starts no more pods for it.

| Method and path | Access | Result |
| --- | --- | --- |
| `GET /api/v1/github` | `read` | The GitHub App (section 4.11.1). It never has the private key. |
| `POST /api/v1/github/setup` | `admin` | `201` and `{"url": "...", "expires": "..."}`. Body (optional): `{"organization": "my-org"}`. Open `url` in a browser (section 4.11.2). |
| `GET /api/v1/github/setup/{key}` | the key | The page that sends the manifest of the app to GitHub. |
| `GET /api/v1/github/callback` | the key | GitHub sends the browser to it. `302` to the installation page of the app. |
| `POST /api/v1/github/import` | `admin` | `200` and the GitHub App. Body: `{"app_id": 123, "private_key": "-----BEGIN RSA PRIVATE KEY-----..."}`. NidaOS uses an app that exists already, for example the app of the tests. |
| `DELETE /api/v1/github` | `admin` | `204`. Disconnect all the apps first. |
| `GET /api/v1/runners` | `read` | `{"github": GitHubApp, "connections": [Connection, ...], "active": [Job, ...]}`. `active` has the jobs that wait or run now. |
| `GET /api/v1/apps/{name}/repository` | `read` | The Connection of the app (section 4.11.3). |
| `PUT /api/v1/apps/{name}/repository` | `admin` | `201` and `{"connection": Connection}`. Body: `{"repository": "me/shop", "branch": "main", "no_pull_request": false}` (section 4.11.3). |
| `DELETE /api/v1/apps/{name}/repository` | `admin` | `204`. NidaOS stops the pods of the app. The workflow stays in the repository. |
| `POST /api/v1/apps/{name}/repository/pull-request` | `admin` | `200` and the Connection. NidaOS opens the pull request with the workflow again, or updates it. |
| `GET /api/v1/apps/{name}/jobs` | `read` | `{"jobs": [Job, ...]}`, the newest first. NidaOS keeps the last 20 jobs of each app. |
| `GET /api/v1/jobs/{id}` | `read` | One Job. `id` is the ID of the job on GitHub. |
| `GET /api/v1/jobs/{id}/log` | `read` | The log of the job as text (`text/plain`), a maximum of 5 MiB. NidaOS gets it from GitHub at the end of the job. |

The remove of an app also removes its Connection.

#### 4.11.1 The GitHub App object

```json
{
  "configured": true,
  "id": 1234567,
  "slug": "nidaos-nidaos-1a2b3c",
  "name": "NidaOS nidaos 1a2b3c",
  "owner": "me",
  "html_url": "https://github.com/apps/nidaos-nidaos-1a2b3c",
  "install_url": "https://github.com/apps/nidaos-nidaos-1a2b3c/installations/new"
}
```

Without an app, the body is `{"configured": false}`.

#### 4.11.2 Make the GitHub App

1. `POST /api/v1/github/setup` gives the address of a setup page. The address has a key that is valid for 30 minutes. The page needs no sign-in: the key is the access.
2. The owner opens the page in a browser of the home network and clicks "Continue on GitHub". GitHub makes the app from the manifest of NidaOS. The app has these permissions: `administration: write` (the runner registrations), `actions: read` (the queued jobs and their logs), `contents: write`, `workflows: write` and `pull_requests: write` (the pull request with the workflow), and `metadata: read`.
3. GitHub sends the browser back to `GET /api/v1/github/callback`. NidaOS keeps the app and its private key in `/var/lib/nidaos/runners.json` (mode 0600), then sends the browser to the installation page.
4. The owner installs the app on the private repositories of the apps.

NidaOS uses the address of `NIDA_CORE_PUBLIC_URL` (default `http://nidaos.local:8470`) in the manifest: the browser must get to it.

#### 4.11.3 The Connection object

```json
{
  "app": "shop",
  "owner": "me",
  "repo": "shop",
  "branch": "main",
  "created": "2026-11-11T10:00:00Z",
  "pull_request": "https://github.com/me/shop/pull/1",
  "problem": ""
}
```

- `PUT .../repository` examines the repository on GitHub: the app must be installed on it (else `409 github_not_installed`), and it must be private (else `409 repository_public`). `branch` is the default branch of the repository when the body has no branch.
- Then NidaOS opens a pull request from the branch `nidaos/deploy-<app>` that adds `.github/workflows/nidaos-deploy-<app>.yml`. When the pull request fails, the connection stays, and the body has `"warning"` with the cause.
- `problem` is the last error of NidaOS for this repository, for example a repository that became public. It is empty when the reads pass.

The workflow runs on `push` to the branch, and on demand. It has one job:

```yaml
jobs:
  deploy:
    runs-on: [self-hosted, nidaos, nidaos-shop]
    timeout-minutes: 60
    steps:
      - uses: actions/checkout@v5
      - name: Build and deploy on NidaOS
        run: nida app deploy shop --build . --wait
```

`nida app deploy NAME --build DIR` uses the `Containerfile`, or else the `Dockerfile`, of `DIR`. The tag of the image is the first 12 characters of the commit.

#### 4.11.4 The Job object

```json
{
  "id": 55500011122,
  "app": "shop",
  "run_id": 1900000001,
  "name": "deploy",
  "sha": "0123456789abcdef0123456789abcdef01234567",
  "url": "https://github.com/me/shop/actions/runs/1900000001/job/55500011122",
  "state": "succeeded",
  "pod": "run-shop-1a2b3c",
  "runner": "run-shop-1a2b3c",
  "created": "2026-11-11T10:00:00Z",
  "started": "2026-11-11T10:00:20Z",
  "finished": "2026-11-11T10:02:10Z",
  "message": "",
  "has_log": true
}
```

| `state` | Meaning |
| --- | --- |
| `queued` | A runner pod waits for the job. |
| `running` | The runner runs the job. |
| `succeeded` | The job passed on GitHub. |
| `failed` | The job failed on GitHub, or the pod failed 3 times. `message` gives the cause. |
| `cancelled` | The job was cancelled on GitHub, or the repository was disconnected. |

#### 4.11.5 The runner pods

- The pods are in the namespace `nida-runners`. A network policy stops each connection that comes in. A pod can open only DNS, the job endpoint and addresses on the internet: not the apps, the databases and the home network.
- The pod has the GitHub runner (`ghcr.io/actions/actions-runner`, the last release; `NIDA_CORE_RUNNER_VERSION` fixes it) and rootless BuildKit (`NIDA_CORE_BUILDKIT_IMAGE`) as the user 1000. No container is privileged. The pod has a maximum of 2 CPU cores, 4 GiB of memory, and 2 hours.
- The pod has the variables `NIDA_SERVER`, `NIDA_TOKEN` (the job token), `NIDA_APP`, `NIDA_REGISTRY` and `BUILDKIT_HOST`, and the commands `nida` and `buildctl` in the `PATH`.

#### 4.11.6 The job endpoint

The job endpoint is a second HTTP server of nida-core on the bridge of the pod network (`NIDA_CORE_JOB_ADDR`, default `10.42.0.1:8471`). Only the runner pods get to it. Each request needs the job token of the pod, as a bearer token or as the password of the basic authentication. The token is valid only while its pod runs, and only for its app:

| Method and path | Result |
| --- | --- |
| `GET /tools/nida` | The `nida` command. It needs no token. |
| `/v2/`, `/v2/{app}/blobs/...`, `/v2/{app}/manifests/...`, `/v2/{app}/tags/...` | The local registry, only for the image of the app of the token. |
| `POST /api/v1/apps/{app}/deploys` | A deploy (section 4.8.4), only of the image `localhost:5000/{app}:...`. |
| `GET /api/v1/apps/{app}` | The App, for `--wait`. |

Each other request gives `403 forbidden`. A request without a valid token gives `401 job_token_required`, with `WWW-Authenticate: Basic realm="nidaos"` for the registry paths.

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

Deploy a new version of an app, then read the result (through the local socket):

```sh
curl --unix-socket /run/nidaos/core.sock -H 'Content-Type: application/json' \
  -d '{"image":"localhost:5000/blog:1.4","commit":"7c1d9a2"}' http://nida/api/v1/apps/blog/deploys
curl --unix-socket /run/nidaos/core.sock http://nida/api/v1/apps/blog
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
