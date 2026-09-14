<script lang="ts">
  import { SendHorizontal, Square } from "@lucide/svelte";

  export let value = "";
  export let busy = false;
  export let onsubmit: () => void;
</script>

<form
  class="absolute right-1/2 bottom-[calc(12px+env(safe-area-inset-bottom))] z-[7] grid w-[min(760px,calc(100%-28px))] translate-x-1/2 grid-cols-[minmax(0,1fr)_54px] gap-2 rounded-md border border-[rgba(220,238,231,0.14)] bg-[rgba(13,18,17,0.08)] p-2 [text-shadow:0_1px_4px_rgba(0,0,0,0.95)] backdrop-blur-[1px] max-[520px]:w-[calc(100%-16px)]"
  aria-label="Message composer"
  on:submit|preventDefault={onsubmit}
>
  <textarea
    class="h-12 max-h-28 min-h-12 w-full resize-none overflow-y-auto rounded-sm border border-[rgba(220,238,231,0.18)] bg-[rgba(13,18,17,0.08)] px-3 py-2.5 leading-[1.35] text-beemo-text outline-0 placeholder:text-[#c2d0cb] focus:border-beemo-mint focus:bg-[rgba(13,18,17,0.22)]"
    bind:value
    rows="2"
    placeholder="Message Beemo"
    aria-label="Message to Beemo"
    on:keydown={(event) => {
      if (event.key === "Enter" && !event.shiftKey) {
        event.preventDefault();
        onsubmit();
      }
    }}
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
