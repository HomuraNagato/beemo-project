<script lang="ts">
  import { onMount } from "svelte";
  import FaceDisplay from "$lib/components/FaceDisplay.svelte";
  import FaceControls from "$lib/components/FaceControls.svelte";
  import ConversationOverlay from "$lib/components/ConversationOverlay.svelte";
  import MessageComposer from "$lib/components/MessageComposer.svelte";
  import ProjectManagerDialog from "$lib/components/ProjectManagerDialog.svelte";
  import SessionManagerDialog from "$lib/components/SessionManagerDialog.svelte";
  import { X } from "@lucide/svelte";
  import { cleanReply, expressionFromText } from "$lib/expressions";
  import { limitNotice, operationalLabel, parseAgentProgress, type StatusTone } from "$lib/agentStatus";
  import type { AgentEvent, ChatMessage, CodeProject, FaceExpression, SessionSummary, SlashCommand, StateUpdate, WakeEvent } from "$lib/types";

  type Mode = "normal" | "code";
  const conversationHideDelayMs = 20_000;

  let expression: FaceExpression = "calm";
  let talking = false;
  let status = "idle";
  let voiceStatus = "voice connecting";
  let mode: Mode = "normal";
  let workspace = "";
  let internetEnabled = false;
  let sessionId = "";
  let messages: ChatMessage[] = [];
  let visibleMessages: ChatMessage[] = [{ role: "assistant", content: "Hi. I am listening." }];
  let conversationOpen = true;
  let conversationExpanded = false;
  let activity = "";
  let notice = "";
  let noticeTone: StatusTone = "normal";
  let composer = "";
  let slashCommands: SlashCommand[] = [];
  let activeController: AbortController | null = null;
  let diffDialog: HTMLDialogElement;
  let projectDialog: ProjectManagerDialog;
  let sessionDialog: SessionManagerDialog;
  let diffOutput = "";
  let talkingTimer: ReturnType<typeof setTimeout> | undefined;
  let conversationTimer: ReturnType<typeof setTimeout> | undefined;
  let stateEvents: EventSource | undefined;
  let wakeEvents: EventSource | undefined;
  let runtimeEvents: EventSource | undefined;
  let latestSessionEventTimestamp = 0;

  $: if (composer.trim()) holdConversation();

  onMount(() => {
    runtimeEvents = connectRuntimeEvents();
    void loadSlashCommands();
    return () => {
      runtimeEvents?.close();
      wakeEvents?.close();
      stateEvents?.close();
      if (talkingTimer) clearTimeout(talkingTimer);
      if (conversationTimer) clearTimeout(conversationTimer);
    };
  });

  async function loadSlashCommands(): Promise<void> {
    try {
      const response = await fetch("/api/commands", { cache: "no-store" });
      if (!response.ok) return;
      const payload = await response.json() as { commands?: SlashCommand[] };
      slashCommands = Array.isArray(payload.commands) ? payload.commands : [];
    } catch {
      slashCommands = [];
    }
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
    conversationOpen = true;
    conversationExpanded = false;
    holdConversation();
    activity = "";
    notice = "";
    noticeTone = "normal";
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
    if (event.state === "control") {
      handleControl(event.payloadJson);
      return;
    }
    const progress = parseAgentProgress(event.payloadJson);
    applyLimitNotice(progress);
    const content = cleanReply(event.message);
    if (event.state === "user" || event.state === "assistant") {
      const latest = messages.at(-1);
      if (content && (latest?.role !== event.state || latest.content !== content)) {
        messages = [...messages, { role: event.state, content }];
      }
      showMessage(event.state, content);
    }
    if (event.state === "user") {
      expression = "thinking";
      status = "thinking";
      activity = "Choosing approach";
    } else if (event.state === "status") {
      expression = "thinking";
      status = event.message || "thinking";
      activity = operationalLabel(status, progress);
    } else if (event.state === "assistant") {
      expression = expressionFromText(content);
      status = "responding";
      animateTalking(content);
      scheduleConversationHide();
    } else if (event.state === "error") {
      expression = "worried";
      status = "error";
      conversationOpen = true;
      holdConversation();
    } else if (event.state === "complete") {
      activity = "";
      status = "idle";
    }
  }

  function handleWakeEvent(event: WakeEvent): void {
    voiceStatus = "voice ready";
    if (!event.transcript && !event.prompt && !event.response) {
      expression = "listening";
      status = "listening";
      voiceStatus = "wake detected";
      return;
    }
    if (event.response) {
      showMessage("assistant", cleanReply(event.response));
      expression = event.status === "error" ? "worried" : expressionFromText(event.response);
      status = event.status || "idle";
      animateTalking(event.response);
      if (event.status === "error") holdConversation();
      else scheduleConversationHide();
      return;
    }
    const prompt = event.prompt || event.transcript;
    if (prompt) {
      showMessage("user", prompt);
      expression = "thinking";
      status = "thinking";
    }
  }

  function showMessage(role: ChatMessage["role"], content: string): void {
    if (!content) return;
    const latest = visibleMessages.at(-1);
    if (latest?.role === role && latest.content === content) return;
    visibleMessages = [...visibleMessages, { role, content }].slice(-20);
    conversationOpen = true;
  }

  function holdConversation(): void {
    if (conversationTimer) clearTimeout(conversationTimer);
    conversationTimer = undefined;
  }

  function scheduleConversationHide(): void {
    holdConversation();
    if (conversationExpanded || status === "error" || status === "offline") return;
    conversationTimer = setTimeout(() => {
      conversationOpen = false;
      conversationTimer = undefined;
    }, conversationHideDelayMs);
  }

  function toggleConversation(): void {
    if (conversationOpen) {
      if (composer.trim()) return;
      closeConversation();
      return;
    }
    conversationOpen = true;
    scheduleConversationHide();
  }

  function expandConversation(): void {
    conversationOpen = true;
    conversationExpanded = true;
    holdConversation();
  }

  function collapseConversation(): void {
    conversationExpanded = false;
    scheduleConversationHide();
  }

  function closeConversation(): void {
    if (composer.trim()) return;
    conversationOpen = false;
    conversationExpanded = false;
    holdConversation();
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
    }, duration);
  }

  function applyLimitNotice(progress: ReturnType<typeof parseAgentProgress>): void {
    const next = limitNotice(progress);
    if (!next) return;
    notice = next.text;
    noticeTone = next.tone;
  }

  async function sendMessage(text: string): Promise<void> {
    holdConversation();
    conversationOpen = true;
    messages = [...messages, { role: "user", content: text }];
    showMessage("user", text);
    expression = "thinking";
    status = "thinking";
    activity = "Choosing approach";
    notice = "";
    noticeTone = "normal";
    activeController = new AbortController();

    const response = await fetch("/api/agent", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      signal: activeController.signal,
      body: JSON.stringify({ sessionId, messages }),
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
    animateTalking(reply);
    scheduleConversationHide();
    activeController = null;
  }

  async function handleAgentEvent(event: AgentEvent): Promise<string> {
    const progress = parseAgentProgress(event.payloadJson);
    applyLimitNotice(progress);
    switch (event.type) {
      case "control":
        handleControl(event.payloadJson);
        break;
      case "assistant":
        status = "responding";
        return event.text || "";
      case "assistant_delta":
        status = "responding";
        break;
      case "status":
        expression = "thinking";
        status = event.text || "thinking";
        activity = operationalLabel(status, progress);
        break;
      case "tool_start":
        expression = "thinking";
        showActivity(operationalLabel(event.text || `Running ${event.tool}`, progress));
        break;
      case "tool_result":
        showActivity(operationalLabel("Planning next step", progress));
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
      case "complete":
        activity = "";
        status = "idle";
        break;
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
    composer = "";
    try {
      await sendMessage(text);
    } catch (error) {
      if (error instanceof DOMException && error.name === "AbortError") return;
      expression = "worried";
      status = "offline";
      showMessage("assistant", error instanceof Error ? error.message : String(error));
      holdConversation();
    } finally {
      activeController = null;
    }
  }

  async function syncSession(): Promise<void> {
    if (activeController || !sessionId) return;
    try {
      const response = await fetch(`/api/session?id=${encodeURIComponent(sessionId)}`, { cache: "no-store" });
      if (!response.ok) return;
      const payload = await response.json() as { session: SessionSummary; events: AgentEvent[] };
      mode = payload.session.mode;
      workspace = payload.session.workspace;
      internetEnabled = payload.session.internetEnabled;
      messages = payload.events
        .filter((event) => event.type === "user" || event.type === "assistant")
        .map((event) => ({ role: event.type as ChatMessage["role"], content: cleanReply(event.text) }));
      visibleMessages = messages.slice(-20);
      latestSessionEventTimestamp = payload.events.at(-1)?.timestampUnixMs || 0;
    } catch {
      status = "offline";
    }
  }

  function handleControl(payloadJson: string): void {
    if (!payloadJson) return;
    try {
      const payload = JSON.parse(payloadJson) as { action?: string; mode?: Mode; workspace?: string; internetEnabled?: boolean };
      if (payload.mode) mode = payload.mode;
      if (payload.workspace !== undefined) workspace = payload.workspace;
      if (payload.internetEnabled !== undefined) internetEnabled = payload.internetEnabled;
      if (payload.action === "project_picker" || payload.action === "project_manager") void projectDialog.open();
    } catch { /* Ignore malformed optional control metadata. */ }
  }

  function applySession(session: SessionSummary): void {
    mode = session.mode;
    workspace = session.workspace;
    internetEnabled = session.internetEnabled;
    activateSession(session.sessionId);
  }

  async function selectProject(project: CodeProject): Promise<void> {
    if (!sessionId) return;
    const response = await fetch("/api/project-selection", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ sessionId, path: project.path }) });
    const payload = await response.json() as { session?: SessionSummary; error?: string };
    if (!response.ok || payload.error || !payload.session) { showActivity(payload.error || "Project could not be selected"); return; }
    mode = payload.session.mode;
    workspace = payload.session.workspace;
    status = `code · ${project.path.split("/").filter(Boolean).at(-1) || "project"}`;
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

<main class="h-dvh min-h-[420px] overflow-hidden bg-beemo-bg" aria-label="Beemo interface">
  <section class="relative h-full min-h-0 overflow-hidden" aria-label="Beemo face display">
    <FaceDisplay {expression} {talking} />
    <FaceControls
      onprojects={() => void projectDialog.open()}
      onsessions={() => void sessionDialog.open()}
      onface={toggleConversation}
    />
    {#if conversationOpen}
      <ConversationOverlay
        messages={visibleMessages}
        {activity}
        {notice}
        {noticeTone}
        {status}
        {voiceStatus}
        {mode}
      {workspace}
      {internetEnabled}
        expanded={conversationExpanded}
        onexpand={expandConversation}
        oncollapse={collapseConversation}
        onclose={closeConversation}
        onhold={holdConversation}
        onrelease={scheduleConversationHide}
        onswitch={() => void projectDialog.open()}
        ondiff={() => void showDiff()}
      />
      <MessageComposer bind:value={composer} busy={Boolean(activeController)} commands={slashCommands} onsubmit={() => void submit()} />
    {/if}
  </section>
</main>

<ProjectManagerDialog bind:this={projectDialog} {sessionId} activeWorkspace={workspace} onstate={(nextMode, nextWorkspace) => { mode = nextMode; workspace = nextWorkspace; }} onselect={selectProject} />
<SessionManagerDialog bind:this={sessionDialog} activeSessionId={sessionId} onactivate={applySession} />

<dialog bind:this={diffDialog} class="h-[min(760px,calc(100dvh-28px))] w-[min(1100px,calc(100vw-28px))] rounded-lg border border-[#52625d] bg-beemo-panel p-0 text-beemo-text backdrop:bg-black/80">
  <header class="flex items-center justify-between border-b border-beemo-border px-4 py-3">
    <h2 class="m-0 text-base font-semibold">Working tree diff</h2>
    <button class="grid size-11 cursor-pointer place-items-center rounded-sm border-0 bg-transparent text-beemo-text hover:bg-[#303a37]" type="button" aria-label="Close diff" title="Close" on:click={() => diffDialog.close()}><X size={21} /></button>
  </header>
  <pre class="m-0 h-[calc(100%-69px)] overflow-auto bg-[#0d100f] p-4 font-mono text-sm leading-6 whitespace-pre-wrap text-beemo-text">{diffOutput}</pre>
</dialog>
