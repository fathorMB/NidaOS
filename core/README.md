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

## Paths

| Path | Usage |
| --- | --- |
| `GET /healthz` | The service is alive. |
| `GET /readyz` | The service can accept requests. |
| `GET /api/v1/health` | The health, the version and the uptime, for the interfaces. |

Each error has the body `{"code": "...", "message": "..."}`.
