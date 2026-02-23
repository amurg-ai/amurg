export type ContentType = "ansi" | "diff" | "json" | "markdown" | "plain" | "file";

const ANSI_RE = /\x1b\[[\d;]*m/;

const DIFF_RE = /^(---|\+\+\+|@@\s)/m;

export function detectContentType(
  content: string,
  direction: "user" | "agent",
  channel: string,
): ContentType {
  // User messages are always plain text.
  if (direction === "user") return "plain";

  // File channel renders as file card.
  if (channel === "file") return "file";

  // System channel is always plain.
  if (channel === "system") return "plain";

  // ANSI escape codes -> terminal renderer.
  if (ANSI_RE.test(content)) return "ansi";

  // Unified diff format.
  if (DIFF_RE.test(content)) return "diff";

  // JSON object or array.
  const trimmed = content.trimStart();
  if (trimmed.startsWith("{") || trimmed.startsWith("[")) {
    try {
      JSON.parse(trimmed);
      return "json";
    } catch {
      // Not valid JSON, fall through.
    }
  }

  // Agent stdout gets markdown rendering.
  if (channel === "stdout" || channel === "") return "markdown";

  // Stderr without ANSI is plain text.
  return "plain";
}
