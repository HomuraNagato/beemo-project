import { json } from "@sveltejs/kit";

export function errorResponse(error: unknown, status = 502): Response {
  const message = error instanceof Error ? error.message : String(error);
  return json({ error: message }, { status });
}

export async function requestJson(request: Request): Promise<Record<string, unknown>> {
  const value: unknown = await request.json();
  return value && typeof value === "object" ? value as Record<string, unknown> : {};
}
