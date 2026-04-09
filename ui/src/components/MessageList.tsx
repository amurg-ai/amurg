import { useEffect, useRef, memo, useMemo, useState } from "react";
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
  ToolCallRenderer,
  QuestionRenderer,
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

function isHistoryChannel(channel: string): boolean {
  return channel === "history_user" || channel === "history_assistant" || channel === "history_tool";
}

function ToolCallGroup({ messages, sessionId }: { messages: StoredMessage[]; sessionId: string }) {
  const [expanded, setExpanded] = useState(false);

  const toolNames = useMemo(() => {
    const names: string[] = [];
    for (const msg of messages) {
      try {
        const data = JSON.parse(msg.content);
        if (data.type === "tool_use" && data.name) {
          names.push(data.name);
        }
      } catch { /* skip */ }
    }
    return names;
  }, [messages]);

  const MAX_SHOWN = 3;
  const shown = toolNames.slice(0, MAX_SHOWN);
  const remaining = toolNames.length - MAX_SHOWN;
  const summary = shown.join(", ") + (remaining > 0 ? ` +${remaining} more` : "");

  return (
    <div className="my-0.5">
      <button
        onClick={() => setExpanded(!expanded)}
        className="flex items-center gap-2 w-full text-left px-3 py-1.5 rounded
                   bg-slate-800/30 border border-slate-700/30 hover:border-slate-600/50
                   transition-colors text-xs font-mono"
      >
        <span className={`text-slate-500 transition-transform duration-150 ${expanded ? "rotate-90" : ""}`}>
          &#9654;
        </span>
        <span className="text-slate-400">
          <span className="text-blue-400">Tools:</span> {summary}
        </span>
        <span className="ml-auto text-slate-600">
          {toolNames.length} call{toolNames.length !== 1 ? "s" : ""}
        </span>
      </button>

      {expanded && (
        <div className="pl-4 border-l border-slate-700/30 ml-3 mt-1 space-y-0.5">
          {messages.map(msg => (
            <LogEntry key={msg.id} msg={msg} sessionId={sessionId} />
          ))}
        </div>
      )}
    </div>
  );
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

  // Split messages into history and live
  const { historyMessages, liveMessages } = useMemo(() => {
    const history: StoredMessage[] = [];
    const live: StoredMessage[] = [];
    for (const msg of sessionMessages) {
      if (isHistoryChannel(msg.channel)) {
        history.push(msg);
      } else {
        live.push(msg);
      }
    }
    return { historyMessages: history, liveMessages: live };
  }, [sessionMessages]);

  // Group messages with turn separators, collapsing consecutive tool messages
  const elements = useMemo(() => {
    const result: React.ReactNode[] = [];
    let lastTurnNumber = 0;
    let toolBuffer: StoredMessage[] = [];
    const sid = activeSessionId || "";

    const flushTools = () => {
      if (toolBuffer.length === 0) return;
      if (toolBuffer.length < 3) {
        for (const msg of toolBuffer) {
          result.push(<LogEntry key={msg.id} msg={msg} sessionId={sid} />);
        }
      } else {
        result.push(
          <ToolCallGroup key={`toolgroup-${toolBuffer[0].id}`} messages={[...toolBuffer]} sessionId={sid} />
        );
      }
      toolBuffer = [];
    };

    for (const msg of liveMessages) {
      // Check if this message starts a new turn
      for (const turn of sessionTurns) {
        if (turn.turnNumber > lastTurnNumber && msg.seq > turn.startSeq) {
          if (turn.turnNumber > 0) {
            flushTools();
            result.push(<TurnSeparator key={`turn-${turn.turnNumber}`} turn={turn} />);
            lastTurnNumber = turn.turnNumber;
          }
          break;
        }
      }

      const isToolMsg = msg.channel === "tool" || msg.channel === "history_tool";
      if (isToolMsg) {
        toolBuffer.push(msg);
      } else {
        flushTools();
        result.push(<LogEntry key={msg.id} msg={msg} sessionId={sid} />);
      }
    }

    flushTools();

    // Show completed turn separator if the last turn just completed
    for (const turn of sessionTurns) {
      if (turn.turnNumber > lastTurnNumber && turn.endSeq != null) {
        result.push(<TurnSeparator key={`turn-${turn.turnNumber}`} turn={turn} />);
        lastTurnNumber = turn.turnNumber;
      }
    }

    return result;
  }, [liveMessages, sessionTurns, activeSessionId]);

  if (sessionMessages.length === 0 && !isResponding) {
    return (
      <div className="flex items-center justify-center h-full text-slate-500">
        <div className="text-center animate-fade-in">
          <svg className="w-10 h-10 mx-auto mb-3 text-slate-600" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5}
              d="M8 9l3 3-3 3m5 0h3M5 20h14a2 2 0 002-2V6a2 2 0 00-2-2H5a2 2 0 00-2 2v12a2 2 0 002 2z" />
          </svg>
          <p className="text-sm text-slate-400">Send a message to start the execution</p>
        </div>
      </div>
    );
  }

  return (
    <div className="px-4 py-4 space-y-0.5 font-mono text-sm">
      {/* History section (from resumed native sessions) */}
      {historyMessages.length > 0 && (
        <HistorySection messages={historyMessages} />
      )}

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

// Renders pre-loaded history from a resumed native session
function HistorySection({ messages }: { messages: StoredMessage[] }) {
  const [expanded, setExpanded] = useState(false);

  // Group into conversation turns: user msg + assistant response + tools
  const turns = useMemo(() => {
    const result: { user?: StoredMessage; assistant: StoredMessage[]; tools: StoredMessage[] }[] = [];
    let current: { user?: StoredMessage; assistant: StoredMessage[]; tools: StoredMessage[] } = {
      assistant: [],
      tools: [],
    };

    for (const msg of messages) {
      if (msg.channel === "history_user") {
        if (current.user || current.assistant.length > 0 || current.tools.length > 0) {
          result.push(current);
        }
        current = { user: msg, assistant: [], tools: [] };
      } else if (msg.channel === "history_assistant") {
        current.assistant.push(msg);
      } else if (msg.channel === "history_tool") {
        current.tools.push(msg);
      }
    }
    if (current.user || current.assistant.length > 0 || current.tools.length > 0) {
      result.push(current);
    }
    return result;
  }, [messages]);

  if (turns.length === 0) return null;

  return (
    <div className="mb-4">
      <button
        onClick={() => setExpanded(!expanded)}
        className="flex items-center gap-2 w-full text-left px-3 py-2 rounded-lg
                   bg-purple-500/5 border border-purple-500/10 hover:border-purple-500/20
                   transition-colors mb-1"
      >
        <span className={`text-xs text-purple-400/60 transition-transform duration-150 ${expanded ? "rotate-90" : ""}`}>
          &#9654;
        </span>
        <span className="text-xs font-sans text-purple-400/80 font-medium">
          Previous session history
        </span>
        <span className="text-[11px] font-sans text-slate-600">
          {turns.length} turn{turns.length !== 1 ? "s" : ""}
        </span>
      </button>

      {expanded && (
        <div className="pl-2 border-l border-purple-500/10 ml-3 space-y-2 opacity-70">
          {turns.map((turn, i) => (
            <div key={i} className="space-y-0.5">
              {turn.user && (
                <div className="flex items-start gap-2 px-3 py-0.5">
                  <span className="text-teal-400 flex-shrink-0 w-5 select-none">$</span>
                  <span className="text-teal-300 text-xs">{turn.user.content}</span>
                </div>
              )}
              {turn.assistant.map((msg) => (
                <div key={msg.id} className="flex items-start gap-2 px-3 py-0.5">
                  <span className="text-slate-600 flex-shrink-0 w-5 select-none">&nbsp;</span>
                  <div className="text-slate-400 text-xs">
                    <MarkdownRenderer content={msg.content} />
                  </div>
                </div>
              ))}
              {turn.tools.length > 0 && (
                <div className="px-3 py-0.5">
                  <span className="text-[11px] text-slate-600">
                    {turn.tools.length} tool call{turn.tools.length !== 1 ? "s" : ""}
                  </span>
                </div>
              )}
            </div>
          ))}
        </div>
      )}
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

    case "question":
      return <QuestionRenderer content={content} sessionId={sessionId} />;

    case "tool":
      return <ToolCallRenderer content={content} />;

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
