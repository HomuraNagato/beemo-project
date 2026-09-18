import { json } from "@sveltejs/kit";
import { errorResponse } from "$lib/server/http";
import { orchestratorClient, record, text, unary } from "$lib/server/grpc";

export async function GET(): Promise<Response> {
  const client = orchestratorClient();
  try {
    const response = await unary(client, "listAgentCommands", {});
    const commands = Array.isArray(response.commands) ? response.commands.map((value) => {
      const command = record(value);
      return { command: text(command.command), description: text(command.description) };
    }).filter((command) => command.command && command.description) : [];
    return json({ commands });
  } catch (error) {
    return errorResponse(error);
  } finally {
    client.close();
  }
}
