import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";

import * as chatApi from "../api/chats";
import * as runApi from "../api/runs";
import * as skillApi from "../api/skills";
import type { ChatSession, Message } from "../domain/chat";
import type { HTTPSkillInput, SkillGroups } from "../domain/skill";
import type { RunEvent, RunStatus } from "../domain/run";
import { terminalRunStatuses } from "../domain/run";
import { runEventTypes, statusText } from "../domain/labels";
import { foldRunEvent, statusFromRunEventType } from "./runEvents";

const emptySkillGroups: SkillGroups = { system: [], user: [] };

export function useNiceAgentWorkspace() {
  const [chats, setChats] = useState<ChatSession[]>([]);
  const [activeChatId, setActiveChatId] = useState<string | null>(null);
  const [activeChat, setActiveChat] = useState<ChatSession | null>(null);
  const [messages, setMessages] = useState<Message[]>([]);
  const [skillGroups, setSkillGroups] = useState<SkillGroups>(emptySkillGroups);
  const [runId, setRunId] = useState<string | null>(null);
  const [runStatus, setRunStatus] = useState<RunStatus>("idle");
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
  const sourceRef = useRef<EventSource | null>(null);
  const refreshTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const runStatusRef = useRef<RunStatus>("idle");

  useEffect(() => {
    runStatusRef.current = runStatus;
  }, [runStatus]);

  const visibleMessages = useMemo<Message[]>(() => {
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

  const canCancel = Boolean(runId && !terminalRunStatuses.has(runStatus) && runStatus !== "idle");

  useEffect(() => {
    void boot();
    return () => closeEvents();
  }, []);

  useEffect(() => {
    const timer = setTimeout(() => {
      void loadChats(false);
    }, 180);
    return () => clearTimeout(timer);
  }, [chatQuery, showArchived]);

  async function boot() {
    try {
      await Promise.all([loadChats(true), loadSkills()]);
    } catch (error) {
      setNotice(errorMessage(error));
    }
  }

  async function loadChats(selectFirst = false) {
    setChatsLoading(true);
    setChatError("");
    try {
      const list = await chatApi.listChats({ query: chatQuery, includeArchived: showArchived });
      setChats(list);
      if (selectFirst && !activeChatId && list.length > 0) {
        await selectChat(list[0].id);
      }
    } catch (error) {
      const message = errorMessage(error);
      setChatError(message);
      setNotice(`会话加载失败：${message}`);
    } finally {
      setChatsLoading(false);
    }
  }

  async function loadSkills() {
    setSkillGroups(await skillApi.listSkills());
  }

  async function createHTTPSkill(inputValue: HTTPSkillInput) {
    try {
      await skillApi.createHTTPSkill(inputValue);
      setNotice("HTTP Skill 已添加");
      await loadSkills();
    } catch (error) {
      setNotice(`添加 Skill 失败：${errorMessage(error)}`);
    }
  }

  async function setSkillEnabled(skillID: string, enabled: boolean) {
    try {
      await skillApi.setSkillEnabled(skillID, enabled);
      setNotice(enabled ? "Skill 已启用" : "Skill 已停用");
      await loadSkills();
    } catch (error) {
      setNotice(`更新 Skill 失败：${errorMessage(error)}`);
    }
  }

  async function createChat() {
    try {
      const chat = await chatApi.createChat("新的远端任务");
      await loadChats(false);
      await selectChat(chat.id);
    } catch (error) {
      setNotice(`新建会话失败：${errorMessage(error)}`);
    }
  }

  async function selectChat(chatID: string) {
    closeEvents();
    resetRunPanels();
    setActiveChatId(chatID);
    setMessageLoading(true);
    try {
      const data = await chatApi.getChat(chatID);
      setActiveChat(data.chat);
      setMessages(data.messages || []);
      if (data.chat.last_run_id) {
        await loadRun(data.chat.last_run_id, chatID);
      }
      await loadChats(false);
    } catch (error) {
      setNotice(`会话加载失败：${errorMessage(error)}`);
    } finally {
      setMessageLoading(false);
    }
  }

  async function loadRun(id: string, chatIdForRefresh = activeChatId) {
    const run = await runApi.getRun(id);
    setRunId(run.id);
    setRunStatus(run.status || "idle");
    setNotice(run.error ? `最近 Run 失败：${run.error}` : `正在查看 Run：${run.id}`);
    if (!terminalRunStatuses.has(run.status || "idle")) {
      openEvents(run.id, chatIdForRefresh, true);
    } else {
      setAssistantDraft("");
      setAgentStatus("");
    }
  }

  async function sendMessage(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const content = input.trim();
    if (!content) return;
    setSending(true);
    try {
      let chatID = activeChatId;
      if (!chatID) {
        const chat = await chatApi.createChat("新的远端任务");
        chatID = chat.id;
        setActiveChatId(chatID);
        setActiveChat(chat);
      }
      setInput("");
      resetRunPanels();
      setRunStatus("queued");
      setAgentStatus("Agent 已接收任务，正在排队启动");
      const response = await chatApi.sendChatMessage(chatID, content);
      setMessages((prev) => [...prev, response.message]);
      setRunId(response.run.id);
      setRunStatus(response.run.status);
      openEvents(response.run.id, chatID, true);
      await loadChats(false);
    } catch (error) {
      setNotice(`发送失败：${errorMessage(error)}`);
      setRunStatus("failed");
    } finally {
      setSending(false);
    }
  }

  async function cancelRun() {
    if (!runId || !canCancel) return;
    const run = await runApi.cancelRun(runId);
    setRunStatus(run.status);
    setNotice("已发送取消请求");
  }

  async function setActiveChatArchived(archived: boolean) {
    if (!activeChatId) return;
    try {
      const chat = await chatApi.setChatArchived(activeChatId, archived);
      setActiveChat(chat);
      setNotice(archived ? "会话已归档" : "会话已恢复");
      await loadChats(false);
    } catch (error) {
      setNotice(`${archived ? "归档" : "恢复"}失败：${errorMessage(error)}`);
    }
  }

  function openEvents(id: string, chatIdForRefresh = activeChatId, refreshOnTerminal = false) {
    closeEvents();
    setAgentStatus("正在连接 Agent Runtime");
    const source = new EventSource(`/api/runs/${encodeURIComponent(id)}/events`);
    sourceRef.current = source;
    source.onopen = () => setAgentStatus("Agent Runtime 已连接");
    source.onerror = () => {
      if (!terminalRunStatuses.has(runStatusRef.current)) setNotice("连接暂时中断，浏览器会自动重连");
    };
    runEventTypes.forEach((type) => {
      source.addEventListener(type, (raw) => {
        const event = JSON.parse((raw as MessageEvent<string>).data) as RunEvent;
        applyRunEvent(event);
        const next = statusFromRunEventType(type);
        if (next) {
          setRunStatus(next);
          if (terminalRunStatuses.has(next)) {
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

  function applyRunEvent(event: RunEvent) {
    const next = foldRunEvent(event);
    if (next.agentStatus) setAgentStatus(next.agentStatus);
    if (next.runStatus) setRunStatus(next.runStatus);
    if (next.assistantToken) {
      setAssistantDraft((prev) => {
        if (event.type === "run.failed" && prev) return prev;
        return event.type === "run.failed" ? next.assistantToken || prev : prev + next.assistantToken;
      });
    }
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

  function scheduleChatRefresh(chatID = activeChatId) {
    if (refreshTimerRef.current) clearTimeout(refreshTimerRef.current);
    refreshTimerRef.current = setTimeout(() => {
      if (chatID) void selectChat(chatID);
    }, 250);
  }

  return {
    activeChat,
    activeChatId,
    agentStatus,
    canCancel,
    cancelRun,
    chatError,
    chatQuery,
    chats,
    chatsLoading,
    createChat,
    createHTTPSkill,
    input,
    messageLoading,
    notice,
    refreshChats: () => loadChats(false),
    runStatus,
    selectChat,
    sendMessage,
    sending,
    setActiveChatArchived,
    setChatQuery,
    setInput,
    setShowArchived,
    setSkillEnabled,
    showArchived,
    skillGroups,
    visibleMessages,
  };
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
