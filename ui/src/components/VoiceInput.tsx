import { useState, useRef, useCallback, useEffect } from "react";
import { tokenGetter } from "@/api/client";

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

interface ParsedWhisperMessage {
  error?: string;
  ignore?: boolean;
  text?: string;
}

function defaultWhisperUrl(): string {
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${location.host}/asr`;
}

function loadConfig(): VoiceConfig {
  try {
    const s = localStorage.getItem("amurg-voice");
    if (s) {
      const p = JSON.parse(s);
      return {
        mode: p.mode || "browser",
        whisperUrl: p.whisperUrl || defaultWhisperUrl(),
      };
    }
  } catch {
    /* ignore */
  }
  return { mode: "browser", whisperUrl: defaultWhisperUrl() };
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

function extractTranscriptEntry(entry: unknown): string {
  if (typeof entry === "string") return entry.trim();
  if (!entry || typeof entry !== "object") return "";
  const text = (entry as { text?: unknown }).text;
  return typeof text === "string" ? text.trim() : "";
}

function firstTranscriptField(payload: Record<string, unknown>, fields: string[]): string {
  for (const field of fields) {
    const value = payload[field];
    if (typeof value === "string" && value.trim()) return value.trim();
  }
  return "";
}

export function parseWhisperMessage(data: unknown): ParsedWhisperMessage {
  if (typeof data !== "string") return {};
  const raw = data.trim();
  if (!raw) return {};

  try {
    const payload = JSON.parse(raw);
    if (!payload || typeof payload !== "object") return { text: raw };

    const message = payload as Record<string, unknown>;
    if (typeof message.error === "string" && message.error.trim()) {
      return { error: message.error.trim() };
    }

    const messageType = typeof message.type === "string" ? message.type : "";
    if (messageType === "config" || messageType === "ready_to_stop") {
      return { ignore: true };
    }

    const lines = Array.isArray(message.lines)
      ? message.lines.map(extractTranscriptEntry).filter(Boolean).join(" ")
      : "";
    const segments = Array.isArray(message.segments)
      ? message.segments.map(extractTranscriptEntry).filter(Boolean).join(" ")
      : "";
    const partial = firstTranscriptField(message, [
      "buffer_transcription",
      "partial",
      "partial_transcript",
    ]);
    const direct = firstTranscriptField(message, ["text", "transcript"]);

    const text = [lines || segments, partial || direct].filter(Boolean).join(" ").trim()
      || direct;
    return text ? { text } : {};
  } catch {
    return { text: raw };
  }
}

// --- Legacy PCM fallback (for browsers without AudioWorklet) ---

function startPCMLegacy(
  audioCtx: AudioContext,
  source: MediaStreamAudioSourceNode,
  nativeRate: number,
  targetRate: number,
  ws: WebSocket,
) {
  // eslint-disable-next-line deprecation/deprecation
  const processor = audioCtx.createScriptProcessor(4096, 1, 1);
  processor.onaudioprocess = (e: AudioProcessingEvent) => {
    if (ws.readyState !== WebSocket.OPEN) return;
    const input = e.inputBuffer.getChannelData(0);
    let floats: Float32Array;
    if (nativeRate === targetRate) {
      floats = input;
    } else {
      const ratio = nativeRate / targetRate;
      const outLen = Math.floor(input.length / ratio);
      floats = new Float32Array(outLen);
      for (let i = 0; i < outLen; i++) {
        const srcIdx = i * ratio;
        const lo = Math.floor(srcIdx);
        const hi = Math.min(lo + 1, input.length - 1);
        const frac = srcIdx - lo;
        floats[i] = input[lo] * (1 - frac) + input[hi] * frac;
      }
    }
    const buf = new ArrayBuffer(floats.length * 2);
    const view = new DataView(buf);
    for (let i = 0; i < floats.length; i++) {
      const s = Math.max(-1, Math.min(1, floats[i]));
      view.setInt16(i * 2, s < 0 ? s * 0x8000 : s * 0x7fff, true);
    }
    ws.send(buf);
  };
  source.connect(processor);
  processor.connect(audioCtx.destination);
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
  const pcmCtxRef = useRef<AudioContext | null>(null);
  const closeRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const holdRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const isHoldRef = useRef(false);
  const browserStopPendingRef = useRef(false);
  const interimRef = useRef("");
  const settingsRef = useRef<HTMLDivElement | null>(null);

  const SR = getSR();
  const canBrowser = !!SR;
  const hasMic =
    typeof navigator !== "undefined" &&
    !!navigator.mediaDevices?.getUserMedia;

  // Auto-detect Whisper availability and default to it.
  useEffect(() => {
    // Only auto-switch if user hasn't explicitly chosen a mode.
    if (localStorage.getItem("amurg-voice")) return;
    fetch("/api/voice/config")
      .then((r) => r.json())
      .then((d) => {
        if (d.whisper_available) {
          handleConfig({ mode: "whisper", whisperUrl: defaultWhisperUrl() });
        }
      })
      .catch(() => {});
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const setRec = useCallback((v: boolean) => {
    recRef.current = v;
    setRecording(v);
  }, []);

  const finalizeInterim = useCallback(() => {
    if (!interimRef.current) return;
    onResult(interimRef.current);
    interimRef.current = "";
    onInterim?.("");
  }, [onResult, onInterim]);

  // ── Audio level monitor ──────────────────────────────────

  const startMonitor = useCallback((stream: MediaStream) => {
    try {
      const ctx = new AudioContext();
      // Mobile browsers start AudioContext in suspended state — resume on user gesture.
      if (ctx.state === "suspended") ctx.resume();
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
    setRec(false);
    browserStopPendingRef.current = false;
    if (closeRef.current) {
      clearTimeout(closeRef.current);
      closeRef.current = null;
    }

    // Web Speech API
    const recognition = srRef.current;
    srRef.current = null;
    if (recognition) {
      browserStopPendingRef.current = true;
      try {
        recognition.stop();
      } catch {
        browserStopPendingRef.current = false;
        finalizeInterim();
      }
    }

    // MediaRecorder
    if (mrRef.current && mrRef.current.state !== "inactive") {
      mrRef.current.stop();
    }
    mrRef.current = null;

    // PCM AudioContext (whisper mode)
    pcmCtxRef.current?.close().catch(() => {});
    pcmCtxRef.current = null;

    // Whisper WebSocket — send empty message to signal end-of-stream,
    // then delay close to receive final transcription.
    const ws = wsRef.current;
    if (ws) {
      wsRef.current = null;
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(new ArrayBuffer(0));
      }
      closeRef.current = setTimeout(() => {
        closeRef.current = null;
        finalizeInterim();
        ws.close();
      }, 1200);
    } else if (!browserStopPendingRef.current) {
      finalizeInterim();
    }

    stopMonitor();
  }, [finalizeInterim, setRec, stopMonitor]);

  // ── Start recording ──────────────────────────────────────

  const start = useCallback(async () => {
    if (recRef.current || disabled) return;
    setRec(true);
    browserStopPendingRef.current = false;
    if (closeRef.current) {
      clearTimeout(closeRef.current);
      closeRef.current = null;
    }
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
      // Authenticate the WebSocket with the user's JWT token.
      const token = await tokenGetter();
      if (!token) {
        onError?.("Not authenticated");
        stopMonitor();
        setRec(false);
        return;
      }
      const sep = config.whisperUrl.includes("?") ? "&" : "?";
      const wsUrl = `${config.whisperUrl}${sep}token=${encodeURIComponent(token)}`;
      let ws: WebSocket;
      try {
        ws = new WebSocket(wsUrl);
      } catch {
        onError?.("Failed to connect to whisper server");
        stopMonitor();
        setRec(false);
        return;
      }
      wsRef.current = ws;
      ws.binaryType = "arraybuffer";

      ws.onmessage = (ev) => {
        const parsed = parseWhisperMessage(ev.data);
        if (parsed.ignore) return;
        if (parsed.error) {
          onError?.(parsed.error);
          stop();
          return;
        }
        if (!parsed.text) return;

        // Always treat ASR text as interim because some Whisper backends
        // stream cumulative finalized text and some stream partials only.
        // We promote once on stop() to avoid duplicated input.
        interimRef.current = parsed.text;
        onInterim?.(parsed.text);
      };

      ws.onopen = async () => {
        try {
          if (!recRef.current) {
            ws.close();
            return;
          }
          // WhisperLiveKit with --pcm-input expects int16 PCM (s16le) at 16kHz.
          // We capture at the native rate and resample + convert via AudioWorklet
          // (createScriptProcessor is deprecated and broken on mobile browsers).
          const audioCtx = new AudioContext();
          if (audioCtx.state === "suspended") await audioCtx.resume();
          pcmCtxRef.current = audioCtx;
          const nativeRate = audioCtx.sampleRate;
          const targetRate = 16000;
          const source = audioCtx.createMediaStreamSource(stream);

          // Inline AudioWorklet processor — avoids a separate file.
          const workletCode = `
            class PCMProcessor extends AudioWorkletProcessor {
              constructor() { super(); }
              process(inputs) {
                const input = inputs[0]?.[0];
                if (input) this.port.postMessage(input);
                return true;
              }
            }
            registerProcessor('pcm-processor', PCMProcessor);
          `;
          const blob = new Blob([workletCode], { type: "application/javascript" });
          const workletUrl = URL.createObjectURL(blob);
          try {
            await audioCtx.audioWorklet.addModule(workletUrl);
          } catch {
            // AudioWorklet not supported — fall back to createScriptProcessor.
            URL.revokeObjectURL(workletUrl);
            startPCMLegacy(audioCtx, source, nativeRate, targetRate, ws);
            return;
          }
          URL.revokeObjectURL(workletUrl);

          const workletNode = new AudioWorkletNode(audioCtx, "pcm-processor");
          workletNode.port.onmessage = (e) => {
            if (ws.readyState !== WebSocket.OPEN) return;
            const input: Float32Array = e.data;
            let floats: Float32Array;
            if (nativeRate === targetRate) {
              floats = input;
            } else {
              const ratio = nativeRate / targetRate;
              const outLen = Math.floor(input.length / ratio);
              floats = new Float32Array(outLen);
              for (let i = 0; i < outLen; i++) {
                const srcIdx = i * ratio;
                const lo = Math.floor(srcIdx);
                const hi = Math.min(lo + 1, input.length - 1);
                const frac = srcIdx - lo;
                floats[i] = input[lo] * (1 - frac) + input[hi] * frac;
              }
            }
            const buf = new ArrayBuffer(floats.length * 2);
            const view = new DataView(buf);
            for (let i = 0; i < floats.length; i++) {
              const s = Math.max(-1, Math.min(1, floats[i]));
              view.setInt16(i * 2, s < 0 ? s * 0x8000 : s * 0x7fff, true);
            }
            ws.send(buf);
          };
          source.connect(workletNode);
          workletNode.connect(audioCtx.destination);
        } catch {
          onError?.("Failed to start microphone capture");
          stop();
        }
      };

      ws.onerror = () => {
        onError?.("Whisper server connection failed");
        stop();
      };
      ws.onclose = (ev) => {
        if (!recRef.current) return;
        const reason =
          ev.reason.trim() ||
          (ev.wasClean ? "Whisper server closed the connection" : "Whisper server disconnected");
        onError?.(reason);
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
          return;
        }
        if (browserStopPendingRef.current) {
          browserStopPendingRef.current = false;
          finalizeInterim();
        }
      };

      recognition.onerror = () => stop();
      srRef.current = recognition;
      try {
        recognition.start();
      } catch {
        onError?.("Failed to start speech recognition");
        stop();
      }
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
    finalizeInterim,
    onResult,
    onInterim,
    onError,
  ]);

  // ── Hold-to-talk / tap-to-toggle gesture ─────────────────

  const handlePointerDown = useCallback(
    (e: React.PointerEvent) => {
      e.preventDefault();
      e.currentTarget.setPointerCapture(e.pointerId);
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
      if (closeRef.current) clearTimeout(closeRef.current);
      if (holdRef.current) clearTimeout(holdRef.current);
      srRef.current?.stop();
      if (mrRef.current?.state !== "inactive") mrRef.current?.stop();
      pcmCtxRef.current?.close().catch(() => {});
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
