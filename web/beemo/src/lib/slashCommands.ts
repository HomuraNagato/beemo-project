import type { SlashCommand } from "$lib/types";

export function isSlashCommandInput(value: string): boolean {
  const query = value.trimStart();
  return query.startsWith("/") && !/\s/.test(query);
}

export function matchingSlashCommands(value: string, commands: SlashCommand[]): SlashCommand[] {
  if (!isSlashCommandInput(value)) return [];
  const query = value.trimStart().toLowerCase();
  const terms = query.slice(1);
  return commands.filter((item) =>
    item.command.toLowerCase().startsWith(query)
    || (terms !== "" && item.description.toLowerCase().includes(terms))
  );
}
