<script lang="ts">
  import { FolderGit2, Menu as MenuIcon, MessagesSquare, X } from "@lucide/svelte";

  export let onprojects: () => void;
  export let onsessions: () => void;
  export let onface: () => void;

  let menuOpen = false;

  function choose(action: () => void): void {
    menuOpen = false;
    action();
  }
</script>

<button class="absolute inset-0 z-[1] cursor-pointer border-0 bg-transparent" type="button" aria-label="Toggle conversation controls" on:click={() => { menuOpen = false; onface(); }}></button>

<div class="absolute top-3.5 right-4 z-[8] max-[520px]:top-2.5 max-[520px]:right-2.5">
  <button class="grid size-12 cursor-pointer place-items-center rounded-md border border-[rgba(220,238,231,0.25)] bg-[rgba(12,18,18,0.16)] text-beemo-text [text-shadow:0_1px_3px_rgba(0,0,0,0.9)] backdrop-blur-[2px] hover:border-beemo-mint hover:bg-[rgba(26,36,33,0.45)]" type="button" aria-label="Open options" title="Options" aria-expanded={menuOpen} on:click={() => { menuOpen = !menuOpen; }}>
    {#if menuOpen}<X size={23} />{:else}<MenuIcon size={25} />{/if}
  </button>
  {#if menuOpen}
    <nav class="absolute top-14 right-0 w-[250px] overflow-hidden rounded-md border border-[#52625d] bg-[rgba(20,25,24,0.98)] shadow-[0_16px_40px_rgba(0,0,0,0.45)] [&_button]:grid [&_button]:min-h-14 [&_button]:w-full [&_button]:cursor-pointer [&_button]:grid-cols-[28px_minmax(0,1fr)] [&_button]:items-center [&_button]:gap-2 [&_button]:border-0 [&_button]:border-b [&_button]:border-[#33403b] [&_button]:bg-transparent [&_button]:px-3 [&_button]:py-2 [&_button]:text-left [&_button]:text-beemo-text [&_button:hover]:bg-[#29322f] [&_button:last-child]:border-b-0" aria-label="Beemo options">
      <button type="button" on:click={() => choose(onprojects)}><FolderGit2 size={19} /><span class="grid gap-0.5"><strong class="text-sm">Add code repo</strong><small class="text-[0.7rem] text-beemo-muted">Manage local projects</small></span></button>
      <button type="button" on:click={() => choose(onsessions)}><MessagesSquare size={19} /><span class="grid gap-0.5"><strong class="text-sm">Sessions</strong><small class="text-[0.7rem] text-beemo-muted">Create, activate, or remove</small></span></button>
    </nav>
  {/if}
</div>

<svelte:window on:keydown={(event) => { if (event.key === "Escape") menuOpen = false; }} />
