import { fireEvent, render, screen, waitFor, cleanup } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({sendMessage: vi.fn(), uploadFile: vi.fn(), addToast: vi.fn()}));
vi.mock("@/stores/sessionStore", () => ({useSessionStore: () => ({...mocks, activeSessionId: "s1", responding: new Set(), canSendInteractiveInput: () => true})}));
vi.mock("@/components/VoiceInput", () => ({VoiceInput: ({onResult}: {onResult: (text: string) => void}) => <button onClick={() => onResult("fix the failing tests")}>Dictate</button>}));
import { MessageInput } from "./MessageInput";

afterEach(() => { cleanup(); vi.clearAllMocks(); });

describe("terminal mobile composer", () => {
 it("keeps voice dictation editable and submits through passthrough", () => {
  const send = vi.fn(() => true);
  render(<MessageInput onSend={send}/>);
  fireEvent.click(screen.getByText("Dictate"));
  expect((screen.getByLabelText("Message") as HTMLTextAreaElement).value).toBe("fix the failing tests");
  expect(send).not.toHaveBeenCalled();
  fireEvent.click(screen.getByText("Send"));
  expect(send).toHaveBeenCalledWith("fix the failing tests");
  expect(mocks.sendMessage).not.toHaveBeenCalled();
 });
 it("allows multiline mobile drafts without submitting on Enter", () => {
  const send = vi.fn(() => true);
  render(<MessageInput onSend={send}/>);
  const field = screen.getByLabelText("Message");
  expect(field.tagName).toBe("TEXTAREA");
  fireEvent.change(field, {target:{value:"First line\nSecond line"}});
  fireEvent.keyDown(field, {key:"Enter"});
  expect(send).not.toHaveBeenCalled();
  fireEvent.click(screen.getByText("Send"));
  expect(send).toHaveBeenCalledWith("First line\nSecond line");
 });
 it("keeps the draft if the terminal disconnects at submission", () => {
  const send = vi.fn(() => false);
  render(<MessageInput onSend={send}/>);
  fireEvent.change(screen.getByLabelText("Message"), {target:{value:"keep this prompt"}});
  fireEvent.click(screen.getByText("Send"));
  expect((screen.getByLabelText("Message") as HTMLTextAreaElement).value).toBe("keep this prompt");
 });
 it("waits for runtime file receipt and sends the path only with the prompt", async () => {
  const send = vi.fn(() => true);
  const upload = vi.fn().mockResolvedValue("/agent/uploads/example.txt");
  const {container} = render(<MessageInput onSend={send} onUpload={upload}/>);
  const file = new File(["contents"], "example.txt", {type:"text/plain"});
  fireEvent.change(container.querySelector('input[type="file"]')!, {target:{files:[file]}});
  await waitFor(() => expect(screen.getByText("example.txt")).toBeTruthy());
  expect(send).not.toHaveBeenCalled();
  fireEvent.change(screen.getByLabelText("Message"), {target:{value:"Review this"}});
  fireEvent.click(screen.getByText("Send"));
  expect(send).toHaveBeenCalledWith("Review this\n\nAttached files:\n/agent/uploads/example.txt");
  expect(mocks.uploadFile).not.toHaveBeenCalled();
 });
});
