const form = document.querySelector("#chatForm");
const composer = document.querySelector("#composer");
const voiceButton = document.querySelector("#voiceButton");
const messageStage = document.querySelector("#messageStage");
const beemoFace = document.querySelector("#beemoFace");
const expressionLabel = document.querySelector("#expressionLabel");
const connectionLabel = document.querySelector("#connectionLabel");

const sessionId = `web-${Date.now()}`;
const messages = [];
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

  const response = await fetch("/api/chat", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      session_id: sessionId,
      messages,
    }),
  });

  const payload = await response.json();
  if (!response.ok || payload.error) {
    throw new Error(payload.error || `request failed: ${response.status}`);
  }

  const reply = payload.text?.trim() || "(empty response)";
  messages.push({ role: "assistant", content: reply });
  setExpression(expressionFromReply(reply));
  showMessage("assistant", cleanReply(reply));
  setStatus("idle");
}

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  const text = composer.value.trim();
  if (!text) return;

  composer.value = "";
  try {
    await sendMessage(text);
  } catch (error) {
    setExpression("error");
    setStatus("offline");
    showMessage("assistant", error.message);
  } finally {
    composer.focus();
  }
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
composer.focus();
