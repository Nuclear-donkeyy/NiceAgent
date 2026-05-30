const TERMINAL_STATUSES = new Set(["succeeded", "failed", "canceled"]);

const statusLabels = {
  idle: "空闲",
  queued: "排队中",
  running: "运行中",
  waiting_for_approval: "等待授权",
  succeeded: "已完成",
  failed: "失败",
  canceled: "已取消",
};

const roleLabels = {
  user: "用户",
  assistant: "Agent",
  system: "系统",
  tool: "工具",
};

const eventLabels = {
  "run.queued": "Run 已入队",
  "run.started": "Run 已开始",
  "model.token": "模型输出",
  "tool.started": "工具开始",
  "tool.output": "工具输出",
  "tool.finished": "工具结束",
  "approval.needed": "等待授权",
  "artifact.created": "产物生成",
  "run.succeeded": "Run 成功",
  "run.failed": "Run 失败",
  "run.canceled": "Run 已取消",
};

const riskLabels = {
  low: "低风险",
  medium: "中风险",
  high: "高风险",
};

const state = {
  chatId: null,
  runId: null,
  runStatus: "idle",
  source: null,
  eventCount: 0,
  cliLines: [],
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
  refreshChats: document.querySelector("#refresh-chats"),
  cancelRun: document.querySelector("#cancel-run"),
  skills: document.querySelector("#skill-list"),
  eventSummary: document.querySelector("#event-summary"),
  eventCount: document.querySelector("#event-count"),
  cliOutput: document.querySelector("#cli-output"),
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

function setStatus(status = "idle") {
  state.runStatus = status;
  els.status.textContent = statusLabels[status] || status;
  els.status.className = `status ${status}`;
  updateCancelButton();
}

function updateCancelButton() {
  const canCancel = state.runId && !TERMINAL_STATUSES.has(state.runStatus) && state.runStatus !== "idle";
  els.cancelRun.disabled = !canCancel;
}

async function loadChats(selectFirst = true) {
  const data = await api("/api/chats");
  els.chats.innerHTML = "";

  if (!data.chats || data.chats.length === 0) {
    els.chats.appendChild(emptyState("还没有会话。点击“新建会话”开始。"));
  } else {
    data.chats.forEach((chat) => {
      const item = document.createElement("button");
      item.className = `chat-item ${chat.id === state.chatId ? "active" : ""}`;
      item.type = "button";

      const title = document.createElement("strong");
      title.textContent = chat.title || "未命名会话";

      const meta = document.createElement("span");
      const count = chat.message_count || 0;
      meta.textContent = `${count} 条消息${chat.last_run_id ? " · 有最近 Run" : ""}`;

      item.append(title, meta);
      item.addEventListener("click", () => selectChat(chat.id));
      els.chats.appendChild(item);
    });
  }

  if (!state.chatId && selectFirst && data.chats && data.chats.length > 0) {
    await selectChat(data.chats[0].id);
  }
}

async function loadSkills() {
  try {
    const data = await api("/api/skills");
    renderSkills(data.skills || []);
  } catch (error) {
    els.skills.innerHTML = "";
    els.skills.appendChild(emptyState(`Skills 加载失败：${error.message}`));
  }
}

function renderSkills(skills) {
  els.skills.innerHTML = "";
  if (skills.length === 0) {
    els.skills.appendChild(emptyState("当前没有可用 skill。"));
    return;
  }

  skills.forEach((skill) => {
    const card = document.createElement("article");
    card.className = "skill-card";

    const header = document.createElement("header");
    const title = document.createElement("h3");
    title.textContent = skill.name || skill.id;
    const version = document.createElement("span");
    version.className = "pill";
    version.textContent = skill.version || "未标版本";
    header.append(title, version);

    const description = document.createElement("p");
    description.textContent = skill.description || "暂无说明。";

    const meta = document.createElement("div");
    meta.className = "skill-meta";
    meta.append(
      pill(riskLabels[skill.risk] || skill.risk || "未知风险", skill.risk),
      pill(skill.requires_auth ? "需要授权" : "无需授权"),
    );

    if (skill.requires_auth) {
      const approve = document.createElement("button");
      approve.className = "secondary";
      approve.type = "button";
      approve.textContent = "授权";
      approve.addEventListener("click", () => approveSkill(skill.id, approve));
      meta.appendChild(approve);
    }

    card.append(header, description, meta);
    els.skills.appendChild(card);
  });
}

async function approveSkill(skillId, button) {
  button.disabled = true;
  const original = button.textContent;
  button.textContent = "授权中";
  try {
    await api(`/api/skills/${encodeURIComponent(skillId)}/approve`, { method: "POST" });
    button.textContent = "已授权";
  } catch (error) {
    button.disabled = false;
    button.textContent = original;
    setEventSummary(`Skill 授权失败：${error.message}`);
  }
}

async function createChat() {
  const chat = await api("/api/chats", {
    method: "POST",
    body: JSON.stringify({ title: "新的远端任务" }),
  });
  await loadChats(false);
  await selectChat(chat.id);
}

async function selectChat(chatId) {
  closeEvents();
  state.chatId = chatId;
  state.runId = null;
  setStatus("idle");
  resetRunPanels();

  const data = await api(`/api/chats/${chatId}`);
  els.title.textContent = data.chat.title || "未命名会话";
  els.messages.innerHTML = "";

  if (!data.messages || data.messages.length === 0) {
    els.messages.appendChild(emptyState("这个会话还没有消息。可以直接发送任务给远端 agent。"));
  } else {
    data.messages.forEach(renderMessage);
  }

  if (data.chat.last_run_id) {
    await loadRun(data.chat.last_run_id);
  }

  await loadChats(false);
}

async function loadRun(runId) {
  const run = await api(`/api/runs/${runId}`);
  state.runId = run.id;
  setStatus(run.status);
  if (run.error) {
    setEventSummary(`最近 Run 失败：${run.error}`);
  } else {
    setEventSummary(`正在查看最近 Run：${run.id}`);
  }
  openEvents(run.id);
}

function renderMessage(message) {
  const node = document.createElement("article");
  node.className = `message ${message.role}`;

  const role = document.createElement("div");
  role.className = "role";
  role.textContent = roleLabels[message.role] || message.role;

  const body = document.createElement("div");
  body.textContent = message.content;

  node.append(role, body);
  els.messages.appendChild(node);
  els.messages.scrollTop = els.messages.scrollHeight;
}

function renderEvent(event) {
  if (!event || event.type === "ping") return;

  state.eventCount += 1;
  els.eventCount.textContent = String(state.eventCount);
  setEventSummary(`${eventLabels[event.type] || event.type}${event.message ? `：${event.message}` : ""}`);

  const node = document.createElement("div");
  node.className = `event ${eventClass(event.type)}`;

  const title = document.createElement("strong");
  title.textContent = eventLabels[event.type] || event.type;
  const meta = document.createElement("small");
  meta.textContent = `#${event.seq || state.eventCount} · ${formatTime(event.created_at)}`;
  const left = document.createElement("div");
  left.append(title, meta);

  const detail = document.createElement("div");
  const message = document.createElement("div");
  message.textContent = event.message || "无事件说明";
  detail.appendChild(message);

  const payload = summarizePayload(event);
  if (payload) {
    const code = document.createElement("code");
    code.textContent = payload;
    detail.appendChild(code);
  }

  node.append(left, detail);
  els.events.appendChild(node);
  els.events.scrollTop = els.events.scrollHeight;

  collectCLIOutput(event);
  if (event.type.startsWith("run.")) {
    setStatus(event.type.replace("run.", ""));
  }
  if (event.type === "approval.needed") {
    setStatus("waiting_for_approval");
  }
}

function summarizePayload(event) {
  const payload = event.payload;
  if (!payload) return "";
  if (event.type === "tool.started" && Array.isArray(payload.command)) {
    return `$ ${payload.command.join(" ")}`;
  }
  if (event.type === "tool.finished" && payload.exit_code !== undefined) {
    return `exit_code=${payload.exit_code}`;
  }
  if (event.type === "tool.output") {
    const parts = [];
    if (payload.stdout) parts.push(`stdout: ${compact(payload.stdout)}`);
    if (payload.stderr) parts.push(`stderr: ${compact(payload.stderr)}`);
    if (payload.error) parts.push(`error: ${compact(payload.error)}`);
    if (payload.exit_code !== undefined) parts.push(`exit_code=${payload.exit_code}`);
    return parts.join(" · ");
  }
  return compact(JSON.stringify(payload));
}

function collectCLIOutput(event) {
  const payload = event.payload || {};
  if (event.type === "tool.started" && Array.isArray(payload.command)) {
    state.cliLines.push(`$ ${payload.command.join(" ")}`);
  }
  if (event.type === "tool.output") {
    if (payload.stdout) state.cliLines.push(payload.stdout.trimEnd());
    if (payload.stderr) state.cliLines.push(`[stderr]\n${payload.stderr.trimEnd()}`);
    if (payload.error) state.cliLines.push(`[error] ${payload.error}`);
    if (payload.exit_code !== undefined) state.cliLines.push(`[exit_code] ${payload.exit_code}`);
    if (payload.duration) state.cliLines.push(`[duration] ${payload.duration}`);
  }
  if (event.type === "approval.needed") {
    state.cliLines.push("[approval] 此操作需要授权后才能继续。");
  }
  renderCLIOutput();
}

function renderCLIOutput() {
  els.cliOutput.textContent =
    state.cliLines.filter(Boolean).join("\n\n") || "暂无 CLI 输出。发送 /cli echo hello 可以测试远端 CLI skill。";
  els.cliOutput.scrollTop = els.cliOutput.scrollHeight;
}

function openEvents(runId) {
  closeEvents();
  state.source = new EventSource(`/api/runs/${runId}/events`);
  state.source.onerror = () => {
    if (!TERMINAL_STATUSES.has(state.runStatus)) {
      setEventSummary("事件流连接中断，稍后会自动重连。");
    }
  };

  [
    "run.queued",
    "run.started",
    "model.token",
    "tool.started",
    "tool.output",
    "tool.finished",
    "approval.needed",
    "artifact.created",
    "run.succeeded",
    "run.failed",
    "run.canceled",
  ].forEach((type) => {
    state.source.addEventListener(type, (event) => {
      const data = JSON.parse(event.data);
      const wasTerminal = TERMINAL_STATUSES.has(state.runStatus);
      renderEvent(data);
      if (TERMINAL_STATUSES.has(type.replace("run.", ""))) {
        closeEvents();
      }
      if (TERMINAL_STATUSES.has(type.replace("run.", "")) && !wasTerminal) {
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

function resetRunPanels() {
  state.eventCount = 0;
  state.cliLines = [];
  els.events.innerHTML = "";
  els.eventCount.textContent = "0";
  setEventSummary("等待新的 run 事件。");
  renderCLIOutput();
}

function setEventSummary(text) {
  els.eventSummary.textContent = text;
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
  resetRunPanels();
  setStatus("queued");

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
els.refreshChats.addEventListener("click", async () => {
  await loadChats(false);
  if (state.chatId) {
    await selectChat(state.chatId);
  }
});
els.cancelRun.addEventListener("click", async () => {
  if (!state.runId || els.cancelRun.disabled) return;
  const run = await api(`/api/runs/${state.runId}/cancel`, { method: "POST" });
  setStatus(run.status);
  setEventSummary("已发送取消请求。");
});

function emptyState(text) {
  const node = document.createElement("div");
  node.className = "empty-state";
  node.textContent = text;
  return node;
}

function pill(text, level = "") {
  const node = document.createElement("span");
  node.className = `pill ${level || ""}`;
  node.textContent = text;
  return node;
}

function eventClass(type) {
  if (type === "run.failed" || type === "run.canceled") return "failed";
  if (type.startsWith("run.")) return "run";
  if (type.startsWith("model.")) return "model";
  if (type.startsWith("tool.")) return "tool";
  if (type.startsWith("approval.")) return "approval";
  return "";
}

function formatTime(value) {
  if (!value) return "刚刚";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "刚刚";
  return date.toLocaleTimeString("zh-CN", { hour12: false });
}

function compact(value) {
  return String(value).replace(/\s+/g, " ").trim();
}

async function boot() {
  await Promise.all([loadChats(), loadSkills()]);
}

boot().catch((error) => {
  els.messages.innerHTML = "";
  els.messages.appendChild(emptyState(`前端初始化失败：${error.message}`));
});
