import { useState, type ReactNode } from "react";

const COLLAPSE_THRESHOLD = 30;

export function CollapsibleOutput({
  content,
  children,
}: {
  content: string;
  children: ReactNode;
}) {
  const [expanded, setExpanded] = useState(false);
  const lineCount = content.split("\n").length;

  if (lineCount <= COLLAPSE_THRESHOLD) {
    return <>{children}</>;
  }

  return (
    <div className="relative">
      <div
        className={
          expanded ? "" : "max-h-[20rem] overflow-hidden"
        }
      >
        {children}
      </div>
      {!expanded && (
        <div className="absolute bottom-0 left-0 right-0 h-16 bg-gradient-to-t from-slate-800 to-transparent" />
      )}
      <button
        onClick={() => setExpanded(!expanded)}
        className="mt-1 text-xs text-teal-400 hover:text-teal-300"
      >
        {expanded
          ? "Collapse"
          : `Show all ${lineCount} lines`}
      </button>
    </div>
  );
}
