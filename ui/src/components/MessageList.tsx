import { useEffect, useRef, memo, useMemo } from "react";
import { useSessionStore } from "@/stores/sessionStore";
import type { StoredMessage, Turn } from "@/types";
import {
  detectContentType,
  MarkdownRenderer,
  AnsiRenderer,
  DiffRenderer,
  JsonRenderer,
  CollapsibleOutput,
  FileRenderer,
} from "./renderers";

function formatElapsed(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  const s = ms / 1000;
  if (s < 60) return `${s.toFixed(1)}s`;
  const m = Math.floor(s / 60);
  const rem = (s % 60).toFixed(0);
  return `${m}m${rem}s`;
}

function formatTimestamp(iso: string): string {
  try {
    const d = new Date(iso);
    return d.toLocaleTimeString("en-US", { hour12: false, hour: "2-digit", minute: "2-digit", second: "2-digit" });
  } catch {
    return "";
  }
}

export function MessageList() {
  const { activeSessionId, messages, responding, turns } = useSessionStore();
  const bottomRef = useRef<HTMLDivElement>(null);

  const sessionMessages = activeSessionId
    ? messages.get(activeSessionId) || []
    : [];

  const sessionTurns = activeSessionId
    ? turns.get(activeSessionId) || []
    : [];

  const isResponding = activeSessionId
    ? responding.has(activeSessionId)
    : false;

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [sessionMessages.length]);

  // Group messages with turn separators
  const elements = useMemo(() => {
    const result: React.ReactNode[] = [];
    let lastTurnNumber = 0;

    for (const msg of sessionMessages) {
      // Check if this message starts a new turn
      for (const turn of sessionTurns) {
        if (turn.turnNumber > lastTurnNumber && msg.seq > turn.startSeq) {
          if (turn.turnNumber > 0) {
            result.push(<TurnSeparator key={`turn-${turn.turnNumber}`} turn={turn} />);
            lastTurnNumber = turn.turnNumber;
          }
          break;
        }
      }

      result.push(<LogEntry key={msg.id} msg={msg} sessionId={activeSessionId || ""} />);
    }

    // Show completed turn separator if the last turn just completed
    for (const turn of sessionTurns) {
      if (turn.turnNumber > lastTurnNumber && turn.endSeq != null) {
        result.push(<TurnSeparator key={`turn-${turn.turnNumber}`} turn={turn} />);
        lastTurnNumber = turn.turnNumber;
      }
    }

    return result;
  }, [sessionMessages, sessionTurns, activeSessionId]);

  if (sessionMessages.length === 0 && !isResponding) {
    return (
      <div className="flex items-center justify-center h-full text-slate-500">
        <p>Send a message to start the execution</p>
      </div>
    );
  }

  return (
    <div className="px-4 py-4 space-y-0.5 font-mono text-sm">
      {elements}

      {isResponding && (
        <div className="flex items-center gap-2 px-3 py-1.5 text-slate-500 text-xs">
          <span className="inline-block w-2 h-2 bg-green-500 rounded-full animate-pulse" />
          Executing...
        </div>
      )}

      <div ref={bottomRef} />
    </div>
  );
}

const TurnSeparator = memo(function TurnSeparator({ turn }: { turn: Turn }) {
  const isComplete = turn.endSeq != null;
  const exitOk = turn.exitCode === 0 || turn.exitCode == null;

  return (
    <div className="flex items-center gap-3 py-2 select-none">
      <div className="flex-1 border-t border-slate-700/50" />
      <span className="text-xs text-slate-600">Turn {turn.turnNumber}</span>
      {isComplete && turn.exitCode != null && (
        <span className={`text-xs font-medium px-1.5 py-0.5 rounded ${exitOk ? "bg-green-900/50 text-green-400" : "bg-red-900/50 text-red-400"}`}>
          Exit {turn.exitCode}
        </span>
      )}
      {isComplete && turn.elapsedMs != null && (
        <span className="text-xs text-slate-600">{formatElapsed(turn.elapsedMs)}</span>
      )}
      <div className="flex-1 border-t border-slate-700/50" />
    </div>
  );
});

const LogEntry = memo(function LogEntry({ msg, sessionId }: { msg: StoredMessage; sessionId: string }) {
  if (msg.channel === "system") {
    return (
      <div className="text-center py-1">
        <span className="text-xs text-slate-500 italic">{msg.content}</span>
      </div>
    );
  }

  const isUser = msg.direction === "user";
  const isStderr = msg.channel === "stderr";

  return (
    <div className={`group flex items-start gap-2 px-3 py-1 rounded hover:bg-slate-800/50 ${isStderr ? "border-l-2 border-red-500/70" : isUser ? "border-l-2 border-teal-500/70" : "border-l-2 border-transparent"}`}>
      {/* Prefix */}
      <span className="flex-shrink-0 w-5 text-slate-600 select-none">
        {isUser ? "$" : isStderr ? "!" : " "}
      </span>

      {/* Content */}
      <div className={`flex-1 min-w-0 ${isStderr ? "text-red-400" : isUser ? "text-teal-300" : "text-slate-200"}`}>
        <MessageContent content={msg.content} direction={msg.direction} channel={msg.channel} sessionId={sessionId} />
      </div>

      {/* Timestamp + seq */}
      <div className="flex-shrink-0 flex items-center gap-2 opacity-0 group-hover:opacity-100 transition-opacity">
        {msg.seq > 0 && <span className="text-xs text-slate-700">#{msg.seq}</span>}
        <span className="text-xs text-slate-600">{formatTimestamp(msg.created_at)}</span>
      </div>
    </div>
  );
});

function MessageContent({
  content,
  direction,
  channel,
  sessionId,
}: {
  content: string;
  direction: "user" | "agent";
  channel: string;
  sessionId: string;
}) {
  const type = detectContentType(content, direction, channel);

  switch (type) {
    case "file":
      return <FileRenderer content={content} sessionId={sessionId} direction={direction} />;

    case "ansi":
      return (
        <CollapsibleOutput content={content}>
          <AnsiRenderer content={content} />
        </CollapsibleOutput>
      );

    case "diff":
      return (
        <CollapsibleOutput content={content}>
          <DiffRenderer content={content} />
        </CollapsibleOutput>
      );

    case "json":
      return <JsonRenderer content={content} />;

    case "markdown":
      return (
        <CollapsibleOutput content={content}>
          <MarkdownRenderer content={content} />
        </CollapsibleOutput>
      );

    case "plain":
    default:
      return <PlainText content={content} />;
  }
}

function PlainText({ content }: { content: string }) {
  return (
    <>
      {content.split("\n").map((line, j, arr) => (
        <span key={j}>
          <Linkify text={line} />
          {j < arr.length - 1 && <br />}
        </span>
      ))}
    </>
  );
}

const URL_REGEX = /https?:\/\/[^\s<>)"']+/g;

function Linkify({ text }: { text: string }) {
  const parts = text.split(URL_REGEX);
  const matches = text.match(URL_REGEX) || [];

  return (
    <>
      {parts.map((part, i) => (
        <span key={i}>
          {part}
          {matches[i] && (
            <a
              href={matches[i]}
              target="_blank"
              rel="noopener noreferrer"
              className="text-teal-400 hover:text-teal-300 underline"
            >
              {matches[i]}
            </a>
          )}
        </span>
      ))}
    </>
  );
}
