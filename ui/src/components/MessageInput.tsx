import { useState, useRef, useEffect, useCallback } from "react";
import { useSessionStore } from "@/stores/sessionStore";
import { VoiceInput } from "@/components/VoiceInput";

export function MessageInput() {
  const [text, setText] = useState("");
  const [multiline, setMultiline] = useState(false);
  const [interimText, setInterimText] = useState("");
  const [uploading, setUploading] = useState(false);
  const [dragOver, setDragOver] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const { sendMessage, uploadFile, activeSessionId, responding, addToast } =
    useSessionStore();

  const isResponding = activeSessionId ? responding.has(activeSessionId) : false;

  // Auto-resize textarea in multiline mode
  useEffect(() => {
    const el = textareaRef.current;
    if (el && multiline) {
      el.style.height = "auto";
      el.style.height = Math.min(el.scrollHeight, 200) + "px";
    }
  }, [text, multiline]);

  const handleSubmit = () => {
    const trimmed = text.trim();
    if (!trimmed || isResponding) return;

    sendMessage(trimmed);
    setText("");
    setMultiline(false);

    // Re-focus single-line input after submit
    setTimeout(() => inputRef.current?.focus(), 0);
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (multiline) {
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
      await uploadFile(activeSessionId, file);
    } catch (err) {
      addToast(err instanceof Error ? err.message : "Upload failed", "error");
    } finally {
      setUploading(false);
    }
  }, [activeSessionId, uploadFile, addToast]);

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
    if (multiline) {
      textareaRef.current?.focus();
    } else {
      inputRef.current?.focus();
    }

    setVoiceFlash(true);
    setTimeout(() => setVoiceFlash(false), 1500);
  };

  return (
    <div
      className={`border-t border-slate-700 bg-slate-800/50 px-4 py-2 ${dragOver ? "ring-2 ring-teal-500/50 bg-teal-900/10" : ""}`}
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
          disabled={!activeSessionId || uploading}
          className="flex-shrink-0 pt-2 text-slate-500 hover:text-slate-300 disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
          title="Attach file"
        >
          <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20" fill="currentColor" className="w-4 h-4">
            <path fillRule="evenodd" d="M15.621 4.379a3 3 0 0 0-4.242 0l-7 7a3 3 0 0 0 4.241 4.243h.001l.497-.5a.75.75 0 0 1 1.064 1.057l-.498.501a4.5 4.5 0 0 1-6.364-6.364l7-7a4.5 4.5 0 0 1 6.368 6.36l-3.455 3.553A2.625 2.625 0 1 1 9.52 9.52l3.45-3.451a.75.75 0 1 1 1.061 1.06l-3.45 3.451a1.125 1.125 0 0 0 1.587 1.595l3.454-3.553a3 3 0 0 0 0-4.242Z" clipRule="evenodd" />
          </svg>
        </button>

        {/* Input area */}
        <div className="flex-1 min-w-0">
          {multiline ? (
            <div>
              <textarea
                ref={textareaRef}
                value={text}
                onChange={(e) => setText(e.target.value)}
                onKeyDown={handleKeyDown}
                placeholder={isResponding ? "Waiting..." : ""}
                disabled={isResponding}
                rows={3}
                className={`w-full px-3 py-2 bg-slate-700/50 border rounded-lg
                           text-slate-100 placeholder-slate-500 resize-none font-mono text-sm
                           focus:outline-none focus:ring-1 focus:ring-teal-500 focus:border-transparent
                           disabled:opacity-50 disabled:cursor-not-allowed
                           ${voiceFlash ? "border-green-400 ring-1 ring-green-400/50" : "border-slate-600"}`}
                autoFocus
              />
              <span className="text-xs text-slate-600 mt-1 block">Ctrl+Enter to send</span>
            </div>
          ) : (
            <input
              ref={inputRef}
              type="text"
              value={text}
              onChange={(e) => setText(e.target.value)}
              onKeyDown={handleKeyDown}
              placeholder={isResponding ? "Waiting..." : "Enter command... (or drop a file)"}
              disabled={isResponding}
              className={`w-full px-3 py-2 bg-transparent border-none
                         text-slate-100 placeholder-slate-500 font-mono text-sm
                         focus:outline-none
                         disabled:opacity-50 disabled:cursor-not-allowed
                         ${voiceFlash ? "ring-1 ring-green-400/50" : ""}`}
            />
          )}
        </div>

        {/* Voice input */}
        <div className="flex-shrink-0 pt-1">
          <VoiceInput
            onResult={handleVoiceResult}
            onInterim={setInterimText}
            onError={(msg) => addToast(msg, "error")}
            disabled={isResponding}
          />
        </div>
      </div>
    </div>
  );
}
