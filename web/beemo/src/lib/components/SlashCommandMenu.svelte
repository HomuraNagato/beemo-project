<script lang="ts">
  import type { SlashCommand } from "$lib/types";

  export let commands: SlashCommand[];
  export let selectedIndex = 0;
  export let onselect: (command: SlashCommand) => void;
</script>

<div
  id="slash-command-menu"
  class="absolute right-0 bottom-[calc(100%+8px)] left-0 overflow-hidden rounded-md border border-[rgba(220,238,231,0.28)] bg-[rgba(13,18,17,0.96)] shadow-[0_12px_32px_rgba(0,0,0,0.4)] backdrop-blur-xl"
  role="listbox"
  aria-label="Available commands"
>
  {#if commands.length}
    {#each commands as item, index (item.command)}
      <button
        id={`slash-command-${index}`}
        class={`flex min-h-14 w-full cursor-pointer items-center gap-3 border-0 px-3 py-2 text-left text-beemo-text ${index === selectedIndex ? "bg-[rgba(102,217,180,0.16)]" : "bg-transparent hover:bg-[rgba(102,217,180,0.1)]"}`}
        type="button"
        role="option"
        aria-selected={index === selectedIndex}
        on:mouseenter={() => selectedIndex = index}
        on:mousedown|preventDefault={() => onselect(item)}
      >
        <strong class="shrink-0 font-mono text-sm text-beemo-mint">{item.command}</strong>
        <span class="min-w-0 text-xs leading-5 text-[#c2d0cb]">{item.description}</span>
      </button>
    {/each}
  {:else}
    <p class="m-0 px-3 py-4 text-sm text-[#c2d0cb]">No matching commands.</p>
  {/if}
</div>
