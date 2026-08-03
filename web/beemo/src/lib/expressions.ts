import type { FaceExpression } from "$lib/types";

const taggedExpression = /^\s*\[(?:emotion|expression|face):\s*([a-z_-]+)\]\s*/i;

const aliases: Record<string, FaceExpression> = {
  apologetic: "worried",
  concerned: "worried",
  curious: "listening",
  error: "worried",
  excited: "surprised",
  happy: "calm",
  neutral: "calm",
  sleepy: "sad",
};

export function expressionFromText(value: string): FaceExpression {
  const tag = value.match(taggedExpression)?.[1]?.toLowerCase().replaceAll("_", "-");
  if (tag) {
    if (tag in aliases) return aliases[tag];
    if (["calm", "sad", "worried", "surprised", "listening", "thinking", "hidden"].includes(tag)) {
      return tag as FaceExpression;
    }
  }

  const text = value.toLowerCase();
  if (hasAny(text, ["request failed", "error", "failed", "can't connect", "cannot connect", "sorry", "apologize", "risk", "uncertain"])) return "worried";
  if (hasAny(text, ["sad", "hurt", "upset", "bad news", "unfortunately", "disappoint"])) return "sad";
  if (hasAny(text, ["wow", "amazing", "surprise", "unexpected", "excellent"])) return "surprised";
  if (hasAny(text, ["let me think", "thinking", "consider", "reason", "checking"])) return "thinking";
  if (hasAny(text, ["listening", "tell me", "go on", "what happened", "clarify"])) return "listening";
  return "calm";
}

export function cleanReply(value: string): string {
  return value.replace(taggedExpression, "").trim();
}

function hasAny(text: string, needles: string[]): boolean {
  return needles.some((needle) => text.includes(needle));
}
