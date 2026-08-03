export type FaceExpression =
  | "calm"
  | "sad"
  | "worried"
  | "surprised"
  | "listening"
  | "thinking"
  | "hidden";

export type AgentEventType =
  | "user"
  | "status"
  | "assistant"
  | "tool_start"
  | "tool_result"
  | "approval"
  | "file_change"
  | "complete"
  | "error";

export interface AgentEvent {
  sessionId: string;
  type: AgentEventType;
  text: string;
  tool: string;
  payloadJson: string;
  timestampUnixMs: number;
  approvalId: string;
}

export interface StateUpdate {
  sessionId: string;
  state: AgentEventType;
  message: string;
  timestampUnixMs: number;
}

export interface WakeEvent {
  sessionId: string;
  timestampUnixMs: number;
  source: string;
  transcript: string;
  prompt: string;
  response: string;
  confidence: number;
  tools: string[];
  status: string;
  errorKind: string;
}

export interface SessionSummary {
  sessionId: string;
  mode: "chat" | "code";
  workspace: string;
  status: string;
  updatedUnixMs: number;
}

export interface ChatMessage {
  role: "user" | "assistant";
  content: string;
}
