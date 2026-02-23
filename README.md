# Amurg

[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8.svg)](https://go.dev/)

**Self-hosted agent control plane. One surface. Full authority. No inbound ports.**

## What is Amurg

Amurg is a self-hosted system that lets you interact with any agent — CLI tools, batch jobs, HTTP services, or custom protocols — through a single mobile-friendly chat UI. Runtimes connect outbound to the hub, so you never expose inbound ports.

Full documentation: [https://amurg.ai](https://amurg.ai)

## Architecture

```
┌─────────┐     ┌─────────┐     ┌─────────┐     ┌─────────┐
│  Agent   │◄───►│ Runtime │────►│   Hub   │◄────│   UI    │
│ (process)│     │  (Go)   │ WS  │  (Go)   │ API │ (React) │
└─────────┘     └─────────┘     └─────────┘     └─────────┘
```

| Component | Role |
|-----------|------|
| **Hub** | Central server. JWT auth, REST API, WebSocket message routing, SQLite/Postgres persistence. Serves the UI static bundle. |
| **Runtime** | Gateway deployed near your agents. Manages process lifecycle, session state, adapter selection. Connects outbound to hub via WebSocket. |
| **UI** | React chat client. TypeScript, Vite, Tailwind, Zustand. Mobile-friendly with voice input, rich rendering, real-time streaming. |

Communication flow: **User → UI → Hub → Runtime → Agent** (and back). The runtime initiates all connections — no inbound ports required on the runtime side.

## Quick Start

### Prerequisites

| Tool | Minimum version | Check command |
|------|----------------|---------------|
| Go | 1.25+ | `go version` |
| Node.js | 20+ | `node --version` |
| npm | 9+ | `npm --version` |
| Make | any | `make --version` |

### 1. Clone and build

```bash
git clone https://github.com/amurg-ai/amurg.git
cd amurg
make build
```

Expected output:
```
# Go binaries appear in bin/
ls bin/
amurg-hub  amurg-runtime

# UI bundle appears in ui/dist/
ls ui/dist/
index.html  assets/
```

### 2. Start the hub

```bash
./bin/amurg-hub -config hub/deploy/config.local.json
```

Expected output:
```
level=DEBUG msg="hub starting" addr=:8090
level=DEBUG msg="storage initialized" driver=sqlite
level=INFO  msg="hub listening" addr=:8090
```

The hub is now running on port 8090. Leave this terminal open.

### 3. Start the runtime

In a new terminal:

```bash
./bin/amurg-runtime -config runtime/deploy/config.local.json
```

Expected output:
```
level=DEBUG msg="connecting to hub" url=ws://localhost:8090/ws/runtime
level=INFO  msg="connected to hub"
level=INFO  msg="registered endpoints" count=4
```

### 4. Start the UI dev server

In a new terminal:

```bash
cd ui && npm run dev
```

Expected output:
```
VITE v6.x.x  ready in XXX ms

➜  Local:   http://localhost:3000/
```

### 5. Log in

Open `http://localhost:3000` in your browser and log in:

- **Username:** `admin`
- **Password:** `admin`

You should see the endpoint list with Bash Shell, Python REPL, Date Job, and Go Test Runner.

### Troubleshooting quick start

| Symptom | Cause | Fix |
|---------|-------|-----|
| `go: go.mod requires go >= 1.25.6` | Go version too old | Install Go 1.25+ from https://go.dev/dl/ |
| Hub says `listen tcp :8090: bind: address already in use` | Port 8090 taken | Change `server.addr` in `hub/deploy/config.local.json` |
| Runtime says `dial ws://localhost:8090/ws/runtime: connection refused` | Hub not running | Start the hub first |
| UI shows blank page at localhost:3000 | Vite not proxying to hub | Ensure the hub is running on port 8090 |

## Configuration Reference

### Hub Configuration

File: JSON passed via `-config` flag. See [`hub/deploy/config.local.json`](hub/deploy/config.local.json) (development) and [`hub/deploy/config.example.json`](hub/deploy/config.example.json) (production template).

```jsonc
{
  "server": {
    "addr": ":8080",              // Listen address. Default: ":8080"
    "ui_static_dir": "/var/lib/amurg/ui"  // Path to UI dist/. Empty = no static serving.
  },
  "auth": {
    "jwt_secret": "change-me-to-a-random-secret-at-least-32-chars",  // REQUIRED. Min 32 chars.
    "jwt_expiry": "24h",          // Token lifetime. Default: "24h"
    "runtime_tokens": [           // Pre-shared tokens for runtime auth
      {
        "runtime_id": "my-runtime",   // Must match runtime.id in runtime config
        "token": "random-secret",     // Shared secret. Must match hub.token in runtime config
        "name": "My Runtime"          // Display name (informational only)
      }
    ],
    "initial_admin": {            // Created on first startup if no admin exists
      "username": "admin",
      "password": "admin"         // CHANGE THIS in production
    }
  },
  "storage": {
    "driver": "sqlite",           // "sqlite" or "postgres"
    "dsn": "/var/lib/amurg/data/amurg.db",  // SQLite path or Postgres connection string
    "retention": "720h"           // Message retention. Default: "720h" (30 days)
  },
  "session": {
    "max_per_user": 20,           // Max concurrent sessions per user. Default: 20
    "idle_timeout": "30m",        // Auto-close idle sessions. Default: "30m"
    "turn_based": true,           // Enforce one message at a time. Default: true
    "replay_buffer": 100          // Messages buffered for reconnect. Default: 100
  },
  "logging": {
    "level": "info",              // "debug", "info", "warn", "error". Default: "info"
    "format": "json"              // "text" or "json". Default: "json"
  }
}
```

### Runtime Configuration

File: JSON passed via `-config` flag. See [`runtime/deploy/config.local.json`](runtime/deploy/config.local.json) (development) and [`runtime/deploy/config.example.json`](runtime/deploy/config.example.json) (production template).

```jsonc
{
  "hub": {
    "url": "ws://localhost:8090/ws/runtime",  // Hub WebSocket URL. Use wss:// with TLS.
    "token": "dev-token-12345",               // Must match a runtime_tokens[].token in hub config
    "tls_skip_verify": false,                 // Skip TLS cert validation. Default: false
    "reconnect_interval": "2s",               // Initial reconnect delay. Default: "2s"
    "max_reconnect_delay": "60s"              // Max reconnect backoff. Default: "60s"
  },
  "runtime": {
    "id": "local-dev",            // Must match a runtime_tokens[].runtime_id in hub config
    "max_sessions": 10,           // Max concurrent sessions across all endpoints. Default: 10
    "default_timeout": "30m",     // Default session timeout. Default: "30m"
    "max_output_bytes": 10485760, // Max output per session (bytes). Default: 10485760 (10 MB)
    "idle_timeout": "10s",        // Idle detection for CLI turn completion. Default: "10s"
    "log_level": "debug"          // "debug", "info", "warn", "error". Default: "info"
  },
  "endpoints": [
    // See "Adapter Types" section below for endpoint examples
  ]
}
```

## Adapter Types

Each endpoint in the runtime config uses one adapter type. The adapter is selected by setting the corresponding config key (`cli`, `job`, `http`, or `external`).

### CLI Adapter (`generic-cli`)

Long-running interactive process. Input is written to stdin, output is streamed from stdout/stderr. Process persists across messages within a session.

**When to use:** Interactive shells, REPLs, chat-style agents, any process that maintains state across messages.

```json
{
  "id": "bash-shell",
  "name": "Bash Shell",
  "profile": "generic-cli",
  "tags": {"env": "dev"},
  "cli": {
    "command": "bash",
    "args": ["--norc", "--noprofile", "-i"],
    "work_dir": "/home/user/project",
    "env": {"PS1": "$ ", "TERM": "dumb"},
    "spawn_policy": "per-session"
  }
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `command` | string | yes | Executable path or name |
| `args` | string[] | no | Command-line arguments |
| `work_dir` | string | no | Working directory for the process |
| `env` | map | no | Environment variables (appended to system env) |
| `spawn_policy` | string | yes | `"per-session"` (new process per session) or `"persistent"` (shared process) |

### Job Adapter (`generic-job`)

Runs a command per message, captures output, reports exit code. Process starts and exits for each user message.

**When to use:** One-shot commands, test runners, build scripts, any command that runs to completion.

```json
{
  "id": "go-test-job",
  "name": "Go Test Runner",
  "profile": "generic-job",
  "tags": {"env": "dev"},
  "job": {
    "command": "bash",
    "args": ["-c", "cd /home/user/project && go test -v ./..."],
    "max_runtime": "30s"
  }
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `command` | string | yes | Executable path or name |
| `args` | string[] | no | Command-line arguments |
| `max_runtime` | duration | no | Kill process after this duration. Example: `"30s"`, `"5m"` |

### HTTP Adapter (`generic-http`)

Proxies user messages to an HTTP endpoint. User message becomes the request body, response body is streamed back.

**When to use:** REST APIs, webhook-based agents, any HTTP service that accepts text input and returns text output.

```json
{
  "id": "my-api",
  "name": "My API",
  "profile": "generic-http",
  "tags": {"env": "dev"},
  "http": {
    "base_url": "https://api.example.com/chat",
    "method": "POST",
    "headers": {"Authorization": "Bearer sk-xxx"},
    "timeout": "30s"
  }
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `base_url` | string | yes | Target URL for requests |
| `method` | string | no | HTTP method. Default: `"POST"` |
| `headers` | map | no | Custom request headers |
| `timeout` | duration | no | Request timeout. Default: `"60s"` |

### External Adapter (`generic-external`)

Spawns a long-lived adapter process that communicates via JSON-Lines over stdin/stdout. Supports multiplexed sessions.

**When to use:** Custom agent protocols, complex session management, agents that need bidirectional structured communication beyond simple stdin/stdout text.

```json
{
  "id": "my-agent",
  "name": "My Custom Agent",
  "profile": "generic-external",
  "tags": {"env": "dev"},
  "external": {
    "command": "/usr/local/bin/my-adapter",
    "args": ["--mode", "production"],
    "work_dir": "/opt/agent",
    "env": {"AGENT_API_KEY": "sk-xxx"}
  }
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `command` | string | yes | Executable path or name |
| `args` | string[] | no | Command-line arguments |
| `work_dir` | string | no | Working directory for the process |
| `env` | map | no | Environment variables |

The external adapter process receives JSON-Lines on stdin (`session.start`, `user.input`) and writes JSON-Lines to stdout (`output`, `turn.complete`, `file.output`). Each message includes a `session_id` field for multiplexing.

## Deployment

### Docker Compose (recommended)

```bash
# Build images
docker compose build

# Start hub + runtime
docker compose up -d

# Check status
docker compose ps

# View logs
docker compose logs -f hub
docker compose logs -f runtime
```

The hub listens on port 8080 and serves the UI static bundle from `/var/lib/amurg/ui`. Edit `hub/deploy/config.example.json` and `runtime/deploy/config.example.json` before deploying.

### Binary + systemd

Build the binaries:

```bash
make build-hub build-runtime
```

Copy binaries and configs:

```bash
sudo cp bin/amurg-hub /usr/local/bin/
sudo cp bin/amurg-runtime /usr/local/bin/
sudo mkdir -p /etc/amurg /var/lib/amurg/data /var/lib/amurg/ui
sudo cp hub/deploy/config.example.json /etc/amurg/hub.json
sudo cp runtime/deploy/config.example.json /etc/amurg/runtime.json
```

Build and copy the UI:

```bash
make build-ui
sudo cp -r ui/dist/* /var/lib/amurg/ui/
```

Set `ui_static_dir` in your hub config:

```json
{
  "server": {
    "addr": ":8080",
    "ui_static_dir": "/var/lib/amurg/ui"
  }
}
```

Create systemd unit files and enable/start the services.

### Production checklist

- [ ] Change `auth.jwt_secret` to a random string (minimum 32 characters)
- [ ] Change `auth.initial_admin.password` from `admin` to a strong password
- [ ] Change `runtime_tokens[].token` to a random secret
- [ ] Use `wss://` (TLS) for the runtime → hub WebSocket URL
- [ ] Set `tls_skip_verify` to `false`
- [ ] Use a persistent SQLite path or Postgres DSN (not `:memory:`)
- [ ] Set `logging.level` to `"info"` and `logging.format` to `"json"`
- [ ] Place the hub behind a reverse proxy (nginx, Caddy) with TLS termination
- [ ] Restrict network access: runtime only needs outbound to hub, hub needs inbound from clients

### Access from your phone (ngrok / Cloudflare Tunnel)

Run the hub and runtime on your home machine, then use a tunnel to access the UI from your phone or any device outside your LAN — no static IP, port forwarding, or VPS needed.

Everything stays local. The tunnel only exposes the hub's HTTP port so you can reach the UI remotely.

**With ngrok:**

```bash
# 1. Start hub + runtime locally (as in Quick Start)
./bin/amurg-hub -config hub/deploy/config.local.json
./bin/amurg-runtime -config runtime/deploy/config.local.json

# 2. Expose the hub via ngrok (free tier works)
ngrok http 8090
```

ngrok prints a public URL like `https://ab12cd34.ngrok-free.app`. Open it on your phone and log in — the hub and runtime stay on your machine, the tunnel just lets the UI reach them.

**With Cloudflare Tunnel (free, stable hostname):**

```bash
# 1. Start hub + runtime locally
./bin/amurg-hub -config hub/deploy/config.local.json
./bin/amurg-runtime -config runtime/deploy/config.local.json

# 2. Expose the hub via cloudflared
cloudflared tunnel --url http://localhost:8090
```

Cloudflare Tunnel gives you a `*.trycloudflare.com` URL (random) or a stable subdomain if you configure a named tunnel.

**Important:** The tunnel makes your hub reachable from the internet. Make sure to:
- Use a strong `jwt_secret` (not the dev default)
- Change the admin password from `admin`
- Use strong runtime tokens

## API Reference

All endpoints return JSON. Errors use `{"error": "message"}` format.

### Public (no auth)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/healthz` | Health check (uptime) |
| GET | `/readyz` | Readiness check (database connectivity) |
| GET | `/api/auth/config` | Auth provider type |
| POST | `/api/auth/login` | Login, returns JWT. Body: `{"username": "...", "password": "..."}` |

### Authenticated (Bearer token)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/endpoints` | List available endpoints |
| GET | `/api/sessions` | List current user's sessions |
| POST | `/api/sessions` | Create session. Body: `{"endpoint_id": "..."}` |
| GET | `/api/sessions/{id}/messages` | Get messages. Query: `limit`, `after_seq` |
| POST | `/api/sessions/{id}/files` | Upload file (multipart) |
| GET | `/api/files/{id}` | Download file. Query: `session_id` |
| POST | `/api/sessions/{id}/close` | Close session |
| GET | `/api/me` | Current user info |

### Admin (admin role required)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/runtimes` | List all runtimes |
| GET | `/api/users` | List all users |
| POST | `/api/users` | Create user. Body: `{"username": "...", "password": "...", "role": "..."}` |
| POST | `/api/permissions` | Grant endpoint access |
| DELETE | `/api/permissions` | Revoke endpoint access |
| GET | `/api/users/{id}/permissions` | List user's permissions |
| GET | `/api/admin/sessions` | List all sessions |
| POST | `/api/admin/sessions/{id}/close` | Force close any session |
| GET | `/api/admin/audit` | Audit log. Query: `action`, `session_id`, `endpoint_id`, `limit`, `offset` |
| GET | `/api/admin/endpoints` | List all endpoints with runtime info |
| GET | `/api/admin/endpoints/{id}/config` | Get endpoint config override |
| PUT | `/api/admin/endpoints/{id}/config` | Update endpoint config override |

### WebSocket

| Path | Auth | Description |
|------|------|-------------|
| `/ws/client` | JWT (query param or header) | UI client connection |
| `/ws/runtime` | Runtime token | Runtime connection |

## Development

```bash
# Build everything (hub + runtime + UI)
make build

# Build individual components
make build-hub
make build-runtime
make build-ui

# Run in dev mode (three terminals)
make dev-hub        # Runs hub with config.local.json
make dev-runtime    # Runs runtime with config.local.json
make dev-ui         # Runs Vite dev server

# Test
make test           # Go tests
make test-ui        # UI tests (vitest)
make test-all       # Both

# Lint and format
make lint           # golangci-lint
make fmt            # go fmt + prettier

# Docker
make docker-build   # Build images
make docker-up      # Start containers
make docker-down    # Stop containers

# Clean build artifacts
make clean
```

## Troubleshooting

| Problem | Solution |
|---------|----------|
| `go: go.mod requires go >= 1.25.6` | Upgrade Go to 1.25+: https://go.dev/dl/ |
| Hub won't start: `address already in use` | Another process uses the port. Change `server.addr` or stop the other process. |
| Runtime can't connect to hub | Verify `hub.url` in runtime config matches the hub's listen address. Ensure hub is running. |
| UI shows "Unauthorized" | Token expired. Log out and log back in. |
| UI shows no endpoints | Runtime not connected, or user lacks permissions. Check runtime logs and admin panel. |
| Session stuck in "responding" | Agent process may have hung. Close the session from admin panel and restart the runtime. |
| `npm run dev` port conflict | Vite defaults to 3000. Set a different port: `npm run dev -- --port 3001` |
| Docker runtime can't reach hub | Ensure both services share a Docker network. The runtime config should use `ws://hub:8080/ws/runtime` (service name, not localhost). |

## Documentation

Full documentation, guides, and examples: [https://amurg.ai](https://amurg.ai)

## Contributing

Contributions are welcome. Please open an issue to discuss significant changes before submitting a PR.

## License

Apache 2.0 — see [LICENSE](LICENSE) for details.
