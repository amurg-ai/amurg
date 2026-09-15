# Amurg Runtime

The Runtime is a lightweight gateway deployed near agents. It connects outbound to the Hub, manages agent sessions, and forwards messages verbatim.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/amurg-ai/amurg/main/scripts/install.sh | sh
```

## Quick Setup

```bash
amurg-runtime init    # interactive wizard — configures hub connection and endpoints
amurg-runtime run     # start with generated config
```

## Infrastructure Requirements

| Resource | Minimum | Recommended |
|----------|---------|-------------|
| CPU | 1 core | 2 cores |
| RAM | 128 MB + agent overhead | 1 GB |
| OS | Linux/macOS; WSL on Windows for interactive agents (amd64/arm64) | - |
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
| `runtime.idle_timeout` | Chat adapter idle timeout (persistent terminals do not expire) | `10s` |
| `runtime.log_level` | Log level | `info` |

### Endpoints (Agents)

Each endpoint defines an agent the runtime can manage.

### Persistent interactive agents

Choose an agent and working directory in `amurg-runtime init`, then open a
conversation in Amurg. Claude Code, Codex, Gemini CLI, GitHub Copilot, Kilo Code,
and custom interactive commands all run as persistent terminal sessions.
The runtime manages persistence automatically.

```json
{
  "id": "claude-code",
  "name": "Claude Code",
  "profile": "claude-code",
  "claude_code": { "work_dir": "/path/to/project" }
}
```

The runtime selects the normal interactive command for each agent. Existing
`command`, `work_dir`, `env`, and `model` settings carry over. Optional `args`
in the agent's settings add native CLI arguments. Amurg does not add print-mode
flags or restart the agent for each message. Legacy Claude `transport` and CLI
`spawn_policy` settings no longer select a different runtime. Stored chat transcripts
remain in the hub; they are not automatically imported into a new native agent process.

The browser shows the agent's live terminal. Use the mobile composer for
multiline text, editable voice dictation, and file attachments, then press Send.
Uploads are confirmed on the runtime host before their paths accompany the
prompt. Direct keyboard input and touch keys handle native prompts and controls.
Agent permission prompts appear in the terminal. Configured native launch
permissions apply when a new process starts; remote chat permission overrides
are not supported for running interactive processes.

Amurg manages its own background tmux server and session names internally.
The installer provisions tmux on Linux/macOS; the runtime container includes it.
No tmux configuration or commands are needed for normal use. Source builds need
`tmux` available on PATH. On Windows, run the interactive runtime in WSL.

- **Runtime restart:** reconnects to the same live agent process.
- **Browser/network reconnect:** restores the current terminal screen.
  Disconnected keystrokes are never queued or replayed.
- **Close in Amurg:** closes the conversation and releases its attachment.
  The agent stays alive; reopening the conversation reattaches it.
- **Ctrl-C:** interrupts the application, just as in a local terminal.
- **History:** the terminal retains screen/scrollback state. Terminal output is
  not converted into chat messages or a durable raw-I/O journal.
- **Multiple viewers:** share the same terminal and its dimensions.

For host administration, managed sessions live on the `amurg` tmux socket;
`tmux -L amurg list-sessions` lists them. The hub's `native_handle` identifies
the session. Exit the native application to end its process permanently.
Container destruction or a host reboot ends the processes; persistence covers
runtime/client restarts while the host and tmux server remain alive.

Other endpoint types:

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
    "env": {"PS1": "$ "}
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

## CLI Reference

```
amurg-runtime run [config-file]       Start the runtime
amurg-runtime run --config path       Start with explicit config path
amurg-runtime init                    Interactive setup wizard
amurg-runtime init --output path      Write config to specific path
amurg-runtime init --systemd          Also generate a systemd unit file
amurg-runtime version                 Print version and exit
```

Running `amurg-runtime` with no subcommand is equivalent to `amurg-runtime run`.

## Run

**Local development:**
```bash
amurg-runtime run --config runtime/deploy/config.local.json
```

**Production (Docker):**
```bash
docker run -d \
  -v /path/to/config.json:/etc/amurg/config.json:ro \
  amurg-runtime
```

**As a systemd service** (generate with `amurg-runtime init --systemd`):
```ini
[Unit]
Description=Amurg Runtime
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/amurg-runtime run /etc/amurg/config.json
Restart=always
RestartSec=5
# Keep managed interactive processes alive across runtime restarts.
KillMode=process
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
