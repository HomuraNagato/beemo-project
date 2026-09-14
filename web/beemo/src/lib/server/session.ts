import { randomBytes } from "node:crypto";
import { readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";

export const sessionFile = process.env.BEEMO_SESSION_FILE || resolve(process.cwd(), "../../.beemo-session");

export function activeSessionId(): string {
  try {
    return readFileSync(sessionFile, "utf8").trim();
  } catch {
    return "";
  }
}

export function newSessionId(): string {
  return `beemo-${new Date().toISOString().replace(/[-:.]/g, "").replace("Z", "Z-")}${randomBytes(4).toString("hex")}`;
}

export function activateSessionFile(sessionId: string): void {
  writeFileSync(sessionFile, `${sessionId}\n`, "utf8");
}
