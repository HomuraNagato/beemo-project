import { describe, expect, it } from "vitest";
import { limitNotice, operationalLabel, parseAgentProgress } from "./agentStatus";

describe("agent status", () => {
  it("reads progress nested in tool payloads", () => {
    expect(parseAgentProgress('{"args":{},"progress":{"step":4,"maxSteps":64}}')).toEqual({ step: 4, maxSteps: 64 });
  });

  it("adds bounded code progress to operational labels", () => {
    expect(operationalLabel("Searching files", { step: 18, maxSteps: 64 })).toBe("Searching files · Step 18/64");
  });

  it("describes conversation progress as rounds", () => {
    expect(operationalLabel("Researching", { phase: "research", step: 2, maxSteps: 8 })).toBe("Researching · Round 2/8");
  });

  it("describes a reached output limit as incomplete", () => {
    expect(limitNotice({ warning: "output_limit", incomplete: true })).toEqual({
      text: "Response token limit reached · response may be incomplete",
      tone: "error",
    });
  });
});
