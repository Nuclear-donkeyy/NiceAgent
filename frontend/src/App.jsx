import { useEffect, useMemo, useRef, useState } from "react";

const TERMINAL = new Set(["succeeded", "failed", "canceled"]);

const statusText = {
  idle: "空闲",
  queued: "排队中",
  running: "运行中",
  waiting_for_approval: "等待确认",
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
  "approval.needed": "等待确认",
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
  const [skillGroups, setSkillGroups] = useState({ system: [], user: [] });
  const [skillFormOpen, setSkillFormOpen] = useState(false);
  const [skillForm, setSkillForm] = useState({
    name: "",
    description: "",
    url: "",
    method: "POST",
    auth_type: "none",
    bearer_token: "",
  });
  const [runId, setRunId] = useState(null);
  const [runStatus, setRunStatus] = useState("idle");
  const [assistantDraft, setAssistantDraft] = useState("");
  const [agentStatus, setAgentStatus] = useState("");
  const [input, setInput] = useState("");
  const [notice, setNotice] = useState("准备就绪");
  const [chatQuery, setChatQuery] = useState("");
  const [showArchived, setShowArchived] = useState(false);
  const [chatsLoading, setChatsLoading] = useState(false);
  const [chatError, setChatError] = useState("");
  const [messageLoading, setMessageLoading] = useState(false);
  const [sending, setSending] = useState(false);
  const sourceRef = useRef(null);
  const refreshTimerRef = useRef(null);

  const canCancel = runId && !TERMINAL.has(runStatus) && runStatus !== "idle";
  const canSend = !sending && input.trim().length > 0;
  const visibleMessages = useMemo(() => {
    if (!assistantDraft && !agentStatus) return messages;
    return [
      ...messages,
      {
        id: "assistant-draft",
        role: "assistant",
        content: assistantDraft,
        status: agentStatus,
        transient: true,
      },
    ];
  }, [messages, assistantDraft, agentStatus]);

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
    const nextSkills = data.skills || [];
    const groups = data.groups || {};
    setSkills(nextSkills);
    setSkillGroups({
      system: groups.system || nextSkills.filter((skill) => skill.scope !== "user"),
      user: groups.user || nextSkills.filter((skill) => skill.scope === "user"),
    });
  }

  async function createHTTPSkill(event) {
    event.preventDefault();
    try {
      const payload = {
        ...skillForm,
        input_schema: "{\"type\":\"object\",\"additionalProperties\":true}",
      };
      if (payload.auth_type !== "bearer") {
        payload.bearer_token = "";
      }
      await api("/api/skills/http", {
        method: "POST",
        body: JSON.stringify(payload),
      });
      setSkillForm({
        name: "",
        description: "",
        url: "",
        method: "POST",
        auth_type: "none",
        bearer_token: "",
      });
      setSkillFormOpen(false);
      setNotice("HTTP Skill 已添加");
      await loadSkills();
    } catch (error) {
      setNotice(`添加 Skill 失败：${error.message}`);
    }
  }

  async function setSkillEnabled(skillID, enabled) {
    try {
      await api(`/api/skills/${encodeURIComponent(skillID)}/${enabled ? "enable" : "disable"}`, { method: "POST" });
      setNotice(enabled ? "Skill 已启用" : "Skill 已停用");
      await loadSkills();
    } catch (error) {
      setNotice(`更新 Skill 失败：${error.message}`);
    }
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
    if (!TERMINAL.has(run.status || "idle")) {
      openEvents(run.id, chatIdForRefresh, true);
    } else {
      setAssistantDraft("");
      setAgentStatus("");
    }
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
      setAgentStatus("Agent 已接收任务，正在排队启动");
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
    setAgentStatus("正在连接 Agent Runtime");
    const source = new EventSource(`/api/runs/${id}/events`);
    sourceRef.current = source;
    source.onopen = () => setAgentStatus("Agent Runtime 已连接");
    source.onerror = () => {
      if (!TERMINAL.has(runStatus)) setNotice("连接暂时中断，浏览器会自动重连");
    };
    Object.keys(eventText).forEach((type) => {
      source.addEventListener(type, (raw) => {
        const event = JSON.parse(raw.data);
        foldRunEvent(event);
        if (type === "approval.needed") {
          setRunStatus("waiting_for_approval");
        }
        if (type.startsWith("run.")) {
          const next = type.replace("run.", "");
          setRunStatus(next);
          if (TERMINAL.has(next)) {
            source.close();
            setAgentStatus(next === "succeeded" ? "Agent 已完成回复" : statusText[next] || next);
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
    setAssistantDraft("");
    setAgentStatus("");
    setNotice("等待新的 Run");
  }

  function scheduleChatRefresh(chatId = activeChatId) {
    clearTimeout(refreshTimerRef.current);
    refreshTimerRef.current = setTimeout(async () => {
      if (chatId) await selectChat(chatId);
    }, 250);
  }

  function foldRunEvent(event) {
    const payload = event.payload || {};
    if (event.type === "run.queued") {
      setAgentStatus("Agent 正在排队");
    } else if (event.type === "run.started") {
      setAgentStatus("Agent Runtime 已开始处理");
    } else if (event.type === "model.token") {
      setAgentStatus("Agent 正在生成回复");
      setAssistantDraft((prev) => prev + (event.message || ""));
    } else if (event.type === "tool.started") {
      const command = Array.isArray(payload.command) ? payload.command.join(" ") : "unknown command";
      setAgentStatus(command === "unknown command" ? "Agent 正在选择可用能力" : `正在通过系统 CLI 获取信息：${command}`);
    } else if (event.type === "tool.output") {
      setAgentStatus(payload.error ? "系统 CLI 返回了策略或执行错误" : "系统 CLI 信息已获取");
    } else if (event.type === "tool.finished") {
      setAgentStatus("工具调用完成，Agent 正在整理回复");
    } else if (event.type === "run.failed") {
      setAgentStatus("Run 失败");
      setAssistantDraft((prev) => prev || `执行失败：${event.message || "未知错误"}`);
    } else if (event.type === "run.canceled") {
      setAgentStatus("Run 已取消");
    } else if (event.type === "approval.needed") {
      setAgentStatus("有能力需要用户确认");
    }
  }

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

        <SectionTitle text="系统能力" />
        <div className="skill-list">
          {skillGroups.system.map((skill) => (
            <article className="skill-card" key={skill.id}>
              <div className="skill-head">
                <strong>{skill.name || skill.id}</strong>
                <span>系统</span>
              </div>
              <p>{skill.description || "暂无说明"}</p>
            </article>
          ))}
          {skillGroups.system.length === 0 && <Empty text="暂无系统能力" />}
        </div>

        <div className="section-row">
          <SectionTitle text="我的能力" />
          <button className="mini-button" onClick={() => setSkillFormOpen((value) => !value)}>
            {skillFormOpen ? "收起" : "添加"}
          </button>
        </div>
        {skillFormOpen && (
          <form className="skill-form" onSubmit={createHTTPSkill}>
            <input
              value={skillForm.name}
              onChange={(event) => setSkillForm((prev) => ({ ...prev, name: event.target.value }))}
              placeholder="Skill 名称"
            />
            <input
              value={skillForm.url}
              onChange={(event) => setSkillForm((prev) => ({ ...prev, url: event.target.value }))}
              placeholder="https://api.example.com/tool"
            />
            <textarea
              value={skillForm.description}
              onChange={(event) => setSkillForm((prev) => ({ ...prev, description: event.target.value }))}
              placeholder="什么时候应该调用这个能力"
              rows={2}
            />
            <div className="skill-form-row">
              <select
                value={skillForm.method}
                onChange={(event) => setSkillForm((prev) => ({ ...prev, method: event.target.value }))}
              >
                <option value="POST">POST</option>
                <option value="GET">GET</option>
              </select>
              <select
                value={skillForm.auth_type}
                onChange={(event) => setSkillForm((prev) => ({ ...prev, auth_type: event.target.value }))}
              >
                <option value="none">无鉴权</option>
                <option value="bearer">Bearer</option>
              </select>
            </div>
            {skillForm.auth_type === "bearer" && (
              <input
                value={skillForm.bearer_token}
                onChange={(event) => setSkillForm((prev) => ({ ...prev, bearer_token: event.target.value }))}
                placeholder="Bearer token，不会展示给前端列表"
                type="password"
              />
            )}
            <button type="submit">保存 HTTP Skill</button>
          </form>
        )}
        <div className="skill-list">
          {skillGroups.user.map((skill) => (
            <article className="skill-card" key={skill.id}>
              <div className="skill-head">
                <strong>{skill.name || skill.id}</strong>
                <span>{skill.enabled ? "已启用" : "已停用"}</span>
              </div>
              <p>{skill.description || "暂无说明"}</p>
              <button onClick={() => setSkillEnabled(skill.id, !skill.enabled)}>
                {skill.enabled ? "停用" : "启用"}
              </button>
            </article>
          ))}
          {skillGroups.user.length === 0 && <Empty text="还没有用户 Skill" />}
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
              <p>可以先试试 `/cli curl https://example.com`，让 agent 通过系统 CLI 获取外部信息。</p>
            </div>
          ) : (
            visibleMessages.map((message) => <Message key={message.id} message={message} />)
          )}
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
      {message.status && <div className="agent-status">{message.status}</div>}
      {message.content ? <p>{message.content}</p> : <p className="muted-text">Agent 正在思考...</p>}
    </article>
  );
}

function SectionTitle({ text }) {
  return <div className="section-title">{text}</div>;
}

function Empty({ text }) {
  return <div className="empty">{text}</div>;
}
