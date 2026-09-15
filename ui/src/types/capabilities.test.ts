import { describe, expect, it } from "vitest";
import { agentCapabilities, isTerminalAgent, type AgentInfo } from "./index";

describe("agent capabilities", () => {
  const agent: AgentInfo = {
    id: "claude", runtime_id: "runtime", profile: "claude-code",
    name: "Claude Code", online: true, caps: {},
  };

  it("selects the terminal for the capability object returned by the hub API", () => {
    expect(isTerminalAgent({ ...agent, caps: { terminal: true, exec_model: "interactive" } })).toBe(true);
  });

  it("accepts legacy encoded capabilities without a transport profile", () => {
    expect(isTerminalAgent({ ...agent, caps: '{"terminal":true}' })).toBe(true);
  });

  it("keeps non-terminal endpoints on their declared interface", () => {
    expect(isTerminalAgent(agent)).toBe(false);
    expect(isTerminalAgent({ ...agent, caps: '{"terminal":false}' })).toBe(false);
    expect(agentCapabilities({ caps: "null" })).toEqual({});
    expect(agentCapabilities({ caps: "invalid" })).toEqual({});
    expect(agentCapabilities(undefined)).toEqual({});
  });
});
