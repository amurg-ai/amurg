import { describe, it, expect, vi, beforeEach } from "vitest";

// Mock websocket before importing the store.
vi.mock("@/api/websocket", () => ({
  socket: {
    on: vi.fn(),
    connect: vi.fn(),
    disconnect: vi.fn(),
    setStateCallback: vi.fn(),
    subscribe: vi.fn(),
    unsubscribe: vi.fn(),
    sendMessage: vi.fn(() => true),
    stopSession: vi.fn(),
  },
}));

// Mock api client before importing the store.
vi.mock("@/api/client", () => ({
  api: {
    isAuthenticated: vi.fn(() => false),
    login: vi.fn(),
    logout: vi.fn(),
    getMe: vi.fn(),
    listEndpoints: vi.fn(() => Promise.resolve([])),
    listSessions: vi.fn(() => Promise.resolve([])),
    createSession: vi.fn(),
    getMessages: vi.fn(() => Promise.resolve([])),
  },
}));

import { useSessionStore } from "./sessionStore";
import { api } from "@/api/client";
import { socket } from "@/api/websocket";

const mockedApi = vi.mocked(api);
const mockedSocket = vi.mocked(socket);

describe("sessionStore", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    // Reset store to initial state.
    useSessionStore.setState({
      user: null,
      isAuthenticated: false,
      endpoints: [],
      sessions: [],
      activeSessionId: null,
      messages: new Map(),
      responding: new Set(),
      turns: new Map(),
      connectionState: "disconnected",
      unreadCounts: new Map(),
      toasts: [],
    });
  });

  // --- Initial state ---
  describe("initial state", () => {
    it("has null user", () => {
      expect(useSessionStore.getState().user).toBeNull();
    });

    it("is not authenticated", () => {
      expect(useSessionStore.getState().isAuthenticated).toBe(false);
    });

    it("has empty sessions", () => {
      expect(useSessionStore.getState().sessions).toEqual([]);
    });

    it("has no active session", () => {
      expect(useSessionStore.getState().activeSessionId).toBeNull();
    });

    it("has empty endpoints", () => {
      expect(useSessionStore.getState().endpoints).toEqual([]);
    });

    it("has disconnected connection state", () => {
      expect(useSessionStore.getState().connectionState).toBe("disconnected");
    });
  });

  // --- sendMessage ---
  describe("sendMessage", () => {
    it("does nothing when no active session", () => {
      useSessionStore.getState().sendMessage("hello");
      // No session, so messages map should be empty.
      expect(useSessionStore.getState().messages.size).toBe(0);
      expect(mockedSocket.sendMessage).not.toHaveBeenCalled();
    });

    it("adds optimistic user message to the active session", () => {
      useSessionStore.setState({
        activeSessionId: "sess-1",
        messages: new Map(),
      });

      useSessionStore.getState().sendMessage("hello world");

      const messages = useSessionStore.getState().messages.get("sess-1");
      expect(messages).toBeDefined();
      expect(messages!.length).toBe(1);
      expect(messages![0].content).toBe("hello world");
      expect(messages![0].direction).toBe("user");
      expect(messages![0].channel).toBe("stdin");
      expect(messages![0].session_id).toBe("sess-1");
    });

    it("sends message via WebSocket", () => {
      useSessionStore.setState({
        activeSessionId: "sess-1",
        messages: new Map(),
      });

      useSessionStore.getState().sendMessage("test input");

      expect(mockedSocket.sendMessage).toHaveBeenCalledWith(
        "sess-1",
        "test input",
      );
    });

    it("appends to existing messages", () => {
      const existingMessages = new Map();
      existingMessages.set("sess-1", [
        {
          id: "msg-0",
          session_id: "sess-1",
          seq: 1,
          direction: "agent" as const,
          channel: "stdout",
          content: "previous",
          created_at: new Date().toISOString(),
        },
      ]);
      useSessionStore.setState({
        activeSessionId: "sess-1",
        messages: existingMessages,
      });

      useSessionStore.getState().sendMessage("new message");

      const messages = useSessionStore.getState().messages.get("sess-1");
      expect(messages!.length).toBe(2);
      expect(messages![0].content).toBe("previous");
      expect(messages![1].content).toBe("new message");
    });
  });

  // --- stopSession ---
  describe("stopSession", () => {
    it("does nothing when no active session", () => {
      useSessionStore.getState().stopSession();
      expect(mockedSocket.stopSession).not.toHaveBeenCalled();
    });

    it("calls socket.stopSession with active session ID", () => {
      useSessionStore.setState({ activeSessionId: "sess-42" });

      useSessionStore.getState().stopSession();

      expect(mockedSocket.stopSession).toHaveBeenCalledWith("sess-42");
    });
  });

  // --- login ---
  describe("login", () => {
    it("calls api.login and api.getMe, then sets user", async () => {
      mockedApi.login.mockResolvedValue({ token: "tok-1" });
      mockedApi.getMe.mockResolvedValue({
        id: "u1",
        username: "admin",
        role: "admin",
      });
      mockedApi.listEndpoints.mockResolvedValue([]);
      mockedApi.listSessions.mockResolvedValue([]);

      await useSessionStore.getState().login("admin", "admin");

      expect(mockedApi.login).toHaveBeenCalledWith("admin", "admin");
      expect(mockedApi.getMe).toHaveBeenCalled();
      expect(useSessionStore.getState().user).toEqual({
        id: "u1",
        username: "admin",
        role: "admin",
      });
      expect(useSessionStore.getState().isAuthenticated).toBe(true);
    });

    it("connects WebSocket after login", async () => {
      mockedApi.login.mockResolvedValue({ token: "tok" });
      mockedApi.getMe.mockResolvedValue({
        id: "u1",
        username: "admin",
        role: "admin",
      });
      mockedApi.listEndpoints.mockResolvedValue([]);
      mockedApi.listSessions.mockResolvedValue([]);

      await useSessionStore.getState().login("admin", "admin");

      expect(mockedSocket.connect).toHaveBeenCalled();
      expect(mockedSocket.setStateCallback).toHaveBeenCalled();
    });
  });

  // --- logout ---
  describe("logout", () => {
    it("disconnects WebSocket and clears state", () => {
      useSessionStore.setState({
        user: { id: "u1", username: "admin", role: "admin" },
        isAuthenticated: true,
        sessions: [
          {
            id: "s1",
            user_id: "u1",
            endpoint_id: "e1",
            runtime_id: "r1",
            profile: "cli",
            state: "active",
            created_at: "",
            updated_at: "",
          },
        ],
        activeSessionId: "s1",
      });

      useSessionStore.getState().logout();

      expect(mockedSocket.disconnect).toHaveBeenCalled();
      expect(mockedApi.logout).toHaveBeenCalled();
      expect(useSessionStore.getState().user).toBeNull();
      expect(useSessionStore.getState().isAuthenticated).toBe(false);
      expect(useSessionStore.getState().sessions).toEqual([]);
      expect(useSessionStore.getState().activeSessionId).toBeNull();
    });
  });

  // --- loadEndpoints ---
  describe("loadEndpoints", () => {
    it("fetches and sets endpoints", async () => {
      const eps = [
        {
          id: "ep1",
          runtime_id: "r1",
          profile: "generic-cli",
          name: "Test CLI",
          online: true,
          caps: "{}",
        },
      ];
      mockedApi.listEndpoints.mockResolvedValue(eps);

      await useSessionStore.getState().loadEndpoints();

      expect(useSessionStore.getState().endpoints).toEqual(eps);
    });

    it("handles null response by setting empty array", async () => {
      mockedApi.listEndpoints.mockResolvedValue(
        null as unknown as ReturnType<typeof api.listEndpoints> extends Promise<
          infer T
        >
          ? T
          : never,
      );

      await useSessionStore.getState().loadEndpoints();

      expect(useSessionStore.getState().endpoints).toEqual([]);
    });
  });

  // --- loadSessions ---
  describe("loadSessions", () => {
    it("fetches, assigns sequence numbers, and sets sessions", async () => {
      const sessions = [
        {
          id: "s1",
          user_id: "u1",
          endpoint_id: "ep1",
          runtime_id: "r1",
          profile: "cli",
          state: "active",
          created_at: "2024-01-01T00:00:00Z",
          updated_at: "2024-01-01T00:00:00Z",
        },
        {
          id: "s2",
          user_id: "u1",
          endpoint_id: "ep1",
          runtime_id: "r1",
          profile: "cli",
          state: "active",
          created_at: "2024-01-02T00:00:00Z",
          updated_at: "2024-01-02T00:00:00Z",
        },
      ];
      mockedApi.listSessions.mockResolvedValue(sessions);

      await useSessionStore.getState().loadSessions();

      const stored = useSessionStore.getState().sessions;
      expect(stored.length).toBe(2);
      // Sequence numbers should be assigned per endpoint
      expect(stored.find((s) => s.id === "s1")?.seq).toBe(1);
      expect(stored.find((s) => s.id === "s2")?.seq).toBe(2);
    });
  });

  // --- selectSession ---
  describe("selectSession", () => {
    it("sets activeSessionId and loads messages", async () => {
      const msgs = [
        {
          id: "m1",
          session_id: "sess-1",
          seq: 1,
          direction: "agent" as const,
          channel: "stdout",
          content: "hello",
          created_at: "2024-01-01T00:00:00Z",
        },
      ];
      mockedApi.getMessages.mockResolvedValue(msgs);

      await useSessionStore.getState().selectSession("sess-1");

      expect(useSessionStore.getState().activeSessionId).toBe("sess-1");
      expect(useSessionStore.getState().messages.get("sess-1")).toEqual(msgs);
    });

    it("unsubscribes from previous session", async () => {
      useSessionStore.setState({ activeSessionId: "old-sess" });
      mockedApi.getMessages.mockResolvedValue([]);

      await useSessionStore.getState().selectSession("new-sess");

      expect(mockedSocket.unsubscribe).toHaveBeenCalledWith("old-sess");
    });

    it("subscribes to new session for live updates", async () => {
      mockedApi.getMessages.mockResolvedValue([]);

      await useSessionStore.getState().selectSession("sess-1");

      expect(mockedSocket.subscribe).toHaveBeenCalledWith("sess-1", 0);
    });

    it("subscribes with max seq from loaded messages", async () => {
      const msgs = [
        {
          id: "m1",
          session_id: "sess-1",
          seq: 5,
          direction: "agent" as const,
          channel: "stdout",
          content: "a",
          created_at: "",
        },
        {
          id: "m2",
          session_id: "sess-1",
          seq: 10,
          direction: "agent" as const,
          channel: "stdout",
          content: "b",
          created_at: "",
        },
      ];
      mockedApi.getMessages.mockResolvedValue(msgs);

      await useSessionStore.getState().selectSession("sess-1");

      expect(mockedSocket.subscribe).toHaveBeenCalledWith("sess-1", 10);
    });

    it("does not re-fetch messages if already loaded", async () => {
      const existingMessages = new Map();
      existingMessages.set("sess-1", [
        {
          id: "m1",
          session_id: "sess-1",
          seq: 3,
          direction: "agent" as const,
          channel: "stdout",
          content: "cached",
          created_at: "",
        },
      ]);
      useSessionStore.setState({ messages: existingMessages });

      await useSessionStore.getState().selectSession("sess-1");

      expect(mockedApi.getMessages).not.toHaveBeenCalled();
      // Should still subscribe with max seq from existing messages.
      expect(mockedSocket.subscribe).toHaveBeenCalledWith("sess-1", 3);
    });
  });
});
