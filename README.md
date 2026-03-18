# Amurg

[![CI](https://github.com/amurg-ai/amurg/actions/workflows/ci.yml/badge.svg)](https://github.com/amurg-ai/amurg/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/amurg-ai/amurg?label=release)](https://github.com/amurg-ai/amurg/releases)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8.svg)](https://go.dev/)

**Self-hosted agent control plane. One surface. Full authority. No inbound ports.**

```
┌─────────┐     ┌─────────┐     ┌─────────┐     ┌─────────┐
│  Agent   │◄───►│ Runtime │────►│   Hub   │◄────│   UI    │
│ (process)│     │  (Go)   │ WS  │  (Go)   │ API │ (React) │
└─────────┘     └─────────┘     └─────────┘     └─────────┘
```

Amurg lets you interact with any agent — CLI tools, batch jobs, HTTP services, or custom protocols — through a single mobile-friendly chat UI. Runtimes connect outbound to the hub, so you never expose inbound ports.

---

## Get Started

### 1. Install

**Linux / macOS:**

```bash
curl -fsSL https://raw.githubusercontent.com/amurg-ai/amurg/main/scripts/install.sh | sh
```

**Windows (PowerShell):**

```powershell
irm https://raw.githubusercontent.com/amurg-ai/amurg/main/scripts/install.ps1 | iex
```

<img src="https://amurg.ai/images/install-script.png" alt="Install script detecting OS, downloading binary, verifying checksum" width="600" />

### 2. Configure

The setup wizard walks you through hub connection, auth token, and agent endpoints:

```bash
amurg-runtime init
```

<img src="https://amurg.ai/images/runtime-init-wizard.png" alt="Interactive runtime setup wizard" width="600" />

### 3. Run

```bash
amurg-runtime run
```

That's it. Your runtime connects to the hub and you can chat with your agents from any device.

---

## Self-Host the Hub

For a full self-hosted stack, you also need the hub. Two options:

### Docker Compose (recommended for the hub)

```bash
git clone https://github.com/amurg-ai/amurg.git
cd amurg
docker compose up -d
```

This starts the hub only. The hub runs on `http://localhost:8080`. Log in with `admin` / `admin`.

Run your runtime separately on the host so it can use your locally installed agent CLIs:

```bash
amurg-runtime init
amurg-runtime run
```

### Standalone Binary

**Linux / macOS:**

```bash
curl -fsSL https://raw.githubusercontent.com/amurg-ai/amurg/main/scripts/install.sh | sh -s -- --binary=amurg-hub
amurg-hub init
amurg-hub run
```

**Windows (PowerShell):**

```powershell
irm https://raw.githubusercontent.com/amurg-ai/amurg/main/scripts/install.ps1 | iex -Binary amurg-hub
amurg-hub init
amurg-hub run
```

<img src="https://amurg.ai/images/hub-init-wizard.png" alt="Hub setup wizard generating JWT secret, admin user, and runtime token" width="600" />

The hub wizard auto-generates a JWT secret, admin credentials, and a runtime token.

---

## Expose to Your Phone

**ngrok** (quickest):

```bash
ngrok http 8080
```

**Cloudflare Tunnel** (free, stable hostname):

```bash
cloudflared tunnel --url http://localhost:8080
```

Open the printed URL on your phone and log in.

## Documentation

Full docs, configuration reference, adapter profiles, API reference, and deployment guides: **[amurg.ai/docs](https://amurg.ai/docs/)**

## Contributing

Contributions are welcome. Please open an issue to discuss significant changes before submitting a PR.

## License

Apache 2.0 — see [LICENSE](LICENSE) for details.
