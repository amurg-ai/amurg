import hljs from "./hljs-setup";
import { CopyButton } from "./CopyButton";

export function DiffRenderer({ content }: { content: string }) {
  const highlighted = hljs.highlight(content, { language: "diff" });

  return (
    <div className="relative group my-2">
      <pre className="overflow-x-auto rounded-lg bg-slate-900 p-3 text-sm">
        <code
          className="hljs language-diff"
          dangerouslySetInnerHTML={{ __html: highlighted.value }}
        />
      </pre>
      <CopyButton text={content} />
    </div>
  );
}
