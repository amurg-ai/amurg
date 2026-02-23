import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import rehypeSanitize from "rehype-sanitize";
import rehypeHighlight from "rehype-highlight";
import { CopyButton } from "./CopyButton";

// Import tree-shaken hljs registration.
import "./hljs-setup";

export function MarkdownRenderer({ content }: { content: string }) {
  return (
    <div className="markdown-content">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        rehypePlugins={[rehypeSanitize, rehypeHighlight]}
        children={content}
        components={{
          pre({ children }) {
            // Extract code text for copy button.
            const codeEl = children as React.ReactElement<{ children?: string }>;
            let codeText = "";
            if (codeEl && typeof codeEl === "object" && "props" in codeEl) {
              codeText = String(codeEl.props.children || "");
            }
            return (
              <div className="relative group my-2">
                <pre className="overflow-x-auto rounded-lg bg-slate-900 p-3 text-sm">
                  {children}
                </pre>
                <CopyButton text={codeText} />
              </div>
            );
          },
          a({ href, children }) {
            return (
              <a
                href={href}
                target="_blank"
                rel="noopener noreferrer"
                className="text-teal-400 hover:text-teal-300 underline"
              >
                {children}
              </a>
            );
          },
        }}
      />
    </div>
  );
}
