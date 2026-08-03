<script lang="ts">
  import { onMount } from "svelte";
  import FaceDisplay from "$lib/components/FaceDisplay.svelte";
  import { cleanReply, expressionFromText } from "$lib/expressions";
  import type { AgentEvent, ChatMessage, FaceExpression, SessionSummary, StateUpdate, WakeEvent } from "$lib/types";

  type Mode = "chat" | "code";

  let expression: FaceExpression = "calm";
  let talking = false;
  let status = "idle";
  let voiceStatus = "voice connecting";
  let mode: Mode = "chat";
  let workspace = "";
  let workspaces: string[] = [];
  let sessions: SessionSummary[] = [];
  let sessionId = "";
  let messages: ChatMessage[] = [];
  let visibleMessages: ChatMessage[] = [{ role: "assistant", content: "Hi. I am listening." }];
  let activity = "";
  let composer = "";
  let activeController: AbortController | null = null;
  let diffDialog: HTMLDialogElement;
  let diffOutput = "";
  let talkingTimer: ReturnType<typeof setTimeout> | undefined;
  let stateEvents: EventSource | undefined;
  let wakeEvents: EventSource | undefined;
  let runtimeEvents: EventSource | undefined;
  let latestSessionEventTimestamp = 0;

  onMount(() => {
    void initialize();
    runtimeEvents = connectRuntimeEvents();
    return () => {
      runtimeEvents?.close();
      wakeEvents?.close();
      stateEvents?.close();
      if (talkingTimer) clearTimeout(talkingTimer);
    };
  });

  async function initialize(): Promise<void> {
    await Promise.all([loadWorkspaces(), loadSessions()]);
  }

  function connectRuntimeEvents(): EventSource {
    const events = new EventSource("/api/runtime-events");
    events.addEventListener("session", (rawEvent) => {
      const payload = JSON.parse((rawEvent as MessageEvent<string>).data) as { sessionId?: string };
      if (payload.sessionId) activateSession(payload.sessionId);
    });
    return events;
  }

  function activateSession(nextSessionId: string): void {
    if (nextSessionId === sessionId && stateEvents && wakeEvents) return;
    sessionId = nextSessionId;
    messages = [];
    visibleMessages = [{ role: "assistant", content: "Hi. I am listening." }];
    activity = "";
    expression = "calm";
    status = "new session";
    latestSessionEventTimestamp = 0;
    stateEvents = connectStateEvents();
    wakeEvents = connectWakeEvents();
    void syncSession();
  }

  function connectWakeEvents(): EventSource {
    wakeEvents?.close();
    const events = new EventSource(`/api/wake-events?session_id=${encodeURIComponent(sessionId)}`);
    events.addEventListener("service", (rawEvent) => {
      const payload = JSON.parse((rawEvent as MessageEvent<string>).data) as { status?: string };
      if (payload.status === "ready") voiceStatus = "voice ready";
      else if (payload.status === "offline") voiceStatus = "voice off";
      else voiceStatus = "voice connecting";
    });
    events.addEventListener("wake", (rawEvent) => {
      const event = JSON.parse((rawEvent as MessageEvent<string>).data) as WakeEvent;
      handleWakeEvent(event);
    });
    events.onerror = () => {
      voiceStatus = "voice off";
    };
    return events;
  }

  function connectStateEvents(): EventSource {
    stateEvents?.close();
    const events = new EventSource(`/api/state-events?session_id=${encodeURIComponent(sessionId)}`);
    events.addEventListener("state", (rawEvent) => {
      handleStateUpdate(JSON.parse((rawEvent as MessageEvent<string>).data) as StateUpdate);
    });
    events.onerror = () => {
      status = "sync offline";
    };
    return events;
  }

  function handleStateUpdate(event: StateUpdate): void {
    if (event.sessionId !== sessionId || event.timestampUnixMs <= latestSessionEventTimestamp) return;
    latestSessionEventTimestamp = event.timestampUnixMs;
    const content = cleanReply(event.message);
    if (event.state === "user" || event.state === "assistant") {
      const latest = messages.at(-1);
      if (content && (latest?.role !== event.state || latest.content !== content)) {
        messages = [...messages, { role: event.state, content }];
      }
      showMessage(event.state, content);
    }
    if (event.state === "user" || event.state === "status") {
      expression = "thinking";
      status = event.message || "thinking";
    } else if (event.state === "assistant") {
      expression = expressionFromText(content);
      status = "responding";
      animateTalking(content);
    } else if (event.state === "error") {
      expression = "worried";
      status = "error";
    }
  }

  function handleWakeEvent(event: WakeEvent): void {
    voiceStatus = "voice ready";
    if (!event.transcript && !event.prompt && !event.response) {
      expression = "listening";
      status = "listening";
      return;
    }
    const prompt = event.prompt || event.transcript;
    if (prompt) {
      showMessage("user", prompt);
      expression = "thinking";
      status = "thinking";
    }
    if (event.response) {
      showMessage("assistant", cleanReply(event.response));
      expression = event.status === "error" ? "worried" : expressionFromText(event.response);
      status = event.status || "idle";
      animateTalking(event.response);
    }
  }

  function showMessage(role: ChatMessage["role"], content: string): void {
    const latest = visibleMessages.at(-1);
    if (latest?.role === role && latest.content === content) return;
    visibleMessages = [...visibleMessages, { role, content }].slice(-2);
  }

  function showActivity(value: string): void {
    activity = value;
  }

  function animateTalking(text: string): void {
    if (talkingTimer) clearTimeout(talkingTimer);
    talking = true;
    const duration = Math.min(6000, Math.max(1200, text.split(/\s+/).length * 170));
    talkingTimer = setTimeout(() => {
      talking = false;
      status = "idle";
    }, duration);
  }

  async function sendMessage(text: string): Promise<void> {
    messages = [...messages, { role: "user", content: text }];
    showMessage("user", text);
    expression = "thinking";
    status = "thinking";
    activeController = new AbortController();

    const response = await fetch("/api/agent", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      signal: activeController.signal,
      body: JSON.stringify({ sessionId, messages, mode, workspace }),
    });
    if (!response.ok || !response.body) {
      const payload = await response.json() as { error?: string };
      throw new Error(payload.error || `request failed: ${response.status}`);
    }

    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    let reply = "";
    while (true) {
      const { value, done } = await reader.read();
      buffer += decoder.decode(value || new Uint8Array(), { stream: !done });
      const lines = buffer.split("\n");
      buffer = lines.pop() || "";
      for (const line of lines) {
        if (!line.trim()) continue;
        const eventReply = await handleAgentEvent(JSON.parse(line) as AgentEvent);
        if (eventReply) reply = eventReply;
      }
      if (done) break;
    }
    if (buffer.trim()) {
      const eventReply = await handleAgentEvent(JSON.parse(buffer) as AgentEvent);
      if (eventReply) reply = eventReply;
    }

    reply = reply.trim() || "(empty response)";
    messages = [...messages, { role: "assistant", content: reply }];
    expression = expressionFromText(reply);
    showMessage("assistant", cleanReply(reply));
    activity = "";
    status = "responding";
    animateTalking(reply);
    activeController = null;
    await loadSessions();
  }

  async function handleAgentEvent(event: AgentEvent): Promise<string> {
    switch (event.type) {
      case "assistant":
        return event.text || "";
      case "tool_start":
        expression = "thinking";
        showActivity(`Running ${event.tool}`);
        break;
      case "tool_result":
        showActivity(event.text ? `${event.tool}: ${event.text.slice(0, 180)}` : `${event.tool} complete`);
        break;
      case "file_change":
        showActivity("Workspace changed");
        break;
      case "approval": {
        const approved = window.confirm(`${event.text}\n\nAllow ${event.tool}?`);
        const response = await fetch("/api/approve", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ sessionId, approvalId: event.approvalId, approved }),
        });
        if (!response.ok) throw new Error("approval request failed");
        showActivity(approved ? `${event.tool} approved` : `${event.tool} denied`);
        break;
      }
      case "error":
        throw new Error(event.text || "agent failed");
    }
    return "";
  }

  async function submit(): Promise<void> {
    if (activeController) {
      activeController.abort();
      activeController = null;
      status = "cancelled";
      return;
    }
    const text = composer.trim();
    if (!text) return;
    if (mode === "code" && !workspace) {
      showMessage("assistant", "Select a repository before using Code mode.");
      return;
    }
    composer = "";
    try {
      await sendMessage(text);
    } catch (error) {
      if (error instanceof DOMException && error.name === "AbortError") return;
      expression = "worried";
      status = "offline";
      showMessage("assistant", error instanceof Error ? error.message : String(error));
    } finally {
      activeController = null;
    }
  }

  function setMode(nextMode: Mode): void {
    mode = nextMode;
    if (mode === "chat") workspace = "";
    status = mode;
  }

  async function loadWorkspaces(): Promise<void> {
    try {
      const response = await fetch("/api/workspaces", { cache: "no-store" });
      const payload = await response.json() as { workspaces?: string[]; error?: string };
      if (!response.ok || payload.error) throw new Error(payload.error || "workspace request failed");
      workspaces = payload.workspaces || [];
    } catch (error) {
      showActivity(`Repositories unavailable: ${error instanceof Error ? error.message : String(error)}`);
    }
  }

  async function loadSessions(): Promise<void> {
    try {
      const response = await fetch("/api/sessions", { cache: "no-store" });
      const payload = await response.json() as { sessions?: SessionSummary[]; error?: string };
      if (!response.ok || payload.error) throw new Error(payload.error || "session request failed");
      sessions = payload.sessions || [];
    } catch (error) {
      showActivity(`Sessions unavailable: ${error instanceof Error ? error.message : String(error)}`);
    }
  }

  async function resumeSession(nextSessionId: string): Promise<void> {
    if (!nextSessionId) {
      startNewSession();
      return;
    }
    const response = await fetch(`/api/session?id=${encodeURIComponent(nextSessionId)}`, { cache: "no-store" });
    const payload = await response.json() as { session: SessionSummary; events: AgentEvent[]; error?: string };
    if (!response.ok || payload.error) throw new Error(payload.error || "session could not be loaded");
    sessionId = payload.session.sessionId;
    stateEvents = connectStateEvents();
    wakeEvents = connectWakeEvents();
    mode = payload.session.mode;
    workspace = payload.session.workspace;
    messages = payload.events
      .filter((event) => event.type === "user" || event.type === "assistant")
      .map((event) => ({ role: event.type as ChatMessage["role"], content: cleanReply(event.text) }));
    visibleMessages = messages.slice(-2);
    latestSessionEventTimestamp = payload.events.at(-1)?.timestampUnixMs || 0;
    if (visibleMessages.length === 0) visibleMessages = [{ role: "assistant", content: "Session resumed." }];
    status = "resumed";
  }

  async function syncSession(): Promise<void> {
    if (activeController || !sessionId) return;
    try {
      const response = await fetch(`/api/session?id=${encodeURIComponent(sessionId)}`, { cache: "no-store" });
      if (!response.ok) return;
      const payload = await response.json() as { session: SessionSummary; events: AgentEvent[] };
      mode = payload.session.mode;
      workspace = payload.session.workspace;
      messages = payload.events
        .filter((event) => event.type === "user" || event.type === "assistant")
        .map((event) => ({ role: event.type as ChatMessage["role"], content: cleanReply(event.text) }));
      visibleMessages = messages.slice(-2);
      latestSessionEventTimestamp = payload.events.at(-1)?.timestampUnixMs || 0;
    } catch {
      status = "offline";
    }
  }

  function startNewSession(): void {
    sessionId = `web-${Date.now()}`;
    stateEvents = connectStateEvents();
    wakeEvents = connectWakeEvents();
    messages = [];
    visibleMessages = [{ role: "assistant", content: "Hi. I am listening." }];
    activity = "";
    expression = "calm";
    status = mode;
    latestSessionEventTimestamp = 0;
  }

  async function showDiff(): Promise<void> {
    if (mode !== "code" || !workspace) return;
    diffOutput = "Loading diff...";
    diffDialog.showModal();
    const response = await fetch("/api/diff", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ sessionId, workspace }),
    });
    const payload = await response.json() as { diff?: string; error?: string };
    diffOutput = response.ok && !payload.error ? payload.diff || "No working tree changes." : payload.error || "Diff request failed.";
  }

</script>

<svelte:head>
  <title>Beemo</title>
  <meta name="description" content="Local Beemo assistant interface" />
  <link rel="icon" href="/assets/display-texture.jpg" />
</svelte:head>

<main class="shell" aria-label="Beemo interface">
  <section class="face-region" aria-label="Beemo face display">
    <FaceDisplay {expression} {talking} />

    <header class="top-bar">
      <div class="brand">
        <strong>Beemo</strong>
        <span>{expression}</span>
      </div>
      <div class="status-row" aria-live="polite">
        <span>{status}</span>
        <span class:online={voiceStatus === "voice ready"}>{voiceStatus}</span>
      </div>
    </header>

    <div class="studio-toolbar" aria-label="Assistant controls">
      <div class="mode-control" role="group" aria-label="Assistant mode">
        <button type="button" class:active={mode === "chat"} on:click={() => setMode("chat")}>Chat</button>
        <button type="button" class:active={mode === "code"} on:click={() => setMode("code")}>Code</button>
      </div>
      {#if mode === "code"}
        <label>
          <span>Repository</span>
          <select bind:value={workspace}>
            <option value="">Select a repository</option>
            {#each workspaces as item}
              <option value={item}>{item.split("/").filter(Boolean).at(-1) || item}</option>
            {/each}
          </select>
        </label>
      {/if}
      <label>
        <span>Session</span>
        <select value={sessionId} on:change={(event) => void resumeSession(event.currentTarget.value)}>
          <option value="">New session</option>
          {#each sessions as session}
            <option value={session.sessionId}>{session.workspace.split("/").filter(Boolean).at(-1) || "chat"} · {new Date(session.updatedUnixMs).toLocaleString()}</option>
          {/each}
        </select>
      </label>
      <button type="button" on:click={startNewSession}>New</button>
      <button type="button" disabled={mode !== "code" || !workspace} on:click={() => void showDiff()}>Diff</button>
    </div>
  </section>

  <section class="conversation-region" aria-label="Conversation">
    {#if activity}<div class="activity">{activity}</div>{/if}
    <div class="messages" aria-live="polite">
      {#each visibleMessages as message, index}
        <p class:previous={index < visibleMessages.length - 1} class:user={message.role === "user"}>{message.content}</p>
      {/each}
    </div>
    <form on:submit|preventDefault={() => void submit()}>
      <textarea bind:value={composer} rows="2" placeholder="Message Beemo" aria-label="Message to Beemo" on:keydown={(event) => {
        if (event.key === "Enter" && !event.shiftKey) {
          event.preventDefault();
          void submit();
        }
      }}></textarea>
      <button class="send-button" type="submit">{activeController ? "Stop" : "Send"}</button>
    </form>
  </section>
</main>

<dialog bind:this={diffDialog} class="diff-dialog">
  <header>
    <h2>Working tree diff</h2>
    <button type="button" aria-label="Close diff" on:click={() => diffDialog.close()}>&times;</button>
  </header>
  <pre>{diffOutput}</pre>
</dialog>

<style>
  :global(*) { box-sizing: border-box; }
  :global(html), :global(body) { height: 100%; margin: 0; }
  :global(body) { background: #111413; color: #edf7f2; font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
  :global(button), :global(textarea), :global(select) { font: inherit; }
  :global(button:focus-visible), :global(textarea:focus-visible), :global(select:focus-visible) { outline: 2px solid #f0c36a; outline-offset: 2px; }

  .shell { display: grid; grid-template-rows: minmax(300px, 58vh) minmax(250px, 1fr); height: 100dvh; min-height: 550px; overflow: hidden; }
  .face-region { position: relative; min-height: 0; overflow: hidden; border-bottom: 1px solid #41504b; }
  .top-bar { position: absolute; inset: 0 0 auto; display: flex; justify-content: space-between; align-items: flex-start; padding: 18px 20px; pointer-events: none; }
  .brand { display: grid; gap: 3px; text-shadow: 0 2px 10px #00110d; }
  .brand strong { font-size: 1.45rem; }
  .brand span { color: #f0c36a; font-size: 0.78rem; text-transform: uppercase; }
  .status-row { display: flex; gap: 8px; }
  .status-row span { min-width: 82px; padding: 6px 9px; border: 1px solid rgba(220, 238, 231, 0.34); border-radius: 6px; background: rgba(12, 18, 18, 0.82); color: #c4d3ce; font-size: 0.76rem; text-align: center; }
  .status-row span.online { color: #80e7c5; border-color: rgba(128, 231, 197, 0.5); }

  .studio-toolbar { position: absolute; inset: auto 0 0; display: flex; align-items: center; justify-content: center; gap: 9px; padding: 12px 16px; background: rgba(10, 16, 16, 0.9); border-top: 1px solid rgba(220, 238, 231, 0.22); }
  .studio-toolbar label { display: flex; align-items: center; gap: 7px; min-width: 0; color: #a9bbb5; font-size: 0.75rem; }
  .studio-toolbar select { width: min(250px, 25vw); height: 34px; min-width: 0; padding: 0 8px; border: 1px solid #52625d; border-radius: 6px; color: #edf7f2; background: #202725; }
  .studio-toolbar button, .mode-control button { height: 34px; padding: 0 12px; border: 1px solid #52625d; border-radius: 6px; color: #edf7f2; background: #202725; cursor: pointer; }
  .studio-toolbar button:disabled { opacity: 0.45; cursor: not-allowed; }
  .mode-control { display: grid; grid-template-columns: repeat(2, 68px); }
  .mode-control button { border-radius: 0; }
  .mode-control button:first-child { border-radius: 6px 0 0 6px; }
  .mode-control button:last-child { border-left: 0; border-radius: 0 6px 6px 0; }
  .mode-control button.active { color: #101514; border-color: #66d9b4; background: #66d9b4; font-weight: 700; }

  .conversation-region { display: grid; grid-template-rows: auto minmax(0, 1fr) auto; min-height: 0; background: #171b1a; }
  .activity { padding: 7px 18px; border-bottom: 1px solid #38443f; color: #f0c36a; background: #202523; font: 0.76rem/1.4 ui-monospace, SFMono-Regular, Menlo, monospace; overflow-wrap: anywhere; }
  .messages { display: grid; align-content: center; gap: 18px; min-height: 0; overflow-y: auto; padding: 22px clamp(20px, 6vw, 90px); }
  .messages p { width: min(900px, 100%); margin: 0 auto; color: #8ee4c6; text-align: center; font-size: clamp(1.18rem, 2vw, 1.9rem); line-height: 1.4; white-space: pre-wrap; overflow-wrap: anywhere; }
  .messages p.user { color: #f0c36a; }
  .messages p.previous { font-size: clamp(0.84rem, 1.1vw, 1rem); opacity: 0.5; }
  form { display: grid; grid-template-columns: minmax(0, 1fr) 84px; gap: 10px; padding: 12px clamp(14px, 4vw, 50px) calc(12px + env(safe-area-inset-bottom)); border-top: 1px solid #41504b; background: #111413; }
  textarea { width: 100%; height: 50px; max-height: 50px; resize: none; padding: 12px; border: 1px solid #52625d; border-radius: 6px; outline: 0; color: #edf7f2; background: #242b29; line-height: 1.35; }
  textarea:focus { border-color: #66d9b4; }
  .send-button { height: 50px; border: 1px solid #66d9b4; border-radius: 6px; color: #101514; background: #66d9b4; font-weight: 700; cursor: pointer; }

  .diff-dialog { width: min(1100px, calc(100vw - 28px)); height: min(760px, calc(100dvh - 28px)); padding: 0; border: 1px solid #52625d; border-radius: 8px; color: #edf7f2; background: #171b1a; }
  .diff-dialog::backdrop { background: rgba(0, 0, 0, 0.78); }
  .diff-dialog header { display: flex; justify-content: space-between; align-items: center; padding: 12px 16px; border-bottom: 1px solid #41504b; }
  .diff-dialog h2 { margin: 0; font-size: 1rem; }
  .diff-dialog button { width: 36px; height: 36px; border: 0; color: #edf7f2; background: transparent; font-size: 1.5rem; cursor: pointer; }
  .diff-dialog pre { height: calc(100% - 61px); margin: 0; overflow: auto; padding: 16px; color: #edf7f2; background: #0d100f; font: 0.82rem/1.5 ui-monospace, SFMono-Regular, Menlo, monospace; white-space: pre-wrap; overflow-wrap: anywhere; }

  @media (max-width: 760px) {
    .shell { grid-template-rows: minmax(290px, 48vh) minmax(260px, 1fr); overflow: auto; }
    .top-bar { padding: 12px; }
    .brand strong { font-size: 1.15rem; }
    .status-row span { min-width: 68px; padding: 5px 6px; font-size: 0.68rem; }
    .studio-toolbar { justify-content: flex-start; overflow-x: auto; padding: 9px 10px; }
    .studio-toolbar label span { display: none; }
    .studio-toolbar select { width: 180px; }
    .messages { padding: 16px; }
    form { grid-template-columns: minmax(0, 1fr) 68px; gap: 7px; padding-inline: 9px; }
  }
</style>
