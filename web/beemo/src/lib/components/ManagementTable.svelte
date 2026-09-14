<script lang="ts">
  export let label: string;
  export let empty = false;
  export let emptyText = "Nothing here yet.";
</script>

<div class="w-full overflow-auto rounded-md border border-beemo-border bg-[#141918] text-sm [&_th]:bg-beemo-raised [&_th]:px-3 [&_th]:py-2.5 [&_th]:text-left [&_th]:text-[0.7rem] [&_th]:font-semibold [&_th]:uppercase [&_th]:text-[#9eb0aa] [&_td]:border-t [&_td]:border-[#33403b] [&_td]:px-3 [&_td]:py-2.5 [&_td]:align-middle [&_tr.clickable]:cursor-pointer [&_tr.clickable:hover]:bg-beemo-raised [&_tr.active]:shadow-[inset_3px_0_#66d9b4] [&_tr.active]:bg-[rgba(102,217,180,0.1)] [&_tr.selected]:bg-[rgba(221,185,100,0.1)] max-[680px]:[&_th.optional]:hidden max-[680px]:[&_td.optional]:hidden" role="region" aria-label={label}>
  {#if empty}
    <p class="m-0 px-5 py-7 text-center text-beemo-muted">{emptyText}</p>
  {:else}
    <table class="w-full border-collapse">
      <slot></slot>
    </table>
  {/if}
</div>
