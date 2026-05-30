const state = {
  chatId: null,
  runId: null,
  source: null,
};

const els = {
  chats: document.querySelector("#chat-list"),
  messages: document.querySelector("#messages"),
  events: document.querySelector("#events"),
  title: document.querySelector("#chat-title"),
  status: document.querySelector("#run-status"),
  form: document.querySelector("#composer"),
  input: document.querySelector("#message-input"),
  newChat: document.querySelector("#new-chat"),
};

async function api(path, options = {}) {
  const response = await fetch(path, {
    headers: { "Content-Type": "application/json", ...(options.headers || {}) },
    ...options,
  });
  if (!response.ok) {
    const body = await response.json().catch(() => ({}));
    throw new Error(body.error || response.statusText);
  }
  return response.json();
}

function setStatus(status) {
  els.status.textContent = status;
  els.status.className = `status ${status}`;
}

async function loadChats(selectFirst = true) {
  const data = await api("/api/chats");
  els.chats.innerHTML = "";
  data.chats.forEach((chat) => {
    const item = document.createElement("button");
    item.className = `chat-item ${chat.id === state.chatId ? "active" : ""}`;
    item.innerHTML = `<strong>${escapeHTML(chat.title)}</strong><span>${chat.message_count} messages</span>`;
    item.addEventListener("click", () => selectChat(chat.id));
    els.chats.appendChild(item);
  });
  if (!state.chatId && selectFirst && data.chats.length > 0) {
    await selectChat(data.chats[0].id);
  }
}

async function createChat() {
  const chat = await api("/api/chats", {
    method: "POST",
    body: JSON.stringify({ title: "New chat" }),
  });
  await loadChats(false);
  await selectChat(chat.id);
}

async function selectChat(chatId) {
  closeEvents();
  state.chatId = chatId;
  state.runId = null;
  setStatus("idle");
  const data = await api(`/api/chats/${chatId}`);
  els.title.textContent = data.chat.title;
  els.messages.innerHTML = "";
  els.events.innerHTML = "";
  data.messages.forEach(renderMessage);
  if (data.chat.last_run_id) {
    state.runId = data.chat.last_run_id;
    await loadRun(data.chat.last_run_id);
  }
  await loadChats(false);
}

async function loadRun(runId) {
  const run = await api(`/api/runs/${runId}`);
  setStatus(run.status);
  openEvents(runId);
}

function renderMessage(message) {
  const node = document.createElement("article");
  node.className = `message ${message.role}`;
  node.innerHTML = `<div class="role">${escapeHTML(message.role)}</div>${escapeHTML(message.content)}`;
  els.messages.appendChild(node);
  els.messages.scrollTop = els.messages.scrollHeight;
}

function renderEvent(event) {
  const node = document.createElement("div");
  node.className = "event";
  node.innerHTML = `<strong>${escapeHTML(event.type)}</strong><span>${escapeHTML(event.message || JSON.stringify(event.payload || ""))}</span>`;
  els.events.appendChild(node);
  els.events.scrollTop = els.events.scrollHeight;
  if (event.type.startsWith("run.")) {
    setStatus(event.type.replace("run.", ""));
  }
}

function openEvents(runId) {
  closeEvents();
  state.source = new EventSource(`/api/runs/${runId}/events`);
  state.source.onmessage = (event) => renderEvent(JSON.parse(event.data));
  [
    "run.queued",
    "run.started",
    "model.token",
    "tool.started",
    "tool.output",
    "tool.finished",
    "run.succeeded",
    "run.failed",
    "run.canceled",
  ].forEach((type) => {
    state.source.addEventListener(type, (event) => {
      const data = JSON.parse(event.data);
      renderEvent(data);
      if (type === "run.succeeded" || type === "run.failed" || type === "run.canceled") {
        refreshCurrentChatSoon();
      }
    });
  });
}

function closeEvents() {
  if (state.source) {
    state.source.close();
    state.source = null;
  }
}

let refreshTimer = null;
function refreshCurrentChatSoon() {
  clearTimeout(refreshTimer);
  refreshTimer = setTimeout(async () => {
    if (state.chatId) {
      const current = state.chatId;
      await selectChat(current);
    }
  }, 250);
}

els.form.addEventListener("submit", async (event) => {
  event.preventDefault();
  const content = els.input.value.trim();
  if (!content) return;
  if (!state.chatId) {
    await createChat();
  }
  els.input.value = "";
  const response = await api(`/api/chats/${state.chatId}/messages`, {
    method: "POST",
    body: JSON.stringify({ content }),
  });
  renderMessage(response.message);
  state.runId = response.run.id;
  setStatus(response.run.status);
  openEvents(response.run.id);
  await loadChats(false);
});

els.newChat.addEventListener("click", createChat);

function escapeHTML(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

loadChats().catch((error) => {
  els.messages.innerHTML = `<article class="message assistant"><div class="role">error</div>${escapeHTML(error.message)}</article>`;
});

