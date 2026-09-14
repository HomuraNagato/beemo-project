import { json } from "@sveltejs/kit";
import { errorResponse, requestJson } from "$lib/server/http";
import {
  number,
  orchestratorClient,
  record,
  text,
  unary,
} from "$lib/server/grpc";
import {
  activateSessionFile,
  activeSessionId,
  newSessionId,
} from "$lib/server/session";

function sessionSummary(value: unknown): Record<string, unknown> {
  const session = record(value);
  return {
    sessionId: text(session.sessionId),
    name: text(session.name) || "New session",
    mode: text(session.mode) || "chat",
    workspace: text(session.workspace),
    status: text(session.status),
    internetEnabled: Boolean(session.internetEnabled),
    updatedUnixMs: number(session.updatedUnixMs),
  };
}

export async function GET(): Promise<Response> {
  const client = orchestratorClient();
  try {
    const response = await unary(client, "listAgentSessions", { limit: 100 });
    const sessions = Array.isArray(response.sessions)
      ? response.sessions.map(sessionSummary)
      : [];
    return json({ sessions, activeSessionId: activeSessionId() });
  } catch (error) {
    return errorResponse(error);
  } finally {
    client.close();
  }
}

export async function POST(): Promise<Response> {
  const client = orchestratorClient();
  try {
    const sessionId = newSessionId();
    const response = await unary(client, "createAgentSession", { sessionId });
    activateSessionFile(sessionId);
    return json(
      { session: sessionSummary(response), activeSessionId: sessionId },
      { status: 201 },
    );
  } catch (error) {
    return errorResponse(error);
  } finally {
    client.close();
  }
}

export async function PATCH({ request }): Promise<Response> {
  let body: Record<string, unknown>;
  try {
    body = await requestJson(request);
  } catch (error) {
    return errorResponse(error, 400);
  }
  const client = orchestratorClient();
  try {
    const response = await unary(client, "renameAgentSession", {
      sessionId: text(body.sessionId),
      name: text(body.name),
    });
    return json({ session: sessionSummary(response) });
  } catch (error) {
    return errorResponse(error, 400);
  } finally {
    client.close();
  }
}

export async function DELETE({ request }): Promise<Response> {
  let body: Record<string, unknown>;
  try {
    body = await requestJson(request);
  } catch (error) {
    return errorResponse(error, 400);
  }
  const sessionIds = [
    ...new Set(
      Array.isArray(body.sessionIds)
        ? body.sessionIds.map(text).filter(Boolean)
        : [],
    ),
  ];
  let active = activeSessionId();
  const client = orchestratorClient();
  try {
    let activeSession: Record<string, unknown> | undefined;
    let activeChanged = false;
    if (sessionIds.includes(active)) {
      const listed = await unary(client, "listAgentSessions", { limit: 100 });
      const replacement = (
        Array.isArray(listed.sessions) ? listed.sessions : []
      )
        .map(sessionSummary)
        .find((session) => !sessionIds.includes(text(session.sessionId)));
      if (replacement) {
        active = text(replacement.sessionId);
        activeSession = sessionSummary(
          await unary(client, "activateAgentSession", { sessionId: active }),
        );
      } else {
        active = newSessionId();
        activeSession = sessionSummary(
          await unary(client, "createAgentSession", { sessionId: active }),
        );
      }
      activeChanged = true;
    }
    const response = await unary(client, "deleteAgentSessions", {
      sessionIds,
      activeSessionId: active,
    });
    if (activeChanged) activateSessionFile(active);
    return json({
      deleted: number(response.deleted),
      activeSessionId: active,
      activeSession,
    });
  } catch (error) {
    return errorResponse(error, 400);
  } finally {
    client.close();
  }
}
