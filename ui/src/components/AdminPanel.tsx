import { Fragment, useEffect, useState } from "react";
import { api } from "@/api/client";
import type { SessionInfo, UserInfo, AuditEvent, RuntimeInfo, AdminEndpointInfo, SecurityProfile, EndpointLimitsWire } from "@/types";
import { PROFILE_DISPLAY } from "@/types";

type Tab = "agents" | "runtimes" | "users" | "sessions" | "audit";

interface AdminPanelProps {
  onClose: () => void;
}

function RefreshButton({ onClick }: { onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      className="text-slate-400 hover:text-slate-200 p-1"
      title="Refresh"
    >
      <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2}
          d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
      </svg>
    </button>
  );
}

function EndpointConfigEditor({
  endpoint,
  onBack,
}: {
  endpoint: AdminEndpointInfo;
  onBack: () => void;
}) {
  // Parse initial values from config_override or endpoint's security.
  const parseSecurity = (): SecurityProfile => {
    if (endpoint.config_override) {
      try {
        return JSON.parse(endpoint.config_override.security);
      } catch { /* fall through */ }
    }
    if (typeof endpoint.security === "string") {
      try { return JSON.parse(endpoint.security); } catch { return {}; }
    }
    return endpoint.security || {};
  };

  const parseLimits = (): EndpointLimitsWire => {
    if (endpoint.config_override) {
      try {
        return JSON.parse(endpoint.config_override.limits);
      } catch { /* fall through */ }
    }
    return {};
  };

  const initSec = parseSecurity();
  const initLim = parseLimits();

  const [cwd, setCwd] = useState(initSec.cwd || "");
  const [permissionMode, setPermissionMode] = useState(initSec.permission_mode || "");
  const [allowedPaths, setAllowedPaths] = useState((initSec.allowed_paths || []).join("\n"));
  const [deniedPaths, setDeniedPaths] = useState((initSec.denied_paths || []).join("\n"));
  const [allowedTools, setAllowedTools] = useState((initSec.allowed_tools || []).join("\n"));
  const [envWhitelist, setEnvWhitelist] = useState((initSec.env_whitelist || []).join("\n"));
  const [maxSessions, setMaxSessions] = useState(initLim.max_sessions?.toString() || "");
  const [sessionTimeout, setSessionTimeout] = useState(initLim.session_timeout || "");
  const [idleTimeout, setIdleTimeout] = useState(initLim.idle_timeout || "");
  const [saving, setSaving] = useState(false);
  const [toast, setToast] = useState<{ msg: string; ok: boolean } | null>(null);

  const splitLines = (s: string) => s.split("\n").map(l => l.trim()).filter(Boolean);

  const handleSave = async () => {
    setSaving(true);
    setToast(null);
    try {
      const security: SecurityProfile = {};
      if (cwd) security.cwd = cwd;
      if (permissionMode) security.permission_mode = permissionMode;
      const ap = splitLines(allowedPaths);
      if (ap.length) security.allowed_paths = ap;
      const dp = splitLines(deniedPaths);
      if (dp.length) security.denied_paths = dp;
      const at = splitLines(allowedTools);
      if (at.length) security.allowed_tools = at;
      const ew = splitLines(envWhitelist);
      if (ew.length) security.env_whitelist = ew;

      const limits: EndpointLimitsWire = {};
      if (maxSessions) limits.max_sessions = parseInt(maxSessions, 10);
      if (sessionTimeout) limits.session_timeout = sessionTimeout;
      if (idleTimeout) limits.idle_timeout = idleTimeout;

      const result = await api.updateEndpointConfig(endpoint.id, { security, limits });
      const pushed = result.pushed_to_runtime ? "pushed to runtime" : "runtime offline, will apply on reconnect";
      setToast({ msg: `Saved - ${pushed}`, ok: true });
    } catch (e) {
      setToast({ msg: `Error: ${e instanceof Error ? e.message : "unknown"}`, ok: false });
    } finally {
      setSaving(false);
    }
  };

  const profileInfo = PROFILE_DISPLAY[endpoint.profile] || { label: endpoint.profile, color: "bg-slate-600", icon: "?" };

  return (
    <div className="p-4 space-y-5">
      <div className="flex items-center gap-3">
        <button onClick={onBack} className="text-slate-400 hover:text-slate-200 text-sm flex items-center gap-1">
          <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15 19l-7-7 7-7" />
          </svg>
          Back
        </button>
        <div className="flex items-center gap-2">
          <span className={`text-xs px-2 py-0.5 rounded-full text-white ${profileInfo.color}`}>{profileInfo.label}</span>
          <h3 className="text-slate-100 font-medium">{endpoint.name || endpoint.id}</h3>
        </div>
      </div>

      {toast && (
        <div className={`text-sm px-3 py-2 rounded ${toast.ok ? "bg-teal-900/50 text-teal-300" : "bg-red-900/50 text-red-300"}`}>
          {toast.msg}
        </div>
      )}

      <div className="space-y-4">
        <h4 className="text-sm font-medium text-slate-300 border-b border-slate-700 pb-1">Security</h4>
        <div className="grid grid-cols-2 gap-4">
          <label className="space-y-1">
            <span className="text-xs text-slate-400">Working Directory</span>
            <input type="text" value={cwd} onChange={e => setCwd(e.target.value)}
              placeholder="/path/to/project"
              className="w-full bg-slate-700 text-slate-200 text-sm rounded px-3 py-1.5 border border-slate-600 focus:border-teal-500 focus:outline-none" />
          </label>
          <label className="space-y-1">
            <span className="text-xs text-slate-400">Permission Mode</span>
            <select value={permissionMode} onChange={e => setPermissionMode(e.target.value)}
              className="w-full bg-slate-700 text-slate-200 text-sm rounded px-3 py-1.5 border border-slate-600 focus:border-teal-500 focus:outline-none">
              <option value="">Default</option>
              <option value="skip">Skip</option>
              <option value="strict">Strict</option>
              <option value="auto">Auto</option>
            </select>
          </label>
        </div>
        <div className="grid grid-cols-2 gap-4">
          <label className="space-y-1">
            <span className="text-xs text-slate-400">Allowed Paths (one per line)</span>
            <textarea value={allowedPaths} onChange={e => setAllowedPaths(e.target.value)} rows={3}
              className="w-full bg-slate-700 text-slate-200 text-sm rounded px-3 py-1.5 border border-slate-600 focus:border-teal-500 focus:outline-none font-mono" />
          </label>
          <label className="space-y-1">
            <span className="text-xs text-slate-400">Denied Paths (one per line)</span>
            <textarea value={deniedPaths} onChange={e => setDeniedPaths(e.target.value)} rows={3}
              className="w-full bg-slate-700 text-slate-200 text-sm rounded px-3 py-1.5 border border-slate-600 focus:border-teal-500 focus:outline-none font-mono" />
          </label>
        </div>
        <div className="grid grid-cols-2 gap-4">
          <label className="space-y-1">
            <span className="text-xs text-slate-400">Allowed Tools (one per line)</span>
            <textarea value={allowedTools} onChange={e => setAllowedTools(e.target.value)} rows={3}
              className="w-full bg-slate-700 text-slate-200 text-sm rounded px-3 py-1.5 border border-slate-600 focus:border-teal-500 focus:outline-none font-mono" />
          </label>
          <label className="space-y-1">
            <span className="text-xs text-slate-400">Env Whitelist (one per line)</span>
            <textarea value={envWhitelist} onChange={e => setEnvWhitelist(e.target.value)} rows={3}
              className="w-full bg-slate-700 text-slate-200 text-sm rounded px-3 py-1.5 border border-slate-600 focus:border-teal-500 focus:outline-none font-mono" />
          </label>
        </div>

        <h4 className="text-sm font-medium text-slate-300 border-b border-slate-700 pb-1 pt-2">Limits</h4>
        <div className="grid grid-cols-3 gap-4">
          <label className="space-y-1">
            <span className="text-xs text-slate-400">Max Sessions</span>
            <input type="number" value={maxSessions} onChange={e => setMaxSessions(e.target.value)}
              placeholder="10"
              className="w-full bg-slate-700 text-slate-200 text-sm rounded px-3 py-1.5 border border-slate-600 focus:border-teal-500 focus:outline-none" />
          </label>
          <label className="space-y-1">
            <span className="text-xs text-slate-400">Session Timeout</span>
            <input type="text" value={sessionTimeout} onChange={e => setSessionTimeout(e.target.value)}
              placeholder="30m"
              className="w-full bg-slate-700 text-slate-200 text-sm rounded px-3 py-1.5 border border-slate-600 focus:border-teal-500 focus:outline-none" />
          </label>
          <label className="space-y-1">
            <span className="text-xs text-slate-400">Idle Timeout</span>
            <input type="text" value={idleTimeout} onChange={e => setIdleTimeout(e.target.value)}
              placeholder="5m"
              className="w-full bg-slate-700 text-slate-200 text-sm rounded px-3 py-1.5 border border-slate-600 focus:border-teal-500 focus:outline-none" />
          </label>
        </div>
      </div>

      <div className="flex justify-end gap-3 pt-2">
        <button onClick={onBack} className="text-sm text-slate-400 hover:text-slate-200 px-4 py-1.5">
          Cancel
        </button>
        <button onClick={handleSave} disabled={saving}
          className="text-sm bg-teal-600 hover:bg-teal-500 text-white px-4 py-1.5 rounded disabled:opacity-50 disabled:cursor-not-allowed">
          {saving ? "Saving..." : "Save"}
        </button>
      </div>
    </div>
  );
}

function AgentsTab() {
  const [endpoints, setEndpoints] = useState<AdminEndpointInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [editing, setEditing] = useState<AdminEndpointInfo | null>(null);

  const load = async () => {
    setLoading(true);
    try {
      setEndpoints(await api.listAdminEndpoints());
    } catch { /* ignore */ }
    setLoading(false);
  };

  useEffect(() => { load(); }, []);

  if (editing) {
    return (
      <EndpointConfigEditor
        endpoint={editing}
        onBack={() => { setEditing(null); load(); }}
      />
    );
  }

  if (loading) return <div className="text-slate-500 p-4 text-center">Loading...</div>;

  // Group endpoints by runtime.
  const grouped = new Map<string, { name: string; online: boolean; endpoints: AdminEndpointInfo[] }>();
  for (const ep of endpoints) {
    const key = ep.runtime_id;
    if (!grouped.has(key)) {
      grouped.set(key, { name: ep.runtime_name || ep.runtime_id, online: ep.runtime_online, endpoints: [] });
    }
    grouped.get(key)!.endpoints.push(ep);
  }

  return (
    <div>
      <div className="flex justify-end px-4 pt-3">
        <RefreshButton onClick={load} />
      </div>
      {endpoints.length === 0 ? (
        <div className="text-slate-500 p-4 text-center">No agents registered</div>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm text-left">
            <thead className="text-xs text-slate-400 border-b border-slate-700">
              <tr>
                <th className="px-4 py-2">Agent</th>
                <th className="px-4 py-2">Profile</th>
                <th className="px-4 py-2">Permission</th>
                <th className="px-4 py-2">Config</th>
              </tr>
            </thead>
            <tbody>
              {[...grouped.entries()].map(([rtId, group]) => (
                <Fragment key={rtId}>
                  <tr className="bg-slate-750">
                    <td colSpan={4} className="px-4 py-2">
                      <div className="flex items-center gap-2">
                        <span className={`w-2 h-2 rounded-full ${group.online ? "bg-green-400" : "bg-red-400"}`} />
                        <span className="text-xs font-medium text-slate-300">{group.name}</span>
                        <span className="text-xs text-slate-500">({group.online ? "online" : "offline"})</span>
                      </div>
                    </td>
                  </tr>
                  {group.endpoints.map((ep) => {
                    const profileInfo = PROFILE_DISPLAY[ep.profile] || { label: ep.profile, color: "bg-slate-600", icon: "?" };
                    const sec = typeof ep.security === "string" ? (() => { try { return JSON.parse(ep.security); } catch { return {}; } })() : (ep.security || {});
                    return (
                      <tr
                        key={ep.id}
                        className="border-b border-slate-700/50 hover:bg-slate-700/30 cursor-pointer"
                        onClick={() => setEditing(ep)}
                      >
                        <td className="px-4 py-2 pl-8 text-slate-200">{ep.name || ep.id.slice(0, 16)}</td>
                        <td className="px-4 py-2">
                          <span className={`text-xs px-2 py-0.5 rounded-full text-white ${profileInfo.color}`}>
                            {profileInfo.label}
                          </span>
                        </td>
                        <td className="px-4 py-2 text-slate-400 text-xs">
                          {sec.permission_mode || "default"}
                        </td>
                        <td className="px-4 py-2">
                          {ep.config_override ? (
                            <span className="text-xs px-2 py-0.5 rounded-full bg-teal-900/50 text-teal-300">
                              override
                            </span>
                          ) : (
                            <span className="text-xs text-slate-500">original</span>
                          )}
                        </td>
                      </tr>
                    );
                  })}
                </Fragment>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function RuntimesTab() {
  const [runtimes, setRuntimes] = useState<RuntimeInfo[]>([]);
  const [loading, setLoading] = useState(true);

  const load = async () => {
    setLoading(true);
    try {
      setRuntimes(await api.listRuntimes());
    } catch { /* ignore */ }
    setLoading(false);
  };

  useEffect(() => { load(); }, []);

  if (loading) return <div className="text-slate-500 p-4 text-center">Loading...</div>;

  return (
    <div>
      <div className="flex justify-end px-4 pt-3">
        <RefreshButton onClick={load} />
      </div>
      {runtimes.length === 0 ? (
        <div className="text-slate-500 p-4 text-center">No runtimes registered</div>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm text-left">
            <thead className="text-xs text-slate-400 border-b border-slate-700">
              <tr>
                <th className="px-4 py-2">Name</th>
                <th className="px-4 py-2">ID</th>
                <th className="px-4 py-2">Status</th>
                <th className="px-4 py-2">Last Seen</th>
              </tr>
            </thead>
            <tbody>
              {runtimes.map((rt) => (
                <tr key={rt.id} className="border-b border-slate-700/50 hover:bg-slate-700/30">
                  <td className="px-4 py-2 text-slate-200">{rt.name || rt.id}</td>
                  <td className="px-4 py-2 text-slate-400 font-mono text-xs">{rt.id.slice(0, 12)}</td>
                  <td className="px-4 py-2">
                    <span className={`inline-flex items-center gap-1.5 text-xs ${rt.online ? "text-green-400" : "text-red-400"}`}>
                      <span className={`w-2 h-2 rounded-full ${rt.online ? "bg-green-400" : "bg-red-400"}`} />
                      {rt.online ? "Online" : "Offline"}
                    </span>
                  </td>
                  <td className="px-4 py-2 text-slate-400 text-xs">{new Date(rt.last_seen).toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function UsersTab() {
  const [users, setUsers] = useState<(UserInfo & { created_at?: string })[]>([]);
  const [loading, setLoading] = useState(true);

  const load = async () => {
    setLoading(true);
    try {
      setUsers(await api.listUsers());
    } catch { /* ignore */ }
    setLoading(false);
  };

  useEffect(() => { load(); }, []);

  if (loading) return <div className="text-slate-500 p-4 text-center">Loading...</div>;

  return (
    <div>
      <div className="flex justify-end px-4 pt-3">
        <RefreshButton onClick={load} />
      </div>
      {users.length === 0 ? (
        <div className="text-slate-500 p-4 text-center">No users</div>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm text-left">
            <thead className="text-xs text-slate-400 border-b border-slate-700">
              <tr>
                <th className="px-4 py-2">Username</th>
                <th className="px-4 py-2">Role</th>
                <th className="px-4 py-2">Created</th>
              </tr>
            </thead>
            <tbody>
              {users.map((u) => (
                <tr key={u.id} className="border-b border-slate-700/50 hover:bg-slate-700/30">
                  <td className="px-4 py-2 text-slate-200">{u.username}</td>
                  <td className="px-4 py-2">
                    <span className={`text-xs px-2 py-0.5 rounded-full ${u.role === "admin" ? "bg-amber-900/50 text-amber-300" : "bg-slate-700 text-slate-300"}`}>
                      {u.role}
                    </span>
                  </td>
                  <td className="px-4 py-2 text-slate-400 text-xs">
                    {u.created_at ? new Date(u.created_at).toLocaleString() : "-"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function SessionsTab() {
  const [sessions, setSessions] = useState<SessionInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [closing, setClosing] = useState<string | null>(null);

  const load = async () => {
    setLoading(true);
    try {
      setSessions(await api.getAdminSessions());
    } catch { /* ignore */ }
    setLoading(false);
  };

  useEffect(() => { load(); }, []);

  const handleClose = async (sessionId: string) => {
    if (closing) return;
    setClosing(sessionId);
    try {
      await api.closeAdminSession(sessionId);
      await load();
    } catch { /* ignore */ } finally {
      setClosing(null);
    }
  };

  if (loading) return <div className="text-slate-500 p-4 text-center">Loading...</div>;

  return (
    <div>
      <div className="flex justify-end px-4 pt-3">
        <RefreshButton onClick={load} />
      </div>
      {sessions.length === 0 ? (
        <div className="text-slate-500 p-4 text-center">No sessions</div>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm text-left">
            <thead className="text-xs text-slate-400 border-b border-slate-700">
              <tr>
                <th className="px-4 py-2">Endpoint</th>
                <th className="px-4 py-2">User</th>
                <th className="px-4 py-2">State</th>
                <th className="px-4 py-2">Profile</th>
                <th className="px-4 py-2">Updated</th>
                <th className="px-4 py-2"></th>
              </tr>
            </thead>
            <tbody>
              {sessions.map((s) => (
                <tr key={s.id} className="border-b border-slate-700/50 hover:bg-slate-700/30">
                  <td className="px-4 py-2 text-slate-200">{s.endpoint_name || s.endpoint_id.slice(0, 12)}</td>
                  <td className="px-4 py-2 text-slate-400 font-mono text-xs">{s.user_id.slice(0, 8)}</td>
                  <td className="px-4 py-2">
                    <span className={`text-xs px-2 py-0.5 rounded-full ${
                      s.state === "active" || s.state === "creating" || s.state === "responding"
                        ? "bg-green-900/50 text-green-300"
                        : s.state === "closed"
                          ? "bg-slate-700 text-slate-400"
                          : "bg-amber-900/50 text-amber-300"
                    }`}>
                      {s.state}
                    </span>
                  </td>
                  <td className="px-4 py-2 text-slate-400 text-xs">{s.profile}</td>
                  <td className="px-4 py-2 text-slate-400 text-xs">{new Date(s.updated_at).toLocaleString()}</td>
                  <td className="px-4 py-2">
                    {s.state !== "closed" && (
                      <button
                        onClick={() => handleClose(s.id)}
                        disabled={!!closing}
                        className="text-xs text-red-400 hover:text-red-300 disabled:opacity-50 disabled:cursor-not-allowed"
                      >
                        {closing === s.id ? "Closing..." : "Close"}
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function AuditTab() {
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [loading, setLoading] = useState(true);
  const [offset, setOffset] = useState(0);
  const [actionFilter, setActionFilter] = useState("");
  const pageSize = 50;

  const load = async (newOffset: number) => {
    setLoading(true);
    try {
      const result = await api.getAuditEvents(pageSize, newOffset, actionFilter || undefined);
      setEvents(result);
      setOffset(newOffset);
    } catch { /* ignore */ }
    setLoading(false);
  };

  useEffect(() => { load(0); }, [actionFilter]);

  const formatDetail = (detail: Record<string, unknown> | string): string => {
    if (!detail) return "-";
    if (typeof detail === "string") {
      // Try parsing as JSON
      try {
        const parsed = JSON.parse(detail);
        return JSON.stringify(parsed, null, 1);
      } catch {
        return detail || "-";
      }
    }
    return JSON.stringify(detail, null, 1);
  };

  if (loading && events.length === 0) {
    return <div className="text-slate-500 p-4 text-center">Loading...</div>;
  }

  return (
    <div>
      <div className="flex items-center justify-between px-4 pt-3 gap-3">
        <select
          value={actionFilter}
          onChange={(e) => setActionFilter(e.target.value)}
          className="bg-slate-700 text-slate-200 text-xs rounded px-2 py-1.5 border border-slate-600 focus:border-teal-500 focus:outline-none"
        >
          <option value="">All actions</option>
          <option value="login.">Login</option>
          <option value="session.">Session</option>
          <option value="message.">Message</option>
          <option value="runtime.">Runtime</option>
          <option value="turn.">Turn</option>
          <option value="permission.">Permission</option>
        </select>
        <RefreshButton onClick={() => load(offset)} />
      </div>
      {events.length === 0 ? (
        <div className="text-slate-500 p-4 text-center">No audit events</div>
      ) : (
        <>
          <div className="overflow-x-auto">
            <table className="w-full text-sm text-left">
              <thead className="text-xs text-slate-400 border-b border-slate-700">
                <tr>
                  <th className="px-4 py-2">Time</th>
                  <th className="px-4 py-2">Action</th>
                  <th className="px-4 py-2">User</th>
                  <th className="px-4 py-2">Session</th>
                  <th className="px-4 py-2">Endpoint</th>
                  <th className="px-4 py-2">Detail</th>
                </tr>
              </thead>
              <tbody>
                {events.map((e) => (
                  <tr key={e.id} className="border-b border-slate-700/50 hover:bg-slate-700/30">
                    <td className="px-4 py-2 text-slate-400 text-xs whitespace-nowrap">
                      {new Date(e.created_at).toLocaleString()}
                    </td>
                    <td className="px-4 py-2 text-slate-200 font-mono text-xs">{e.action}</td>
                    <td className="px-4 py-2 text-slate-400 font-mono text-xs">
                      {e.user_id ? e.user_id.slice(0, 8) : "-"}
                    </td>
                    <td className="px-4 py-2 text-slate-400 font-mono text-xs">
                      {e.session_id ? e.session_id.slice(0, 8) : "-"}
                    </td>
                    <td className="px-4 py-2 text-slate-400 font-mono text-xs">
                      {e.endpoint_id ? e.endpoint_id.slice(0, 8) : "-"}
                    </td>
                    <td className="px-4 py-2 text-slate-400 text-xs truncate max-w-48" title={formatDetail(e.detail)}>
                      {formatDetail(e.detail)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="flex justify-between items-center px-4 py-3 border-t border-slate-700">
            <button
              onClick={() => load(Math.max(0, offset - pageSize))}
              disabled={offset === 0}
              className="text-xs text-slate-400 hover:text-slate-200 disabled:opacity-30 disabled:cursor-not-allowed"
            >
              Previous
            </button>
            <span className="text-xs text-slate-500">
              Showing {offset + 1}&ndash;{offset + events.length}
            </span>
            <button
              onClick={() => load(offset + pageSize)}
              disabled={events.length < pageSize}
              className="text-xs text-slate-400 hover:text-slate-200 disabled:opacity-30 disabled:cursor-not-allowed"
            >
              Next
            </button>
          </div>
        </>
      )}
    </div>
  );
}

const TABS: { key: Tab; label: string }[] = [
  { key: "agents", label: "Agents" },
  { key: "runtimes", label: "Runtimes" },
  { key: "users", label: "Users" },
  { key: "sessions", label: "Sessions" },
  { key: "audit", label: "Audit Log" },
];

export function AdminPanel({ onClose }: AdminPanelProps) {
  const [activeTab, setActiveTab] = useState<Tab>("agents");

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [onClose]);

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 px-4"
      onClick={onClose}
    >
      <div
        className="w-full max-w-4xl max-h-[80vh] bg-slate-800 rounded-2xl border border-slate-700 shadow-xl flex flex-col"
        role="dialog"
        aria-modal="true"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className="flex items-center justify-between px-5 py-4 border-b border-slate-700 shrink-0">
          <h2 className="text-lg font-semibold text-slate-100">Admin Dashboard</h2>
          <button
            onClick={onClose}
            className="text-slate-400 hover:text-slate-200 p-1"
          >
            <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        </div>

        {/* Tabs */}
        <div className="flex border-b border-slate-700 px-4 shrink-0">
          {TABS.map((tab) => (
            <button
              key={tab.key}
              onClick={() => setActiveTab(tab.key)}
              className={`px-4 py-2.5 text-sm font-medium transition-colors relative ${
                activeTab === tab.key
                  ? "text-teal-400"
                  : "text-slate-400 hover:text-slate-200"
              }`}
            >
              {tab.label}
              {activeTab === tab.key && (
                <span className="absolute bottom-0 left-0 right-0 h-0.5 bg-teal-400 rounded-full" />
              )}
            </button>
          ))}
        </div>

        {/* Content */}
        <div className="flex-1 overflow-y-auto">
          {activeTab === "agents" && <AgentsTab />}
          {activeTab === "runtimes" && <RuntimesTab />}
          {activeTab === "users" && <UsersTab />}
          {activeTab === "sessions" && <SessionsTab />}
          {activeTab === "audit" && <AuditTab />}
        </div>
      </div>
    </div>
  );
}
