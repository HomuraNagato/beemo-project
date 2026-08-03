const form = document.querySelector("#chatForm");
const composer = document.querySelector("#composer");
const voiceButton = document.querySelector("#voiceButton");
const messageStage = document.querySelector("#messageStage");
const beemoFace = document.querySelector("#beemoFace");
const expressionLabel = document.querySelector("#expressionLabel");
const connectionLabel = document.querySelector("#connectionLabel");
const activityStrip = document.querySelector("#activityStrip");
const workspaceSelect = document.querySelector("#workspaceSelect");
const modeButtons = [...document.querySelectorAll(".mode-button")];
const sendButton = document.querySelector(".send-button");
const sessionSelect = document.querySelector("#sessionSelect");
const newSessionButton = document.querySelector("#newSessionButton");
const diffButton = document.querySelector("#diffButton");
const diffDialog = document.querySelector("#diffDialog");
const diffOutput = document.querySelector("#diffOutput");
const closeDiffButton = document.querySelector("#closeDiffButton");

let sessionId = `web-${Date.now()}`;
const messages = [];
let mode = "chat";
let activeController = null;
const visibleMessages = [
  {
    role: "assistant",
    content: "Hi. I am listening.",
  },
];

const expressions = new Set([
  "neutral",
  "happy",
  "excited",
  "curious",
  "thinking",
  "concerned",
  "sad",
  "surprised",
  "apologetic",
  "sleepy",
  "error",
]);

function setExpression(expression) {
  const next = expressions.has(expression) ? expression : "neutral";
  beemoFace.dataset.expression = next;
  expressionLabel.textContent = next;
}

function setStatus(status) {
  connectionLabel.textContent = status;
}

function expressionFromReply(reply) {
  const tag = reply.match(/^\s*\[(?:emotion|expression|face):\s*([a-z_-]+)\]\s*/i);
  if (tag) {
    const tagged = tag[1].toLowerCase().replaceAll("_", "-");
    if (expressions.has(tagged)) return tagged;
  }

  const text = reply.toLowerCase();
  if (hasAny(text, ["request failed", "error", "failed", "can't connect", "cannot connect"])) return "error";
  if (hasAny(text, ["sorry", "apologize", "my mistake", "i was wrong"])) return "apologetic";
  if (hasAny(text, ["i'm not sure", "i do not know", "i don't know", "missing", "need more", "clarify"])) return "concerned";
  if (hasAny(text, ["?", "wonder", "curious", "what detail", "who is this about"])) return "curious";
  if (hasAny(text, ["let me think", "thinking", "consider", "reason"])) return "thinking";
  if (hasAny(text, ["great", "nice", "glad", "happy", "that works", "done"])) return "happy";
  if (hasAny(text, ["wow", "amazing", "excited", "awesome", "excellent"])) return "excited";
  if (hasAny(text, ["oh", "surprise", "unexpected"])) return "surprised";
  if (hasAny(text, ["sad", "hurt", "upset", "unfortunately"])) return "sad";
  if (hasAny(text, ["tired", "sleepy", "rest"])) return "sleepy";
  return "neutral";
}

function cleanReply(reply) {
  return reply.replace(/^\s*\[(?:emotion|expression|face):\s*[a-z_-]+\]\s*/i, "").trim();
}

function hasAny(text, needles) {
  return needles.some((needle) => text.includes(needle));
}

function showMessage(role, content) {
  visibleMessages.push({ role, content });
  while (visibleMessages.length > 2) {
    visibleMessages.shift();
  }
  renderVisibleMessages();
}

function renderVisibleMessages() {
  messageStage.replaceChildren();

  const previous = visibleMessages.length > 1 ? visibleMessages[0] : null;
  const current = visibleMessages[visibleMessages.length - 1];

  if (previous) {
    messageStage.appendChild(messageElement(previous, "previous"));
  } else {
    const spacer = document.createElement("div");
    spacer.setAttribute("aria-hidden", "true");
    messageStage.appendChild(spacer);
  }

  messageStage.appendChild(messageElement(current, "current"));
}

function messageElement(message, state) {
  const element = document.createElement("p");
  element.className = `chat-line ${state} ${message.role === "user" ? "user" : "assistant"}`;
  element.textContent = message.content;
  return element;
}

async function sendMessage(text) {
  messages.push({ role: "user", content: text });
  showMessage("user", text);
  setExpression("thinking");
  setStatus("thinking");

  activeController = new AbortController();
  sendButton.textContent = "Stop";
  const response = await fetch("/api/agent", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    signal: activeController.signal,
    body: JSON.stringify({
      session_id: sessionId,
      messages,
      mode,
      workspace: workspaceSelect.value,
    }),
  });

  if (!response.ok || !response.body) {
    const payload = await response.json();
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
      const event = JSON.parse(line);
      const eventReply = await handleAgentEvent(event);
      if (eventReply) reply = eventReply;
    }
    if (done) break;
  }
  if (buffer.trim()) {
    const eventReply = await handleAgentEvent(JSON.parse(buffer));
    if (eventReply) reply = eventReply;
  }

  reply = reply.trim() || "(empty response)";
  messages.push({ role: "assistant", content: reply });
  setExpression(expressionFromReply(reply));
  showMessage("assistant", cleanReply(reply));
  setStatus("idle");
  activityStrip.hidden = true;
  activeController = null;
  sendButton.textContent = "Send";
  await loadSessions();
  sessionSelect.value = sessionId;
}

async function handleAgentEvent(event) {
  switch (event.type) {
    case "assistant":
      return event.text || "";
    case "tool_start":
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
        body: JSON.stringify({
          session_id: sessionId,
          approval_id: event.approval_id,
          approved,
        }),
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

function showActivity(text) {
  activityStrip.textContent = text;
  activityStrip.hidden = false;
}

function setMode(nextMode) {
  mode = nextMode === "code" ? "code" : "chat";
  for (const button of modeButtons) {
    button.classList.toggle("is-active", button.dataset.mode === mode);
  }
  workspaceSelect.disabled = mode !== "code";
  diffButton.disabled = mode !== "code" || !workspaceSelect.value;
  setStatus(mode);
}

async function loadWorkspaces() {
  try {
    const response = await fetch("/api/workspaces", { cache: "no-store" });
    const payload = await response.json();
    if (!response.ok || payload.error) throw new Error(payload.error || "workspace request failed");
    for (const workspace of payload.workspaces || []) {
      const option = document.createElement("option");
      option.value = workspace;
      option.textContent = workspace.split("/").filter(Boolean).at(-1) || workspace;
      option.title = workspace;
      workspaceSelect.appendChild(option);
    }
  } catch (error) {
    showActivity(`Repositories unavailable: ${error.message}`);
  }
}

async function loadSessions() {
  try {
    const response = await fetch("/api/sessions", { cache: "no-store" });
    const payload = await response.json();
    if (!response.ok || payload.error) throw new Error(payload.error || "session request failed");
    const selected = sessionId;
    sessionSelect.replaceChildren(new Option("New session", ""));
    for (const session of payload.sessions || []) {
      const repository = session.workspace?.split("/").filter(Boolean).at(-1) || "chat";
      const updated = new Date(session.updated_unix_ms).toLocaleString([], {
        month: "short",
        day: "numeric",
        hour: "numeric",
        minute: "2-digit",
      });
      const option = new Option(`${repository} · ${updated}`, session.session_id);
      option.title = `${session.mode} · ${session.status} · ${session.workspace || "no repository"}`;
      sessionSelect.appendChild(option);
    }
    sessionSelect.value = [...sessionSelect.options].some((option) => option.value === selected) ? selected : "";
  } catch (error) {
    showActivity(`Sessions unavailable: ${error.message}`);
  }
}

async function resumeSession(nextSessionId) {
  if (!nextSessionId) {
    startNewSession();
    return;
  }
  const response = await fetch(`/api/session?id=${encodeURIComponent(nextSessionId)}`, { cache: "no-store" });
  const payload = await response.json();
  if (!response.ok || payload.error) throw new Error(payload.error || "session could not be loaded");

  sessionId = payload.session.session_id;
  messages.splice(0, messages.length);
  visibleMessages.splice(0, visibleMessages.length);
  for (const event of payload.events || []) {
    if (event.type !== "user" && event.type !== "assistant") continue;
    const message = { role: event.type, content: cleanReply(event.text || "") };
    messages.push(message);
    visibleMessages.push(message);
  }
  if (visibleMessages.length === 0) {
    visibleMessages.push({ role: "assistant", content: "Session resumed." });
  }
  while (visibleMessages.length > 2) visibleMessages.shift();
  setMode(payload.session.mode);
  if (payload.session.workspace) workspaceSelect.value = payload.session.workspace;
  diffButton.disabled = mode !== "code" || !workspaceSelect.value;
  renderVisibleMessages();
  setStatus("resumed");
}

function startNewSession() {
  sessionId = `web-${Date.now()}`;
  messages.splice(0, messages.length);
  visibleMessages.splice(0, visibleMessages.length, { role: "assistant", content: "Hi. I am listening." });
  sessionSelect.value = "";
  activityStrip.hidden = true;
  renderVisibleMessages();
  setStatus(mode);
}

async function showDiff() {
  if (mode !== "code" || !workspaceSelect.value) return;
  diffOutput.textContent = "Loading diff...";
  diffDialog.showModal();
  const response = await fetch("/api/diff", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ session_id: sessionId, workspace: workspaceSelect.value }),
  });
  const payload = await response.json();
  if (!response.ok || payload.error) {
    diffOutput.textContent = payload.error || "Diff request failed.";
    return;
  }
  diffOutput.textContent = payload.diff || "No working tree changes.";
}

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  if (activeController) {
    activeController.abort();
    activeController = null;
    sendButton.textContent = "Send";
    setStatus("cancelled");
    return;
  }
  const text = composer.value.trim();
  if (!text) return;
  if (mode === "code" && !workspaceSelect.value) {
    showMessage("assistant", "Select a repository before using Code mode.");
    workspaceSelect.focus();
    return;
  }

  composer.value = "";
  try {
    await sendMessage(text);
  } catch (error) {
    if (error.name === "AbortError") return;
    setExpression("error");
    setStatus("offline");
    showMessage("assistant", error.message);
  } finally {
    activeController = null;
    sendButton.textContent = "Send";
    composer.focus();
  }
});

for (const button of modeButtons) {
  button.addEventListener("click", () => setMode(button.dataset.mode));
}

workspaceSelect.addEventListener("change", () => {
  diffButton.disabled = mode !== "code" || !workspaceSelect.value;
});

sessionSelect.addEventListener("change", async () => {
  try {
    await resumeSession(sessionSelect.value);
  } catch (error) {
    showActivity(error.message);
  }
});

newSessionButton.addEventListener("click", startNewSession);
diffButton.addEventListener("click", showDiff);
closeDiffButton.addEventListener("click", () => diffDialog.close());
diffDialog.addEventListener("click", (event) => {
  if (event.target === diffDialog) diffDialog.close();
});

composer.addEventListener("keydown", (event) => {
  if (event.key === "Enter" && !event.shiftKey) {
    event.preventDefault();
    form.requestSubmit();
  }
});

function setupVoiceInput() {
  const SpeechRecognition = window.SpeechRecognition || window.webkitSpeechRecognition;
  if (!SpeechRecognition) {
    voiceButton.disabled = true;
    voiceButton.title = "Voice input is not supported by this browser";
    return;
  }

  const recognition = new SpeechRecognition();
  recognition.continuous = false;
  recognition.interimResults = true;
  recognition.lang = "en-US";

  recognition.addEventListener("start", () => {
    voiceButton.classList.add("is-listening");
    setExpression("curious");
    setStatus("listening");
  });

  recognition.addEventListener("result", (event) => {
    let text = "";
    for (const result of event.results) {
      text += result[0].transcript;
    }
    composer.value = text.trim();
  });

  recognition.addEventListener("end", () => {
    voiceButton.classList.remove("is-listening");
    setStatus("idle");
    composer.focus();
  });

  recognition.addEventListener("error", () => {
    voiceButton.classList.remove("is-listening");
    setExpression("concerned");
    setStatus("voice error");
  });

  voiceButton.addEventListener("click", () => {
    recognition.start();
  });
}

setupVoiceInput();
async function initialize() {
  await loadWorkspaces();
  await loadSessions();
  composer.focus();
}

initialize();
