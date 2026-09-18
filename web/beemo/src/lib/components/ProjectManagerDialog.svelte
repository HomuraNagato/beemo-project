<script lang="ts">
  import { FolderPlus, GitBranch, Trash2, X } from "@lucide/svelte";
  import type { CodeProject } from "$lib/types";
  import DirectoryBrowser from "./DirectoryBrowser.svelte";
  import ManagementTable from "./ManagementTable.svelte";

  export let sessionId = "";
  export let activeWorkspace = "";
  export let onstate: (mode: "normal" | "code", workspace: string) => void;
  export let onselect: (project: CodeProject) => Promise<void>;

  let dialog: HTMLDialogElement;
  let browser: DirectoryBrowser;
  let projects: CodeProject[] = [];
  let loading = false;
  let error = "";

  export async function open(): Promise<void> {
    dialog.showModal();
    await Promise.all([loadProjects(), browser.reset()]);
  }

  export async function refresh(): Promise<void> {
    await loadProjects();
  }

  async function loadProjects(): Promise<void> {
    loading = true;
    error = "";
    try {
      const response = await fetch("/api/projects", { cache: "no-store" });
      const payload = await response.json() as { projects?: CodeProject[]; error?: string };
      if (!response.ok || payload.error) throw new Error(payload.error || "project request failed");
      projects = payload.projects || [];
    } catch (caught) {
      error = caught instanceof Error ? caught.message : String(caught);
    } finally { loading = false; }
  }

  async function addProject(path: string): Promise<void> {
    const response = await fetch("/api/projects", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ path }) });
    const payload = await response.json() as { error?: string };
    if (!response.ok || payload.error) throw new Error(payload.error || "project could not be added");
    await loadProjects();
  }

  async function removeProject(project: CodeProject): Promise<void> {
    if (!window.confirm(`Remove ${project.name} from Beemo?\n\nNo files will be deleted.`)) return;
    const response = await fetch("/api/projects", { method: "DELETE", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ path: project.path, sessionId }) });
    const payload = await response.json() as { mode?: "normal" | "code"; workspace?: string; error?: string };
    if (!response.ok || payload.error) { error = payload.error || "project could not be removed"; return; }
    if (payload.mode) onstate(payload.mode, payload.workspace || "");
    await loadProjects();
  }
</script>

<dialog bind:this={dialog} class="max-h-[calc(100dvh-28px)] w-[min(980px,calc(100vw-28px))] rounded-lg border border-[#52625d] bg-beemo-panel p-0 text-beemo-text backdrop:bg-black/80 max-[600px]:h-dvh max-[600px]:max-h-none max-[600px]:w-screen max-[600px]:max-w-none max-[600px]:rounded-none max-[600px]:border-0" on:click={(event) => { if (event.target === dialog) dialog.close(); }}>
  <div class="grid gap-5.5 p-4.5 max-[600px]:px-2.5 max-[600px]:pb-6">
    <header class="flex items-start justify-between gap-4.5"><div><h2 class="m-0 text-lg font-semibold">Code repositories</h2><p class="mt-1 text-xs text-beemo-muted">Choose which local projects Beemo may use in Code mode.</p></div><button class="grid size-11 shrink-0 cursor-pointer place-items-center rounded-sm border-0 bg-transparent text-beemo-text hover:bg-[#303a37]" type="button" aria-label="Close" title="Close" on:click={() => dialog.close()}><X size={22} /></button></header>
    {#if error}<div class="rounded-sm border border-[rgba(255,182,173,0.4)] bg-[rgba(120,30,24,0.15)] px-3 py-2.5 text-[#ffb6ad]" role="alert">{error}</div>{/if}
    <section>
      <h3 class="mb-2 flex items-center gap-2 text-xs font-semibold text-[#c9d8d3]">Added projects</h3>
      <ManagementTable label="Added code projects" empty={!loading && projects.length === 0} emptyText="No projects have been added.">
        <thead><tr><th>Project</th><th class="optional">Path</th><th class="w-12 px-1.5 text-center"><span class="sr-only">Remove</span></th></tr></thead>
        <tbody>
          {#each projects as project}
            <tr class="clickable" class:active={project.path === activeWorkspace} on:click={(event) => { if (!(event.target as HTMLElement).closest("button")) void onselect(project); }}><td><div class="flex items-center gap-2 text-[#dce8e4]">{#if project.isGit}<GitBranch class="text-beemo-mint" size={15} />{/if}<strong>{project.name}</strong></div></td><td class="optional max-w-[440px] overflow-hidden text-ellipsis whitespace-nowrap text-[#aebdb8]" title={project.path}>{project.path}</td><td class="w-12 px-1.5 text-center"><button class="inline-grid size-10 cursor-pointer place-items-center rounded-sm border-0 bg-transparent text-[#b9c8c3] hover:bg-[rgba(210,74,62,0.15)] hover:text-[#ffb6ad]" type="button" aria-label={`Remove ${project.name}`} title="Remove project" on:click={() => void removeProject(project)}><Trash2 size={18} /></button></td></tr>
          {/each}
        </tbody>
      </ManagementTable>
    </section>
    <section>
      <h3 class="mb-2 flex items-center gap-2 text-xs font-semibold text-[#c9d8d3]"><FolderPlus size={17} /> Add a directory</h3>
      <DirectoryBrowser bind:this={browser} onadd={addProject} />
    </section>
  </div>
</dialog>
