import { useState, useEffect, useMemo } from "react";
import { useSessionStore } from "@/stores/sessionStore";
import { PROFILE_DISPLAY } from "@/types";
import type { UnifiedSession } from "@/types";
import { SecurityBadge } from "@/components/SecurityBadge";
import { OnboardingGuide } from "@/components/OnboardingGuide";

function timeAgo(dateStr: string): string {
  if (!dateStr) return "";
  const then = new Date(dateStr).getTime();
  if (isNaN(then)) return "";
  const now = Date.now();
  const diffMs = now - then;
  const mins = Math.floor(diffMs / 60000);
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  if (days < 30) return `${days}d ago`;
  return `${Math.floor(days / 30)}mo ago`;
}

function formatDate(dateStr: string): string {
  if (!dateStr) return "";
  const d = new Date(dateStr);
  if (isNaN(d.getTime())) return "";
  return d.toLocaleDateString("en-US", { month: "short", day: "numeric" });
}

function SourceBadge({ source }: { source: "hub" | "native" }) {
  return (
    <span
      className={`inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium leading-none ${
        source === "hub"
          ? "bg-teal-500/15 text-teal-400"
          : "bg-purple-500/15 text-purple-400"
      }`}
    >
      {source}
    </span>
  );
}

function StateBadge({ state }: { state?: string }) {
  if (!state) return null;
  const isActive = state === "active" || state === "waiting";
  return (
    <span
      className={`inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium leading-none ${
        isActive
          ? "bg-green-500/15 text-green-400"
          : "bg-slate-600/30 text-slate-500"
      }`}
    >
      {state}
    </span>
  );
}

const SESSIONS_PER_GROUP = 5;

interface SessionGroup {
  profile: string;
  agentName: string;
  sessions: UnifiedSession[];
  latestUpdate: number;
}

export function AgentHomeScreen() {
  const {
    agents,
    sessions,
    createSession,
    selectSession,
    loadAgents,
    addToast,
    nativeSessionsByAgent,
    nativeSessionsLoading,
    previewSessionIds,
    loadAllNativeSessions,
    createSessionWithResume,
  } = useSessionStore();
  const [creating, setCreating] = useState<string | null>(null);
  const [resumingId, setResumingId] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [expandedGroups, setExpandedGroups] = useState<Set<string>>(new Set());

  useEffect(() => { loadAgents(); }, [loadAgents]);

  // Load native sessions from all capable agents once agents are available
  useEffect(() => {
    if (agents.length > 0) {
      loadAllNativeSessions();
    }
  }, [agents, loadAllNativeSessions]);

  // Build agent lookup for profile/name resolution
  const agentMap = useMemo(() => {
    const m = new Map<string, { profile: string; name: string }>();
    for (const a of agents) {
      m.set(a.id, { profile: a.profile, name: a.name });
    }
    return m;
  }, [agents]);

  // Build unified session list
  const unifiedSessions = useMemo((): UnifiedSession[] => {
    const items: UnifiedSession[] = [];

    // Hub sessions (exclude preview sessions)
    for (const s of sessions.filter(s => !previewSessionIds.has(s.id))) {
      const agent = agentMap.get(s.agent_id);
      const profile = agent?.profile || s.profile || "generic-cli";
      const profileMeta = PROFILE_DISPLAY[profile];
      const agentName = s.agent_name || agent?.name || profileMeta?.label || profile;
      items.push({
        id: s.id,
        source: "hub",
        profile,
        agentName,
        agentId: s.agent_id,
        label: `${agentName} #${s.seq || "?"}`,
        state: s.state,
        createdAt: s.created_at,
        updatedAt: s.updated_at,
        seq: s.seq,
      });
    }

    // Native sessions (from all agents)
    for (const [agentId, resp] of nativeSessionsByAgent) {
      const agent = agentMap.get(agentId);
      const profile = agent?.profile || "claude-code";
      const profileMeta = PROFILE_DISPLAY[profile];
      const agentName = agent?.name || profileMeta?.label || profile;
      for (const ns of resp.sessions) {
        items.push({
          id: ns.session_id,
          source: "native",
          profile,
          agentName,
          agentId,
          label: ns.summary || ns.first_prompt || ns.session_id.slice(0, 12),
          projectPath: ns.project_path,
          gitBranch: ns.git_branch,
          messageCount: ns.message_count,
          createdAt: ns.created || ns.modified || "",
          updatedAt: ns.modified || ns.created || "",
        });
      }
    }

    return items;
  }, [sessions, nativeSessionsByAgent, agentMap]);

  // Filter by search
  const filtered = useMemo(() => {
    if (!search.trim()) return unifiedSessions;
    const q = search.toLowerCase();
    return unifiedSessions.filter(
      (s) =>
        s.label.toLowerCase().includes(q) ||
        s.agentName.toLowerCase().includes(q) ||
        s.projectPath?.toLowerCase().includes(q) ||
        s.gitBranch?.toLowerCase().includes(q) ||
        s.state?.toLowerCase().includes(q)
    );
  }, [unifiedSessions, search]);

  // Group by profile, sort groups by most recent, sort sessions within by updatedAt desc
  const groups = useMemo((): SessionGroup[] => {
    const map = new Map<string, SessionGroup>();
    for (const s of filtered) {
      const key = s.profile;
      if (!map.has(key)) {
        map.set(key, {
          profile: s.profile,
          agentName: s.agentName,
          sessions: [],
          latestUpdate: 0,
        });
      }
      const group = map.get(key)!;
      group.sessions.push(s);
      const ts = new Date(s.updatedAt).getTime();
      if (ts > group.latestUpdate) group.latestUpdate = ts;
    }

    for (const group of map.values()) {
      group.sessions.sort(
        (a, b) => new Date(b.updatedAt).getTime() - new Date(a.updatedAt).getTime()
      );
    }

    return Array.from(map.values()).sort((a, b) => b.latestUpdate - a.latestUpdate);
  }, [filtered]);

  const handleSelect = async (agentId: string) => {
    if (creating) return;
    setCreating(agentId);
    try {
      await createSession(agentId);
    } catch (err) {
      addToast(err instanceof Error ? err.message : "Failed to create session", "error");
    } finally {
      setCreating(null);
    }
  };

  const handleSessionClick = async (session: UnifiedSession) => {
    if (session.source === "hub") {
      await selectSession(session.id);
    } else {
      // Native session — resume via agent
      if (!session.agentId || resumingId) return;
      setResumingId(session.id);
      try {
        await createSessionWithResume(session.agentId, session.id);
      } catch (err) {
        addToast(err instanceof Error ? err.message : "Failed to resume session", "error");
      } finally {
        setResumingId(null);
      }
    }
  };

  const toggleGroup = (profile: string) => {
    setExpandedGroups((prev) => {
      const next = new Set(prev);
      if (next.has(profile)) {
        next.delete(profile);
      } else {
        next.add(profile);
      }
      return next;
    });
  };

  if (!agents || agents.length === 0) {
    return <OnboardingGuide />;
  }

  const totalSessions = unifiedSessions.length;

  return (
    <div className="flex justify-center h-full px-4 sm:px-6 overflow-y-auto">
      <div className="w-full max-w-3xl py-12 animate-fade-in">
        {/* Header */}
        <div className="mb-8">
          <h2 className="text-lg font-semibold text-slate-200 mb-1">Agents</h2>
          <p className="text-sm text-slate-500">Select an agent to start a new session</p>
        </div>

        {/* Agent cards */}
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
          {agents.map((ep) => {
            const profile = PROFILE_DISPLAY[ep.profile] || {
              label: ep.profile,
              color: "bg-slate-600",
              icon: "?",
            };

            return (
              <button
                key={ep.id}
                onClick={() => handleSelect(ep.id)}
                disabled={!!creating}
                className="group relative flex items-center gap-3 p-4 rounded-xl
                           bg-slate-800/60 border border-slate-700/50
                           hover:bg-slate-700/80 hover:border-teal-500/30 hover:shadow-lg hover:shadow-teal-900/10
                           active:scale-[0.98]
                           transition-all duration-150
                           disabled:opacity-50 disabled:cursor-not-allowed text-left"
              >
                <span
                  className={`
                    inline-flex items-center justify-center w-10 h-10 rounded-lg text-lg
                    ${profile.color} text-white
                    group-hover:scale-105 transition-transform duration-150
                  `}
                >
                  {profile.icon}
                </span>

                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="text-sm font-medium text-slate-100 truncate">
                      {ep.name || profile.label}
                    </span>
                    <span
                      className={`w-1.5 h-1.5 rounded-full flex-shrink-0 ${ep.online ? "bg-green-400" : "bg-slate-600"}`}
                      title={ep.online ? "Online" : "Offline"}
                    />
                    <SecurityBadge security={ep.security} />
                  </div>
                  <span className="text-xs text-slate-500">{profile.label}</span>
                </div>

                {creating === ep.id && (
                  <div className="absolute inset-0 flex items-center justify-center bg-slate-800/90 rounded-xl">
                    <svg className="w-5 h-5 animate-spin text-teal-400" fill="none" viewBox="0 0 24 24">
                      <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                      <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                    </svg>
                  </div>
                )}
              </button>
            );
          })}
        </div>

        {/* All Sessions */}
        {(totalSessions > 0 || nativeSessionsLoading) && (
          <div className="mt-12">
            {/* Section header with search */}
            <div className="flex items-center justify-between mb-4">
              <div>
                <h3 className="text-sm font-semibold text-slate-300">All Sessions</h3>
                <p className="text-xs text-slate-500 mt-0.5">
                  {totalSessions} session{totalSessions !== 1 ? "s" : ""} across {groups.length} agent{groups.length !== 1 ? "s" : ""}
                </p>
              </div>
              <div className="relative">
                <input
                  type="text"
                  placeholder="Search..."
                  value={search}
                  onChange={(e) => setSearch(e.target.value)}
                  className="w-48 pl-7 pr-3 py-1.5 text-xs rounded-lg
                             bg-slate-800/60 border border-slate-700/50
                             text-slate-200 placeholder:text-slate-600
                             focus:outline-none focus:border-teal-500/30 focus:ring-1 focus:ring-teal-500/10
                             transition-colors"
                />
                <svg
                  className="absolute left-2 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-slate-600"
                  fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}
                >
                  <path strokeLinecap="round" strokeLinejoin="round" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
                </svg>
              </div>
            </div>

            {nativeSessionsLoading && groups.length === 0 && (
              <div className="flex items-center gap-2 text-sm text-slate-500 py-4">
                <svg className="w-4 h-4 animate-spin" fill="none" viewBox="0 0 24 24">
                  <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                  <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                </svg>
                Loading sessions...
              </div>
            )}

            {/* Session groups */}
            <div className="flex flex-col gap-6">
              {groups.map((group) => {
                const isExpanded = expandedGroups.has(group.profile);
                const visibleSessions = isExpanded
                  ? group.sessions
                  : group.sessions.slice(0, SESSIONS_PER_GROUP);
                const hiddenCount = group.sessions.length - SESSIONS_PER_GROUP;
                const profileMeta = PROFILE_DISPLAY[group.profile];

                return (
                  <div key={group.profile}>
                    {/* Group header */}
                    <div className="flex items-center gap-2 mb-2">
                      <span
                        className={`inline-flex items-center justify-center w-6 h-6 rounded text-xs
                          ${profileMeta?.color || "bg-slate-600"} text-white`}
                      >
                        {profileMeta?.icon || "?"}
                      </span>
                      <span className="text-xs font-semibold text-slate-400 uppercase tracking-wider">
                        {profileMeta?.label || group.agentName}
                      </span>
                      <span className="text-xs text-slate-600">({group.sessions.length})</span>
                      {nativeSessionsLoading && (
                        <svg className="w-3 h-3 animate-spin text-slate-600" fill="none" viewBox="0 0 24 24">
                          <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                          <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                        </svg>
                      )}
                    </div>

                    {/* Session rows */}
                    <div className="flex flex-col gap-1">
                      {visibleSessions.map((s) => (
                        <button
                          key={`${s.source}-${s.id}`}
                          onClick={() => handleSessionClick(s)}
                          disabled={resumingId === s.id}
                          className="w-full text-left px-3 py-2.5 rounded-lg
                                     bg-slate-800/40 border border-transparent
                                     hover:bg-slate-700/60 hover:border-slate-600/50
                                     disabled:opacity-50 disabled:cursor-not-allowed
                                     transition-all duration-100 group"
                        >
                          <div className="flex items-center gap-3">
                            <SourceBadge source={s.source} />

                            <span className="text-sm text-slate-200 truncate flex-1 min-w-0">
                              {s.label}
                            </span>

                            {s.source === "hub" && <StateBadge state={s.state} />}

                            {s.source === "native" && s.messageCount != null && s.messageCount > 0 && (
                              <span className="text-[11px] text-slate-500 flex-shrink-0">
                                {s.messageCount} msgs
                              </span>
                            )}

                            <span className="flex-shrink-0 text-[11px] text-slate-600 whitespace-nowrap">
                              {timeAgo(s.updatedAt)}
                            </span>

                            {resumingId === s.id && (
                              <svg className="w-3.5 h-3.5 animate-spin text-teal-400 flex-shrink-0" fill="none" viewBox="0 0 24 24">
                                <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                                <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                              </svg>
                            )}
                          </div>

                          {/* Second line for native sessions: folder + date */}
                          {s.source === "native" && (s.projectPath || s.updatedAt) && (
                            <div className="flex items-center gap-3 mt-1 ml-[38px]">
                              {s.projectPath && (
                                <span className="text-[11px] text-slate-500 truncate" title={s.projectPath}>
                                  <span className="text-slate-600">~/</span>
                                  {s.projectPath.replace(/^\/home\/[^/]+\//, "")}
                                </span>
                              )}
                              {s.updatedAt && (
                                <span className="flex-shrink-0 text-[11px] text-slate-600 ml-auto">
                                  {formatDate(s.updatedAt)}
                                </span>
                              )}
                            </div>
                          )}
                        </button>
                      ))}
                    </div>

                    {/* Show more toggle */}
                    {hiddenCount > 0 && (
                      <button
                        onClick={() => toggleGroup(group.profile)}
                        className="mt-1 px-3 py-1.5 text-xs text-slate-500 hover:text-slate-300 transition-colors"
                      >
                        {isExpanded ? "Show less" : `Show ${hiddenCount} more`}
                      </button>
                    )}
                  </div>
                );
              })}
            </div>

            {/* No results */}
            {search && groups.length === 0 && !nativeSessionsLoading && (
              <p className="text-sm text-slate-500 py-4">No sessions matching &quot;{search}&quot;</p>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
