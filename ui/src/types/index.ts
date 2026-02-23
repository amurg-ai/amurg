// Wire protocol types matching the Go protocol package.

export interface Envelope {
  type: string;
  id?: string;
  session_id?: string;
  ts: string;
  payload?: unknown;
}

export interface SecurityProfile {
  allowed_paths?: string[];
  denied_paths?: string[];
  allowed_tools?: string[];
  permission_mode?: string;
  cwd?: string;
  env_whitelist?: string[];
}

export interface EndpointInfo {
  id: string;
  runtime_id: string;
  profile: string;
  name: string;
  tags?: Record<string, string>;
  online: boolean;
  caps: string; // JSON-encoded caps from store
  security?: string | SecurityProfile; // JSON string from hub or parsed object
}

export type ConnectionState = "connected" | "disconnected" | "reconnecting";

export interface SessionInfo {
  id: string;
  user_id: string;
  endpoint_id: string;
  runtime_id: string;
  profile: string;
  state: string;
  created_at: string;
  updated_at: string;
  endpoint_name?: string;
  seq?: number;
}

export interface StoredMessage {
  id: string;
  session_id: string;
  seq: number;
  direction: "user" | "agent";
  channel: string;
  content: string;
  created_at: string;
}

export interface AgentOutput {
  session_id: string;
  message_id?: string;
  seq: number;
  channel: string;
  content: string;
  final: boolean;
}

export interface TurnStarted {
  session_id: string;
  in_response_to: string;
}

export interface TurnCompleted {
  session_id: string;
  in_response_to?: string;
  exit_code?: number;
}

export interface UserInfo {
  id: string;
  username: string;
  role: string;
}

export interface AuditEvent {
  id: string;
  action: string;
  user_id: string;
  runtime_id: string;
  session_id: string;
  endpoint_id: string;
  detail: Record<string, unknown> | string;
  created_at: string;
}

export interface RuntimeInfo {
  id: string;
  name: string;
  online: boolean;
  last_seen: string;
}

export interface Turn {
  turnNumber: number;
  startSeq: number;
  endSeq?: number;
  exitCode?: number;
  elapsedMs?: number;
  startTime: number; // Date.now()
}

export interface PermissionRequest {
  session_id: string;
  request_id: string;
  tool: string;
  description: string;
  resource?: string;
}

export interface PermissionResponse {
  session_id: string;
  request_id: string;
  approved: boolean;
  always_allow?: boolean;
}

export interface FileMetadata {
  file_id: string;
  name: string;
  mime_type: string;
  size: number;
  direction: "upload" | "download";
}

// Admin endpoint info with runtime details and config override
export interface AdminEndpointInfo {
  id: string;
  org_id: string;
  runtime_id: string;
  runtime_name: string;
  runtime_online: boolean;
  profile: string;
  name: string;
  tags: Record<string, string>;
  caps: Record<string, unknown>;
  security: SecurityProfile;
  config_override?: EndpointConfigOverride;
}

export interface EndpointConfigOverride {
  endpoint_id: string;
  org_id: string;
  security: string; // JSON string
  limits: string; // JSON string
  updated_by: string;
  updated_at: string;
}

export interface EndpointLimitsWire {
  max_sessions?: number;
  session_timeout?: string;
  max_output_bytes?: number;
  idle_timeout?: string;
}

// Profile display metadata
export const PROFILE_DISPLAY: Record<
  string,
  { label: string; color: string; icon: string }
> = {
  "generic-cli": {
    label: "CLI",
    color: "bg-gray-600",
    icon: ">_",
  },
  "generic-job": {
    label: "Job",
    color: "bg-amber-700",
    icon: "\u25B6",
  },
  "generic-http": {
    label: "HTTP",
    color: "bg-blue-700",
    icon: "\u21C4",
  },
  "claude-code": {
    label: "Claude Code",
    color: "bg-orange-600",
    icon: "\u2728",
  },
  "github-copilot": {
    label: "Copilot",
    color: "bg-purple-700",
    icon: "\uD83D\uDE80",
  },
  codex: {
    label: "Codex",
    color: "bg-green-700",
    icon: "\uD83E\uDDE0",
  },
  external: {
    label: "External",
    color: "bg-teal-700",
    icon: "\u2699",
  },
  "kilo-code": {
    label: "Kilo Code",
    color: "bg-indigo-700",
    icon: "K",
  },
};
