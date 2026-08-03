import { json } from "@sveltejs/kit";
import { errorResponse } from "$lib/server/http";
import { number, orchestratorClient, record, text, unary } from "$lib/server/grpc";

export async function GET({ url }): Promise<Response> {
  const sessionId = url.searchParams.get("id")?.trim() || "";
  if (!sessionId) return json({ error: "session id is required" }, { status: 400 });

  const client = orchestratorClient();
  try {
    const response = await unary(client, "getAgentSession", { sessionId });
    const summary = record(response.session);
    const events = Array.isArray(response.events) ? response.events.map((item) => {
      const event = record(item);
      return {
        sessionId: text(event.sessionId),
        type: text(event.type),
        text: text(event.text),
        tool: text(event.tool),
        payloadJson: text(event.payloadJson),
        timestampUnixMs: number(event.timestampUnixMs),
        approvalId: text(event.approvalId),
      };
    }) : [];
    return json({
      session: {
        sessionId: text(summary.sessionId),
        mode: text(summary.mode),
        workspace: text(summary.workspace),
        status: text(summary.status),
        updatedUnixMs: number(summary.updatedUnixMs),
      },
      events,
    });
  } catch (error) {
    return errorResponse(error, 404);
  } finally {
    client.close();
  }
}
