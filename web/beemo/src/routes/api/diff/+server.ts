import { json } from "@sveltejs/kit";
import { errorResponse, requestJson } from "$lib/server/http";
import { orchestratorClient, text, unary } from "$lib/server/grpc";

export async function POST({ request }): Promise<Response> {
  const body = await requestJson(request);
  const client = orchestratorClient();
  try {
    const response = await unary(client, "getAgentDiff", {
      sessionId: text(body.sessionId),
      workspace: text(body.workspace),
    });
    return json({ diff: text(response.diff), error: text(response.error) });
  } catch (error) {
    return errorResponse(error);
  } finally {
    client.close();
  }
}
