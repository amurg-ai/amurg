import Ansi from "ansi-to-react";

export function AnsiRenderer({ content }: { content: string }) {
  return (
    <pre className="overflow-x-auto text-sm font-mono whitespace-pre-wrap">
      <Ansi>{content}</Ansi>
    </pre>
  );
}
