import { useState, useRef, useEffect, useCallback } from "react";
import { useSessionStore } from "@/stores/sessionStore";
import { VoiceInput } from "@/components/VoiceInput";

interface MessageInputProps {
  onSend?: (content: string) => boolean;
  onUpload?: (file: File) => Promise<string>;
  disabled?: boolean;
}

export function MessageInput({ onSend, onUpload, disabled = false }: MessageInputProps = {}) {
  const [text, setText] = useState("");
  const [attachments, setAttachments] = useState<{ name: string; path: string }[]>([]);
  const [multiline, setMultiline] = useState(false);
  const editingMultiline = multiline || Boolean(onSend);
  const [interimText, setInterimText] = useState("");
  const [uploading, setUploading] = useState(false);
  const [dragOver, setDragOver] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const { sendMessage, uploadFile, activeSessionId, responding, addToast, canSendInteractiveInput } =
    useSessionStore();

  const isResponding = activeSessionId ? responding.has(activeSessionId) : false;
  const canReplyWhileResponding = activeSessionId
    ? isResponding && canSendInteractiveInput(activeSessionId)
    : false;
  const inputLocked = disabled || (!onSend && isResponding && !canReplyWhileResponding);

  // Auto-resize textarea in multiline mode
  useEffect(() => {
    const el = textareaRef.current;
    if (el && editingMultiline) {
      el.style.height = "auto";
      el.style.height = Math.min(el.scrollHeight, 200) + "px";
    }
  }, [text, editingMultiline]);

  const handleSubmit = () => {
    const trimmed = text.trim();
    if ((!trimmed && attachments.length === 0) || inputLocked || uploading) return;
    const content = [onSend ? text : trimmed, attachments.length ? "Attached files:\n" + attachments.map((a) => a.path).join("\n") : ""].filter(Boolean).join("\n\n");
    if (onSend) {
      if (!onSend(content)) return; // Keep the draft if disconnected.
    } else sendMessage(content);
    setAttachments([]);
    setText("");
    setMultiline(false);

    // Keep the composer focused for the next prompt.
    setTimeout(() => (onSend ? textareaRef.current : inputRef.current)?.focus(), 0);
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (editingMultiline) {
      if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
        e.preventDefault();
        handleSubmit();
      }
    } else {
      if (e.key === "Enter" && e.shiftKey) {
        e.preventDefault();
        setMultiline(true);
        setText(text + "\n");
      } else if (e.key === "Enter") {
        e.preventDefault();
        handleSubmit();
      }
    }
  };

  const handleFileUpload = useCallback(async (file: File) => {
    if (!activeSessionId) return;
    setUploading(true);
    try {
      if (onUpload) {
        const path = await onUpload(file);
        setAttachments((current) => [...current, { name: file.name, path }]);
      } else await uploadFile(activeSessionId, file);
    } catch (err) {
      addToast(err instanceof Error ? err.message : "Upload failed", "error");
    } finally {
      setUploading(false);
    }
  }, [activeSessionId, uploadFile, addToast, onUpload]);

  const handleFileSelect = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (file) handleFileUpload(file);
    // Reset so the same file can be selected again
    e.target.value = "";
  }, [handleFileUpload]);

  const handleDrop = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    setDragOver(false);
    const file = e.dataTransfer.files[0];
    if (file) handleFileUpload(file);
  }, [handleFileUpload]);

  const handleDragOver = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    setDragOver(true);
  }, []);

  const handleDragLeave = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    setDragOver(false);
  }, []);

  const [voiceFlash, setVoiceFlash] = useState(false);

  const handleVoiceResult = (transcript: string) => {
    setText((prev) => (prev ? prev + " " + transcript : transcript));
    if (editingMultiline) {
      textareaRef.current?.focus();
    } else {
      inputRef.current?.focus();
    }

    setVoiceFlash(true);
    setTimeout(() => setVoiceFlash(false), 1500);
  };

  return (
    <div
      className={`border-t border-slate-700 bg-slate-800/50 px-3 sm:px-4 py-3 ${dragOver ? "ring-2 ring-teal-500/50 bg-teal-900/10" : ""}`}
      onDrop={handleDrop}
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
    >
      {/* Hidden file input */}
      <input
        ref={fileInputRef}
        type="file"
        className="hidden"
        onChange={handleFileSelect}
      />

      {attachments.length > 0 && (
        <div className="flex flex-wrap gap-2 pb-2" aria-label="Attached files">
          {attachments.map((attachment, index) => (
            <span key={attachment.path} className="inline-flex items-center gap-2 rounded bg-slate-700 px-2 py-1 text-xs text-slate-200">
              {attachment.name}
              <button type="button" aria-label={`Remove ${attachment.name}`} onClick={() => setAttachments((items) => items.filter((_, i) => i !== index))}>×</button>
            </span>
          ))}
        </div>
      )}

      {/* Live transcription preview */}
      {interimText && (
        <div className="text-xs text-slate-500 italic px-7 pb-1 truncate">
          {interimText}
        </div>
      )}

      {uploading && (
        <div className="text-xs text-teal-400 px-7 pb-1 flex items-center gap-1">
          <span className="inline-block w-2 h-2 bg-teal-500 rounded-full animate-pulse" />
          Uploading file...
        </div>
      )}

      <div className="flex items-start gap-2">
        {/* $ prefix */}
        <span className="text-green-500 font-mono text-sm leading-9 select-none flex-shrink-0">$</span>

        {/* File upload button */}
        <button
          type="button"
          onClick={() => fileInputRef.current?.click()}
          disabled={!activeSessionId || uploading || disabled}
          className="flex-shrink-0 p-2 text-slate-500 hover:text-slate-300 disabled:opacity-30 disabled:cursor-not-allowed transition-colors rounded-lg"
          title="Attach file"
        >
          <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20" fill="currentColor" className="w-5 h-5">
            <path fillRule="evenodd" d="M15.621 4.379a3 3 0 0 0-4.242 0l-7 7a3 3 0 0 0 4.241 4.243h.001l.497-.5a.75.75 0 0 1 1.064 1.057l-.498.501a4.5 4.5 0 0 1-6.364-6.364l7-7a4.5 4.5 0 0 1 6.368 6.36l-3.455 3.553A2.625 2.625 0 1 1 9.52 9.52l3.45-3.451a.75.75 0 1 1 1.061 1.06l-3.45 3.451a1.125 1.125 0 0 0 1.587 1.595l3.454-3.553a3 3 0 0 0 0-4.242Z" clipRule="evenodd" />
          </svg>
        </button>

        {/* Input area */}
        <div className="flex-1 min-w-0">
          {editingMultiline ? (
            <div>
              <textarea
                ref={textareaRef}
                aria-label="Message"
                value={text}
                onChange={(e) => setText(e.target.value)}
                onKeyDown={handleKeyDown}
                placeholder={inputLocked ? "Waiting..." : onSend ? "Message the agent…" : (canReplyWhileResponding ? "Reply to the running prompt..." : "")}
                disabled={inputLocked}
                rows={onSend ? 1 : 3}
                className={`w-full px-3 py-2 bg-slate-700/50 border rounded-lg
                           text-slate-100 placeholder-slate-500 resize-none font-mono text-sm
                           focus:outline-none focus:ring-1 focus:ring-teal-500 focus:border-transparent
                           disabled:opacity-50 disabled:cursor-not-allowed
                           ${voiceFlash ? "border-green-400 ring-1 ring-green-400/50" : "border-slate-600"}`}
                autoFocus={!onSend}
              />
              {!onSend && <span className="text-xs text-slate-600 mt-1 block">Ctrl+Enter to send</span>}
            </div>
          ) : (
            <input
              ref={inputRef}
              aria-label="Message"
              type="text"
              value={text}
              onChange={(e) => setText(e.target.value)}
              onKeyDown={handleKeyDown}
              placeholder={inputLocked ? "Waiting..." : (canReplyWhileResponding ? "Reply to the running prompt..." : "Enter command... (or drop a file)")}
              disabled={inputLocked}
              className={`w-full px-3 py-2 bg-transparent border-none
                         text-slate-100 placeholder-slate-500 font-mono text-sm
                         focus:outline-none
                         disabled:opacity-50 disabled:cursor-not-allowed
                         ${voiceFlash ? "ring-1 ring-green-400/50" : ""}`}
            />
          )}
        </div>

        {onSend && <button type="button" onClick={handleSubmit} disabled={inputLocked || uploading || (!text.trim() && attachments.length === 0)} className="rounded bg-teal-600 px-3 py-2 text-sm text-white disabled:opacity-40">Send</button>}

        {/* Voice input */}
        <div className="flex-shrink-0 pt-1">
          <VoiceInput
            onResult={handleVoiceResult}
            onInterim={setInterimText}
            onError={(msg) => addToast(msg, "error")}
            disabled={inputLocked}
          />
        </div>
      </div>
    </div>
  );
}
