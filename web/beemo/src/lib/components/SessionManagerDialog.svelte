<script lang="ts">
  import { tick } from "svelte";
  import { Check, CheckSquare2, Edit3, Plus, Square, Trash2, X } from "@lucide/svelte";
  import type { SessionSummary } from "$lib/types";
  import ManagementTable from "./ManagementTable.svelte";

  export let activeSessionId = "";
  export let onactivate: (session: SessionSummary) => void;

  let dialog: HTMLDialogElement;
  let sessions: SessionSummary[] = [];
  let selected: string[] = [];
  let editingId = "";
  let editName = "";
  let editInput: HTMLInputElement;
  let loading = false;
  let error = "";

  export async function open(): Promise<void> {
    selected = [];
    editingId = "";
    dialog.showModal();
    await loadSessions();
  }

  async function loadSessions(): Promise<void> {
    loading = true;
    error = "";
    try {
      const response = await fetch("/api/sessions", { cache: "no-store" });
      const payload = await response.json() as { sessions?: SessionSummary[]; activeSessionId?: string; error?: string };
      if (!response.ok || payload.error) throw new Error(payload.error || "session request failed");
      sessions = payload.sessions || [];
      activeSessionId = payload.activeSessionId || activeSessionId;
    } catch (caught) { error = caught instanceof Error ? caught.message : String(caught); }
    finally { loading = false; }
  }

  async function createSession(): Promise<void> {
    const response = await fetch("/api/sessions", { method: "POST" });
    const payload = await response.json() as { session?: SessionSummary; activeSessionId?: string; error?: string };
    if (!response.ok || payload.error || !payload.session) { error = payload.error || "session could not be created"; return; }
    activeSessionId = payload.activeSessionId || payload.session.sessionId;
    sessions = [payload.session, ...sessions];
    onactivate(payload.session);
    beginRename(payload.session);
  }

  async function activate(session: SessionSummary, event?: MouseEvent): Promise<void> {
    if (event && (event.target as HTMLElement).closest("button, input")) return;
    if (session.sessionId === activeSessionId) return;
    const response = await fetch("/api/session", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ sessionId: session.sessionId }) });
    const payload = await response.json() as { session?: SessionSummary; error?: string };
    if (!response.ok || payload.error || !payload.session) { error = payload.error || "session could not be activated"; return; }
    activeSessionId = payload.session.sessionId;
    selected = selected.filter((id) => id !== activeSessionId);
    onactivate(payload.session);
  }

  async function beginRename(session: SessionSummary): Promise<void> {
    editingId = session.sessionId;
    editName = session.name;
    await tick();
    editInput?.focus();
    editInput?.select();
  }

  async function saveRename(session: SessionSummary): Promise<void> {
    if (editingId !== session.sessionId) return;
    const name = editName.trim();
    if (!name) { error = "Session name cannot be empty."; return; }
    const response = await fetch("/api/sessions", { method: "PATCH", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ sessionId: session.sessionId, name }) });
    const payload = await response.json() as { session?: SessionSummary; error?: string };
    if (!response.ok || payload.error || !payload.session) { error = payload.error || "session could not be renamed"; return; }
    sessions = sessions.map((item) => item.sessionId === session.sessionId ? payload.session! : item);
    editingId = "";
  }

  function toggle(sessionId: string): void {
    selected = selected.includes(sessionId) ? selected.filter((id) => id !== sessionId) : [...selected, sessionId];
  }

  async function deleteSelected(): Promise<void> {
    if (selected.length === 0 || !window.confirm(`Permanently delete ${selected.length} selected session${selected.length === 1 ? "" : "s"}?`)) return;
    const response = await fetch("/api/sessions", { method: "DELETE", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ sessionIds: selected }) });
    const payload = await response.json() as { deleted?: number; activeSession?: SessionSummary; activeSessionId?: string; error?: string };
    if (!response.ok || payload.error) { error = payload.error || "sessions could not be deleted"; return; }
    selected = [];
    if (payload.activeSession) {
      activeSessionId = payload.activeSessionId || payload.activeSession.sessionId;
      onactivate(payload.activeSession);
    }
    await loadSessions();
  }

  function projectName(path: string): string {
    return path.split("/").filter(Boolean).at(-1) || "—";
  }
</script>

<dialog bind:this={dialog} class="max-h-[calc(100dvh-28px)] w-[min(980px,calc(100vw-28px))] rounded-lg border border-[#52625d] bg-beemo-panel p-0 text-beemo-text backdrop:bg-black/80 max-[600px]:h-dvh max-[600px]:max-h-none max-[600px]:w-screen max-[600px]:max-w-none max-[600px]:rounded-none max-[600px]:border-0" on:click={(event) => { if (event.target === dialog) dialog.close(); }}>
  <div class="grid gap-4 p-4.5 max-[600px]:px-2 max-[600px]:pb-6">
    <header class="flex items-center justify-between gap-3">
      <div><h2 class="m-0 text-lg font-semibold">Sessions</h2><p class="mt-1 text-xs text-beemo-muted">Activate, name, or remove saved conversations.</p></div>
      <button class="grid size-11 shrink-0 cursor-pointer place-items-center rounded-sm border-0 bg-transparent text-beemo-text hover:bg-[#303a37]" type="button" aria-label="Close" title="Close" on:click={() => dialog.close()}><X size={22} /></button>
    </header>
    <div class="flex items-center justify-between gap-3">
      <button class="flex min-h-11 cursor-pointer items-center gap-2 rounded-sm border border-beemo-mint bg-beemo-mint px-3 font-bold text-[#10201b]" type="button" on:click={() => void createSession()}><Plus size={18} /> New session</button>
      <button class="flex min-h-11 cursor-pointer items-center gap-2 rounded-sm border border-[#754a45] bg-[#2a201f] px-3 text-[#ffc0b8] disabled:cursor-not-allowed disabled:opacity-40" type="button" disabled={selected.length === 0} on:click={() => void deleteSelected()}><Trash2 size={17} /> Delete {selected.length > 0 ? selected.length : ""}</button>
    </div>
    {#if error}<div class="rounded-sm border border-[rgba(255,182,173,0.4)] bg-[rgba(120,30,24,0.15)] px-3 py-2.5 text-[#ffb6ad]" role="alert">{error}</div>{/if}
    <ManagementTable label="Beemo sessions" empty={!loading && sessions.length === 0} emptyText="No saved sessions.">
      <thead><tr><th class="w-11 text-center"><span class="sr-only">Select</span></th><th>Name</th><th>Mode</th><th class="optional">Project</th><th class="optional">Updated</th><th class="w-12 px-1.5 text-center"><span class="sr-only">Rename</span></th></tr></thead>
      <tbody>
        {#each sessions as session (session.sessionId)}
          <tr class:active={session.sessionId === activeSessionId} class:selected={selected.includes(session.sessionId)} class="clickable" aria-selected={selected.includes(session.sessionId)} on:click={(event) => void activate(session, event)}>
            <td class="w-12 p-0 text-center">
              <button
                class="inline-grid size-11 cursor-pointer place-items-center rounded-sm border-0 bg-transparent text-[#9eb0aa] hover:bg-[#303a37] hover:text-beemo-text aria-pressed:text-beemo-gold"
                type="button"
                aria-label={`${selected.includes(session.sessionId) ? "Deselect" : "Select"} ${session.name}`}
                aria-pressed={selected.includes(session.sessionId)}
                title={`${selected.includes(session.sessionId) ? "Deselect" : "Select"} ${session.name}`}
                on:click|stopPropagation={() => toggle(session.sessionId)}
              >
                {#if selected.includes(session.sessionId)}<CheckSquare2 size={21} />{:else}<Square size={21} />{/if}
              </button>
            </td>
            <td>
              {#if editingId === session.sessionId}
                <input class="min-h-9 w-[min(300px,100%)] rounded-sm border border-beemo-mint bg-[#242b29] px-2 py-1.5 text-beemo-text" bind:this={editInput} bind:value={editName} maxlength="80" aria-label="Session name" on:blur={() => void saveRename(session)} on:keydown={(event) => { if (event.key === "Enter") { event.preventDefault(); void saveRename(session); } else if (event.key === "Escape") editingId = ""; }} />
              {:else}
                <div class="flex flex-wrap items-center gap-2"><strong>{session.name}</strong>{#if session.sessionId === activeSessionId}<span class="inline-flex items-center gap-1 text-[0.68rem] uppercase text-beemo-mint"><Check size={13} /> Active</span>{/if}</div>
              {/if}
            </td>
            <td><span class:border-[#8b7241]={session.mode === "code"} class:text-beemo-gold={session.mode === "code"} class="inline-block min-w-12 rounded-sm border border-[#52625d] px-1.5 py-1 text-center text-[0.7rem] uppercase text-[#b9c8c3]">{session.mode}</span></td>
            <td class="optional text-beemo-muted">{projectName(session.workspace)}</td>
            <td class="optional text-beemo-muted">{new Date(session.updatedUnixMs).toLocaleString()}</td>
            <td class="w-12 px-1.5 text-center"><button class="inline-grid size-10 cursor-pointer place-items-center rounded-sm border-0 bg-transparent text-[#b9c8c3] hover:bg-[#303a37] hover:text-beemo-text" type="button" aria-label={`Rename ${session.name}`} title="Rename session" on:click={() => void beginRename(session)}><Edit3 size={17} /></button></td>
          </tr>
        {/each}
      </tbody>
    </ManagementTable>
  </div>
</dialog>
