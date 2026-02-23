import { describe, it, expect } from "vitest";
import { detectContentType } from "./contentDetection";

describe("detectContentType", () => {
  // --- User messages are always plain ---
  it("returns plain for user messages regardless of content", () => {
    expect(detectContentType("# Heading", "user", "stdin")).toBe("plain");
    expect(detectContentType('{"key": "value"}', "user", "stdin")).toBe("plain");
    expect(detectContentType("\x1b[31mred\x1b[0m", "user", "stdin")).toBe("plain");
  });

  // --- System channel is always plain ---
  it("returns plain for system channel", () => {
    expect(detectContentType("anything here", "agent", "system")).toBe("plain");
    expect(detectContentType('{"json": true}', "agent", "system")).toBe("plain");
  });

  // --- ANSI detection ---
  it("detects ANSI escape sequences", () => {
    expect(detectContentType("\x1b[31mred text\x1b[0m", "agent", "stdout")).toBe("ansi");
  });

  it("detects ANSI with multiple codes", () => {
    expect(detectContentType("\x1b[1;32mbold green\x1b[0m", "agent", "stdout")).toBe("ansi");
  });

  it("detects ANSI on stderr", () => {
    expect(detectContentType("\x1b[33mwarning\x1b[0m", "agent", "stderr")).toBe("ansi");
  });

  // --- Diff detection ---
  it("detects unified diff content", () => {
    const diff = "--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new";
    expect(detectContentType(diff, "agent", "stdout")).toBe("diff");
  });

  it("detects diff starting with ---", () => {
    const diff = "--- old\n+++ new\n@@ -1,3 +1,3 @@";
    expect(detectContentType(diff, "agent", "stdout")).toBe("diff");
  });

  it("detects diff starting with @@", () => {
    const diff = "@@ -10,5 +10,7 @@\n context\n-removed\n+added";
    expect(detectContentType(diff, "agent", "stdout")).toBe("diff");
  });

  // --- JSON detection ---
  it("detects JSON object", () => {
    expect(detectContentType('{"key": "value"}', "agent", "stdout")).toBe("json");
  });

  it("detects JSON array", () => {
    expect(detectContentType("[1, 2, 3]", "agent", "stdout")).toBe("json");
  });

  it("detects JSON with leading whitespace", () => {
    expect(detectContentType('  {"key": "value"}', "agent", "stdout")).toBe("json");
  });

  it("does not detect invalid JSON starting with {", () => {
    expect(detectContentType("{not valid json", "agent", "stdout")).toBe("markdown");
  });

  it("does not detect invalid JSON starting with [", () => {
    expect(detectContentType("[not an array", "agent", "stdout")).toBe("markdown");
  });

  // --- Markdown / stdout fallback ---
  it("returns markdown for agent stdout with plain text", () => {
    expect(detectContentType("Hello, world!", "agent", "stdout")).toBe("markdown");
  });

  it("returns markdown for agent stdout with headers", () => {
    expect(detectContentType("# Heading\n\nSome text", "agent", "stdout")).toBe("markdown");
  });

  it("returns markdown for empty channel (defaults to stdout behavior)", () => {
    expect(detectContentType("some text", "agent", "")).toBe("markdown");
  });

  // --- Stderr fallback ---
  it("returns plain for stderr without ANSI", () => {
    expect(detectContentType("error: something failed", "agent", "stderr")).toBe("plain");
  });

  // --- Priority: ANSI beats diff/json/markdown ---
  it("prioritizes ANSI over diff-like content", () => {
    const ansiDiff = "\x1b[31m--- a/file\x1b[0m\n+++ b/file";
    expect(detectContentType(ansiDiff, "agent", "stdout")).toBe("ansi");
  });

  it("prioritizes ANSI over JSON-like content", () => {
    const ansiJson = '\x1b[32m{"key": "value"}\x1b[0m';
    expect(detectContentType(ansiJson, "agent", "stdout")).toBe("ansi");
  });
});
