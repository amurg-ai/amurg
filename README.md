# Amurg

**Self-hosted agent control plane. One surface. Full authority. No inbound ports.**

![Amurg desktop view](docs/desktop-session-history.png)

Amurg gives you a single, mobile-friendly interface to interact with any agent — CLI tools, batch jobs, HTTP services, or custom protocols — through a secure hub that requires zero inbound ports.

## Why Amurg

- **Control, not platform** — Amurg doesn't wrap your tools. It connects to them. Run agents your way with CLI, job, HTTP, or external adapters.
- **No inbound ports** — Runtimes connect outbound to the hub. Deploy behind NAT, VPNs, or air-gapped networks with zero attack surface.
- **No SaaS dependency** — Your hub, your data, your rules. SQLite database, single binary, MIT license.

## Features

| Feature | Description |
|---|---|
| Rich rendering | Markdown, ANSI, JSON, and diff output with syntax highlighting and copy buttons |
| Voice input | Push-to-talk or tap-to-toggle with Web Speech API or local Whisper server |
| Multi-adapter runtime | CLI, job, HTTP, and external (JSON-Lines) adapters for any agent type |
| Turn-based execution | Message → response → exit code flow with elapsed time and turn separators |
| Audit log | Login, message, session, and runtime events with full accountability trail |
| Session management | Per-user sessions with idle timeout, max limits, and admin dashboard |
| Admin dashboard | Runtime status, user management, session overview, and audit log viewer |
| Mobile-first UI | Responsive design that works on phones, tablets, and desktops |

## Architecture

```
┌─────────┐     ┌─────────┐     ┌─────────┐     ┌─────────┐
│  Agent   │◄───►│ Runtime │────►│   Hub   │◄────│   UI    │
│ (process)│     │  (Go)   │ WS  │  (Go)   │ API │ (React) │
└─────────┘     └─────────┘     └─────────┘     └─────────┘
```

- **[Hub](hub/README.md)** — Central server handling authentication (JWT), API routing, WebSocket message relay, and persistence (SQLite). Serves the UI static bundle.
- **[Runtime](runtime/README.md)** — Gateway that sits near your agents. Manages process lifecycle, session state, and adapter selection. Connects outbound to the hub via WebSocket.
- **[UI](ui/README.md)** — React chat client (TypeScript, Vite, Tailwind, Zustand). Mobile-friendly with voice input, rich rendering, and real-time updates.

## Quick Start

### Prerequisites

- Go 1.22+
- Node.js 20+

### Build

```bash
make build
```

### Run

Start all three components:

```bash
# Hub (port 8090)
./bin/amurg-hub -config hub/deploy/config.local.json

# Runtime (connects to hub)
./bin/amurg-runtime -config runtime/deploy/config.local.json

# UI dev server (port 3000)
cd ui && npm run dev -- --port 3000
```

Open `http://localhost:3000` and log in with `admin` / `admin`.

### Configure Endpoints

Edit `runtime/deploy/config.local.json` to add agent endpoints:

**CLI adapter** (long-running, interactive):
```json
{
  "id": "bash-shell",
  "name": "Bash Shell",
  "profile": "generic-cli",
  "cli": {
    "command": "bash",
    "args": ["--norc", "--noprofile", "-i"],
    "work_dir": "/path/to/project",
    "spawn_policy": "per-session"
  }
}
```

**Job adapter** (runs per message, exits):
```json
{
  "id": "test-runner",
  "name": "Go Tests",
  "profile": "generic-job",
  "job": {
    "command": "bash",
    "args": ["-c", "cd /path/to/project && go test -v ./..."],
    "max_runtime": "30s"
  }
}
```

### Production (Docker)

```bash
docker compose build
docker compose up -d
```

## Infrastructure Requirements

| Component | CPU | RAM | Disk | Network |
|-----------|-----|-----|------|---------|
| Hub | 1-2 cores | 256-512 MB | 100 MB + data | Inbound port (default 8080) |
| Runtime | 1-2 cores | 128 MB + agent overhead | Minimal | Outbound to Hub only |
| UI | Static files | - | ~2 MB | Served by Hub or CDN |

## Project Structure

```
hub/                Hub server (auth, API, WebSocket routing, SQLite)
  deploy/           Dockerfile, config examples
runtime/            Runtime gateway (adapters, session manager, hub client)
  deploy/           Dockerfile, config examples
ui/                 React frontend (Vite + Tailwind + Zustand)
pkg/protocol/       Shared wire protocol types
docker-compose.yml  Multi-container deployment
```

## Contributing

Contributions are welcome. Please open an issue to discuss significant changes before submitting a PR.

## License

MIT
