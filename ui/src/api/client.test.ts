import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { api } from "./client";

// Save originals so we can restore them.
const originalFetch = globalThis.fetch;

describe("api client", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    localStorage.clear();
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
  });

  // --- login ---
  describe("login", () => {
    it("stores token on successful login", async () => {
      globalThis.fetch = vi.fn().mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ token: "test-token-123" }),
      });

      const result = await api.login("admin", "admin");

      expect(result.token).toBe("test-token-123");
      expect(localStorage.getItem("amurg_token")).toBe("test-token-123");
    });

    it("sends correct request body", async () => {
      globalThis.fetch = vi.fn().mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ token: "tok" }),
      });

      await api.login("myuser", "mypass");

      const [url, options] = (globalThis.fetch as ReturnType<typeof vi.fn>).mock
        .calls[0];
      expect(url).toBe("/api/auth/login");
      expect(options.method).toBe("POST");
      expect(JSON.parse(options.body)).toEqual({
        username: "myuser",
        password: "mypass",
      });
    });

    it("throws on invalid credentials", async () => {
      globalThis.fetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 401,
      });

      await expect(api.login("bad", "creds")).rejects.toThrow(
        "Invalid credentials",
      );
    });

    it("does not store token on failed login", async () => {
      globalThis.fetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 401,
      });

      try {
        await api.login("bad", "creds");
      } catch {
        // expected
      }

      expect(localStorage.getItem("amurg_token")).toBeNull();
    });
  });

  // --- isAuthenticated ---
  describe("isAuthenticated", () => {
    it("returns false when no token", () => {
      expect(api.isAuthenticated()).toBe(false);
    });

    it("returns true when token is present", () => {
      localStorage.setItem("amurg_token", "some-token");
      expect(api.isAuthenticated()).toBe(true);
    });

    it("returns false after token is removed", () => {
      localStorage.setItem("amurg_token", "some-token");
      localStorage.removeItem("amurg_token");
      expect(api.isAuthenticated()).toBe(false);
    });
  });

  // --- authenticated requests ---
  describe("listAgents", () => {
    it("sends Authorization header with stored token", async () => {
      localStorage.setItem("amurg_token", "my-token");
      globalThis.fetch = vi.fn().mockResolvedValue({
        ok: true,
        json: () => Promise.resolve([]),
      });

      await api.listAgents();

      const [url, options] = (globalThis.fetch as ReturnType<typeof vi.fn>)
        .mock.calls[0];
      expect(url).toBe("/api/agents");
      expect(options.headers.Authorization).toBe("Bearer my-token");
      expect(options.headers["Content-Type"]).toBe("application/json");
    });

    it("does not send Authorization header when no token", async () => {
      globalThis.fetch = vi.fn().mockResolvedValue({
        ok: true,
        json: () => Promise.resolve([]),
      });

      await api.listAgents();

      const [, options] = (globalThis.fetch as ReturnType<typeof vi.fn>).mock
        .calls[0];
      expect(options.headers.Authorization).toBeUndefined();
    });
  });

  describe("listSessions", () => {
    it("calls correct path", async () => {
      localStorage.setItem("amurg_token", "tok");
      globalThis.fetch = vi.fn().mockResolvedValue({
        ok: true,
        json: () => Promise.resolve([]),
      });

      await api.listSessions();

      const [url] = (globalThis.fetch as ReturnType<typeof vi.fn>).mock
        .calls[0];
      expect(url).toBe("/api/sessions");
    });
  });

  describe("createSession", () => {
    it("sends POST with agent_id", async () => {
      localStorage.setItem("amurg_token", "tok");
      globalThis.fetch = vi.fn().mockResolvedValue({
        ok: true,
        json: () =>
          Promise.resolve({
            id: "sess-1",
            agent_id: "ep-1",
            state: "active",
          }),
      });

      const result = await api.createSession("ep-1");

      const [url, options] = (globalThis.fetch as ReturnType<typeof vi.fn>)
        .mock.calls[0];
      expect(url).toBe("/api/sessions");
      expect(options.method).toBe("POST");
      expect(JSON.parse(options.body)).toEqual({ agent_id: "ep-1" });
      expect(result.id).toBe("sess-1");
    });
  });

  describe("getMessages", () => {
    it("calls correct path with session ID", async () => {
      localStorage.setItem("amurg_token", "tok");
      globalThis.fetch = vi.fn().mockResolvedValue({
        ok: true,
        json: () => Promise.resolve([]),
      });

      await api.getMessages("sess-abc");

      const [url] = (globalThis.fetch as ReturnType<typeof vi.fn>).mock
        .calls[0];
      expect(url).toBe("/api/sessions/sess-abc/messages");
    });
  });

  // --- error handling ---
  describe("error handling", () => {
    it("throws error message from response body", async () => {
      localStorage.setItem("amurg_token", "tok");
      globalThis.fetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 400,
        json: () => Promise.resolve({ error: "bad request details" }),
      });

      await expect(api.listAgents()).rejects.toThrow("bad request details");
    });

    it("falls back to statusText when response body has no error field", async () => {
      localStorage.setItem("amurg_token", "tok");
      globalThis.fetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 500,
        statusText: "Internal Server Error",
        json: () => Promise.resolve({}),
      });

      await expect(api.listAgents()).rejects.toThrow(
        "Internal Server Error",
      );
    });

    it("handles non-JSON error responses", async () => {
      localStorage.setItem("amurg_token", "tok");
      globalThis.fetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 502,
        statusText: "Bad Gateway",
        json: () => Promise.reject(new Error("not JSON")),
      });

      await expect(api.listAgents()).rejects.toThrow("Bad Gateway");
    });
  });
});
