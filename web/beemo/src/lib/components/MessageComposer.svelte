<script lang="ts">
  import { tick } from "svelte";
  import { SendHorizontal, Square } from "@lucide/svelte";
  import { isSlashCommandInput, matchingSlashCommands } from "$lib/slashCommands";
  import type { SlashCommand } from "$lib/types";
  import SlashCommandMenu from "./SlashCommandMenu.svelte";

  export let value = "";
  export let busy = false;
  export let commands: SlashCommand[] = [];
  export let onsubmit: () => void;

  let textarea: HTMLTextAreaElement;
  let selectedIndex = 0;
  let dismissedValue = "";
  let previousValue = "";

  $: if (value !== previousValue) {
    if (value !== dismissedValue) dismissedValue = "";
    previousValue = value;
    selectedIndex = 0;
  }
  $: menuOpen = isSlashCommandInput(value) && value !== dismissedValue;
  $: matchingCommands = menuOpen ? matchingSlashCommands(value, commands) : [];
  $: if (matchingCommands.length > 0 && selectedIndex >= matchingCommands.length) selectedIndex = 0;

  async function selectCommand(command: SlashCommand): Promise<void> {
    value = command.command;
    dismissedValue = command.command;
    selectedIndex = 0;
    await tick();
    textarea.focus();
    textarea.setSelectionRange(value.length, value.length);
  }

  function handleKeydown(event: KeyboardEvent): void {
    if (menuOpen) {
      if (event.key === "ArrowDown" && matchingCommands.length) {
        event.preventDefault();
        selectedIndex = (selectedIndex + 1) % matchingCommands.length;
        return;
      }
      if (event.key === "ArrowUp" && matchingCommands.length) {
        event.preventDefault();
        selectedIndex = (selectedIndex - 1 + matchingCommands.length) % matchingCommands.length;
        return;
      }
      if ((event.key === "Enter" || event.key === "Tab") && matchingCommands.length) {
        event.preventDefault();
        void selectCommand(matchingCommands[selectedIndex]);
        return;
      }
      if (event.key === "Escape") {
        event.preventDefault();
        dismissedValue = value;
        return;
      }
    }
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      onsubmit();
    }
  }
</script>

<form
  class="absolute right-1/2 bottom-[calc(12px+env(safe-area-inset-bottom))] z-[7] grid w-[min(760px,calc(100%-28px))] translate-x-1/2 grid-cols-[minmax(0,1fr)_54px] gap-2 rounded-md border border-[rgba(220,238,231,0.14)] bg-[rgba(13,18,17,0.08)] p-2 [text-shadow:0_1px_4px_rgba(0,0,0,0.95)] backdrop-blur-[1px] max-[520px]:w-[calc(100%-16px)]"
  aria-label="Message composer"
  on:submit|preventDefault={onsubmit}
>
  {#if menuOpen}
    <SlashCommandMenu commands={matchingCommands} bind:selectedIndex onselect={(command) => void selectCommand(command)} />
  {/if}
  <textarea
    bind:this={textarea}
    class="h-12 max-h-28 min-h-12 w-full resize-none overflow-y-auto rounded-sm border border-[rgba(220,238,231,0.18)] bg-[rgba(13,18,17,0.08)] px-3 py-2.5 leading-[1.35] text-beemo-text outline-0 placeholder:text-[#c2d0cb] focus:border-beemo-mint focus:bg-[rgba(13,18,17,0.22)]"
    bind:value
    rows="2"
    placeholder="Message Beemo"
    aria-label="Message to Beemo"
    role="combobox"
    aria-autocomplete="list"
    aria-expanded={menuOpen}
    aria-controls={menuOpen ? "slash-command-menu" : undefined}
    aria-activedescendant={menuOpen && matchingCommands.length ? `slash-command-${selectedIndex}` : undefined}
    on:keydown={handleKeydown}
  ></textarea>
  <button
    class="grid size-[50px] cursor-pointer place-items-center rounded-sm border border-beemo-mint bg-beemo-mint text-[#101514] hover:bg-[#82e8c7]"
    type="submit"
    aria-label={busy ? "Stop response" : "Send message"}
    title={busy ? "Stop" : "Send"}
  >
    {#if busy}<Square size={18} fill="currentColor" />{:else}<SendHorizontal size={21} />{/if}
  </button>
</form>
