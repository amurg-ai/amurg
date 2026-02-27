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
} from "@/types";
import { api } from "@/api/client";
import { socket } from "@/api/websocket";

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
  createSession: (agentId: string) => Promise<SessionInfo>;
  selectSession: (sessionId: string) => Promise<void>;
  deselectSession: () => void;
  sendMessage: (content: string) => void;
  stopSession: () => void;
  closeSession: (sessionId: string) => Promise<void>;
  cleanupSession: (sessionId: string) => void;
  respondToPermission: (sessionId: string, requestId: string, approved: boolean, alwaysAllow?: boolean) => void;
  uploadFile: (sessionId: string, file: File) => Promise<void>;
}

export const useSessionStore = create<SessionState>((set, get) => {
  // Register WebSocket handlers eagerly at store creation time.
  // Handlers use get() for fresh state, so they work regardless of
  // when the WebSocket actually connects.
  (function setupSocketHandlers() {
    socket.on("agent.output", (env: Envelope) => {
      const output = env.payload as AgentOutput;
      const { messages } = get();
      const sessionMessages = messages.get(output.session_id) || [];

      const newMessage: StoredMessage = {
        id: output.message_id || crypto.randomUUID(),
        session_id: output.session_id,
        seq: output.seq,
        direction: "agent",
        channel: output.channel,
        content: output.content,
        created_at: new Date().toISOString(),
      };

      const updated = new Map(messages);
      updated.set(output.session_id, [...sessionMessages, newMessage]);
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
      const { responding, turns, messages } = get();

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

      set({ responding: updatedResponding, turns: updatedTurns });
    });

    socket.on("turn.completed", (_env: Envelope) => {
      const payload = _env.payload as TurnCompleted;
      const { responding, turns, messages } = get();

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

      set({ responding: updatedResponding, turns: updatedTurns });
    });

    socket.on("history.response", (env: Envelope) => {
      const payload = env.payload as {
        session_id: string;
        messages: StoredMessage[];
      };
      const { messages } = get();
      const existing = messages.get(payload.session_id) || [];
      const existingIds = new Set(existing.map((m) => m.id));
      const newMsgs = payload.messages.filter((m) => !existingIds.has(m.id));

      if (newMsgs.length > 0) {
        const updated = new Map(messages);
        updated.set(payload.session_id, [...existing, ...newMsgs]);
        set({ messages: updated });
      }
    });

    socket.on("session.closed", (env: Envelope) => {
      const payload = env.payload as { session_id: string };
      const { sessions } = get();

      // Update session state in the sessions array
      set({
        sessions: sessions.map(s =>
          s.id === payload.session_id ? { ...s, state: "closed" } : s
        ),
      });

      // Cleanup messages and turns for the closed session
      get().cleanupSession(payload.session_id);
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
    toasts: [],

    addToast: (message, type) => {
      const id = crypto.randomUUID();
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
      setupSocketHandlers();
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

    createSession: async (agentId: string) => {
      const session = await api.createSession(agentId);
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

      // Load history if not already loaded
      const { messages } = get();
      if (!messages.has(sessionId)) {
        try {
          const msgs = await api.getMessages(sessionId);
          const updated = new Map(get().messages);
          updated.set(sessionId, msgs || []);
          set({ messages: updated });
        } catch {
          // Session might not have messages yet
          const updated = new Map(get().messages);
          updated.set(sessionId, []);
          set({ messages: updated });
        }
      }

      // Subscribe for live updates
      const sessionMessages = get().messages.get(sessionId) || [];
      const maxSeq = sessionMessages.reduce(
        (max, m) => Math.max(max, m.seq),
        0
      );
      socket.subscribe(sessionId, maxSeq);
    },

    deselectSession: () => {
      const { activeSessionId } = get();
      if (activeSessionId) {
        socket.unsubscribe(activeSessionId);
      }
      set({ activeSessionId: null });
    },

    sendMessage: (content: string) => {
      const { activeSessionId, messages } = get();
      if (!activeSessionId) return;

      const messageId = crypto.randomUUID();
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
      set({ messages: updated });

      const sent = socket.sendMessage(activeSessionId, content);
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
      get().addToast("Session closed", "success");
    },

    uploadFile: async (sessionId: string, file: File) => {
      const { messages } = get();

      // Optimistic message — insert a placeholder file message.
      const tempId = crypto.randomUUID();
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
  };
});
