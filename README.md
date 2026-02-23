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

This gets you Claude Code running through Amurg in a few minutes. You'll have a chat UI you can access from your phone.

### 1. Clone and start with Docker

```bash
git clone https://github.com/amurg-ai/amurg.git
cd amurg
docker compose up -d
```

The hub is now running on `http://localhost:8080`. Open it in your browser and log in with `admin` / `admin`.

### 2. Expose it to your phone

Pick one:

**ngrok** (quickest):

```bash
ngrok http 8080
```

**Cloudflare Tunnel** (free, stable hostname):

```bash
cloudflared tunnel --url http://localhost:8080
```

Open the printed URL on your phone and log in.

### 3. Before you go public

The default config ships with placeholder secrets. Before exposing to the internet, edit the config files:

- **`hub/deploy/config.example.json`** — change `jwt_secret` and `initial_admin.password`
- **`runtime/deploy/config.example.json`** — change `hub.token` to match the hub's `runtime_tokens[].token`

Then restart: `docker compose up -d`.

## Documentation

Full docs, configuration reference, adapter types, API reference, and deployment guides: **[amurg.ai](https://amurg.ai)**

## Contributing

Contributions are welcome. Please open an issue to discuss significant changes before submitting a PR.

## License

Apache 2.0 — see [LICENSE](LICENSE) for details.
