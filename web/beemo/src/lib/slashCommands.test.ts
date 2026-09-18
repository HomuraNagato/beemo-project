import { describe, expect, it } from "vitest";
import { isSlashCommandInput, matchingSlashCommands } from "$lib/slashCommands";
import type { SlashCommand } from "$lib/types";

const commands: SlashCommand[] = [
  { command: "/internet", description: "Toggle internet search for this session." },
];

describe("slash commands", () => {
  it("recognizes a command only at the start of the composer", () => {
    expect(isSlashCommandInput("/")).toBe(true);
    expect(isSlashCommandInput("  /int")).toBe(true);
    expect(isSlashCommandInput("ask /internet")).toBe(false);
    expect(isSlashCommandInput("/internet now")).toBe(false);
  });

  it("filters commands by command text or description", () => {
    expect(matchingSlashCommands("/int", commands)).toEqual(commands);
    expect(matchingSlashCommands("/search", commands)).toEqual(commands);
    expect(matchingSlashCommands("/missing", commands)).toEqual([]);
  });
});
