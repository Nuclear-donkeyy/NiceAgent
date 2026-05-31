import { useEffect, useMemo, useRef, useState } from "react";

const TERMINAL = new Set(["succeeded", "failed", "canceled"]);

const statusText = {
  idle: "空闲",
  queued: "排队中",
  running: "运行中",
  waiting_for_approval: "等待授权",
  succeeded: "已完成",
  failed: "失败",
  canceled: "已取消",
};

const roleText = {
  user: "用户",
  assistant: "Agent",
  system: "系统",
  tool: "工具",
};

const eventText = {
  "run.queued": "Run 入队",
  "run.started": "Run 开始",
  "model.token": "模型输出",
  "tool.started": "工具开始",
  "tool.output": "工具输出",
  "tool.finished": "工具结束",
  "approval.needed": "等待授权",
  "artifact.created": "产物生成",
  "run.succeeded": "Run 成功",
  "run.failed": "Run 失败",
  "run.canceled": "Run 取消",
};

const riskText = {
  low: "低",
  medium: "中",
  high: "高",
};

async function api(path, options = {}) {
  const response = await fetch(path, {
    ...options,
    headers: {
      "Content-Type": "application/json",
      ...(options.headers || {}),
    },
  });
  if (!response.ok) {
    const body = await response.json().catch(() => ({}));
    throw new Error(body.error || response.statusText);
  }
  return response.json();
}

export default function App() {
  const [chats, setChats] = useState([]);
  const [activeChatId, setActiveChatId] = useState(null);
  const [activeChat, setActiveChat] = useState(null);
  const [messages, setMessages] = useState([]);
  const [skills, setSkills] = useState([]);
  const [runId, setRunId] = useState(null);
  const [runStatus, setRunStatus] = useState("idle");
  const [events, setEvents] = useState([]);
  const [cliLines, setCliLines] = useState([]);
  const [input, setInput] = useState("");
  const [notice, setNotice] = useState("准备就绪");
  const [chatQuery, setChatQuery] = useState("");
  const [showArchived, setShowArchived] = useState(false);
  const [chatsLoading, setChatsLoading] = useState(false);
  const [chatError, setChatError] = useState("");
  const [messageLoading, setMessageLoading] = useState(false);
  const [sending, setSending] = useState(false);
  const [eventsReplaying, setEventsReplaying] = useState(false);
  const sourceRef = useRef(null);
  const refreshTimerRef = useRef(null);

  const canCancel = runId && !TERMINAL.has(runStatus) && runStatus !== "idle";
  const canSend = !sending && input.trim().length > 0;
  const eventSummary = events.length > 0 ? events[events.length - 1].message || eventText[events[events.length - 1].type] : "暂无事件";

  useEffect(() => {
    boot();
    return () => closeEvents();
  }, []);

  useEffect(() => {
    const timer = setTimeout(() => {
      loadChats(false);
    }, 180);
    return () => clearTimeout(timer);
  }, [chatQuery, showArchived]);

  async function boot() {
    try {
      await Promise.all([loadChats(true), loadSkills()]);
    } catch (error) {
      setNotice(error.message);
    }
  }

  async function loadChats(selectFirst = false) {
    setChatsLoading(true);
    setChatError("");
    try {
      const params = new URLSearchParams();
      if (chatQuery.trim()) params.set("q", chatQuery.trim());
      if (showArchived) params.set("include_archived", "true");
      const path = params.toString() ? `/api/chats?${params.toString()}` : "/api/chats";
      const data = await api(path);
      const list = data.chats || [];
      setChats(list);
      if (selectFirst && !activeChatId && list.length > 0) {
        await selectChat(list[0].id);
      }
    } catch (error) {
      setChatError(error.message);
      setNotice(`会话加载失败：${error.message}`);
    } finally {
      setChatsLoading(false);
    }
  }

  async function loadSkills() {
    const data = await api("/api/skills");
    setSkills(data.skills || []);
  }

  async function createChat() {
    try {
      const chat = await api("/api/chats", {
        method: "POST",
        body: JSON.stringify({ title: "新的远端任务" }),
      });
      await loadChats(false);
      await selectChat(chat.id);
    } catch (error) {
      setNotice(`新建会话失败：${error.message}`);
    }
  }

  async function selectChat(chatId) {
    closeEvents();
    resetRunPanels();
    setActiveChatId(chatId);
    setMessageLoading(true);
    try {
      const data = await api(`/api/chats/${chatId}`);
      setActiveChat(data.chat);
      setMessages(data.messages || []);
      if (data.chat?.last_run_id) {
        await loadRun(data.chat.last_run_id, chatId);
      }
      await loadChats(false);
    } catch (error) {
      setNotice(`会话加载失败：${error.message}`);
    } finally {
      setMessageLoading(false);
    }
  }

  async function loadRun(id, chatIdForRefresh = activeChatId) {
    const run = await api(`/api/runs/${id}`);
    setRunId(run.id);
    setRunStatus(run.status || "idle");
    setNotice(run.error ? `最近 Run 失败：${run.error}` : `正在查看 Run：${run.id}`);
    openEvents(run.id, chatIdForRefresh, !TERMINAL.has(run.status || "idle"));
  }

  async function sendMessage(event) {
    event.preventDefault();
    const content = input.trim();
    if (!content) return;
    setSending(true);
    try {
      let chatId = activeChatId;
      if (!chatId) {
        const chat = await api("/api/chats", {
          method: "POST",
          body: JSON.stringify({ title: "新的远端任务" }),
        });
        chatId = chat.id;
        setActiveChatId(chatId);
        setActiveChat(chat);
      }
      setInput("");
      resetRunPanels();
      setRunStatus("queued");
      const response = await api(`/api/chats/${chatId}/messages`, {
        method: "POST",
        body: JSON.stringify({ content }),
      });
      setMessages((prev) => [...prev, response.message]);
      setRunId(response.run.id);
      setRunStatus(response.run.status);
      openEvents(response.run.id, chatId, true);
      await loadChats(false);
    } catch (error) {
      setNotice(`发送失败：${error.message}`);
      setRunStatus("failed");
    } finally {
      setSending(false);
    }
  }

  async function cancelRun() {
    if (!canCancel) return;
    const run = await api(`/api/runs/${runId}/cancel`, { method: "POST" });
    setRunStatus(run.status);
    setNotice("已发送取消请求");
  }

  async function approveSkill(id) {
    await api(`/api/skills/${encodeURIComponent(id)}/approve`, { method: "POST" });
    setNotice(`已记录 ${id} 授权，恢复执行会在后续阶段实现`);
  }

  async function setActiveChatArchived(archived) {
    if (!activeChatId) return;
    try {
      const action = archived ? "archive" : "restore";
      const chat = await api(`/api/chats/${activeChatId}/${action}`, { method: "POST" });
      setActiveChat(chat);
      setNotice(archived ? "会话已归档" : "会话已恢复");
      await loadChats(false);
    } catch (error) {
      setNotice(`${archived ? "归档" : "恢复"}失败：${error.message}`);
    }
  }

  function openEvents(id, chatIdForRefresh = activeChatId, refreshOnTerminal = false) {
    closeEvents();
    setEventsReplaying(true);
    const source = new EventSource(`/api/runs/${id}/events`);
    sourceRef.current = source;
    source.onopen = () => setEventsReplaying(false);
    source.onerror = () => {
      setEventsReplaying(false);
      if (!TERMINAL.has(runStatus)) setNotice("事件流暂时中断，浏览器会自动重连");
    };
    Object.keys(eventText).forEach((type) => {
      source.addEventListener(type, (raw) => {
        const event = JSON.parse(raw.data);
        setEvents((prev) => [...prev, event]);
        collectCLI(event);
        if (type === "approval.needed") {
          setRunStatus("waiting_for_approval");
        }
        if (type.startsWith("run.")) {
          const next = type.replace("run.", "");
          setRunStatus(next);
          if (TERMINAL.has(next)) {
            setEventsReplaying(false);
            source.close();
            if (refreshOnTerminal) {
              scheduleChatRefresh(chatIdForRefresh);
            }
          }
        }
      });
    });
  }

  function closeEvents() {
    if (sourceRef.current) {
      sourceRef.current.close();
      sourceRef.current = null;
    }
  }

  function resetRunPanels() {
    closeEvents();
    setRunId(null);
    setRunStatus("idle");
    setEvents([]);
    setCliLines([]);
    setNotice("等待新的 Run");
  }

  function scheduleChatRefresh(chatId = activeChatId) {
    clearTimeout(refreshTimerRef.current);
    refreshTimerRef.current = setTimeout(async () => {
      if (chatId) await selectChat(chatId);
    }, 250);
  }

  function collectCLI(event) {
    const payload = event.payload || {};
    if (event.type === "tool.started" && Array.isArray(payload.command)) {
      setCliLines((prev) => [...prev, `$ ${payload.command.join(" ")}`]);
    }
    if (event.type === "tool.output") {
      const next = [];
      if (payload.stdout) next.push(payload.stdout.trimEnd());
      if (payload.stderr) next.push(`[stderr]\n${payload.stderr.trimEnd()}`);
      if (payload.error) next.push(`[error] ${payload.error}`);
      if (payload.exit_code !== undefined) next.push(`[exit_code] ${payload.exit_code}`);
      if (payload.duration) next.push(`[duration] ${payload.duration}`);
      setCliLines((prev) => [...prev, ...next.filter(Boolean)]);
    }
    if (event.type === "approval.needed") {
      const command = Array.isArray(payload.command) ? payload.command.join(" ") : "unknown command";
      const reason = payload.reason || payload.error || "approval required";
      setCliLines((prev) => [...prev, `$ ${command}`, `[approval] ${reason}`]);
    }
  }

  const cliOutput = useMemo(() => {
    return cliLines.length > 0 ? cliLines.join("\n\n") : "暂无 CLI 输出。发送 /cli echo hello 可以测试。";
  }, [cliLines]);

  return (
    <main className="shell">
      <aside className="sidebar">
        <div className="brand">
          <div className="brand-mark">NA</div>
          <div>
            <h1>NiceAgent</h1>
            <p>远端 agent 控制台</p>
          </div>
        </div>

        <div className="sidebar-actions">
          <button onClick={createChat}>新建</button>
          <button onClick={() => loadChats(false)}>刷新</button>
        </div>

        <SectionTitle text="会话" />
        <div className="chat-filters">
          <input
            value={chatQuery}
            onChange={(event) => setChatQuery(event.target.value)}
            placeholder="搜索会话标题"
          />
          <label>
            <input
              type="checkbox"
              checked={showArchived}
              onChange={(event) => setShowArchived(event.target.checked)}
            />
            显示归档
          </label>
        </div>
        <div className="chat-list">
          {chatsLoading ? (
            <Empty text="正在加载会话" />
          ) : chatError ? (
            <Empty text={`会话加载失败：${chatError}`} />
          ) : chats.length === 0 ? (
            <Empty text={chatQuery ? "没有匹配的会话" : "还没有会话"} />
          ) : (
            chats.map((chat) => (
              <button
                key={chat.id}
                className={[
                  "chat-row",
                  chat.id === activeChatId ? "active" : "",
                  chat.archived ? "archived" : "",
                ]
                  .filter(Boolean)
                  .join(" ")}
                onClick={() => selectChat(chat.id)}
              >
                <span>
                  {chat.title || "未命名会话"}
                  {chat.archived && <em>归档</em>}
                </span>
                <small>{chat.message_count || 0} 条消息</small>
              </button>
            ))
          )}
        </div>

        <SectionTitle text="Skills" />
        <div className="skill-list">
          {skills.map((skill) => (
            <article className="skill-card" key={skill.id}>
              <div className="skill-head">
                <strong>{skill.name || skill.id}</strong>
                <span>{riskText[skill.risk] || skill.risk || "未知"}</span>
              </div>
              <p>{skill.description || "暂无说明"}</p>
              {skill.requires_auth && <button onClick={() => approveSkill(skill.id)}>授权</button>}
            </article>
          ))}
        </div>
      </aside>

      <section className="main">
        <header className="topbar">
          <div>
            <div className="overline">当前会话</div>
            <h2>{activeChat?.title || "新的对话"}</h2>
            {activeChat?.archived && <p className="topbar-note">此会话已归档，可恢复后继续使用。</p>}
          </div>
          <div className="run-state">
            <span className={`status ${runStatus}`}>{statusText[runStatus] || runStatus}</span>
            {activeChat && (
              <button className="line-button" onClick={() => setActiveChatArchived(!activeChat.archived)}>
                {activeChat.archived ? "恢复" : "归档"}
              </button>
            )}
            <button className="line-button" disabled={!canCancel} onClick={cancelRun}>
              取消
            </button>
          </div>
        </header>

        <section className="conversation">
          {messageLoading ? (
            <Empty text="正在加载消息" />
          ) : messages.length === 0 ? (
            <div className="welcome">
              <h3>今天要让远端 agent 做什么？</h3>
              <p>可以先试试 `/cli echo hello`，观察右侧事件和 CLI 输出。</p>
            </div>
          ) : (
            messages.map((message) => <Message key={message.id} message={message} />)
          )}
        </section>

        <section className="activity">
          <div className="activity-head">
            <div>
              <SectionTitle text="运行事件" />
              <p>{eventsReplaying ? "正在恢复事件流" : eventSummary}</p>
            </div>
            <span>{events.length}</span>
          </div>
          <div className="activity-grid">
            <div className="event-list">
              {events.length === 0 ? (
                <Empty text="暂无事件" />
              ) : (
                events.map((event) => <RunEvent key={event.id} event={event} />)
              )}
            </div>
            <pre className="cli-output">{cliOutput}</pre>
          </div>
        </section>

        <form className="composer" onSubmit={sendMessage}>
          <textarea
            value={input}
            onChange={(event) => setInput(event.target.value)}
            placeholder="发送消息给远端 agent"
            rows={3}
            disabled={sending}
          />
          <button type="submit" disabled={!canSend}>{sending ? "发送中" : "发送"}</button>
        </form>
        <div className="notice">{notice}</div>
      </section>
    </main>
  );
}

function Message({ message }) {
  return (
    <article className={`message ${message.role}`}>
      <div className="message-role">{roleText[message.role] || message.role}</div>
      <p>{message.content}</p>
    </article>
  );
}

function RunEvent({ event }) {
  return (
    <article className="event-row">
      <div>
        <strong>{eventText[event.type] || event.type}</strong>
        <small>#{event.seq}</small>
      </div>
      <p>{event.message || payloadSummary(event.payload)}</p>
    </article>
  );
}

function payloadSummary(payload) {
  if (!payload) return "无详情";
  if (payload.command) return `$ ${payload.command.join(" ")}`;
  if (payload.stdout) return payload.stdout.trim();
  if (payload.error) return payload.error;
  return JSON.stringify(payload);
}

function SectionTitle({ text }) {
  return <div className="section-title">{text}</div>;
}

function Empty({ text }) {
  return <div className="empty">{text}</div>;
}
