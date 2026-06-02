import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";

import * as artifactApi from "../api/artifacts";
import * as chatApi from "../api/chats";
import * as projectApi from "../api/projects";
import * as runApi from "../api/runs";
import * as skillApi from "../api/skills";
import type { Artifact } from "../domain/artifact";
import type { ChatSession, Message } from "../domain/chat";
import type { ProjectRuntimePolicy, SkillRiskPolicy } from "../domain/project";
import type {
  HTTPSkillInput,
  MCPImportCreateInput,
  MCPImportPreviewInput,
  MCPImportPreviewResponse,
  OpenAPIImportCreateInput,
  OpenAPIImportPreviewInput,
  OpenAPIImportPreviewResponse,
  SkillGroups,
} from "../domain/skill";
import type { RunEvent, RunStatus } from "../domain/run";
import { terminalRunStatuses } from "../domain/run";
import { runEventTypes, statusText } from "../domain/labels";
import { foldRunEvent, statusFromRunEventType } from "./runEvents";

const emptySkillGroups: SkillGroups = { system: [], user: [] };
const demoProjectID = "demo-project";
const defaultRuntimePolicy: ProjectRuntimePolicy = {
  project_id: demoProjectID,
  skill_risk_policy: "allow",
};

export function useNiceAgentWorkspace() {
  const [chats, setChats] = useState<ChatSession[]>([]);
  const [activeChatId, setActiveChatId] = useState<string | null>(null);
  const [activeChat, setActiveChat] = useState<ChatSession | null>(null);
  const [messages, setMessages] = useState<Message[]>([]);
  const [skillGroups, setSkillGroups] = useState<SkillGroups>(emptySkillGroups);
  const [runtimePolicy, setRuntimePolicy] = useState<ProjectRuntimePolicy>(defaultRuntimePolicy);
  const [runtimePolicyLoading, setRuntimePolicyLoading] = useState(false);
  const [runId, setRunId] = useState<string | null>(null);
  const [runStatus, setRunStatus] = useState<RunStatus>("idle");
  const [assistantDraft, setAssistantDraft] = useState("");
  const [agentStatus, setAgentStatus] = useState("");
  const [artifacts, setArtifacts] = useState<Artifact[]>([]);
  const [artifactLoading, setArtifactLoading] = useState(false);
  const [artifactError, setArtifactError] = useState("");
  const [input, setInput] = useState("");
  const [notice, setNotice] = useState("准备就绪");
  const [chatQuery, setChatQuery] = useState("");
  const [showArchived, setShowArchived] = useState(false);
  const [chatsLoading, setChatsLoading] = useState(false);
  const [chatError, setChatError] = useState("");
  const [messageLoading, setMessageLoading] = useState(false);
  const [sending, setSending] = useState(false);
  const sourceRef = useRef<EventSource | null>(null);
  const lastSeqByRunRef = useRef<Map<string, number>>(new Map());
  const refreshTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const runStatusRef = useRef<RunStatus>("idle");
  const reconnectingRef = useRef(false);

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
      await Promise.all([loadChats(true), loadSkills(), loadRuntimePolicy()]);
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

  async function loadRuntimePolicy() {
    setRuntimePolicyLoading(true);
    try {
      setRuntimePolicy(await projectApi.getProjectRuntimePolicy(demoProjectID));
    } catch (error) {
      const message = errorMessage(error);
      setNotice(`运行策略加载失败：${message}`);
    } finally {
      setRuntimePolicyLoading(false);
    }
  }

  async function updateRuntimeRiskPolicy(policy: SkillRiskPolicy) {
    setRuntimePolicyLoading(true);
    try {
      const updated = await projectApi.updateProjectRuntimePolicy(demoProjectID, {
        skill_risk_policy: policy,
      });
      setRuntimePolicy(updated);
      setNotice("项目运行策略已更新");
    } catch (error) {
      const message = errorMessage(error);
      setNotice(`更新运行策略失败：${message}`);
      throw new Error(message, { cause: error });
    } finally {
      setRuntimePolicyLoading(false);
    }
  }

  async function createHTTPSkill(inputValue: HTTPSkillInput) {
    try {
      await skillApi.createHTTPSkill(inputValue);
      setNotice("HTTP Skill 已添加");
      await loadSkills();
    } catch (error) {
      const message = errorMessage(error);
      setNotice(`添加 Skill 失败：${message}`);
      throw new Error(message, { cause: error });
    }
  }

  async function previewOpenAPIImport(
    inputValue: OpenAPIImportPreviewInput,
  ): Promise<OpenAPIImportPreviewResponse> {
    try {
      return await skillApi.previewOpenAPIImport(inputValue);
    } catch (error) {
      const message = errorMessage(error);
      setNotice(`OpenAPI 预览失败：${message}`);
      throw new Error(message, { cause: error });
    }
  }

  async function createOpenAPIImportedSkill(inputValue: OpenAPIImportCreateInput) {
    try {
      await skillApi.createOpenAPIImportedSkill(inputValue);
      setNotice("OpenAPI Skill 已导入");
      await loadSkills();
    } catch (error) {
      const message = errorMessage(error);
      setNotice(`导入 OpenAPI Skill 失败：${message}`);
      throw new Error(message, { cause: error });
    }
  }

  async function createMCPImportedSkill(inputValue: MCPImportCreateInput) {
    try {
      await skillApi.createMCPImportedSkill(inputValue);
      setNotice("MCP Skill 已导入");
      await loadSkills();
    } catch (error) {
      const message = errorMessage(error);
      setNotice(`导入 MCP Skill 失败：${message}`);
      throw new Error(message, { cause: error });
    }
  }

  async function previewMCPImport(
    inputValue: MCPImportPreviewInput,
  ): Promise<MCPImportPreviewResponse> {
    try {
      return await skillApi.previewMCPImport(inputValue);
    } catch (error) {
      const message = errorMessage(error);
      setNotice(`MCP 预览失败：${message}`);
      throw new Error(message, { cause: error });
    }
  }

  async function setSkillEnabled(skillID: string, enabled: boolean) {
    try {
      await skillApi.setSkillEnabled(skillID, enabled);
      setNotice(enabled ? "Skill 已启用" : "Skill 已停用");
      await loadSkills();
    } catch (error) {
      const message = errorMessage(error);
      setNotice(`更新 Skill 失败：${message}`);
      throw new Error(message, { cause: error });
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
    await loadArtifacts(run.id);
    if (!terminalRunStatuses.has(run.status || "idle")) {
      openEvents(run.id, chatIdForRefresh, true);
    } else {
      setAssistantDraft("");
      setAgentStatus("");
    }
  }

  async function loadArtifacts(id: string) {
    setArtifactLoading(true);
    setArtifactError("");
    try {
      setArtifacts(await artifactApi.listRunArtifacts(id));
    } catch (error) {
      const message = errorMessage(error);
      setArtifactError(message);
      setNotice(`产物加载失败：${message}`);
    } finally {
      setArtifactLoading(false);
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
    reconnectingRef.current = false;
    setAgentStatus("正在连接 Agent Runtime");
    const afterSeq = lastSeqByRunRef.current.get(id) || 0;
    const params = afterSeq > 0 ? `?after=${encodeURIComponent(String(afterSeq))}` : "";
    const source = new EventSource(`/api/runs/${encodeURIComponent(id)}/events${params}`);
    sourceRef.current = source;
    source.onopen = () => {
      if (sourceRef.current !== source || terminalRunStatuses.has(runStatusRef.current)) return;
      if (reconnectingRef.current) {
        reconnectingRef.current = false;
        setAgentStatus("连接已恢复，正在补齐事件");
        setNotice("连接已恢复");
        return;
      }
      setAgentStatus("Agent Runtime 已连接");
    };
    source.onerror = () => {
      if (sourceRef.current !== source || terminalRunStatuses.has(runStatusRef.current)) return;
      reconnectingRef.current = true;
      setAgentStatus("连接暂时中断，正在重连");
      setNotice("连接暂时中断，浏览器会自动重连");
    };
    runEventTypes.forEach((type) => {
      source.addEventListener(type, (raw) => {
        const event = JSON.parse((raw as MessageEvent<string>).data) as RunEvent;
        if (!markRunEventSeen(id, event)) return;
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

  function markRunEventSeen(expectedRunID: string, event: RunEvent): boolean {
    if (event.run_id && event.run_id !== expectedRunID) return false;
    const seq = Number(event.seq);
    if (!Number.isFinite(seq) || seq <= 0) return true;
    const lastSeq = lastSeqByRunRef.current.get(expectedRunID) || 0;
    if (seq <= lastSeq) return false;
    lastSeqByRunRef.current.set(expectedRunID, seq);
    return true;
  }

  function applyRunEvent(event: RunEvent) {
    const next = foldRunEvent(event);
    if (next.agentStatus) setAgentStatus(next.agentStatus);
    if (next.runStatus) setRunStatus(next.runStatus);
    if (next.artifact) setArtifacts((prev) => upsertArtifact(prev, next.artifact as Artifact));
    if (next.assistantToken) {
      setAssistantDraft((prev) => {
        if (event.type === "run.failed" && prev) return prev;
        return event.type === "run.failed"
          ? next.assistantToken || prev
          : prev + next.assistantToken;
      });
    }
  }

  function closeEvents() {
    if (sourceRef.current) {
      sourceRef.current.close();
      sourceRef.current = null;
    }
    reconnectingRef.current = false;
  }

  function resetRunPanels() {
    closeEvents();
    setRunId(null);
    setRunStatus("idle");
    setAssistantDraft("");
    setAgentStatus("");
    setArtifacts([]);
    setArtifactError("");
    setArtifactLoading(false);
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
    artifactError,
    artifactLoading,
    artifacts,
    canCancel,
    cancelRun,
    chatError,
    chatQuery,
    chats,
    chatsLoading,
    createChat,
    createHTTPSkill,
    previewOpenAPIImport,
    createOpenAPIImportedSkill,
    createMCPImportedSkill,
    previewMCPImport,
    input,
    messageLoading,
    notice,
    refreshChats: () => loadChats(false),
    runtimePolicy,
    runtimePolicyLoading,
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
    updateRuntimeRiskPolicy,
    visibleMessages,
  };
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function upsertArtifact(existing: Artifact[], next: Artifact): Artifact[] {
  const nextKey = artifactKey(next);
  if (!nextKey) return existing;
  const index = existing.findIndex((artifact) => artifactKey(artifact) === nextKey);
  if (index === -1) return [...existing, next];
  return existing.map((artifact, currentIndex) => (currentIndex === index ? next : artifact));
}

function artifactKey(artifact: Artifact): string {
  return artifact.id || artifact.path;
}
