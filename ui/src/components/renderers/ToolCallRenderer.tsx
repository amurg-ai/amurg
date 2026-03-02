import { useState } from "react";

/**
 * Renders a tool_use or tool_result as a collapsible card.
 */
export function ToolCallRenderer({ content }: { content: string }) {
  const [expanded, setExpanded] = useState(false);

  let data: {
    type: string;
    name?: string;
    id?: string;
    tool_use_id?: string;
    input?: unknown;
    content?: string;
    is_error?: boolean;
  };
  try {
    data = JSON.parse(content);
  } catch {
    return <pre className="text-xs text-slate-400">{content}</pre>;
  }

  const isResult = data.type === "tool_result";
  const isError = data.is_error === true;
  const name = data.name || data.tool_use_id || "tool";

  // Format the detail content
  let detail = "";
  if (isResult) {
    detail = typeof data.content === "string" ? data.content : JSON.stringify(data.content, null, 2);
  } else {
    detail = typeof data.input === "string" ? data.input : JSON.stringify(data.input, null, 2);
  }

  // Truncate detail for collapsed view
  const previewLen = 120;
  const preview = detail.length > previewLen
    ? detail.slice(0, previewLen) + "..."
    : detail;

  return (
    <div
      className={`my-1 rounded border text-xs font-mono ${
        isError
          ? "border-red-800/50 bg-red-950/20"
          : isResult
            ? "border-slate-700/50 bg-slate-900/30"
            : "border-slate-700/50 bg-slate-800/30"
      }`}
    >
      <button
        onClick={() => setExpanded(!expanded)}
        className="w-full flex items-center gap-2 px-3 py-1.5 text-left hover:bg-slate-800/50 transition-colors"
      >
        {/* Icon */}
        <span className={`flex-shrink-0 ${isError ? "text-red-400" : isResult ? "text-slate-500" : "text-blue-400"}`}>
          {isResult ? (isError ? "!" : "\u2190") : "\u2192"}
        </span>

        {/* Label */}
        <span className={`flex-shrink-0 font-medium ${isError ? "text-red-300" : "text-slate-300"}`}>
          {isResult ? "result" : name}
        </span>

        {/* Preview */}
        {!expanded && preview && (
          <span className="flex-1 truncate text-slate-500">
            {preview}
          </span>
        )}

        {/* Expand indicator */}
        <span className="flex-shrink-0 text-slate-600">
          {expanded ? "\u25BC" : "\u25B6"}
        </span>
      </button>

      {expanded && detail && (
        <pre className="px-3 py-2 border-t border-slate-700/30 overflow-x-auto text-slate-400 whitespace-pre-wrap break-all max-h-64 overflow-y-auto">
          {detail}
        </pre>
      )}
    </div>
  );
}
