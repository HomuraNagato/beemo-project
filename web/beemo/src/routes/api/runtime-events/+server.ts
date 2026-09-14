import { watch, type FSWatcher } from "node:fs";
import { activeSessionId, sessionFile } from "$lib/server/session";

const encoder = new TextEncoder();
function readSession(): string {
  return activeSessionId();
}

function event(sessionId: string): Uint8Array {
  return encoder.encode(`event: session\ndata: ${JSON.stringify({ sessionId })}\n\n`);
}

export function GET(): Response {
  let watcher: FSWatcher | undefined;
  let settled = false;
  let current = "";

  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      const publish = (): void => {
        if (settled) return;
        const sessionId = readSession();
        if (!sessionId || sessionId === current) return;
        current = sessionId;
        controller.enqueue(event(sessionId));
      };
      publish();
      try {
        watcher = watch(sessionFile, publish);
      } catch {
        settled = true;
        controller.close();
      }
    },
    cancel() {
      settled = true;
      watcher?.close();
    },
  });

  return new Response(stream, {
    headers: {
      "Cache-Control": "no-cache",
      "Connection": "keep-alive",
      "Content-Type": "text/event-stream",
      "X-Accel-Buffering": "no",
    },
  });
}
