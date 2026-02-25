import { useEffect, useRef, useState, useCallback } from "react";
import { useSessionStore } from "@/stores/sessionStore";
import { SessionList } from "@/components/SessionList";
import { MessageList } from "@/components/MessageList";
import { MessageInput } from "@/components/MessageInput";
import { EndpointPicker } from "@/components/EndpointPicker";
import { AdminPanel } from "@/components/AdminPanel";
import { ToastContainer } from "@/components/Toast";
import { PermissionBanner } from "@/components/PermissionBanner";
import { AgentHomeScreen } from "@/components/AgentHomeScreen";
import { PROFILE_DISPLAY } from "@/types";

function ConnectionBanner() {
  const connectionState = useSessionStore((s) => s.connectionState);
  const [hasConnected, setHasConnected] = useState(false);
  const [showReconnected, setShowReconnected] = useState(false);
  const prevState = useRef(connectionState);

  useEffect(() => {
    if (connectionState === "connected" && !hasConnected) {
      setHasConnected(true);
    }

    // Show "Reconnected" flash when transitioning from disconnected/reconnecting to connected
    if (
      connectionState === "connected" &&
      hasConnected &&
      (prevState.current === "disconnected" || prevState.current === "reconnecting")
    ) {
      setShowReconnected(true);
      const timer = setTimeout(() => setShowReconnected(false), 3000);
      prevState.current = connectionState;
      return () => clearTimeout(timer);
    }

    prevState.current = connectionState;
  }, [connectionState, hasConnected]);

  // Before the first successful connection, show connecting state
  if (!hasConnected) {
    if (connectionState === "disconnected" || connectionState === "reconnecting") {
      return (
        <div className="px-4 py-2 bg-teal-900/80 text-teal-200 text-sm text-center">
          Connecting...
        </div>
      );
    }
    return null; // connected but hasConnected not yet set (brief flash)
  }

  if (connectionState === "disconnected") {
    return (
      <div className="px-4 py-2 bg-red-900/80 text-red-200 text-sm text-center">
        Connection lost. Reconnecting...
      </div>
    );
  }

  if (connectionState === "reconnecting") {
    return (
      <div className="px-4 py-2 bg-amber-900/80 text-amber-200 text-sm text-center">
        Reconnecting...
      </div>
    );
  }

  if (showReconnected) {
    return (
      <div className="px-4 py-2 bg-green-900/80 text-green-200 text-sm text-center">
        Reconnected
      </div>
    );
  }

  return null;
}

function formatDuration(ms: number): string {
  const totalSec = Math.floor(ms / 1000);
  const h = Math.floor(totalSec / 3600);
  const m = Math.floor((totalSec % 3600) / 60);
  const s = totalSec % 60;
  if (h > 0) return `${h}:${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}`;
  return `${m}:${String(s).padStart(2, "0")}`;
}

function StateIndicator({ state, isResponding }: { state: string; isResponding: boolean }) {
  if (isResponding || state === "responding") {
    return (
      <span className="flex items-center gap-1.5 text-xs text-green-400">
        <span className="w-2 h-2 bg-green-500 rounded-full animate-pulse" />
        Executing
      </span>
    );
  }
  switch (state) {
    case "active":
      return (
        <span className="flex items-center gap-1.5 text-xs text-green-400">
          <span className="w-2 h-2 bg-green-500 rounded-full" />
          Ready
        </span>
      );
    case "idle":
      return (
        <span className="flex items-center gap-1.5 text-xs text-yellow-400">
          <span className="w-2 h-2 bg-yellow-500 rounded-full" />
          Idle
        </span>
      );
    case "closed":
      return (
        <span className="flex items-center gap-1.5 text-xs text-red-400">
          <span className="w-2 h-2 bg-red-500 rounded-full" />
          Closed
        </span>
      );
    case "creating":
      return (
        <span className="flex items-center gap-1.5 text-xs text-teal-400">
          <span className="w-2 h-2 bg-teal-500 rounded-full animate-pulse" />
          Creating
        </span>
      );
    default:
      return (
        <span className="flex items-center gap-1.5 text-xs text-slate-400">
          <span className="w-2 h-2 bg-slate-500 rounded-full" />
          {state}
        </span>
      );
  }
}

function SessionTimer({ createdAt }: { createdAt: string }) {
  const [elapsed, setElapsed] = useState(() => Date.now() - new Date(createdAt).getTime());

  useEffect(() => {
    const start = new Date(createdAt).getTime();
    setElapsed(Date.now() - start);
    const id = setInterval(() => setElapsed(Date.now() - start), 1000);
    return () => clearInterval(id);
  }, [createdAt]);

  return <span className="text-xs text-slate-500 font-mono">{formatDuration(elapsed)}</span>;
}

function CopyableSessionId({ id }: { id: string }) {
  const [copied, setCopied] = useState(false);

  const copy = useCallback(() => {
    navigator.clipboard.writeText(id).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    });
  }, [id]);

  return (
    <button
      onClick={copy}
      className="text-xs text-slate-600 hover:text-slate-400 font-mono transition-colors"
      title={`Copy session ID: ${id}`}
    >
      {copied ? "copied" : id.slice(0, 8)}
    </button>
  );
}

export function Chat() {
  const { activeSessionId, sessions, user, logout, stopSession, closeSession, deselectSession, responding, pendingPermissions } = useSessionStore();
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [pickerOpen, setPickerOpen] = useState(false);
  const [adminOpen, setAdminOpen] = useState(false);

  const activeSession = activeSessionId ? sessions.find((s) => s.id === activeSessionId) : null;
  const isResponding = activeSessionId ? responding.has(activeSessionId) : false;
  const pendingCount = activeSessionId ? (pendingPermissions.get(activeSessionId)?.length || 0) : 0;

  const handleCloseSession = () => {
    if (!activeSessionId) return;
    if (!window.confirm("Are you sure you want to close this session?")) return;
    closeSession(activeSessionId);
  };

  return (
    <div className="h-full flex">
      {/* Sidebar */}
      <div
        className={`
          fixed inset-y-0 left-0 z-30 w-72 bg-slate-800 border-r border-slate-700
          transform transition-transform duration-200 ease-in-out
          md:relative md:translate-x-0
          ${sidebarOpen ? "translate-x-0" : "-translate-x-full"}
        `}
      >
        <div className="flex flex-col h-full">
          {/* Sidebar header */}
          <div className="flex items-center justify-between p-4 border-b border-slate-700">
            <div className="flex flex-col">
              <div className="flex items-center gap-2">
                <button onClick={deselectSession} className="text-lg font-semibold amurg-logo hover:opacity-80 transition-opacity">Amurg</button>
                {user?.role === "admin" && (
                  <button
                    onClick={() => setAdminOpen(true)}
                    className="text-slate-500 hover:text-slate-300 transition-colors p-2 -mr-1"
                    title="Admin Dashboard"
                  >
                    <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2}
                        d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.066 2.573c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.573 1.066c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.066-2.573c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
                    </svg>
                  </button>
                )}
              </div>
              <span className="text-xs text-slate-500">Agent Control Plane</span>
            </div>
            <button
              onClick={() => setSidebarOpen(false)}
              className="md:hidden p-2 text-slate-400 hover:text-slate-200 rounded-lg"
            >
              <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
              </svg>
            </button>
          </div>

          {/* New session button */}
          <div className="p-3">
            <button
              onClick={() => setPickerOpen(true)}
              className="w-full py-2 px-3 bg-teal-600 hover:bg-teal-700 text-white
                         rounded-lg font-medium transition-colors text-sm flex items-center justify-center gap-2"
            >
              <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
              </svg>
              New Session
            </button>
          </div>

          {/* Session list */}
          <div className="flex-1 overflow-y-auto">
            <SessionList onSelect={() => setSidebarOpen(false)} />
          </div>

          {/* User footer */}
          <div className="p-3 border-t border-slate-700 flex items-center justify-between">
            <span className="text-sm text-slate-400 truncate">{user?.username}</span>
            <button
              onClick={logout}
              className="text-xs text-slate-500 hover:text-slate-300 transition-colors"
            >
              Sign out
            </button>
          </div>
        </div>
      </div>

      {/* Sidebar overlay (mobile) */}
      {sidebarOpen && (
        <div
          className="fixed inset-0 z-20 bg-black/50 md:hidden"
          onClick={() => setSidebarOpen(false)}
        />
      )}

      {/* Main chat area */}
      <div className="flex-1 flex flex-col min-w-0">
        {/* Top bar */}
        <div className="flex items-center gap-2 sm:gap-3 px-3 sm:px-4 py-2.5 border-b border-slate-700 bg-slate-800/50">
          {/* Hamburger (mobile) */}
          <button
            onClick={() => setSidebarOpen(true)}
            className="md:hidden p-2 -ml-1 text-slate-400 hover:text-slate-200 rounded-lg"
          >
            <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 6h16M4 12h16M4 18h16" />
            </svg>
          </button>

          {activeSession ? (
            <>
              {/* Home button */}
              <button
                onClick={deselectSession}
                className="p-2 -ml-1 md:ml-0 text-slate-400 hover:text-slate-200 rounded-lg transition-colors"
                title="Back to agents"
              >
                <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 001 1h3m10-11l2 2m-2-2v10a1 1 0 01-1 1h-3m-6 0a1 1 0 001-1v-4a1 1 0 011-1h2a1 1 0 011 1v4a1 1 0 001 1m-6 0h6" />
                </svg>
              </button>

              {/* Left: endpoint name + state */}
              <div className="flex items-center gap-2 min-w-0 flex-1">
                <span className="text-sm text-slate-200 font-medium truncate">
                  {activeSession.endpoint_name || PROFILE_DISPLAY[activeSession.profile]?.label || activeSession.profile}
                  {activeSession.seq != null && <span className="text-slate-500"> #{activeSession.seq}</span>}
                </span>
                <StateIndicator state={activeSession.state} isResponding={isResponding} />
                {pendingCount > 0 && (
                  <span className="inline-flex items-center justify-center w-5 h-5 text-xs font-bold bg-amber-600 text-white rounded-full">
                    {pendingCount}
                  </span>
                )}
              </div>

              {/* Right: timer + session ID (hidden on mobile) + actions */}
              <div className="flex items-center gap-2 flex-shrink-0">
                <span className="hidden md:inline-block">
                  <SessionTimer createdAt={activeSession.created_at} />
                </span>
                <span className="hidden md:inline-block">
                  <CopyableSessionId id={activeSession.id} />
                </span>
                {activeSession.state !== "closed" && !isResponding && (
                  <button
                    onClick={handleCloseSession}
                    className="p-2 bg-slate-600 hover:bg-slate-500 text-white rounded-lg transition-colors"
                    title="Close session"
                  >
                    <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 8h14M5 8a2 2 0 110-4h14a2 2 0 110 4M5 8v10a2 2 0 002 2h10a2 2 0 002-2V8m-9 4h4" />
                    </svg>
                  </button>
                )}
                {isResponding && (
                  <button
                    onClick={stopSession}
                    className="p-2 bg-red-600 hover:bg-red-700 text-white rounded-lg transition-colors"
                    title="Stop execution"
                  >
                    <svg className="w-4 h-4" fill="currentColor" viewBox="0 0 24 24">
                      <rect x="6" y="6" width="12" height="12" rx="1" />
                    </svg>
                  </button>
                )}
              </div>
            </>
          ) : (
            <div className="flex items-center gap-2">
              <h1 className="text-lg font-semibold amurg-logo md:hidden">Amurg</h1>
              <span className="text-sm text-slate-400 truncate hidden md:block">
                Select an agent below or pick a session from the sidebar
              </span>
            </div>
          )}
        </div>

        {/* Connection banner */}
        <ConnectionBanner />

        {/* Messages or Agent Home */}
        <div className="flex-1 overflow-y-auto">
          {activeSessionId ? (
            <MessageList />
          ) : (
            <AgentHomeScreen />
          )}
        </div>

        {/* Permission banner */}
        {activeSessionId && <PermissionBanner />}

        {/* Input */}
        {activeSessionId && <MessageInput />}
      </div>

      {/* Endpoint picker modal */}
      {pickerOpen && <EndpointPicker onClose={() => setPickerOpen(false)} />}

      {/* Admin panel modal */}
      {adminOpen && <AdminPanel onClose={() => setAdminOpen(false)} />}

      {/* Toast notifications */}
      <ToastContainer />
    </div>
  );
}
