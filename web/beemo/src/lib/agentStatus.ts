export type StatusTone = "normal" | "warning" | "error";

export interface AgentProgress {
  phase?: string;
  step?: number;
  maxSteps?: number;
  promptTokens?: number;
  completionTokens?: number;
  contextLimit?: number;
  outputLimit?: number;
  warning?: string;
  incomplete?: boolean;
}

export function parseAgentProgress(payloadJson: string): AgentProgress {
  if (!payloadJson) return {};
  try {
    const payload = JSON.parse(payloadJson) as AgentProgress & { progress?: AgentProgress };
    return payload.progress || payload;
  } catch {
    return {};
  }
}

export function operationalLabel(label: string, progress: AgentProgress): string {
  if (progress.step && progress.maxSteps) {
    const unit = progress.phase === "normal" || progress.phase === "research" ? "Round" : "Step";
    return `${label} · ${unit} ${progress.step}/${progress.maxSteps}`;
  }
  return label;
}

export function limitNotice(progress: AgentProgress): { text: string; tone: StatusTone } | null {
  switch (progress.warning) {
    case "tool_near_limit":
      return { text: `Approaching tool limit · Step ${progress.step || "?"}/${progress.maxSteps || "?"}`, tone: "warning" };
    case "tool_limit":
      return { text: "Tool limit reached · response may be incomplete", tone: "error" };
    case "context_near_limit": {
      const percent = progress.contextLimit && progress.promptTokens
        ? Math.round((progress.promptTokens / progress.contextLimit) * 100)
        : null;
      return { text: `Context near limit${percent ? ` · ${percent}% used` : ""}`, tone: "warning" };
    }
    case "output_near_limit":
      return { text: "Response is approaching its token limit", tone: "warning" };
    case "output_limit":
      return { text: "Response token limit reached · response may be incomplete", tone: "error" };
    default:
      return null;
  }
}
