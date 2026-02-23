import type { FileMetadata } from "@/types";
import { api } from "@/api/client";

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function getFileIcon(mimeType: string): string {
  if (mimeType.startsWith("image/")) return "\u{1F5BC}";
  if (mimeType.startsWith("text/")) return "\u{1F4C4}";
  if (mimeType.includes("pdf")) return "\u{1F4D1}";
  if (mimeType.includes("zip") || mimeType.includes("tar") || mimeType.includes("gzip")) return "\u{1F4E6}";
  if (mimeType.includes("json") || mimeType.includes("xml")) return "\u{1F4CB}";
  return "\u{1F4CE}";
}

export function FileRenderer({
  content,
  sessionId,
  direction,
}: {
  content: string;
  sessionId: string;
  direction: "user" | "agent";
}) {
  let meta: FileMetadata;
  try {
    meta = JSON.parse(content);
  } catch {
    return <span className="text-red-400">Invalid file metadata</span>;
  }

  const isUpload = direction === "user" || meta.direction === "upload";
  const isImage = meta.mime_type.startsWith("image/");
  const downloadUrl = api.getFileUrl(meta.file_id, sessionId);

  return (
    <div className="inline-flex items-center gap-3 px-3 py-2 rounded-lg bg-slate-700/50 border border-slate-600/50 max-w-sm">
      <span className="text-lg flex-shrink-0">{getFileIcon(meta.mime_type)}</span>
      <div className="flex-1 min-w-0">
        <div className="text-sm font-medium text-slate-200 truncate">{meta.name}</div>
        <div className="text-xs text-slate-400">
          {formatSize(meta.size)}
          {isUpload ? " \u00B7 uploaded" : " \u00B7 from agent"}
        </div>
      </div>
      {!isUpload && (
        <a
          href={downloadUrl}
          className="flex-shrink-0 p-1.5 rounded hover:bg-slate-600 text-teal-400 hover:text-teal-300 transition-colors"
          title="Download"
        >
          <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20" fill="currentColor" className="w-4 h-4">
            <path d="M10.75 2.75a.75.75 0 0 0-1.5 0v8.614L6.295 8.235a.75.75 0 1 0-1.09 1.03l4.25 4.5a.75.75 0 0 0 1.09 0l4.25-4.5a.75.75 0 0 0-1.09-1.03l-2.955 3.129V2.75Z" />
            <path d="M3.5 12.75a.75.75 0 0 0-1.5 0v2.5A2.75 2.75 0 0 0 4.75 18h10.5A2.75 2.75 0 0 0 18 15.25v-2.5a.75.75 0 0 0-1.5 0v2.5c0 .69-.56 1.25-1.25 1.25H4.75c-.69 0-1.25-.56-1.25-1.25v-2.5Z" />
          </svg>
        </a>
      )}
      {isImage && (
        <a
          href={downloadUrl}
          target="_blank"
          rel="noopener noreferrer"
          className="block mt-1"
        >
          <img
            src={downloadUrl}
            alt={meta.name}
            className="max-w-xs max-h-48 rounded border border-slate-600"
            loading="lazy"
          />
        </a>
      )}
    </div>
  );
}
