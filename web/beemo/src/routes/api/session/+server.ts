import { json } from "@sveltejs/kit";
import { errorResponse, requestJson } from "$lib/server/http";
import { number, orchestratorClient, record, text, unary } from "$lib/server/grpc";
import { activateSessionFile } from "$lib/server/session";

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
        name: text(summary.name) || "New session",
        mode: text(summary.mode),
        workspace: text(summary.workspace),
        status: text(summary.status),
        internetEnabled: Boolean(summary.internetEnabled),
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

export async function POST({ request }): Promise<Response> {
  let body: Record<string, unknown>;
  try {
    body = await requestJson(request);
  } catch (error) {
    return errorResponse(error, 400);
  }
  const sessionId = text(body.sessionId).trim();
  if (!sessionId) return json({ error: "session id is required" }, { status: 400 });
  const client = orchestratorClient();
  try {
    const response = await unary(client, "activateAgentSession", { sessionId });
    activateSessionFile(sessionId);
    const summary = record(response);
    return json({ session: {
      sessionId: text(summary.sessionId), name: text(summary.name), mode: text(summary.mode),
      workspace: text(summary.workspace), status: text(summary.status), internetEnabled: Boolean(summary.internetEnabled), updatedUnixMs: number(summary.updatedUnixMs),
    } });
  } catch (error) {
    return errorResponse(error, 400);
  } finally {
    client.close();
  }
}
