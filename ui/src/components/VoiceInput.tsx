import { useState, useRef, useCallback, useEffect } from "react";

interface VoiceInputProps {
  onResult: (transcript: string) => void;
  onInterim?: (transcript: string) => void;
  onError?: (message: string) => void;
  disabled?: boolean;
}

// --- Voice config (persisted in localStorage) ---

interface VoiceConfig {
  mode: "browser" | "whisper";
  whisperUrl: string;
}

function loadConfig(): VoiceConfig {
  try {
    const s = localStorage.getItem("amurg-voice");
    if (s) {
      const p = JSON.parse(s);
      return { mode: p.mode || "browser", whisperUrl: p.whisperUrl || "" };
    }
  } catch {
    /* ignore */
  }
  return { mode: "browser", whisperUrl: "" };
}

function saveConfig(c: VoiceConfig) {
  localStorage.setItem("amurg-voice", JSON.stringify(c));
}

// --- Browser speech recognition support ---

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type SpeechRecognitionInstance = any;
type SpeechRecognitionCtor = new () => SpeechRecognitionInstance;

function getSR(): SpeechRecognitionCtor | null {
  if (typeof window === "undefined") return null;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const w = window as any;
  return w.SpeechRecognition || w.webkitSpeechRecognition || null;
}

// --- Component ---

export function VoiceInput({
  onResult,
  onInterim,
  onError,
  disabled,
}: VoiceInputProps) {
  const [recording, setRecording] = useState(false);
  const [audioLevel, setAudioLevel] = useState(0);
  const [showSettings, setShowSettings] = useState(false);
  const [config, setConfigState] = useState<VoiceConfig>(loadConfig);

  // Refs — avoids stale closures in async callbacks.
  const recRef = useRef(false);
  const srRef = useRef<SpeechRecognitionInstance | null>(null);
  const streamRef = useRef<MediaStream | null>(null);
  const ctxRef = useRef<AudioContext | null>(null);
  const rafRef = useRef(0);
  const wsRef = useRef<WebSocket | null>(null);
  const mrRef = useRef<MediaRecorder | null>(null);
  const holdRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const isHoldRef = useRef(false);
  const interimRef = useRef("");
  const settingsRef = useRef<HTMLDivElement | null>(null);

  const SR = getSR();
  const canBrowser = !!SR;
  const hasMic =
    typeof navigator !== "undefined" &&
    !!navigator.mediaDevices?.getUserMedia;

  useEffect(() => {
    recRef.current = recording;
  }, [recording]);

  const setRec = useCallback(
    (v: boolean) => setRecording(v),
    [],
  );

  // ── Audio level monitor ──────────────────────────────────

  const startMonitor = useCallback((stream: MediaStream) => {
    try {
      const ctx = new AudioContext();
      ctxRef.current = ctx;
      const analyser = ctx.createAnalyser();
      analyser.fftSize = 256;
      analyser.smoothingTimeConstant = 0.7;
      ctx.createMediaStreamSource(stream).connect(analyser);
      const buf = new Uint8Array(analyser.frequencyBinCount);
      const tick = () => {
        analyser.getByteFrequencyData(buf);
        let s = 0;
        for (let i = 0; i < buf.length; i++) s += buf[i] * buf[i];
        setAudioLevel(Math.sqrt(s / buf.length) / 255);
        rafRef.current = requestAnimationFrame(tick);
      };
      tick();
    } catch {
      /* AudioContext may fail */
    }
  }, []);

  const stopMonitor = useCallback(() => {
    if (rafRef.current) cancelAnimationFrame(rafRef.current);
    rafRef.current = 0;
    ctxRef.current?.close().catch(() => {});
    ctxRef.current = null;
    streamRef.current?.getTracks().forEach((t) => t.stop());
    streamRef.current = null;
    setAudioLevel(0);
  }, []);

  // ── Stop recording ───────────────────────────────────────

  const stop = useCallback(() => {
    // Web Speech API
    srRef.current?.stop();
    srRef.current = null;

    // MediaRecorder
    if (mrRef.current && mrRef.current.state !== "inactive") {
      mrRef.current.stop();
    }
    mrRef.current = null;

    // Whisper WebSocket — delay close to receive final result.
    const ws = wsRef.current;
    if (ws) {
      wsRef.current = null;
      setTimeout(() => {
        // If interim text wasn't resolved by server, promote it.
        if (interimRef.current) {
          onResult(interimRef.current);
          interimRef.current = "";
          onInterim?.("");
        }
        ws.close();
      }, 800);
    } else {
      // Browser mode: promote remaining interim immediately.
      if (interimRef.current) {
        onResult(interimRef.current);
        interimRef.current = "";
        onInterim?.("");
      }
    }

    stopMonitor();
    setRec(false);
  }, [stopMonitor, setRec, onResult, onInterim]);

  // ── Start recording ──────────────────────────────────────

  const start = useCallback(async () => {
    if (recRef.current || disabled) return;
    setRec(true);
    setShowSettings(false);

    // Get microphone access for audio level monitoring (both modes).
    let stream: MediaStream;
    try {
      stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      streamRef.current = stream;
    } catch {
      onError?.("Microphone access denied");
      setRec(false);
      return;
    }

    startMonitor(stream);

    if (config.mode === "whisper" && config.whisperUrl) {
      // ── Whisper WebSocket mode ──
      let ws: WebSocket;
      try {
        ws = new WebSocket(config.whisperUrl);
      } catch {
        onError?.("Failed to connect to whisper server");
        stopMonitor();
        setRec(false);
        return;
      }
      wsRef.current = ws;

      ws.onmessage = (ev) => {
        try {
          const d = JSON.parse(ev.data);
          const text = (
            d.text ??
            d.transcript ??
            d.buffer ??
            d.segments
              ?.map((s: { text: string }) => s.text)
              .join(" ") ??
            ""
          )
            .toString()
            .trim();
          if (!text) return;

          const isFinal = d.is_final !== false && d.type !== "partial";
          if (isFinal) {
            interimRef.current = "";
            onResult(text);
            onInterim?.("");
          } else {
            interimRef.current = text;
            onInterim?.(text);
          }
        } catch {
          /* ignore bad JSON */
        }
      };

      ws.onopen = () => {
        if (!recRef.current) {
          ws.close();
          return;
        }
        const mime = MediaRecorder.isTypeSupported("audio/webm;codecs=opus")
          ? "audio/webm;codecs=opus"
          : "audio/webm";
        const mr = new MediaRecorder(stream, { mimeType: mime });
        mrRef.current = mr;
        mr.ondataavailable = (e) => {
          if (e.data.size > 0 && ws.readyState === WebSocket.OPEN) {
            ws.send(e.data);
          }
        };
        mr.start(250); // Send chunks every 250ms
      };

      ws.onerror = () => {
        onError?.("Whisper server connection failed");
        stop();
      };
    } else if (SR) {
      // ── Web Speech API mode ──
      const recognition = new SR();
      recognition.continuous = true;
      recognition.interimResults = true;
      recognition.lang = navigator.language || "en-US";

      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      recognition.onresult = (event: any) => {
        let interim = "";
        let final = "";
        for (let i = event.resultIndex; i < event.results.length; i++) {
          const t: string = event.results[i][0].transcript;
          if (event.results[i].isFinal) final += t;
          else interim += t;
        }
        if (final) {
          interimRef.current = "";
          onResult(final);
          onInterim?.("");
        }
        if (interim) {
          interimRef.current = interim;
          onInterim?.(interim);
        }
      };

      recognition.onend = () => {
        // Mobile browsers kill recognition after ~60s. Restart if still recording.
        if (recRef.current) {
          try {
            recognition.start();
          } catch {
            stop();
          }
        }
      };

      recognition.onerror = () => stop();
      srRef.current = recognition;
      recognition.start();
    } else {
      onError?.("No speech recognition available");
      stopMonitor();
      setRec(false);
    }
  }, [
    disabled,
    config,
    SR,
    startMonitor,
    stopMonitor,
    stop,
    setRec,
    onResult,
    onInterim,
    onError,
  ]);

  // ── Hold-to-talk / tap-to-toggle gesture ─────────────────

  const handlePointerDown = useCallback(
    (e: React.PointerEvent) => {
      e.preventDefault();
      (e.target as HTMLElement).setPointerCapture(e.pointerId);
      if (recording) return; // will stop on pointer up

      isHoldRef.current = false;
      holdRef.current = setTimeout(() => {
        isHoldRef.current = true;
        start();
      }, 200);
    },
    [recording, start],
  );

  const handlePointerUp = useCallback(() => {
    if (holdRef.current) {
      clearTimeout(holdRef.current);
      holdRef.current = null;
    }
    if (isHoldRef.current) {
      // End of hold-to-talk.
      stop();
      isHoldRef.current = false;
    } else if (recording) {
      // Tap while recording → stop.
      stop();
    } else {
      // Tap while not recording → start.
      start();
    }
  }, [recording, start, stop]);

  // ── Close settings on outside click ──────────────────────

  useEffect(() => {
    if (!showSettings) return;
    const handler = (e: MouseEvent) => {
      if (
        settingsRef.current &&
        !settingsRef.current.contains(e.target as Node)
      ) {
        setShowSettings(false);
      }
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [showSettings]);

  // ── Cleanup on unmount ───────────────────────────────────

  useEffect(
    () => () => {
      if (holdRef.current) clearTimeout(holdRef.current);
      srRef.current?.stop();
      if (mrRef.current?.state !== "inactive") mrRef.current?.stop();
      wsRef.current?.close();
      stopMonitor();
    },
    [stopMonitor],
  );

  // ── Config setter ────────────────────────────────────────

  const handleConfig = useCallback((u: Partial<VoiceConfig>) => {
    setConfigState((prev) => {
      const next = { ...prev, ...u };
      saveConfig(next);
      return next;
    });
  }, []);

  // Don't render if neither mode is possible.
  if (!canBrowser && !hasMic) return null;

  const ringScale = 1 + audioLevel * 0.6;

  return (
    <div className="relative" ref={settingsRef}>
      {/* Mic button */}
      <button
        onPointerDown={handlePointerDown}
        onPointerUp={handlePointerUp}
        onPointerCancel={handlePointerUp}
        disabled={disabled}
        className={`relative p-2.5 rounded-xl transition-colors flex-shrink-0 touch-none select-none
          disabled:opacity-50 disabled:cursor-not-allowed
          ${
            recording
              ? "bg-red-600 text-white"
              : "bg-slate-700 text-slate-400 hover:text-slate-200 hover:bg-slate-600"
          }`}
        title={recording ? "Tap or release to stop" : "Hold to talk, tap to toggle"}
      >
        {/* Audio level ring */}
        {recording && (
          <span
            className="absolute inset-[-3px] rounded-xl border-2 border-red-400 pointer-events-none"
            style={{
              transform: `scale(${ringScale})`,
              opacity: 0.3 + audioLevel * 0.7,
              transition: "transform 80ms, opacity 80ms",
            }}
          />
        )}
        <svg
          className="w-5 h-5 relative z-10"
          fill="none"
          viewBox="0 0 24 24"
          stroke="currentColor"
        >
          <path
            strokeLinecap="round"
            strokeLinejoin="round"
            strokeWidth={2}
            d="M19 11a7 7 0 01-7 7m0 0a7 7 0 01-7-7m7 7v4m0 0H8m4 0h4m-4-8a3 3 0 01-3-3V5a3 3 0 116 0v6a3 3 0 01-3 3z"
          />
        </svg>
      </button>

      {/* Settings gear */}
      <button
        onClick={(e) => {
          e.stopPropagation();
          setShowSettings(!showSettings);
        }}
        className="absolute -top-1 -right-1 w-4 h-4 rounded-full bg-slate-600 hover:bg-slate-500
                   flex items-center justify-center text-slate-400 hover:text-slate-200 z-20"
        title="Voice settings"
      >
        <svg
          className="w-2.5 h-2.5"
          fill="none"
          viewBox="0 0 24 24"
          stroke="currentColor"
          strokeWidth={2}
        >
          <path
            strokeLinecap="round"
            strokeLinejoin="round"
            d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.066 2.573c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.573 1.066c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.066-2.573c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z"
          />
          <path
            strokeLinecap="round"
            strokeLinejoin="round"
            d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"
          />
        </svg>
      </button>

      {/* Settings popover */}
      {showSettings && (
        <div className="absolute bottom-full right-0 mb-2 w-64 bg-slate-800 border border-slate-600 rounded-lg shadow-xl p-3 z-30">
          <div className="text-xs font-medium text-slate-300 mb-2">
            Voice Input
          </div>

          <label className="flex items-center gap-2 text-xs text-slate-400 mb-1.5 cursor-pointer">
            <input
              type="radio"
              name="stt-mode"
              checked={config.mode === "browser"}
              onChange={() => handleConfig({ mode: "browser" })}
              className="accent-teal-500"
            />
            Browser{!canBrowser && " (not supported)"}
          </label>

          <label className="flex items-center gap-2 text-xs text-slate-400 mb-1.5 cursor-pointer">
            <input
              type="radio"
              name="stt-mode"
              checked={config.mode === "whisper"}
              onChange={() => handleConfig({ mode: "whisper" })}
              className="accent-teal-500"
            />
            Local Whisper
          </label>

          {config.mode === "whisper" && (
            <input
              type="text"
              value={config.whisperUrl}
              onChange={(e) => handleConfig({ whisperUrl: e.target.value })}
              placeholder="ws://localhost:8000/asr"
              className="w-full mt-1 px-2 py-1 bg-slate-700 border border-slate-600 rounded
                         text-xs text-slate-200 placeholder-slate-500
                         focus:outline-none focus:border-teal-500"
            />
          )}
        </div>
      )}
    </div>
  );
}
