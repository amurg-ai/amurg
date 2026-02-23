Amurg v0 — System Requirements (Runtime + Hub + UI)
0) Scope and guiding principles
Goal
Provide a self-hosted, open-source system that enables a user to interact with agents through a mobile-friendly chat UI, with voice/typing input, while agents run anywhere (prod, dev, CI, local) and are reachable without exposing inbound ports.
Non-goals
No new agent framework.
No tool/capability injection into agents.
No command interpretation or intent parsing.
No runtime-side shaping, parsing, formatting, media extraction, or file uploading on behalf of agents.
No claim of being “safer than SSH” beyond the security properties explicitly described below.

---
1) Components
1.1 Amurg Runtime (the “gateway”)
A lightweight process deployed near the agent (same machine, container, pod, or environment).
Responsibilities
Maintain a secure outbound connection to the Hub.
Authenticate as a specific runtime identity.
Provide a multiplexed transport for multiple agent sessions/endpoints.
Forward user messages to the agent adapter verbatim.
Forward agent output back to the Hub verbatim.
Provide minimal transport metadata when available from the adapter (e.g., stream open/close, stdout/stderr channel, process exit).
Enforce basic session mechanics if needed (connect/disconnect, stop/terminate where the adapter supports it).

Explicitly NOT allowed
Parsing or transforming content (no code-fence detection, no markdown shaping, no log folding).
Uploading or reading user/agent files for rendering (no “attach /tmp/image.png” automation).
Creating images/videos or turning local files into artifacts.
Adding semantics such as “completed” unless it is strictly derived from adapter mechanics (e.g., process exit).

---
1.2 Amurg Hub (server)
A self-hosted service that acts as the routing, persistence, and policy boundary between clients and runtimes.
Responsibilities
Accept inbound connections from clients (web/mobile) and runtimes.
Authenticate and authorize both sides.
Route messages between client sessions and runtime sessions.
Persist conversation transcripts and related metadata (as configured).
Provide search/index over persisted transcripts (optional for v0).
Expose a stable API to:
list runtimes / endpoints
create/close sessions
fetch history

Provide “stop/cancel” signals to runtimes (best-effort, adapter-dependent).
Host the UI (or serve the UI frontend separately).

Trust boundary (v0)
The hub is trusted to read message payloads (plaintext within the hub boundary). This is acceptable because deployments are self-hosted.

---
1.3 Amurg UI (client)
A user-facing web/mobile interface connected to the Hub.
Responsibilities
Provide a chat-first interface to each agent session.
Provide voice dictation (speech-to-text) and typed input.
Provide edit-before-send for dictated text.
Enforce turn-based UX rules (see §4).
Render agent output with rich presentation transforms in the UI, without requiring agents or runtime to emit structured UI objects.

Allowed shaping (UI-side only)
Code block detection and syntax highlighting.
ANSI terminal rendering (optional).
Collapsing / folding long outputs.
Linkification (URLs, paths, hashes).
Diff rendering if output resembles a diff.
Safe markdown rendering (sanitized).
Embedding media if the agent outputs valid references/URLs that are accessible to the UI.

Explicitly NOT required
UI does not need to “understand” commands or agent intent.
UI does not need to provide tool calling or agent reasoning.

---
2) Agent definition and runtime adjacency
2.1 What is an “agent” in Amurg?
An “agent” is whatever produces output in response to user input and lives behind a runtime adapter. Examples:
Interactive CLI process (stdin/stdout).
Run-to-completion command or script.
HTTP service responding to requests.
Any program that can be driven via a local mechanism the runtime supports.

2.2 How agents receive instructions
Agents do not subscribe to hub streams directly. The runtime does.
The runtime receives user input from the hub.
The runtime forwards input to the agent via its adapter mechanism.

No requirement that agents run an event loop, maintain sockets, or implement “ack/completed.”

---
3) Communication model
3.1 Topology
Runtime maintains an outbound-only connection to Hub.
Client connects to Hub.
Hub routes messages between client and runtime.

No direct client↔runtime connectivity is required.
3.2 Transport requirements (runtime↔hub)
Must support bidirectional streaming.
Must support reconnect and session resume (see §4.3).
Must operate over standard outbound connectivity (works behind NAT/firewalls).
Must be implementable with open-source components only.

(Implementation choices are not mandated here; WebSocket or gRPC streaming are acceptable.)
3.3 Payload handling
Runtime forwards payloads verbatim.
Hub stores and forwards payloads verbatim.
UI may apply presentation transforms, but must preserve the original payload as the canonical transcript.

3.4 Optional hub internals
The hub may use an internal message backbone (Redis/NATS/Kafka) for scaling/durability, but this is an implementation detail:
Runtimes do not connect to the backbone directly.
Backbone is internal to the hub deployment boundary.

---
4) Session and interaction requirements
4.1 Sessions
A session represents an active conversation between:
a user/client and
a specific agent endpoint behind a specific runtime.

Sessions must support:
streaming output (incremental updates)
persisted history (configurable)
reconnection without losing context

4.2 Turn-based user experience (required)
To avoid agent confusion and degraded UX:
The UI must be able to operate in turn-based mode:
user sends one message
UI prevents sending another until the agent response is considered complete

Completion determination is adapter-driven, not agent-signaled:
Run-to-completion: completion when the command exits.
HTTP: completion when response ends.
Interactive CLI: completion inferred by stream closure or idle timeout since last output (hub/UI policy).

Turn-based enforcement may be implemented in:
UI only (preferred for simplicity), and/or
hub policy (optional), and/or
runtime gating (optional), but without requiring agent cooperation.

4.3 Reliability and resume (required)
Because mobile networks drop:
Connections must support automatic reconnect.
Sessions must support resume such that:
previously delivered outputs are not duplicated in the UI
missing outputs can be replayed from hub persistence/buffer
client messages are idempotent (retries do not cause duplicate execution)

This requires:
stable message identifiers
ordered delivery per session
hub-side persistence or buffering sufficient to replay recent session output

---
5) Security requirements (self-hosted, hub-trusted)
5.1 Authentication
Runtime must authenticate to hub as a distinct runtime identity.
User/client must authenticate to hub as a user identity.
Hub must authorize which users can access which runtimes/endpoints.

No dependence on external hosted identity providers is required; self-hosted auth is acceptable.
5.2 Transport security
All connections must be encrypted in transit (TLS).
Runtime connections must be outbound-only to avoid exposing inbound ports.

5.3 Authorization boundaries
Hub is the policy enforcement point:
which user can open sessions to which runtime/endpoint
which operations are allowed (connect, send message, stop)

Runtime must reject requests that do not match its authenticated identity context (basic sanity).

5.4 Audit and retention
Hub must allow configurable retention of transcripts and metadata.
Hub must support basic auditing capabilities:
who connected to what
when messages were sent
session open/close events

(Exact audit schema is out of scope here.)

---
6) Output rendering and media handling
6.1 Rendering
The UI is responsible for making raw outputs readable.
Rendering must be safe (no arbitrary HTML/script execution).
Rendering must not alter the canonical stored transcript.

6.2 Media
Because runtime does not upload or extract files:
Media can be rendered only if the agent provides:
accessible URLs, or
hub-recognized references (if the agent itself uploads to the hub via a separate mechanism)

Media upload workflows are optional and agent-driven; they are not a runtime responsibility.

---
7) Deployment requirements
7.1 Self-hosting
All components must be deployable self-hosted.
No dependency on proprietary SaaS services.

7.2 Runtime deployment targets
Must support running as:
system service on a VM/bare metal
container in Kubernetes (sidecar/daemon/service)
local daemon on a developer machine

7.3 Hub deployment targets
Must support running as a containerized service.
Should support deployment on Kubernetes and single-node Docker setups.

---
8) Explicit boundaries summary
Runtime boundary
Trusted to connect securely and forward bytes.
Not trusted/allowed to parse, format, upload, or interpret.

Hub boundary
Trusted in v0 to see plaintext (self-hosted assumption).
Responsible for auth, routing, storage, basic policy.

UI boundary
Allowed to shape and render content for usability.
Must remain non-executing/safe (sanitize).

# Amurg Runtime v0 Specification

## 1. Purpose

The Amurg Runtime is a **gateway process** that:

- Connects securely to the Hub (outbound only)
- Hosts one or more configured Agent Endpoints
- Maps Hub sessions to native agent sessions
- Streams input/output between Hub and agents
- Enforces minimal operational limits

It does **not**:

- Shape content
- Render UI structures
- Upload local files
- Interpret commands

---

# 2. Connectivity

## 2.1 Network Model

- Runtime maintains a **single persistent outbound TLS connection** to the Hub.
- No inbound ports are required.
- Connection must support bidirectional streaming.
- Runtime must auto-reconnect with backoff.

## 2.2 Connection Lifecycle

On startup:

1. Runtime loads configuration.
2. Establishes TLS connection to Hub.
3. Authenticates using configured identity.
4. Registers available agent endpoints.
5. Waits for session events.

On disconnect:

- Retry connection with exponential backoff.
- On reconnect, re-register endpoints.
- Resume any active sessions if supported by profile.

---

# 3. Security Infrastructure

## 3.1 Transport Security

- All traffic runtime ↔ hub must use TLS.
- Certificate validation must be enforced.
- Runtime must not allow plaintext fallback.

## 3.2 Runtime Identity

Each runtime has a unique identity within a hub instance.

Authentication methods supported (v0 minimal requirement):

- Static keypair (public/private key)
- Or pre-issued runtime token
- Or mTLS client certificate

The chosen method must:

- Bind the runtime to a specific Hub instance
- Be revocable by the Hub
- Not require third-party SaaS

## 3.3 Authorization

Hub determines:

- Which runtime is allowed to connect
- Which endpoints are allowed
- Which users may open sessions on that runtime

Runtime must reject commands not bound to its authenticated identity context.

---

# 4. Configuration

Runtime configuration is declarative (JSON).

Single config file per runtime instance.

---

## 4.1 Top-Level Structure

The configuration file must define:

- Hub connection settings
- Runtime identity credentials
- Global runtime limits
- Agent endpoint definitions

---

## 4.2 Hub Section

Must include:

- Hub URL
- Authentication credentials
- Optional TLS settings
- Reconnect policy parameters

---

## 4.3 Global Runtime Settings

Must support:

- Max concurrent sessions
- Default session timeout
- Max output size per session
- Idle timeout for inferred completion
- Logging level

Runtime must enforce these limits locally.

---

# 5. Agent Profiles

Agent Profiles define how the runtime interacts with a specific agent type.

Profiles are shared definitions between Hub and Runtime.

Each endpoint references one profile.

---

## 5.1 Profile Types (v0 Required)

Runtime must support:

1. `generic-cli`
2. `generic-job`
3. `generic-http`

Additionally, runtime may support:

1. Named integrations (e.g. `cloud-code`, `codex`, etc.)

---

## 5.2 Profile Capabilities Declaration

Each profile must declare:

- Supports native session IDs: true/false
- Supports explicit turn completion events: true/false
- Supports resume/attach: true/false
- Execution model:
    - interactive
    - request/response
    - run-to-completion

This metadata is known to both runtime and hub.

---

# 6. Endpoint Configuration

Each agent endpoint entry must include:

- `id` (stable identifier)
- `profile` (profile name)
- Profile-specific parameters
- Operational limits
- Metadata tags

---

## 6.1 Generic CLI Endpoint Requirements

Must define:

- Executable path
- Arguments
- Working directory
- Environment variables (optional)
- Spawn policy:
    - per session
    - persistent

Runtime responsibilities:

- Spawn process
- Pipe stdin/stdout
- Stream stdout/stderr separately if available
- Detect process exit
- Infer completion via:
    - process exit, or
    - idle timeout

---

## 6.2 Generic Job Endpoint Requirements

Must define:

- Command template or fixed command
- Execution environment
- Max runtime duration

Runtime responsibilities:

- Execute command per turn
- Stream stdout/stderr
- Report exit code
- Terminate on timeout

---

## 6.3 Generic HTTP Endpoint Requirements

Must define:

- Base URL
- Method (if fixed)
- Headers (optional)
- Timeout

Runtime responsibilities:

- Send user message as request
- Stream or buffer response
- Mark completion on response end

---

## 6.4 Integrated Agent Profile Requirements

For named integrations:

Profile must define:

- How to create native session
- How to attach to native session
- How to obtain native session ID
- How to detect turn start
- How to detect turn completion
- Whether native history can be queried

Runtime must:

- Store native session handle per hub session
- Reattach on resume
- Forward native lifecycle events to hub

Runtime must treat native session handle as opaque.

---

# 7. Session Handling

## 7.1 Session Creation

When Hub requests session creation:

- Runtime locates endpoint by ID.
- Creates or attaches to native session (profile-defined).
- Returns success or failure.
- Stores session mapping internally.

---

## 7.2 Message Flow

User → Hub → Runtime → Agent

Agent → Runtime → Hub → User

Runtime forwards payload verbatim.

Runtime must preserve message ordering per session.

---

## 7.3 Turn Gating

Runtime may enforce single in-flight turn if configured.

Completion detection:

- Native signal (if profile supports it)
- Adapter mechanics (process exit, response end)
- Idle timeout (CLI inferred mode)

---

## 7.4 Stop / Cancel

Runtime must support a best-effort stop:

- CLI: send interrupt / terminate
- Job: kill process
- HTTP: cancel request if supported
- Integrated profile: use native cancel if available

Stop behavior is profile-defined.

---

# 8. Resume Behavior

If runtime reconnects:

- It must re-register endpoints.
- For active sessions:
    - If profile supports attach: reattach using native handle.
    - Otherwise: mark session terminated or orphaned.

Runtime does not reconstruct history; Hub stores transcript.

---

# 9. Logging and Observability

Runtime must support:

- Structured logs
- Session lifecycle logs
- Connection lifecycle logs
- Adapter-level errors

Logging must not expose secrets.

---

# 10. Boundaries (Explicit)

Runtime:

- Secure transport
- Session lifecycle bridge
- Adapter execution
- No content shaping

Hub:

- Auth, routing, persistence, policy

UI:

- Rendering and presentation transforms

---

If you want next, we can write:

- A **minimal Hub specification** that aligns exactly with this runtime spec
    
    or
    
- A **deployment profile** (single-node vs Kubernetes production topology)

Keep it focused.

## Amurg Hub v0 Specification

### 1) Purpose

The Amurg Hub is the **self-hosted server** that:

- authenticates users and runtimes
- routes messages between UI clients and runtimes
- stores session history and metadata
- provides the web UI (or serves it as a separate frontend)

The hub is **trusted to read payloads** (plaintext within the self-hosted boundary).

---

## 2) Components (logical)

### 2.1 Hub Gateway (Realtime Router)

- Terminates client connections (web/mobile)
- Terminates runtime connections
- Manages active sessions
- Streams messages bidirectionally

### 2.2 Hub API (Control Plane)

- CRUD for runtimes/endpoints visibility
- Session creation/close
- History fetch
- User/org management (depending on auth mode)
- Policy evaluation endpoints (internal)

### 2.3 Hub Storage

- Persistent store for:
    - runtimes registry
    - endpoints registry
    - session metadata
    - conversation transcripts
    - audit records
- Optional short-lived buffer for live streaming/replay during reconnect

(Internal choice of DB/stream is implementation detail; hub owns it.)

---

## 3) Connectivity

### 3.1 Client ↔ Hub

- HTTPS + realtime channel (WebSocket or equivalent)
- Supports reconnect and resuming viewing a session

### 3.2 Runtime ↔ Hub

- TLS encrypted outbound connection from runtime to hub
- Bidirectional streaming over a single connection
- Hub must support many runtimes and many sessions per runtime

---

## 4) Security and Authentication

### 4.1 Transport Security

- TLS required for all external connections
- No plaintext endpoints

### 4.2 Runtime Authentication (required)

Hub must support at least one runtime auth mode (v0 minimal), configurable per deployment:

- mTLS client certs **or**
- signed runtime token **or**
- runtime keypair registration

Hub must be able to:

- register a runtime identity
- revoke a runtime identity
- reject unknown/invalid runtime connections

### 4.3 User Authentication (required)

Hub must support self-hostable user auth, configurable per deployment:

- local username/password (minimum)
- optionally OIDC/SSO (not required for v0)

### 4.4 Authorization (required)

Hub must enforce:

- which users can see which runtimes/endpoints
- which users can create sessions to which endpoints
- which users can send messages / stop sessions

Authorization must be evaluated server-side (UI is not trusted for this).

---

## 5) Runtime and Endpoint Registry

### 5.1 Runtime registration

Hub stores for each runtime:

- runtime identity
- display name/labels (optional)
- online/offline status
- last seen timestamp
- enrolled endpoints list (current snapshot)

### 5.2 Endpoint registration

Hub stores for each endpoint:

- endpoint id (stable)
- runtime id (owner)
- profile name (e.g., `generic-cli`, `codex`, `cloud-code`)
- tags/metadata (prod/dev/role)
- capability flags from profile (resume support, native completion, etc.)

Hub treats profile-specific details as opaque configuration owned by the runtime; hub only needs the profile name + capability flags.

---

## 6) Sessions

### 6.1 Session creation

When a user creates a session:

1. Hub checks authorization.
2. Hub creates a session record.
3. Hub asks the runtime to attach/create the session on the endpoint.
4. Session becomes “active” only if runtime confirms.

Session is bound to:

- user identity (creator and/or participants)
- runtime id
- endpoint id
- profile name
- optional native session handle (opaque string returned by runtime for integrated profiles)

### 6.2 Session resume

When a user opens an existing session:

- Hub returns transcript/history and current state
- If the session is active:
    - Hub re-subscribes the UI to live updates
    - If needed, Hub requests the runtime to reattach (using the stored native handle when available)

Hub does not interpret native handles; it stores and forwards them only.

### 6.3 Session termination

A session may end by:

- user request
- runtime disconnect / attach failure
- idle timeout (policy)
- admin action

Hub must persist termination reason and timestamps.

---

## 7) Message Routing and Delivery

### 7.1 Payload rules

- Hub forwards user messages to runtime **verbatim**.
- Hub forwards runtime output to UI **verbatim**.
- Hub may store payloads as canonical transcript.

### 7.2 Ordering

Within a session:

- Hub must preserve message ordering as received per direction.
- Hub must not reorder runtime output chunks.

### 7.3 Idempotency (required)

Hub must support client retries without duplicate execution by:

- requiring a stable message id per user message
- de-duplicating messages it has already accepted for a session (within a reasonable retention window)

### 7.4 Streaming

Hub must support streaming output:

- incremental output chunks from runtime → UI
- optional buffering for reconnect

---

## 8) Turn-based UX Support (server-side)

Even if enforced in UI, hub must support turn-based behavior as a policy option:

- default: one in-flight user message per session
- hub rejects or queues additional user messages while a session is “responding”
- “responding” state is determined by:
    - explicit completion events for integrated profiles, or
    - adapter mechanics signals forwarded by runtime, or
    - hub-configured idle timeout since last runtime output

Hub must not require agents themselves to emit “done”.

---

## 9) Stop / Cancel

Hub must expose a “Stop” operation:

- Authorized user requests stop for a session
- Hub forwards stop signal to runtime
- Hub records stop request event in audit/history

Stop is best-effort; hub must reflect stop failure/timeout clearly.

---

## 10) UI Rendering Boundary

Hub provides raw transcript data to the UI.

UI may apply presentation transforms (code highlighting, folding, etc.), but hub remains the canonical store of raw messages.

Hub must ensure rendering safety by:

- serving payloads in a way that prevents injection
- not allowing raw HTML execution via any server-rendered views

---

## 11) Storage and Retention

Hub must support configurable:

- transcript retention duration
- maximum transcript size per session (optional)
- audit log retention
- export/download of session transcript (optional)

---

## 12) Profiles (shared with runtime)

Hub must recognize a set of agent profile names and treat them consistently with runtime.

For each profile, hub needs only:

- display name/icon (UI)
- capability flags (resume support, native completion, etc.)
- any hub-side policy defaults (e.g., strict turn-based for this profile)

Implementation may use shared code/modules between hub and runtime for profile definitions.

---

If you want, next we can do branding in the same style: short constraints + naming + visual direction + what the product “is” in one sentence, without marketing fluff.