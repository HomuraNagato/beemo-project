import { json } from "@sveltejs/kit";
import { errorResponse } from "$lib/server/http";
import { number, orchestratorClient, record, text, unary } from "$lib/server/grpc";

export async function GET(): Promise<Response> {
  const client = orchestratorClient();
  try {
    const response = await unary(client, "listAgentSessions", { limit: 30 });
    const sessions = Array.isArray(response.sessions) ? response.sessions.map((item) => {
      const session = record(item);
      return {
        sessionId: text(session.sessionId),
        mode: text(session.mode),
        workspace: text(session.workspace),
        status: text(session.status),
        updatedUnixMs: number(session.updatedUnixMs),
      };
    }) : [];
    return json({ sessions });
  } catch (error) {
    return errorResponse(error);
  } finally {
    client.close();
  }
}
