# tidefly-agent

Worker agent for [Tidefly](https://github.com/tidefly-oss/tidefly-plane) — connects to the Plane via gRPC mTLS and manages containers on remote servers.

## Quick Install

```bash
curl -fsSL https://raw.githubusercontent.com/tidefly-oss/tidefly-agent/main/scripts/install.sh | \
  PLANE_TOKEN=tfy_reg_xxx \
  PLANE_ENDPOINT=plane.example.com:7443 \
  sh
```

Requires Docker or Podman on the target server.

## What it does

- Connects to Tidefly Plane via bidirectional gRPC stream (mTLS)
- Receives deploy commands and runs containers locally
- Sends heartbeat metrics (CPU, memory, container count) every 30s
- Registers Caddy routes for exposed services
- Auto-renews mTLS certificates before expiry

## Requirements

- Linux (amd64, arm64, armv7)
- Docker ≥ 20.10 or Podman ≥ 4.0
- Outbound TCP to your Plane on port 7443 (gRPC) and 8181 (HTTP, registration only)

## Configuration

Config is written to `/etc/tidefly-agent/.env` by the installer.

| Variable              | Description                                 | Default                     |
|-----------------------|---------------------------------------------|-----------------------------|
| `PLANE_ENDPOINT`      | Plane gRPC endpoint                         | required                    |
| `PLANE_TOKEN`         | One-time registration token                 | required (first start only) |
| `PLANE_HTTP_ENDPOINT` | Plane HTTP endpoint (auto-derived if empty) | —                           |
| `AGENT_ID`            | UUID — generated on install, do not change  | auto                        |
| `AGENT_NAME`          | Human-readable server name                  | hostname                    |
| `RUNTIME_TYPE`        | `docker` or `podman`                        | `docker`                    |
| `RUNTIME_SOCKET`      | Socket path                                 | `/var/run/docker.sock`      |
| `CADDY_ENABLED`       | Enable local Caddy integration              | `true`                      |
| `CADDY_ADMIN_URL`     | Caddy Admin API URL                         | `http://127.0.0.1:2019`     |
| `LOG_LEVEL`           | `debug`, `info`, `warn`, `error`            | `info`                      |

## Logs

```bash
docker logs -f tidefly-agent
```

## Development

```bash
# Run locally (requires .env in project root)
task run

# Build binary
task build

# Cross-compile all platforms
task build-all

# Lint
task lint
```

## Architecture

```
Tidefly Plane ──gRPC mTLS──► tidefly-agent
                               │
                               ├── Docker/Podman API
                               │     └── manages containers
                               └── Caddy Admin API
                                     └── registers routes
```

## License

AGPLv3 — see [LICENSE](LICENSE)