import { create } from "zustand";
import type {
  SessionInfo,
  StoredMessage,
  AgentInfo,
  UserInfo,
  Envelope,
  AgentOutput,
  TurnCompleted,
  Turn,
  ConnectionState,
  PermissionRequest,
  NativeSessionsResponse,
} from "@/types";
import { api } from "@/api/client";
import { socket } from "@/api/websocket";
import { uuid } from "@/lib/uuid";

function assignSequenceNumbers(sessions: SessionInfo[]): SessionInfo[] {
  const byAgent = new Map<string, SessionInfo[]>();
  for (const s of sessions) {
    const key = s.agent_id;
    if (!byAgent.has(key)) byAgent.set(key, []);
    byAgent.get(key)!.push(s);
  }
  for (const group of byAgent.values()) {
    group.sort((a, b) => new Date(a.created_at).getTime() - new Date(b.created_at).getTime());
    group.forEach((s, i) => { s.seq = i + 1; });
  }
  return sessions;
}

function compareMessages(a: StoredMessage, b: StoredMessage): number {
  const aSeq = a.seq > 0 ? a.seq : Number.MAX_SAFE_INTEGER;
  const bSeq = b.seq > 0 ? b.seq : Number.MAX_SAFE_INTEGER;
  if (aSeq !== bSeq) return aSeq - bSeq;
  return new Date(a.created_at).getTime() - new Date(b.created_at).getTime();
}

function mergeMessages(existing: StoredMessage[], incoming: StoredMessage[]): StoredMessage[] {
  const merged = [...existing];

  for (const msg of incoming) {
    let idx = -1;

    if (msg.seq > 0) {
      idx = merged.findIndex((m) => m.seq > 0 && m.seq === msg.seq);
    }
    if (idx === -1 && msg.id) {
      idx = merged.findIndex((m) => m.id === msg.id);
    }

    if (idx >= 0) {
      merged[idx] = msg;
    } else {
      merged.push(msg);
    }
  }

  merged.sort(compareMessages);
  return merged;
}

function patchSession(
  sessions: SessionInfo[],
  sessionId: string,
  patch: Partial<SessionInfo>,
): SessionInfo[] {
  const updatedAt = patch.updated_at || new Date().toISOString();
  return sessions.map((session) =>
    session.id === sessionId
      ? {
          ...session,
          ...patch,
          updated_at: updatedAt,
        }
      : session,
  );
}

function upsertSession(sessions: SessionInfo[], incoming: SessionInfo): SessionInfo[] {
  const exists = sessions.some((session) => session.id === incoming.id);
  const next = exists
    ? sessions.map((session) => (session.id === incoming.id ? incoming : session))
    : [incoming, ...sessions];
  return assignSequenceNumbers(next);
}

function parseAgentExecModel(agent: AgentInfo | undefined): string | null {
  if (!agent?.caps) return null;
  try {
    const caps = JSON.parse(agent.caps);
    return typeof caps.exec_model === "string" ? caps.exec_model : null;
  } catch {
    return null;
  }
}

function sessionAllowsInteractiveInput(
  sessionId: string,
  sessions: SessionInfo[],
  agents: AgentInfo[],
): boolean {
  const session = sessions.find((entry) => entry.id === sessionId);
  if (!session) return false;
  const agent = agents.find((entry) => entry.id === session.agent_id);
  return parseAgentExecModel(agent) === "interactive";
}

interface SessionState {
  // Auth
  user: UserInfo | null;
  isAuthenticated: boolean;
  authProvider: "builtin" | null; // null = loading

  // Data
  agents: AgentInfo[];
  sessions: SessionInfo[];
  activeSessionId: string | null;
  messages: Map<string, StoredMessage[]>;
  responding: Set<string>; // sessions currently responding
  turns: Map<string, Turn[]>;
  connectionState: ConnectionState;
  unreadCounts: Map<string, number>;
  pendingPermissions: Map<string, PermissionRequest[]>;
  sessionToolAllowlist: Map<string, Set<string>>;
  nativeSessionsByAgent: Map<string, NativeSessionsResponse>;
  nativeSessionsLoading: boolean;
  _nativePendingCount: number;
  previewSessionIds: Set<string>;

  // Toasts
  toasts: { id: string; message: string; type: "info" | "error" | "success" }[];
  addToast: (message: string, type: "info" | "error" | "success") => void;
  removeToast: (id: string) => void;

  // Actions
  init: () => Promise<void>;
  login: (username: string, password: string) => Promise<void>;
  logout: () => void;
  loadAgents: () => Promise<void>;
  loadSessions: () => Promise<void>;
  createSession: (agentId: string, promptProfile?: string) => Promise<SessionInfo>;
  selectSession: (sessionId: string) => Promise<void>;
  deselectSession: () => void;
  sendMessage: (content: string) => void;
  canSendInteractiveInput: (sessionId: string) => boolean;
  stopSession: () => void;
  closeSession: (sessionId: string) => Promise<void>;
  cleanupSession: (sessionId: string) => void;
  respondToPermission: (sessionId: string, requestId: string, approved: boolean, alwaysAllow?: boolean) => void;
  uploadFile: (sessionId: string, file: File) => Promise<void>;
  loadNativeSessions: (agentId: string) => void;
  loadAllNativeSessions: () => void;
  createSessionWithResume: (agentId: string, resumeSessionId: string, promptProfile?: string) => Promise<SessionInfo>;
}

export const useSessionStore = create<SessionState>((set, get) => {
  // Register WebSocket handlers eagerly at store creation time.
  // Handlers use get() for fresh state, so they work regardless of
  // when the WebSocket actually connects.
  (function setupSocketHandlers() {
    socket.on("session.created", (env: Envelope) => {
      const payload = env.payload as {
        session_id: string;
        ok: boolean;
        error?: string;
        native_handle?: string;
      };
      const { sessions } = get();

      if (!payload.ok) {
        set({ sessions: patchSession(sessions, payload.session_id, { state: "closed" }) });
        if (payload.error) {
          get().addToast(payload.error, "error");
        }
        return;
      }

      set({
        sessions: patchSession(sessions, payload.session_id, {
          state: "active",
          ...(payload.native_handle ? { native_handle: payload.native_handle } : {}),
        }),
      });
    });

    socket.on("agent.output", (env: Envelope) => {
      const output = env.payload as AgentOutput;
      const { messages } = get();
      const sessionMessages = messages.get(output.session_id) || [];

      const newMessage: StoredMessage = {
        id: output.message_id || uuid(),
        session_id: output.session_id,
        seq: output.seq,
        direction: "agent",
        channel: output.channel,
        content: output.content,
        created_at: new Date().toISOString(),
      };

      const updated = new Map(messages);
      updated.set(output.session_id, mergeMessages(sessionMessages, [newMessage]));
      set({ messages: updated });

      // Increment unread count if not the active session
      const { activeSessionId: currentActive } = get();
      if (output.session_id !== currentActive) {
        const unreadCounts = new Map(get().unreadCounts);
        unreadCounts.set(output.session_id, (unreadCounts.get(output.session_id) || 0) + 1);
        set({ unreadCounts });
      }
    });

    socket.on("turn.started", (env: Envelope) => {
      const payload = env.payload as { session_id: string };
      const { responding, turns, messages, sessions } = get();

      // Mark responding
      const updatedResponding = new Set(responding);
      updatedResponding.add(payload.session_id);

      // Create turn entry
      const sessionTurns = turns.get(payload.session_id) || [];
      const sessionMessages = messages.get(payload.session_id) || [];
      const lastSeq = sessionMessages.length > 0 ? sessionMessages[sessionMessages.length - 1].seq : 0;
      const newTurn: Turn = {
        turnNumber: sessionTurns.length + 1,
        startSeq: lastSeq,
        startTime: Date.now(),
      };
      const updatedTurns = new Map(turns);
      updatedTurns.set(payload.session_id, [...sessionTurns, newTurn]);

      set({
        responding: updatedResponding,
        turns: updatedTurns,
        sessions: patchSession(sessions, payload.session_id, { state: "responding" }),
      });
    });

    socket.on("turn.completed", (_env: Envelope) => {
      const payload = _env.payload as TurnCompleted;
      const { responding, turns, messages, sessions } = get();

      // Clear responding
      const updatedResponding = new Set(responding);
      updatedResponding.delete(payload.session_id);

      // Update last turn with completion info
      const sessionTurns = [...(turns.get(payload.session_id) || [])];
      if (sessionTurns.length > 0) {
        const lastTurn = { ...sessionTurns[sessionTurns.length - 1] };
        const sessionMessages = messages.get(payload.session_id) || [];
        lastTurn.endSeq = sessionMessages.length > 0 ? sessionMessages[sessionMessages.length - 1].seq : lastTurn.startSeq;
        lastTurn.exitCode = payload.exit_code;
        lastTurn.elapsedMs = Date.now() - lastTurn.startTime;
        sessionTurns[sessionTurns.length - 1] = lastTurn;
      }
      const updatedTurns = new Map(turns);
      updatedTurns.set(payload.session_id, sessionTurns);

      set({
        responding: updatedResponding,
        turns: updatedTurns,
        sessions: patchSession(sessions, payload.session_id, {
          state: "active",
          ...(payload.native_handle ? { native_handle: payload.native_handle } : {}),
        }),
      });
    });

    socket.on("history.response", (env: Envelope) => {
      const payload = env.payload as {
        session_id: string;
        messages: StoredMessage[];
      };
      const { messages } = get();
      const existing = messages.get(payload.session_id) || [];
      const merged = mergeMessages(existing, payload.messages || []);
      const updated = new Map(messages);
      updated.set(payload.session_id, merged);
      set({ messages: updated });
    });

    socket.on("session.closed", (env: Envelope) => {
      const payload = env.payload as { session_id: string };
      const { sessions } = get();

      // Update session state in the sessions array
      set({
        sessions: patchSession(sessions, payload.session_id, { state: "closed" }),
      });

      // CGR-28: drop any queued outbound messages for this session so they
      // are not accidentally sent on the next reconnect.
      socket.purgePending(payload.session_id);

      // CGR-24: only clean up transient state (permissions, allowlists).
      // Preserve messages and turns so users can still view chat history.
      const { pendingPermissions, sessionToolAllowlist } = get();
      const updatedPerms = new Map(pendingPermissions);
      updatedPerms.delete(payload.session_id);
      const updatedAllowlist = new Map(sessionToolAllowlist);
      updatedAllowlist.delete(payload.session_id);
      set({ pendingPermissions: updatedPerms, sessionToolAllowlist: updatedAllowlist });
    });

    socket.on("session.reopened", (env: Envelope) => {
      const payload = env.payload as { session_id: string };
      const { sessions } = get();
      set({
        sessions: patchSession(sessions, payload.session_id, { state: "active" }),
      });
    });

    socket.on("native.sessions.response", (env: Envelope) => {
      const resp = env.payload as NativeSessionsResponse;
      const { nativeSessionsByAgent, _nativePendingCount } = get();
      const updated = new Map(nativeSessionsByAgent);
      if (!resp.error) {
        updated.set(resp.agent_id, resp);
      }
      const remaining = Math.max(0, _nativePendingCount - 1);
      set({
        nativeSessionsByAgent: updated,
        nativeSessionsLoading: remaining > 0,
        _nativePendingCount: remaining,
      });
    });

    socket.on("error", (env: Envelope) => {
      const payload = env.payload as { code?: string; message?: string };
      if (payload?.message) {
        get().addToast(payload.message, "error");
      }
    });

    socket.on("permission.request", (env: Envelope) => {
      const req = env.payload as PermissionRequest;
      const { pendingPermissions, sessionToolAllowlist } = get();

      // Auto-approve if tool is in session allowlist
      const allowlist = sessionToolAllowlist.get(req.session_id);
      if (allowlist?.has(req.tool)) {
        // Auto-approve
        socket.send("permission.response", {
          session_id: req.session_id,
          request_id: req.request_id,
          approved: true,
          always_allow: true,
        }, req.session_id);
        return;
      }

      const updated = new Map(pendingPermissions);
      const existing = updated.get(req.session_id) || [];
      updated.set(req.session_id, [...existing, req]);
      set({ pendingPermissions: updated });
    });

    socket.on("permission.response", (env: Envelope) => {
      const resp = env.payload as { session_id: string; request_id: string };
      const { pendingPermissions } = get();
      const requests = pendingPermissions.get(resp.session_id) || [];
      if (requests.some(r => r.request_id === resp.request_id)) {
        const updated = new Map(pendingPermissions);
        updated.set(resp.session_id, requests.filter(r => r.request_id !== resp.request_id));
        if (updated.get(resp.session_id)?.length === 0) updated.delete(resp.session_id);
        set({ pendingPermissions: updated });
      }
    });
  })();

  return {
    user: null,
    isAuthenticated: api.isAuthenticated(),
    authProvider: null,
    agents: [],
    sessions: [],
    activeSessionId: null,
    messages: new Map(),
    responding: new Set(),
    turns: new Map(),
    connectionState: "disconnected",
    unreadCounts: new Map(),
    pendingPermissions: new Map(),
    sessionToolAllowlist: new Map(),
    nativeSessionsByAgent: new Map(),
    nativeSessionsLoading: false,
    _nativePendingCount: 0,
    previewSessionIds: new Set(),
    toasts: [],

    addToast: (message, type) => {
      const id = uuid();
      set({ toasts: [...get().toasts, { id, message, type }] });
      setTimeout(() => {
        set({ toasts: get().toasts.filter(t => t.id !== id) });
      }, 5000);
    },

    removeToast: (id) => {
      set({ toasts: get().toasts.filter(t => t.id !== id) });
    },

    cleanupSession: (sessionId: string) => {
      const { messages, turns, pendingPermissions, sessionToolAllowlist } = get();
      const updatedMessages = new Map(messages);
      updatedMessages.delete(sessionId);
      const updatedTurns = new Map(turns);
      updatedTurns.delete(sessionId);
      const updatedPerms = new Map(pendingPermissions);
      updatedPerms.delete(sessionId);
      const updatedAllowlist = new Map(sessionToolAllowlist);
      updatedAllowlist.delete(sessionId);
      set({ messages: updatedMessages, turns: updatedTurns, pendingPermissions: updatedPerms, sessionToolAllowlist: updatedAllowlist });
    },

    init: async () => {
      set({ authProvider: "builtin" });

      if (!api.isAuthenticated()) return;

      try {
        const user = await api.getMe();
        set({ user, isAuthenticated: true });
        socket.setStateCallback((state) => {
          set({ connectionState: state });
          // 1d: Clear responding set on reconnect so stale state is removed
          if (state === "connected") {
            set({ responding: new Set() });
          }
        });
        await socket.connect();
        await Promise.all([get().loadAgents(), get().loadSessions()]);
      } catch {
        set({ isAuthenticated: false });
      }
    },

    login: async (username: string, password: string) => {
      await api.login(username, password);
      const user = await api.getMe();
      set({ user, isAuthenticated: true });
      socket.setStateCallback((state) => {
        set({ connectionState: state });
        if (state === "connected") {
          set({ responding: new Set() });
        }
      });
      await socket.connect();
      // Load data in background — don't let failures block login.
      Promise.all([get().loadAgents(), get().loadSessions()]).catch(() => {});
    },

    logout: () => {
      socket.disconnect();
      api.logout();
      set({
        user: null,
        isAuthenticated: false,
        sessions: [],
        agents: [],
        messages: new Map(),
        turns: new Map(),
        activeSessionId: null,
        unreadCounts: new Map(),
        pendingPermissions: new Map(),
        sessionToolAllowlist: new Map(),
        toasts: [],
      });
    },

    loadAgents: async () => {
      const agents = await api.listAgents();
      set({ agents: agents || [] });
    },

    loadSessions: async () => {
      const sessions = await api.listSessions();
      set({ sessions: assignSequenceNumbers(sessions || []) });
    },

    createSession: async (agentId: string, promptProfile?: string) => {
      const session = await api.createSession(agentId, undefined, promptProfile);
      const { sessions } = get();
      set({ sessions: assignSequenceNumbers([session, ...sessions]) });
      await get().selectSession(session.id);
      return session;
    },

    selectSession: async (sessionId: string) => {
      const { activeSessionId } = get();

      // Unsubscribe from previous session
      if (activeSessionId) {
        socket.unsubscribe(activeSessionId);
      }

      // Clear unread count for selected session
      const unreadCounts = new Map(get().unreadCounts);
      unreadCounts.delete(sessionId);
      set({ activeSessionId: sessionId, unreadCounts });

      // Subscribe immediately to avoid a race where live output arrives between
      // the history fetch and the WebSocket subscription.
      const currentMessages = get().messages.get(sessionId) || [];
      const initialMaxSeq = currentMessages.reduce(
        (max, m) => Math.max(max, m.seq),
        0
      );
      socket.subscribe(sessionId, initialMaxSeq);

      const refreshSession = async () => {
        try {
          const latestSession = await api.getSession(sessionId);
          set({ sessions: upsertSession(get().sessions, latestSession) });
        } catch {
          // Session metadata refresh is best-effort.
        }
      };

      // Load history if not already loaded, then advance the tracked replay
      // cursor once the merged transcript is known.
      const hasLoadedMessages = get().messages.has(sessionId);
      if (!hasLoadedMessages) {
        try {
          const [msgs] = await Promise.all([
            api.getMessages(sessionId),
            refreshSession(),
          ]);
          const updated = new Map(get().messages);
          const existing = updated.get(sessionId) || [];
          const merged = mergeMessages(existing, msgs || []);
          updated.set(sessionId, merged);
          set({ messages: updated });

          const mergedMaxSeq = merged.reduce(
            (max, m) => Math.max(max, m.seq),
            0
          );
          socket.subscribe(sessionId, mergedMaxSeq);
        } catch {
          // Session might not have messages yet
          if (!get().messages.has(sessionId)) {
            const updated = new Map(get().messages);
            updated.set(sessionId, []);
            set({ messages: updated });
          }
        }
      } else {
        void refreshSession();
      }
    },

    deselectSession: () => {
      const { activeSessionId, previewSessionIds, sessions, messages } = get();
      if (activeSessionId) {
        socket.unsubscribe(activeSessionId);
        // CGR-36/38: Only close preview sessions if no messages were sent.
        // Previously this closed all preview sessions on deselect, which
        // could kill sessions before the agent had time to respond.
        if (previewSessionIds.has(activeSessionId)) {
          const sessionMessages = messages.get(activeSessionId) || [];
          const hasSentMessage = sessionMessages.some((m) => m.direction === "user");
          if (!hasSentMessage) {
            const updatedPreview = new Set(previewSessionIds);
            updatedPreview.delete(activeSessionId);
            api.closeSession(activeSessionId).catch(() => {});
            set({
              activeSessionId: null,
              previewSessionIds: updatedPreview,
              sessions: sessions.filter(s => s.id !== activeSessionId),
            });
            return;
          }
          // Has messages — promote to permanent, don't close.
          const updatedPreview = new Set(previewSessionIds);
          updatedPreview.delete(activeSessionId);
          set({ previewSessionIds: updatedPreview });
        }
      }
      set({ activeSessionId: null });
    },

    sendMessage: (content: string) => {
      const { activeSessionId, messages, previewSessionIds, responding, sessions, agents } = get();
      if (!activeSessionId) return;

      // Promote preview session to permanent on first message
      if (previewSessionIds.has(activeSessionId)) {
        const updated = new Set(previewSessionIds);
        updated.delete(activeSessionId);
        set({ previewSessionIds: updated });
      }

      const messageId = uuid();
      const userMessage: StoredMessage = {
        id: messageId,
        session_id: activeSessionId,
        seq: 0, // will be assigned by hub
        direction: "user",
        channel: "stdin",
        content,
        created_at: new Date().toISOString(),
      };

      // Optimistic update
      const sessionMessages = messages.get(activeSessionId) || [];
      const updated = new Map(messages);
      updated.set(activeSessionId, [...sessionMessages, userMessage]);
      set({
        messages: updated,
        sessions: patchSession(get().sessions, activeSessionId, {}),
      });

      const shouldSendInteractive =
        responding.has(activeSessionId) &&
        sessionAllowsInteractiveInput(activeSessionId, sessions, agents);
      const sent = shouldSendInteractive
        ? socket.sendInteractiveInput(activeSessionId, content)
        : socket.sendMessage(activeSessionId, content);
      if (!sent) {
        // Remove the optimistic message on failure
        const currentMessages = get().messages;
        const currentSessionMessages = currentMessages.get(activeSessionId) || [];
        const rolledBack = new Map(currentMessages);
        rolledBack.set(
          activeSessionId,
          currentSessionMessages.filter(m => m.id !== messageId)
        );
        set({ messages: rolledBack });
        get().addToast("Message not sent — connection lost. It will be retried when reconnected.", "error");
      }
    },

    canSendInteractiveInput: (sessionId: string) => {
      const { sessions, agents } = get();
      return sessionAllowsInteractiveInput(sessionId, sessions, agents);
    },

    stopSession: () => {
      const { activeSessionId } = get();
      if (!activeSessionId) return;
      socket.stopSession(activeSessionId);
    },

    closeSession: async (sessionId: string) => {
      await api.closeSession(sessionId);
      const { sessions } = get();
      set({
        sessions: sessions.map(s => s.id === sessionId ? { ...s, state: "closed" } : s),
      });
      // CGR-28: purge any queued outbound messages for this session
      socket.purgePending(sessionId);
      get().addToast("Session closed", "success");
    },

    uploadFile: async (sessionId: string, file: File) => {
      const { messages } = get();

      // Optimistic message — insert a placeholder file message.
      const tempId = uuid();
      const tempMeta = JSON.stringify({
        file_id: tempId,
        name: file.name,
        mime_type: file.type || "application/octet-stream",
        size: file.size,
        direction: "upload",
      });
      const tempMessage: StoredMessage = {
        id: tempId,
        session_id: sessionId,
        seq: 0,
        direction: "user",
        channel: "file",
        content: tempMeta,
        created_at: new Date().toISOString(),
      };

      const sessionMessages = messages.get(sessionId) || [];
      const updated = new Map(messages);
      updated.set(sessionId, [...sessionMessages, tempMessage]);
      set({ messages: updated });

      try {
        await api.uploadFile(sessionId, file);
      } catch (err) {
        // Roll back optimistic message on failure.
        const currentMessages = get().messages;
        const currentSessionMessages = currentMessages.get(sessionId) || [];
        const rolledBack = new Map(currentMessages);
        rolledBack.set(sessionId, currentSessionMessages.filter(m => m.id !== tempId));
        set({ messages: rolledBack });
        throw err;
      }
    },

    respondToPermission: (sessionId: string, requestId: string, approved: boolean, alwaysAllow?: boolean) => {
      const { pendingPermissions, sessionToolAllowlist } = get();
      const requests = pendingPermissions.get(sessionId) || [];
      const request = requests.find(r => r.request_id === requestId);

      // Handle "always allow" for this session
      if (approved && alwaysAllow && request) {
        const allowlist = new Map(sessionToolAllowlist);
        const tools = allowlist.get(sessionId) || new Set<string>();
        tools.add(request.tool);
        allowlist.set(sessionId, tools);
        set({ sessionToolAllowlist: allowlist });
      }

      // Remove from pending
      const updated = new Map(pendingPermissions);
      updated.set(sessionId, requests.filter(r => r.request_id !== requestId));
      if (updated.get(sessionId)?.length === 0) updated.delete(sessionId);
      set({ pendingPermissions: updated });

      // Send response via WebSocket
      socket.send("permission.response", {
        session_id: sessionId,
        request_id: requestId,
        approved,
        always_allow: alwaysAllow || false,
      }, sessionId);
    },

    loadNativeSessions: (agentId: string) => {
      set({ nativeSessionsLoading: true, _nativePendingCount: 1 });
      const requestId = uuid();
      socket.requestNativeSessions(agentId, requestId);
    },

    loadAllNativeSessions: () => {
      const { agents } = get();
      const capable = agents.filter((a) => {
        if (!a.online) return false;
        try {
          const caps = JSON.parse(a.caps || "{}");
          return caps.native_session_ids === true;
        } catch {
          return false;
        }
      });
      if (capable.length === 0) return;
      set({ nativeSessionsLoading: true, _nativePendingCount: capable.length });
      for (const agent of capable) {
        const requestId = uuid();
        socket.requestNativeSessions(agent.id, requestId);
      }
    },

    createSessionWithResume: async (agentId: string, resumeSessionId: string, promptProfile?: string) => {
      const session = await api.createSession(agentId, resumeSessionId, promptProfile);
      const { sessions, previewSessionIds } = get();
      const updatedPreview = new Set(previewSessionIds);
      updatedPreview.add(session.id);
      set({ sessions: assignSequenceNumbers([session, ...sessions]), previewSessionIds: updatedPreview });
      await get().selectSession(session.id);
      return session;
    },
  };
});
