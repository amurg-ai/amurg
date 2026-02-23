# Amurg Runtime

The Runtime is a lightweight gateway deployed near agents. It connects outbound to the Hub, manages agent sessions, and forwards messages verbatim.

## Infrastructure Requirements

| Resource | Minimum | Recommended |
|----------|---------|-------------|
| CPU | 1 core | 2 cores |
| RAM | 128 MB + agent overhead | 1 GB |
| OS | Linux, macOS, Windows (amd64/arm64) | - |
| Network | Outbound to Hub (WebSocket) | No inbound ports required |

The Runtime needs to be on the same machine (or network) as the agents it manages. It only makes outbound connections — no inbound ports need to be exposed.

## Build

**Binary:**
```bash
# From the repository root
make build-runtime
# Output: bin/amurg-runtime
```

**Docker:**
```bash
docker build -f runtime/deploy/Dockerfile -t amurg-runtime .
```

## Configuration

Copy the example config and edit:
```bash
cp runtime/deploy/config.example.json runtime/deploy/config.local.json
```

### Hub Connection

| Field | Description | Default |
|-------|-------------|---------|
| `hub.url` | Hub WebSocket URL | `ws://localhost:8090/ws/runtime` |
| `hub.token` | Pre-shared auth token (must match hub config) | - |
| `hub.tls_skip_verify` | Skip TLS verification (dev only) | `false` |
| `hub.reconnect_interval` | Initial reconnect delay | `2s` |
| `hub.max_reconnect_delay` | Max backoff for reconnect | `60s` |

### Runtime Settings

| Field | Description | Default |
|-------|-------------|---------|
| `runtime.id` | Unique runtime identifier | - |
| `runtime.max_sessions` | Max concurrent sessions | `10` |
| `runtime.default_timeout` | Default session timeout | `30m` |
| `runtime.max_output_bytes` | Max output buffer per session | `10485760` (10 MB) |
| `runtime.idle_timeout` | CLI idle detection timeout | `10s` |
| `runtime.log_level` | Log level | `info` |

### Endpoints (Agents)

Each endpoint defines an agent the runtime can manage. Four adapter profiles are supported:

**CLI** — Long-running interactive process (bash, python, etc.):
```json
{
  "id": "bash-shell",
  "name": "Bash Shell",
  "profile": "generic-cli",
  "cli": {
    "command": "bash",
    "args": ["--norc", "--noprofile", "-i"],
    "work_dir": "/path/to/project",
    "env": {"PS1": "$ ", "TERM": "dumb"},
    "spawn_policy": "per-session"
  }
}
```

**Job** — Runs command per message, exits with code:
```json
{
  "id": "test-runner",
  "name": "Go Tests",
  "profile": "generic-job",
  "job": {
    "command": "bash",
    "args": ["-c", "cd /project && go test -v ./..."],
    "max_runtime": "30s"
  }
}
```

**HTTP** — Forwards messages as HTTP requests:
```json
{
  "id": "my-api",
  "name": "Agent API",
  "profile": "generic-http",
  "http": {
    "base_url": "http://localhost:9000/chat",
    "method": "POST",
    "timeout": "30s"
  }
}
```

**External** — JSON-Lines stdio protocol for custom adapters:
```json
{
  "id": "my-agent",
  "name": "Custom Agent",
  "profile": "external",
  "external": {
    "command": "/path/to/adapter",
    "args": ["--mode", "chat"],
    "work_dir": "/workspace"
  }
}
```

See the [External Adapter Protocol](../specs.md) for the JSON-Lines message format.

## Run

**Local development:**
```bash
./bin/amurg-runtime -config runtime/deploy/config.local.json
```

**Production (Docker):**
```bash
docker run -d \
  -v /path/to/config.json:/etc/amurg/config.json:ro \
  amurg-runtime
```

**As a systemd service:**
```ini
[Unit]
Description=Amurg Runtime
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/amurg-runtime -config /etc/amurg/config.json
Restart=always
RestartSec=5
User=amurg
Group=amurg

[Install]
WantedBy=multi-user.target
```

## Deployment Patterns

**Same machine as hub** (simple setup):
```
hub.url = "ws://localhost:8080/ws/runtime"
```

**Remote machine over Tailscale/WireGuard:**
```
hub.url = "wss://hub.tailnet:8080/ws/runtime"
```

**Kubernetes sidecar:**
Deploy as a sidecar container alongside your agent pod. The runtime connects outbound to the hub — no ingress needed.
