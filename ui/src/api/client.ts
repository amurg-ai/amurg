import type { AgentInfo, SessionInfo, StoredMessage, UserInfo, AuditEvent, RuntimeInfo, AdminAgentInfo, AgentConfigOverride, SecurityProfile, AgentLimitsWire } from "@/types";

const BASE = "";

export type TokenGetter = () => Promise<string | null>;

let tokenGetter: TokenGetter = async () => localStorage.getItem("amurg_token");

export function setTokenGetter(getter: TokenGetter) {
  tokenGetter = getter;
}

export { tokenGetter };

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const token = await tokenGetter();
  const res = await fetch(`${BASE}${path}`, {
    ...options,
    headers: {
      "Content-Type": "application/json",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...options?.headers,
    },
  });

  if (res.status === 401) {
    localStorage.removeItem("amurg_token");
    window.location.href = "/login";
    throw new Error("Unauthorized");
  }

  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }));
    throw new Error(body.error || res.statusText);
  }

  return res.json();
}

export const api = {
  login: async (
    username: string,
    password: string
  ): Promise<{ token: string }> => {
    const res = await fetch(`${BASE}/api/auth/login`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username, password }),
    });

    if (!res.ok) {
      throw new Error("Invalid credentials");
    }

    const data = await res.json();
    // Security: storing JWT in localStorage exposes it to XSS. This is an accepted
    // trade-off because (1) httpOnly cookies don't work with WebSocket auth, and
    // (2) the hub's Content-Security-Policy restricts script sources to mitigate XSS.
    localStorage.setItem("amurg_token", data.token);
    return data;
  },

  logout: () => {
    localStorage.removeItem("amurg_token");
    window.location.href = "/login";
  },

  isAuthenticated: (): boolean => {
    return !!localStorage.getItem("amurg_token");
  },

  getMe: () => request<UserInfo>("/api/me"),

  getAuthConfig: async (): Promise<{ provider: string }> => {
    const res = await fetch(`${BASE}/api/auth/config`);
    if (!res.ok) throw new Error("Failed to fetch auth config");
    return res.json();
  },

  listAgents: () => request<AgentInfo[]>("/api/agents"),

  listSessions: () => request<SessionInfo[]>("/api/sessions"),

  createSession: (agentId: string) =>
    request<SessionInfo>("/api/sessions", {
      method: "POST",
      body: JSON.stringify({ agent_id: agentId }),
    }),

  getMessages: (sessionId: string) =>
    request<StoredMessage[]>(`/api/sessions/${sessionId}/messages`),

  closeSession: (sessionId: string) =>
    request<{ status: string }>(`/api/sessions/${sessionId}/close`, {
      method: "POST",
    }),

  // Admin
  listRuntimes: () => request<RuntimeInfo[]>("/api/runtimes"),

  listUsers: () => request<UserInfo[]>("/api/users"),

  getAdminSessions: () => request<SessionInfo[]>("/api/admin/sessions"),

  closeAdminSession: (sessionId: string) =>
    request<{ status: string }>(`/api/admin/sessions/${sessionId}/close`, {
      method: "POST",
    }),

  getAuditEvents: (limit: number, offset: number, action?: string) => {
    let url = `/api/admin/audit?limit=${limit}&offset=${offset}`;
    if (action) url += `&action=${encodeURIComponent(action)}`;
    return request<AuditEvent[]>(url);
  },

  // Admin agent config
  listAdminAgents: () => request<AdminAgentInfo[]>("/api/admin/agents"),

  getAgentConfig: (agentId: string) =>
    request<AgentConfigOverride | { agent_id: string; override: null }>(
      `/api/admin/agents/${agentId}/config`
    ),

  updateAgentConfig: (
    agentId: string,
    body: { security?: SecurityProfile; limits?: AgentLimitsWire }
  ) =>
    request<{ status: string; pushed_to_runtime: boolean }>(
      `/api/admin/agents/${agentId}/config`,
      {
        method: "PUT",
        body: JSON.stringify(body),
      }
    ),

  uploadFile: async (sessionId: string, file: File): Promise<{ file_id: string; name: string; mime_type: string; size: number; seq: number }> => {
    const token = await tokenGetter();
    const formData = new FormData();
    formData.append("file", file);
    const res = await fetch(`${BASE}/api/sessions/${sessionId}/files`, {
      method: "POST",
      headers: {
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
      body: formData,
    });
    if (res.status === 401) {
      localStorage.removeItem("amurg_token");
      window.location.href = "/login";
      throw new Error("Unauthorized");
    }
    if (!res.ok) {
      const body = await res.json().catch(() => ({ error: res.statusText }));
      throw new Error(body.error || res.statusText);
    }
    return res.json();
  },

  getFileUrl: (fileId: string, sessionId: string): string =>
    `${BASE}/api/files/${fileId}?session_id=${sessionId}`,

  approveRuntimeRegistration: (userCode: string, runtimeName: string) =>
    request<{ ok: boolean; runtime_id: string }>("/api/runtime/register/approve", {
      method: "POST",
      body: JSON.stringify({ user_code: userCode, runtime_name: runtimeName }),
    }),
};
