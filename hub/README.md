# Amurg Hub

The Hub is the central server component. It handles authentication, message routing, session persistence, and serves the web UI.

## Infrastructure Requirements

| Resource | Minimum | Recommended |
|----------|---------|-------------|
| CPU | 1 core | 2 cores |
| RAM | 256 MB | 512 MB |
| Disk | 100 MB + data | 1 GB+ for transcripts |
| OS | Linux (amd64/arm64) | - |
| Network | Inbound port (default 8080) | Behind reverse proxy with TLS |

## Build

**Binary:**
```bash
# From the repository root
make build-hub
# Output: bin/amurg-hub
```

**Docker:**
```bash
docker build -f hub/deploy/Dockerfile -t amurg-hub .
```

## Configuration

Copy the example config and edit:
```bash
cp hub/deploy/config.example.json hub/deploy/config.local.json
```

| Field | Description | Default |
|-------|-------------|---------|
| `server.addr` | Listen address | `:8080` |
| `server.ui_static_dir` | Path to built UI files (empty = don't serve) | `/var/lib/amurg/ui` |
| `auth.jwt_secret` | JWT signing secret (min 32 chars) | **change me** |
| `auth.jwt_expiry` | Token lifetime | `24h` |
| `auth.runtime_tokens` | Pre-shared tokens for runtime auth | - |
| `auth.initial_admin` | Bootstrap admin credentials | `admin/admin` |
| `storage.driver` | Storage backend | `sqlite` |
| `storage.dsn` | SQLite database path (`:memory:` for dev) | `/var/lib/amurg/data/amurg.db` |
| `storage.retention` | Message retention duration | `720h` (30 days) |
| `session.max_per_user` | Max concurrent sessions per user | `20` |
| `session.idle_timeout` | Auto-close idle sessions after | `30m` |
| `session.turn_based` | Enforce turn-based input | `true` |
| `logging.level` | Log level: debug, info, warn, error | `info` |
| `logging.format` | Log format: text or json | `json` |

## Run

**Local development:**
```bash
# Uses in-memory SQLite, debug logging, port 8090
./bin/amurg-hub -config hub/deploy/config.local.json
```

**Production (Docker):**
```bash
docker run -d \
  -p 8080:8080 \
  -v /path/to/config.json:/etc/amurg/config.json:ro \
  -v amurg-data:/var/lib/amurg/data \
  amurg-hub
```

## Endpoints

| Path | Description |
|------|-------------|
| `POST /api/auth/login` | Authenticate user |
| `GET /api/auth/me` | Get current user |
| `GET /api/endpoints` | List available agent endpoints |
| `GET /api/sessions` | List user sessions |
| `POST /api/sessions` | Create new session |
| `GET /api/sessions/{id}/messages` | Get session messages (paginated) |
| `POST /api/sessions/{id}/close` | Close a session |
| `GET /ws` | Client WebSocket |
| `GET /ws/runtime` | Runtime WebSocket |
| `GET /healthz` | Health check |

## Security Notes

- Always change `jwt_secret` and `runtime_tokens` in production
- Change the default admin password after first login
- Put behind a reverse proxy (nginx/caddy) with TLS in production
- The hub sees all message content in plaintext (self-hosted trust model)
