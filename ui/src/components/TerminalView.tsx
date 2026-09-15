import { useCallback, useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { MessageInput } from "@/components/MessageInput";
import { api } from "@/api/client";
import { socket } from "@/api/websocket";
import { useSessionStore } from "@/stores/sessionStore";
import type { TerminalOutput } from "@/types";

export function TerminalView({ sessionId, closed = false }: { sessionId: string; closed?: boolean }) {
  const host = useRef<HTMLDivElement>(null);
  const terminal = useRef<Terminal | null>(null);
  const fit = useRef<FitAddon | null>(null);
  const generation = useRef("");
  const [status, setStatus] = useState("Connecting terminal…");
  const [ready, setReady] = useState(false);
  const [directInput, setDirectInput] = useState(false);
  const directInputEnabled = useRef(false);
  const connection = useSessionStore((s) => s.connectionState);

  const sendBytes = useCallback((bytes: Uint8Array) => {
    if (!generation.current || !socket.connected) return false;
    // Chunk pastes without changing their bytes or appending a newline.
    for (let i = 0; i < bytes.length; i += 32 * 1024) {
      const data = btoa(Array.from(bytes.subarray(i, i + 32 * 1024), (b) => String.fromCharCode(b)).join(""));
      if (!socket.send("terminal.input", { session_id: sessionId, generation: generation.current, data }, sessionId)) return false;
    }
    return true;
  }, [sessionId]);

  const send = useCallback((text: string) => sendBytes(new TextEncoder().encode(text)), [sendBytes]);

  const attach = useCallback(() => {
    if (!terminal.current || !socket.connected) return;
    generation.current = "";
    setReady(false);
    setStatus("Connecting terminal…");
    fit.current?.fit();
    socket.send("terminal.attach", {
      session_id: sessionId,
      cols: Math.min(500, Math.max(2, terminal.current.cols)),
      rows: Math.min(300, Math.max(2, terminal.current.rows)),
    }, sessionId);
  }, [sessionId]);

  const upload = useCallback((file: File): Promise<string> => {
    if (!generation.current || !socket.connected) return Promise.reject(new Error("Reconnect before uploading a file"));
    return new Promise((resolve, reject) => {
      let id = "";
      const received = new Map<string, string>();
      const finish = () => {
        const path = received.get(id);
        if (path) { clearTimeout(timer); off(); resolve(path); }
      };
      const off = socket.on("file.received", (env) => {
        const receipt = env.payload as { session_id: string; file_id: string; path: string };
        if (receipt.session_id !== sessionId) return;
        received.set(receipt.file_id, receipt.path);
        finish();
      });
      const timer = setTimeout(() => { off(); reject(new Error("The runtime has not confirmed the upload. Reconnect and try again.")); }, 30_000);
      api.uploadFile(sessionId, file).then((result) => { id = result.file_id; finish(); }).catch((err) => { clearTimeout(timer); off(); reject(err); });
    });
  }, [sessionId]);

  useEffect(() => {
    if (!host.current) return;
    const term = new Terminal({
      cursorBlink: true,
      fontSize: 14,
      fontFamily: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace",
      theme: { background: "#0f172a", foreground: "#e2e8f0" },
      scrollback: 5000,
      allowProposedApi: false,
    });
    const addon = new FitAddon();
    term.loadAddon(addon);
    term.open(host.current);
    term.attachCustomKeyEventHandler(() => directInputEnabled.current);
    if (term.textarea) term.textarea.readOnly = true;
    terminal.current = term;
    fit.current = addon;
    addon.fit();
    const input = term.onData(send);
    const binaryInput = term.onBinary((data) => sendBytes(Uint8Array.from(data, (c) => c.charCodeAt(0))));
    const off = socket.on("terminal.output", (env) => {
      const out = env.payload as TerminalOutput;
      if (out.session_id !== sessionId) return;
      if (out.kind === "reconnect") { attach(); return; }
      if (out.kind === "error") {
        setStatus(out.error || "Terminal connection failed");
        generation.current = "";
        setReady(false);
        return;
      }
      if (out.kind === "reset") {
        generation.current = out.generation;
        // Queue reset with output writes so pending writes from the old
        // attachment cannot repaint over the new tmux screen.
        term.write("\x1bc");
        if (out.cols && out.rows) term.resize(out.cols, out.rows);
        setStatus("Connected");
        setReady(true);
        // Attaching starts a real persistent terminal, so it is no longer a
        // disposable chat preview even if no chat messages have been created.
        const previews = new Set(useSessionStore.getState().previewSessionIds);
        previews.delete(sessionId);
        useSessionStore.setState({ previewSessionIds: previews });
        return;
      }
      if (out.generation !== generation.current) return;
      if (out.kind === "output" && out.data) {
        term.write(Uint8Array.from(atob(out.data), (c) => c.charCodeAt(0)));
      } else if (out.kind === "detached") {
        generation.current = "";
        setReady(false);
        setStatus("Terminal detached. Reconnect to attach again.");
      }
    });
    let resizeTimer: ReturnType<typeof setTimeout>;
    const observer = new ResizeObserver(() => {
      clearTimeout(resizeTimer);
      resizeTimer = setTimeout(() => {
        addon.fit();
        if (generation.current) socket.send("terminal.resize", {
          session_id: sessionId, generation: generation.current,
          cols: Math.min(500, Math.max(2, term.cols)), rows: Math.min(300, Math.max(2, term.rows)),
        }, sessionId);
      }, 100);
    });
    observer.observe(host.current);
    return () => {
      off(); input.dispose(); binaryInput.dispose(); observer.disconnect(); clearTimeout(resizeTimer);
      generation.current = "";
      terminal.current = null;
      fit.current = null;
      term.dispose();
    };
  }, [sessionId, send, sendBytes, attach]);

  useEffect(() => {
    if (connection === "connected" && !closed) attach();
    else {
      generation.current = "";
      setReady(false);
      setStatus(closed ? "Session closed. Reconnect to reopen the terminal." : "Disconnected. Input is disabled until reconnection.");
    }
  }, [connection, closed, attach]);

  return (
    <div className="h-full min-h-0 flex flex-col bg-slate-900">
      <div className="flex items-center gap-2 px-3 py-2 text-xs text-slate-400 border-b border-slate-800">
        <button type="button" aria-pressed={directInput} disabled={!ready} onClick={() => {
          const enabled = !directInput;
          setDirectInput(enabled);
          directInputEnabled.current = enabled;
          if (terminal.current) {
            if (terminal.current.textarea) terminal.current.textarea.readOnly = !enabled;
            if (enabled) terminal.current.focus();
          }
        }} className="px-2 py-1 rounded bg-slate-700 disabled:opacity-50">Direct input</button>
        <span className="flex-1" role="status">{status}</span>
        <button type="button" onClick={attach} disabled={connection !== "connected"} className="px-2 py-1 rounded bg-slate-700 disabled:opacity-50">Reconnect</button>
      </div>
      <div ref={host} className="flex-1 min-h-0 overflow-hidden p-2" aria-label="Agent terminal" />
      <div className="border-t border-slate-700 p-2 space-y-2">
        <div className="flex flex-wrap gap-2">
          {[["Esc", "\x1b"], ["Tab", "\t"], ["↑", "\x1b[A"], ["↓", "\x1b[B"], ["Enter", "\r"], ["Ctrl-C", "\x03"]].map(([label, data]) => (
            <button key={label} type="button" disabled={!ready} onClick={() => send((label === "↑" || label === "↓") && terminal.current?.modes.applicationCursorKeysMode ? data.replace("[", "O") : data)} className="rounded bg-slate-800 px-3 py-1 text-sm text-slate-200 disabled:opacity-40">{label}</button>
          ))}
        </div>

      </div>
      <MessageInput disabled={!ready} onSend={(content) => {
        if (!ready || !terminal.current || !socket.connected) return false;
        terminal.current.paste(content);
        return send("\r");
      }} onUpload={upload} />
    </div>
  );
}
