<script lang="ts">
  import { afterUpdate } from "svelte";
  import { ArrowDown, FileDiff, Maximize2, Mic, Minimize2, RefreshCw, X } from "@lucide/svelte";
  import type { ChatMessage } from "$lib/types";
  import MessageText from "./MessageText.svelte";

  export let messages: ChatMessage[];
  export let activity = "";
  export let notice = "";
  export let noticeTone: "normal" | "warning" | "error" = "normal";
  export let status = "idle";
  export let voiceStatus = "voice connecting";
  export let mode: "normal" | "code" = "normal";
  export let workspace = "";
  export let internetEnabled = false;
  export let expanded = false;
  export let onexpand: () => void;
  export let oncollapse: () => void;
  export let onclose: () => void;
  export let onhold: () => void;
  export let onrelease: () => void;
  export let onswitch: () => void;
  export let ondiff: () => void;

  let messageList: HTMLDivElement;
  let pinnedToLatest = true;
  let previousMessageCount = 0;
  let previousActivity = "";

  afterUpdate(() => {
    if (!messageList) return;
    if (pinnedToLatest && (messages.length !== previousMessageCount || activity !== previousActivity)) {
      messageList.scrollTop = messageList.scrollHeight;
    }
    previousMessageCount = messages.length;
    previousActivity = activity;
  });

  function handleScroll(): void {
    const remaining = messageList.scrollHeight - messageList.scrollTop - messageList.clientHeight;
    pinnedToLatest = remaining < 72;
    onhold();
    onrelease();
  }

  function jumpToLatest(): void {
    pinnedToLatest = true;
    messageList.scrollTo({ top: messageList.scrollHeight, behavior: "smooth" });
    onhold();
  }

  function projectName(): string {
    return workspace.split("/").filter(Boolean).at(-1) || "No project";
  }
</script>

<section
  class={`absolute z-[6] flex overflow-hidden rounded-md border text-beemo-text transition-[inset,width,background-color,opacity] duration-200 ${expanded
    ? "inset-x-4 top-4 bottom-[96px] border-[rgba(220,238,231,0.35)] bg-[rgba(13,18,17,0.96)] shadow-[0_16px_48px_rgba(0,0,0,0.42)] backdrop-blur-xl max-[700px]:inset-x-2 max-[700px]:top-16 max-[700px]:bottom-[88px]"
    : "top-[clamp(76px,12vh,110px)] bottom-[104px] left-4 w-[min(34vw,460px)] min-w-[300px] border-[rgba(220,238,231,0.14)] bg-[rgba(13,18,17,0.08)] [text-shadow:0_1px_4px_rgba(0,0,0,0.95)] backdrop-blur-[1px] max-[700px]:inset-x-2 max-[700px]:top-[72px] max-[700px]:bottom-[92px] max-[700px]:w-auto max-[700px]:min-w-0"}`}
  aria-label="Conversation"
  on:pointerenter={onhold}
  on:pointerleave={onrelease}
  on:focusin={onhold}
  on:focusout={onrelease}
>
  <div class="grid min-h-0 w-full grid-rows-[auto_minmax(0,1fr)]">
    <header class="flex min-h-12 items-center justify-between gap-3 border-b border-[rgba(220,238,231,0.18)] px-3">
      <div class="min-w-0">
        <strong class="block overflow-hidden text-sm text-ellipsis whitespace-nowrap">{mode === "code" ? `Code · ${projectName()}` : "Normal"}{internetEnabled ? " · Internet" : ""}</strong>
        <div class="flex min-w-0 items-center gap-1.5">
          {#if status === "listening"}
            <Mic class="shrink-0 animate-pulse text-beemo-gold" size={12} aria-hidden="true" />
          {/if}
          <span
            class:text-[#ffd27a]={noticeTone === "warning" || status === "listening"}
            class:text-[#ff9e93]={noticeTone === "error"}
            class:text-[#c2d0cb]={noticeTone === "normal" && status !== "listening"}
            class="block overflow-hidden text-[0.68rem] text-ellipsis whitespace-nowrap"
          >{notice || activity || (status === "idle" ? `${status} · ${voiceStatus}` : status)}</span>
        </div>
      </div>
      <div class="flex shrink-0 gap-1">
        {#if mode === "code"}
          <button class="grid size-10 cursor-pointer place-items-center rounded-sm border-0 bg-transparent text-beemo-text hover:bg-[rgba(102,217,180,0.13)]" type="button" aria-label="Switch project" title="Switch project" on:click={onswitch}><RefreshCw size={18} /></button>
          <button class="grid size-10 cursor-pointer place-items-center rounded-sm border-0 bg-transparent text-beemo-text hover:bg-[rgba(102,217,180,0.13)]" type="button" aria-label="View working tree diff" title="View diff" on:click={ondiff}><FileDiff size={18} /></button>
        {/if}
        <button
          class="grid size-10 cursor-pointer place-items-center rounded-sm border-0 bg-transparent text-beemo-text hover:bg-[rgba(102,217,180,0.13)]"
          type="button"
          aria-label={expanded ? "Collapse conversation" : "Expand conversation"}
          title={expanded ? "Collapse" : "Expand"}
          on:click={expanded ? oncollapse : onexpand}
        >
          {#if expanded}<Minimize2 size={18} />{:else}<Maximize2 size={18} />{/if}
        </button>
        <button
          class="grid size-10 cursor-pointer place-items-center rounded-sm border-0 bg-transparent text-beemo-text hover:bg-[rgba(210,74,62,0.16)] hover:text-[#ffb6ad]"
          type="button"
          aria-label="Hide conversation"
          title="Hide conversation"
          on:click={onclose}
        ><X size={19} /></button>
      </div>
    </header>

    <div class="relative min-h-0">
      <div
        bind:this={messageList}
        class="h-full overflow-y-auto overscroll-contain px-3"
        aria-live="polite"
        on:scroll={handleScroll}
      >
        {#each messages as message}
          <article class="border-b border-[rgba(220,238,231,0.14)] py-3.5 last:border-b-0">
            <span class:text-beemo-gold={message.role === "user"} class:text-beemo-mint={message.role === "assistant"} class="mb-1.5 block text-[0.68rem] font-bold tracking-[0.08em] uppercase">{message.role === "user" ? "You" : "Beemo"}</span>
            <p class="m-0 [overflow-wrap:anywhere] whitespace-pre-wrap text-sm leading-6 text-[#e2eeea]"><MessageText content={message.content} /></p>
          </article>
        {/each}
      </div>

      {#if !pinnedToLatest}
        <button
          class="absolute right-3 bottom-3 flex h-10 cursor-pointer items-center gap-1.5 rounded-full border border-[#52625d] bg-[#202725] px-3 text-xs font-semibold text-beemo-text shadow-lg hover:border-beemo-mint"
          type="button"
          on:click={jumpToLatest}
        ><ArrowDown size={16} />Latest</button>
      {/if}
    </div>
  </div>
</section>
