import { json } from "@sveltejs/kit";
import { errorResponse, requestJson } from "$lib/server/http";
import { orchestratorClient, text, unary } from "$lib/server/grpc";

export async function POST({ request }): Promise<Response> {
  const body = await requestJson(request);
  const client = orchestratorClient();
  try {
    const response = await unary(client, "approve", {
      sessionId: text(body.sessionId),
      approvalId: text(body.approvalId),
      approved: body.approved === true,
    });
    return json({ accepted: response.accepted === true, status: text(response.status) });
  } catch (error) {
    return errorResponse(error);
  } finally {
    client.close();
  }
}
