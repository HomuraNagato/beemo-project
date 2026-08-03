import { json } from "@sveltejs/kit";
import { errorResponse } from "$lib/server/http";
import { orchestratorClient, strings, unary } from "$lib/server/grpc";

export async function GET(): Promise<Response> {
  const client = orchestratorClient();
  try {
    const response = await unary(client, "listAgentWorkspaces", {});
    return json({ workspaces: strings(response.roots) });
  } catch (error) {
    return errorResponse(error);
  } finally {
    client.close();
  }
}
