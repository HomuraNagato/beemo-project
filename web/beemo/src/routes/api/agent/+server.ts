import { errorResponse, requestJson } from "$lib/server/http";
import { number, orchestratorClient, record, serverStream, text } from "$lib/server/grpc";

const encoder = new TextEncoder();

export async function POST({ request }): Promise<Response> {
  let body: Record<string, unknown>;
  try {
    body = await requestJson(request);
  } catch (error) {
    return errorResponse(error, 400);
  }

  const messages = Array.isArray(body.messages) ? body.messages.map((item) => {
    const message = record(item);
    return { role: text(message.role), content: text(message.content) };
  }).filter((message) => message.role && message.content) : [];
  if (messages.length === 0) return errorResponse(new Error("message is required"), 400);

  const client = orchestratorClient();
  let call: ReturnType<typeof serverStream> | undefined;
  let settled = false;
  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      const enqueue = (payload: unknown): void => {
        if (!settled) controller.enqueue(encoder.encode(`${JSON.stringify(payload)}\n`));
      };
      const finish = (): void => {
        if (settled) return;
        settled = true;
        controller.close();
        client.close();
      };

      try {
        call = serverStream(client, "runAgent", {
          sessionId: text(body.sessionId) || "web",
          messages,
          mode: text(body.mode) || "chat",
          workspace: text(body.workspace),
          options: record(body.options),
        });
      } catch (error) {
        controller.error(error);
        client.close();
        return;
      }
      call.on("data", (value) => {
        const event = record(value);
        const payload = {
          sessionId: text(event.sessionId),
          type: text(event.type),
          text: text(event.text),
          tool: text(event.tool),
          payloadJson: text(event.payloadJson),
          timestampUnixMs: number(event.timestampUnixMs),
          approvalId: text(event.approvalId),
        };
        enqueue(payload);
      });
      call.on("error", (error) => {
        const payload = { type: "error", text: error.message };
        enqueue(payload);
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
      "Cache-Control": "no-store",
      "Content-Type": "application/x-ndjson; charset=utf-8",
      "X-Accel-Buffering": "no",
    },
  });
}
