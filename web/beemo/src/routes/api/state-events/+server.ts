import { number, orchestratorClient, record, serverStream, text } from "$lib/server/grpc";

const encoder = new TextEncoder();

function sse(event: string, value: unknown): Uint8Array {
  return encoder.encode(`event: ${event}\ndata: ${JSON.stringify(value)}\n\n`);
}

export function GET({ url }): Response {
  const client = orchestratorClient();
  const sessionId = url.searchParams.get("session_id")?.trim() || "voice-loop";
  let call: ReturnType<typeof serverStream> | undefined;
  let settled = false;

  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      const enqueue = (event: string, value: unknown): void => {
        if (!settled) controller.enqueue(sse(event, value));
      };
      const finish = (): void => {
        if (settled) return;
        settled = true;
        controller.close();
        client.close();
      };

      enqueue("service", { status: "connecting" });
      client.waitForReady(Date.now() + 5000, (error) => {
        if (error) enqueue("service", { status: "offline", error: error.message });
        else enqueue("service", { status: "ready" });
      });
      try {
        call = serverStream(client, "streamState", { sessionId });
      } catch (error) {
        enqueue("service", { status: "offline", error: String(error) });
        finish();
        return;
      }
      call.on("data", (value) => {
        const update = record(value);
        enqueue("state", {
          sessionId: text(update.sessionId),
          state: text(update.state),
          message: text(update.message),
          timestampUnixMs: number(update.timestampUnixMs),
        });
      });
      call.on("error", (error) => {
        enqueue("service", { status: "offline", error: error.message });
        finish();
      });
      call.on("end", finish);
    },
    cancel() {
      if (settled) return;
      settled = true;
      call?.cancel();
      client.close();
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
