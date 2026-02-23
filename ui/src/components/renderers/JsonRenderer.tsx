import hljs from "./hljs-setup";
import { CopyButton } from "./CopyButton";

export function JsonRenderer({ content }: { content: string }) {
  let formatted: string;
  try {
    formatted = JSON.stringify(JSON.parse(content), null, 2);
  } catch {
    formatted = content;
  }

  const highlighted = hljs.highlight(formatted, { language: "json" });

  return (
    <div className="relative group my-2">
      <pre className="overflow-x-auto rounded-lg bg-slate-900 p-3 text-sm">
        <code
          className="hljs language-json"
          // Safe: highlight.js output is generated from deterministic grammar-based
          // parsing — it only produces <span class="hljs-*"> tags, never executable content.
          dangerouslySetInnerHTML={{ __html: highlighted.value }}
        />
      </pre>
      <CopyButton text={formatted} />
    </div>
  );
}
