<script lang="ts">
  import { ChevronLeft, Folder, GitBranch, Plus } from "@lucide/svelte";
  import type { DirectoryEntry } from "$lib/types";

  export let onadd: (path: string) => Promise<void>;

  let path = "";
  let parent = "";
  let roots: string[] = [];
  let directories: DirectoryEntry[] = [];
  let isGit = false;
  let loading = false;
  let error = "";

  export async function reset(): Promise<void> {
    await browse("");
  }

  async function browse(nextPath: string): Promise<void> {
    loading = true;
    error = "";
    try {
      const response = await fetch(`/api/directories?path=${encodeURIComponent(nextPath)}`, { cache: "no-store" });
      const payload = await response.json() as { path?: string; parent?: string; roots?: string[]; directories?: DirectoryEntry[]; isGit?: boolean; error?: string };
      if (!response.ok || payload.error) throw new Error(payload.error || "directory request failed");
      path = payload.path || "";
      parent = payload.parent || "";
      roots = payload.roots || [];
      directories = payload.directories || [];
      isGit = Boolean(payload.isGit);
      if (!path && roots.length === 1) await browse(roots[0]);
    } catch (caught) {
      error = caught instanceof Error ? caught.message : String(caught);
    } finally {
      loading = false;
    }
  }

  async function addCurrent(): Promise<void> {
    if (!path) return;
    loading = true;
    try {
      await onadd(path);
    } catch (caught) {
      error = caught instanceof Error ? caught.message : String(caught);
    } finally { loading = false; }
  }
</script>

<section class="min-h-60 overflow-hidden rounded-md border border-beemo-border bg-[#141918]" aria-label="Directory browser">
  <div class="grid grid-cols-[42px_minmax(0,1fr)_auto] items-center gap-2 border-b border-beemo-border bg-beemo-raised p-2 max-[600px]:grid-cols-[42px_minmax(0,1fr)]">
    <button class="grid size-10 cursor-pointer place-items-center rounded-sm border-0 bg-transparent text-beemo-text hover:bg-[#303a37] disabled:cursor-not-allowed disabled:opacity-40" type="button" aria-label="Parent directory" title="Parent directory" disabled={!parent} on:click={() => void browse(parent)}><ChevronLeft size={20} /></button>
    <div class="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap font-mono text-xs text-[#aebdb8]" title={path}>{path || "Choose a root"}</div>
    <button class="flex min-h-10 cursor-pointer items-center gap-2 rounded-sm border border-beemo-mint bg-beemo-mint px-3 font-bold text-[#10201b] disabled:cursor-not-allowed disabled:opacity-40 max-[600px]:col-span-full max-[600px]:justify-center" type="button" disabled={!path || loading} on:click={() => void addCurrent()}><Plus size={17} /> Add this folder</button>
  </div>
  {#if error}<p class="m-0 p-6 text-center text-[#ffb6ad]" role="alert">{error}</p>{/if}
  {#if !path}
    <div class="grid max-h-80 overflow-y-auto">
      {#each roots as root}
        <button class="grid min-h-12 cursor-pointer grid-cols-[24px_minmax(0,1fr)_24px] items-center gap-2 border-0 border-b border-[#2c3733] bg-transparent px-3 py-2 text-left text-beemo-text hover:bg-beemo-raised" type="button" on:click={() => void browse(root)}><Folder size={19} /><span class="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap">{root}</span></button>
      {/each}
    </div>
  {:else if directories.length > 0}
    <div class="grid max-h-80 overflow-y-auto">
      {#each directories as directory}
        <button class="grid min-h-12 cursor-pointer grid-cols-[24px_minmax(0,1fr)_24px] items-center gap-2 border-0 border-b border-[#2c3733] bg-transparent px-3 py-2 text-left text-beemo-text hover:bg-beemo-raised" type="button" on:click={() => void browse(directory.path)}>
          <Folder size={19} /><span class="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap">{directory.name}</span>{#if directory.isGit}<GitBranch class="text-beemo-mint" size={15} aria-label="Git repository" />{/if}
        </button>
      {/each}
    </div>
  {:else if !loading}
    <p class="m-0 p-6 text-center text-beemo-muted">No subdirectories.</p>
  {/if}
  {#if loading}<div class="p-6 text-center text-beemo-muted">Loading…</div>{/if}
</section>
