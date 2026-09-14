import { json } from "@sveltejs/kit";
import { errorResponse, requestJson } from "$lib/server/http";
import { number, orchestratorClient, record, text, unary } from "$lib/server/grpc";

function project(value: unknown): Record<string, unknown> {
  const item = record(value);
  return {
    path: text(item.path), name: text(item.name), isGit: Boolean(item.isGit),
    createdUnixMs: number(item.createdUnixMs), lastUsedUnixMs: number(item.lastUsedUnixMs),
  };
}

export async function GET(): Promise<Response> {
  const client = orchestratorClient();
  try {
    const response = await unary(client, "listAgentProjects", {});
    return json({ projects: Array.isArray(response.projects) ? response.projects.map(project) : [] });
  } catch (error) {
    return errorResponse(error);
  } finally {
    client.close();
  }
}

export async function POST({ request }): Promise<Response> {
  let body: Record<string, unknown>;
  try { body = await requestJson(request); } catch (error) { return errorResponse(error, 400); }
  const client = orchestratorClient();
  try {
    const response = await unary(client, "addAgentProject", { path: text(body.path) });
    return json({ project: project(response) }, { status: 201 });
  } catch (error) {
    return errorResponse(error, 400);
  } finally {
    client.close();
  }
}

export async function DELETE({ request }): Promise<Response> {
  let body: Record<string, unknown>;
  try { body = await requestJson(request); } catch (error) { return errorResponse(error, 400); }
  const client = orchestratorClient();
  try {
    const response = await unary(client, "removeAgentProject", { path: text(body.path), sessionId: text(body.sessionId) });
    return json({ removed: Boolean(response.removed), mode: text(response.mode), workspace: text(response.workspace) });
  } catch (error) {
    return errorResponse(error, 400);
  } finally {
    client.close();
  }
}
