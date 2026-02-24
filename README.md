# Amurg

[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8.svg)](https://go.dev/)

**Self-hosted agent control plane. One surface. Full authority. No inbound ports.**

## What is Amurg

Amurg is a self-hosted system that lets you interact with any agent — CLI tools, batch jobs, HTTP services, or custom protocols — through a single mobile-friendly chat UI. Runtimes connect outbound to the hub, so you never expose inbound ports.

```
┌─────────┐     ┌─────────┐     ┌─────────┐     ┌─────────┐
│  Agent   │◄───►│ Runtime │────►│   Hub   │◄────│   UI    │
│ (process)│     │  (Go)   │ WS  │  (Go)   │ API │ (React) │
└─────────┘     └─────────┘     └─────────┘     └─────────┘
```

## Get Started

### Quick Install (runtime binary)

Install the runtime and configure it interactively:

```bash
curl -fsSL https://raw.githubusercontent.com/amurg-ai/amurg/main/scripts/install.sh | sh
amurg-runtime init
amurg-runtime run
```

This downloads a pre-built binary, walks you through setup (hub URL, auth token, agent endpoints), and starts the runtime.

### Full Stack (Docker Compose)

Run both the hub and runtime together:

```bash
git clone https://github.com/amurg-ai/amurg.git
cd amurg
docker compose up -d
```

The hub is now running on `http://localhost:8080`. Log in with `admin` / `admin`.

### Expose it to your phone

**ngrok** (quickest):

```bash
ngrok http 8080
```

**Cloudflare Tunnel** (free, stable hostname):

```bash
cloudflared tunnel --url http://localhost:8080
```

Open the printed URL on your phone and log in.

### Production setup

Before exposing to the internet, generate secure configs using the init wizards:

```bash
amurg-hub init       # generates JWT secret, admin credentials, runtime token
amurg-runtime init   # configure hub connection and agent endpoints
```

The wizards auto-generate secrets and walk you through every setting.

## Documentation

Full docs, configuration reference, adapter types, API reference, and deployment guides: **[amurg.ai](https://amurg.ai)**

## Contributing

Contributions are welcome. Please open an issue to discuss significant changes before submitting a PR.

## License

Apache 2.0 — see [LICENSE](LICENSE) for details.
