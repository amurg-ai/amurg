# Amurg — Architectural Decisions

This document records all significant architectural and technology decisions made during development.

---

## D001: Monorepo Structure

**Decision:** Single repository with all three components (runtime, hub, UI).

**Rationale:** Shared protocol types between runtime and hub make a monorepo natural. Single Go module for both Go components. UI is separate (TypeScript) but benefits from co-location for Docker builds and CI.

---

## D002: Go for Runtime and Hub

**Decision:** Both Runtime and Hub are written in Go.

**Rationale:** Go excels at concurrent network services, has excellent WebSocket support, compiles to static binaries (easy deployment), and sharing a single Go module allows shared protocol types without versioning overhead.

---

## D003: WebSocket for All Real-Time Communication

**Decision:** WebSocket (RFC 6455) for both runtime↔hub and client↔hub connections.

**Rationale:**
- Works through NAT/firewalls (outbound HTTP upgrade)
- Native browser support (no gRPC-web proxy needed)
- Bidirectional streaming over a single connection
- Simpler than gRPC for this use case
- Well-supported in Go (`gorilla/websocket`) and browsers

gRPC was considered but rejected because the UI client would need gRPC-web + proxy, adding deployment complexity for v0.

---

## D004: SQLite for Hub Storage (v0)

**Decision:** SQLite via `modernc.org/sqlite` (pure Go, no CGO required).

**Rationale:**
- Zero external dependencies (self-hosted goal)
- Single file database, trivial backup
- More than sufficient for v0 scale
- Pure Go driver means static binary compilation
- Can migrate to PostgreSQL later via storage interface

---

## D005: chi Router for Hub API

**Decision:** Use `github.com/go-chi/chi/v5` for HTTP routing.

**Rationale:** Lightweight, idiomatic Go, stdlib `net/http` compatible, good middleware ecosystem. Preferred over gin/echo for simplicity and stdlib alignment.

---

## D006: JWT for User Authentication

**Decision:** JWT tokens for user session auth, bcrypt for password hashing.

**Rationale:**
- Stateless token verification (no session store needed)
- Self-contained (no external auth service required for v0)
- Standard, well-understood
- HMAC-SHA256 signing with configurable secret

---

## D007: Pre-shared Token for Runtime Authentication (v0)

**Decision:** Runtime authenticates to hub using a pre-shared bearer token.

**Rationale:** Simplest auth method that satisfies the spec requirement. Token is configured in both runtime and hub config. Can add mTLS or keypair auth later. Token is revocable by regenerating on the hub side.

---

## D008: React + TypeScript + Vite for UI

**Decision:** React 18, TypeScript, Vite build tool, Tailwind CSS.

**Rationale:**
- React: largest ecosystem, best component library support
- TypeScript: type safety for protocol messages
- Vite: fast dev server, optimized builds
- Tailwind: mobile-first utility CSS, no component library lock-in

---

## D009: Zustand for UI State Management

**Decision:** Zustand instead of Redux/Context.

**Rationale:** Minimal boilerplate, no providers needed, works well with WebSocket-driven state updates, TypeScript-friendly. Much simpler than Redux for this use case.

---

## D010: Web Speech API for Voice Input

**Decision:** Use the browser's built-in Web Speech API for speech-to-text.

**Rationale:** No server-side speech processing needed, works on mobile browsers (Chrome, Safari), zero dependencies. Falls back gracefully when unavailable.

---

## D011: JSON Wire Protocol

**Decision:** JSON-encoded messages over WebSocket for all protocol communication.

**Rationale:**
- Human-readable for debugging
- Native browser support (no protobuf compilation step)
- Sufficient performance for chat-style interactions
- Easy to extend with new message types
- Can switch to binary (msgpack/protobuf) later if needed

---

## D012: Profile System Design

**Decision:** Profiles are implemented as Go interfaces with a registry pattern. Each profile is a separate file implementing the `Adapter` interface.

**Profiles implemented:**
| Profile | Execution Model | Completion Detection | Resume |
|---------|----------------|---------------------|--------|
| `generic-cli` | interactive | idle timeout / process exit | no |
| `generic-job` | run-to-completion | process exit | no |
| `generic-http` | request/response | response end | no |
| `claude-code` | interactive | idle timeout / process exit | no |
| `github-copilot` | interactive | idle timeout / process exit | no |
| `codex` | run-to-completion | process exit | no |

---

## D013: Hub Serves UI Static Files

**Decision:** Hub serves the built UI as static files from an embedded or configured directory.

**Rationale:** Single deployment target for self-hosted users. No separate web server needed. Hub already has an HTTP server. UI build artifacts are just static files.

---

## D014: Structured Logging with slog

**Decision:** Use Go's `log/slog` (stdlib, Go 1.21+) for structured logging.

**Rationale:** No external dependency, structured JSON output, leveled logging, context-aware. Standard library solution is sufficient.

---

## D015: Message Ordering and Idempotency

**Decision:**
- Messages carry a client-generated UUID for idempotency
- Hub assigns monotonic sequence numbers per session for ordering
- Hub deduplicates by message ID within a session

**Rationale:** Satisfies spec §7.3 (idempotency) and §7.2 (ordering). UUIDs are generated client-side to support retry without server round-trip.

---

## D016: In-Memory SQLite Shared Cache

**Decision:** When DSN is `:memory:`, automatically rewrite to `file::memory:?cache=shared` so all connections in the `database/sql` pool share the same database.

**Rationale:** Without shared cache, each pooled connection opens a separate in-memory database. This causes intermittent failures where login succeeds on one connection but queries on another connection return empty results or errors. Discovered during integration testing.

---

## D017: Non-Blocking Data Load After Login

**Decision:** UI fires `loadEndpoints()` and `loadSessions()` in a fire-and-forget pattern after login, catching errors silently.

**Rationale:** If these calls fail (e.g. due to timing issues or transient errors), the login itself should still succeed. The user can manually refresh or the data will load on next navigation. Previously, a failure in loading sessions propagated up and showed "Invalid credentials" even though login succeeded.

---

## D018: Rich Content Rendering Pipeline

**Decision:** Content-type detection pipeline in the UI renders agent output using specialized renderers: ANSI (ansi-to-react), Diff (highlight.js), JSON (highlight.js + pretty-print), Markdown (react-markdown + remark-gfm + rehype-highlight), and plain text.

**Rationale:** Agent output contains diverse content types (terminal colors, code, JSON payloads, diff patches). Automatic detection per message avoids requiring agents to declare content type. Detection order: ANSI escape codes > unified diff > valid JSON > markdown (default for agent stdout) > plain text. User messages are always plain text. Long outputs (>30 lines) are collapsible. Code blocks include copy buttons.

---

## D019: Per-Endpoint Authorization

**Decision:** Optional per-endpoint access control via `endpoint_permissions` table. Controlled by `auth.default_endpoint_access` config: `"all"` (default, backward compatible) grants all users access to all endpoints; `"none"` requires explicit grants.

**Rationale:** Self-hosted deployments may want to restrict which users can access which agents (e.g., production CLI vs dev sandbox). The `"all"` default preserves zero-config simplicity while the `"none"` mode enables enterprise-style access control. Admins always see all endpoints.

---

## D020: External Stdio Adapter Protocol

**Decision:** External adapters communicate with the runtime via JSON-Lines over stdin/stdout. Messages are multiplexed by session_id. Types: `session.start`, `user.input`, `stop`, `session.close` (runtime→adapter) and `output`, `turn.complete` (adapter→runtime).

**Rationale:** JSON-Lines over stdio is the simplest possible IPC mechanism — any language can implement it with just JSON parsing and stdin/stdout. The adapter process is long-lived and handles multiple sessions, avoiding per-message process spawn overhead. This enables custom agent integrations without modifying the runtime.

---

## D021: Hub Turn Gating

**Decision:** When `session.turn_based` is enabled, the hub rejects user messages sent while a session is in "responding" state, returning a `turn_in_progress` error.

**Rationale:** Prevents message interleaving in agents that don't handle concurrent input (most CLI tools, AI agents). Enforced at the hub level (not UI) so all clients respect the constraint. Off by default for backward compatibility.

---

## D022: Exit Code Forwarding

**Decision:** Job adapter captures process exit codes and forwards them through the session manager to the hub via `TurnCompleted.exit_code`.

**Rationale:** Exit codes are essential for understanding whether a job succeeded or failed. The UI already has the `exit_code` field in the protocol types. The runtime captures `cmd.ProcessState.ExitCode()` after process exit.

---

## D023: Session Idle Reaper

**Decision:** Hub runs a background goroutine that periodically checks for sessions idle longer than `session.idle_timeout` and closes them.

**Rationale:** Prevents resource leaks from abandoned sessions. Checks every minute, configurable timeout (default 30 minutes). Logs `session.idle_close` audit events for visibility.

---

## D024: Generalized Endpoint Security Profile

**Decision:** Every endpoint can declare a `SecurityConfig` with `allowed_paths`, `denied_paths`, `allowed_tools`, `permission_mode` ("skip"/"strict"/"auto"), `cwd`, and `env_whitelist`. This config travels from runtime config → protocol registration → hub store → UI display.

**Rationale:** Previously only `claude-code` had `permission_mode` and `allowed_tools` in its profile-specific config. Generalizing to a top-level `security` block on `EndpointConfig` makes these controls available to all profiles (CLI, job, external, etc.) and enables the hub to store and display security posture. The security block takes precedence over profile-specific fields when both are set.

---

## D025: Runtime Permission Request/Response Protocol

**Decision:** New `permission.request` / `permission.response` message types allow agents to request user approval at runtime. The hub tracks pending permissions with a configurable timeout (default 60s, auto-deny on expiry). The UI shows a banner with approve/deny buttons and an "always allow this tool" checkbox.

**Rationale:** Static `--allowedTools` and `--dangerously-skip-permissions` are too coarse. Interactive permission prompting gives users fine-grained control over what tools agents use, matching the approval UX of Claude Code's native terminal mode. The external adapter is the guaranteed interactive path (JSON-Lines `permission.request`/`permission.response`). The flow: adapter → session manager → runtime → hub (tracks + relays) → UI → hub → runtime → adapter.

---

## D026: Enhanced Audit Logging

**Decision:** `AuditEvent.Detail` changed from `string` to `json.RawMessage` for structured data. Added `endpoint_id` field. New events: `session.create_denied`, `turn.completed` (with `duration_ms`), `permission.requested/granted/denied/timeout`. Added `AuditFilter` with prefix-match on action and exact-match on session/endpoint IDs.

**Rationale:** String details are opaque and hard to query. Structured JSON enables filtering, dashboards, and automated analysis. The `endpoint_id` field allows correlating events to specific agents. Turn timing (`duration_ms`) provides performance visibility. Permission audit events create a complete approval trail.

---

## D027: First-Class Agent CLI Adapters (Copilot, Codex, Kilo Code)

**Decision:** Replaced stub adapters for GitHub Copilot CLI and OpenAI Codex CLI with proper spawn-per-Send implementations. Added new Kilo Code CLI adapter. All three follow the same pattern as the Claude Code adapter: spawn a new process per `Send()`, parse structured output, and use resume/continue flags for session continuity. Each has a dedicated config struct (`CopilotConfig`, `CodexConfig`, `KiloConfig`) on `EndpointConfig`.

**Details:**
- **Copilot CLI** (`copilot -p --silent --no-color`): Plain text output (no JSONL yet), `--continue` for session resume, `--allow-all`/`--allow-tool`/`--deny-tool` for permissions, `--model` for model selection.
- **Codex CLI** (`codex exec --json`): Full JSONL streaming with event types (`turn.started`, `item.completed`, etc.), `codex exec resume --last` for session resume, `--ask-for-approval` (untrusted/on-request/never) + `--sandbox` for permissions, `--cd` for working directory, `--model` for model selection.
- **Kilo Code CLI** (`kilo run --auto --json`): JSON messages on stdout, `--continue` for session resume, `--yolo` for auto-approve, model/provider via `KILO_MODEL`/`KILO_PROVIDER` env vars.

**Rationale:** The original stubs (copilot wrapping deprecated `gh copilot suggest`, codex wrapping the generic job adapter) didn't use the actual CLI features. All three modern agent CLIs support non-interactive modes, structured output, and session continuity — matching the same pattern already proven with the Claude Code adapter. Dedicated config structs expose each tool's native flags (models, permission modes, sandbox policies) without overloading the generic SecurityConfig.
