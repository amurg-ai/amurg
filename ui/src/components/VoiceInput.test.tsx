import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/api/client", () => ({
  tokenGetter: vi.fn(),
}));

import { VoiceInput, parseWhisperMessage } from "./VoiceInput";

class MockSpeechRecognition {
  static instances: MockSpeechRecognition[] = [];

  continuous = false;
  interimResults = false;
  lang = "";
  onend?: () => void;
  onerror?: () => void;
  onresult?: (event: unknown) => void;
  start = vi.fn();
  stop = vi.fn(() => {
    this.onend?.();
  });

  constructor() {
    MockSpeechRecognition.instances.push(this);
  }
}

class MockAnalyser {
  fftSize = 0;
  smoothingTimeConstant = 0;
  frequencyBinCount = 8;

  getByteFrequencyData(buffer: Uint8Array) {
    buffer.fill(0);
  }
}

class MockAudioContext {
  state: AudioContextState = "running";
  sampleRate = 48000;
  destination = {} as AudioDestinationNode;

  close = vi.fn().mockResolvedValue(undefined);
  resume = vi.fn().mockResolvedValue(undefined);

  createAnalyser() {
    return new MockAnalyser() as unknown as AnalyserNode;
  }

  createMediaStreamSource() {
    return {
      connect: vi.fn(),
    } as unknown as MediaStreamAudioSourceNode;
  }
}

const originalAudioContext = globalThis.AudioContext;
const originalGetUserMedia = navigator.mediaDevices?.getUserMedia;
const originalSetPointerCapture = HTMLElement.prototype.setPointerCapture;
const originalReleasePointerCapture = HTMLElement.prototype.releasePointerCapture;
const originalRequestAnimationFrame = globalThis.requestAnimationFrame;
const originalCancelAnimationFrame = globalThis.cancelAnimationFrame;

describe("parseWhisperMessage", () => {
  it("combines cumulative and partial Whisper payloads", () => {
    expect(
      parseWhisperMessage(
        JSON.stringify({
          lines: [{ text: "hello" }, { text: "world" }],
          buffer_transcription: "again",
        }),
      ),
    ).toEqual({ text: "hello world again" });
  });

  it("accepts generic plain-text transcripts", () => {
    expect(parseWhisperMessage("hello from whisper")).toEqual({
      text: "hello from whisper",
    });
  });
});

describe("VoiceInput", () => {
  beforeEach(() => {
    MockSpeechRecognition.instances = [];
    localStorage.clear();
    localStorage.setItem(
      "amurg-voice",
      JSON.stringify({
        mode: "browser",
        whisperUrl: "ws://localhost:3000/asr",
      }),
    );

    Object.defineProperty(globalThis, "AudioContext", {
      configurable: true,
      value: MockAudioContext,
    });
    Object.defineProperty(window, "webkitSpeechRecognition", {
      configurable: true,
      value: MockSpeechRecognition,
    });
    Object.defineProperty(navigator, "mediaDevices", {
      configurable: true,
      value: {
        getUserMedia: vi.fn().mockResolvedValue({
          getTracks: () => [{ stop: vi.fn() }],
        }),
      },
    });
    Object.defineProperty(HTMLElement.prototype, "setPointerCapture", {
      configurable: true,
      value: vi.fn(),
    });
    Object.defineProperty(HTMLElement.prototype, "releasePointerCapture", {
      configurable: true,
      value: vi.fn(),
    });
    globalThis.requestAnimationFrame = vi.fn(() => 1);
    globalThis.cancelAnimationFrame = vi.fn();
  });

  afterEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();

    Object.defineProperty(globalThis, "AudioContext", {
      configurable: true,
      value: originalAudioContext,
    });
    Object.defineProperty(window, "webkitSpeechRecognition", {
      configurable: true,
      value: undefined,
    });
    Object.defineProperty(navigator, "mediaDevices", {
      configurable: true,
      value: originalGetUserMedia
        ? { getUserMedia: originalGetUserMedia }
        : undefined,
    });
    Object.defineProperty(HTMLElement.prototype, "setPointerCapture", {
      configurable: true,
      value: originalSetPointerCapture,
    });
    Object.defineProperty(HTMLElement.prototype, "releasePointerCapture", {
      configurable: true,
      value: originalReleasePointerCapture,
    });
    globalThis.requestAnimationFrame = originalRequestAnimationFrame;
    globalThis.cancelAnimationFrame = originalCancelAnimationFrame;
  });

  it("finalizes browser interim text without restarting recognition on stop", async () => {
    const onResult = vi.fn();
    const onInterim = vi.fn();

    render(
      <VoiceInput
        onResult={onResult}
        onInterim={onInterim}
      />,
    );

    const startButton = screen.getByTitle("Hold to talk, tap to toggle");
    fireEvent.pointerDown(startButton, { pointerId: 1 });
    fireEvent.pointerUp(startButton, { pointerId: 1 });

    await waitFor(() => {
      expect(MockSpeechRecognition.instances).toHaveLength(1);
    });

    const recognition = MockSpeechRecognition.instances[0];
    await waitFor(() => {
      expect(recognition.start).toHaveBeenCalledTimes(1);
    });

    act(() => {
      recognition.onresult?.({
        resultIndex: 0,
        results: [
          {
            0: { transcript: "hello world" },
            isFinal: false,
          },
        ],
      });
    });

    const stopButton = await screen.findByTitle("Tap or release to stop");
    fireEvent.pointerDown(stopButton, { pointerId: 2 });
    fireEvent.pointerUp(stopButton, { pointerId: 2 });

    await waitFor(() => {
      expect(onResult).toHaveBeenCalledWith("hello world");
    });
    expect(onResult).toHaveBeenCalledTimes(1);
    expect(recognition.start).toHaveBeenCalledTimes(1);
  });
});
