import { json } from "@sveltejs/kit";
import { errorResponse } from "$lib/server/http";
import { orchestratorClient, record, text, unary } from "$lib/server/grpc";

export async function GET({ url }): Promise<Response> {
  const client = orchestratorClient();
  try {
    const response = await unary(client, "browseAgentDirectories", { path: url.searchParams.get("path") || "" });
    const directories = Array.isArray(response.directories) ? response.directories.map((value) => {
      const item = record(value);
      return { name: text(item.name), path: text(item.path), isGit: Boolean(item.isGit) };
    }) : [];
    return json({ path: text(response.path), parent: text(response.parent), roots: Array.isArray(response.roots) ? response.roots.map(text) : [], isGit: Boolean(response.isGit), directories });
  } catch (error) {
    return errorResponse(error, 400);
  } finally {
    client.close();
  }
}
