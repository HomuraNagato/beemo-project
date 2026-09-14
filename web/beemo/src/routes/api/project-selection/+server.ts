import { json } from "@sveltejs/kit";
import { errorResponse, requestJson } from "$lib/server/http";
import { number, orchestratorClient, record, text, unary } from "$lib/server/grpc";

export async function POST({ request }): Promise<Response> {
  let body: Record<string, unknown>;
  try { body = await requestJson(request); } catch (error) { return errorResponse(error, 400); }
  const client = orchestratorClient();
  try {
    const response = record(await unary(client, "selectAgentProject", { sessionId: text(body.sessionId), path: text(body.path) }));
    return json({ session: {
      sessionId: text(response.sessionId), name: text(response.name), mode: text(response.mode), workspace: text(response.workspace),
      status: text(response.status), internetEnabled: Boolean(response.internetEnabled), updatedUnixMs: number(response.updatedUnixMs),
    } });
  } catch (error) {
    return errorResponse(error, 400);
  } finally {
    client.close();
  }
}
