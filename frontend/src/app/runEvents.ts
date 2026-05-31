import type { Artifact } from "../domain/artifact";
import type { RunEvent, RunStatus } from "../domain/run";

export interface FoldedRunState {
  assistantToken?: string;
  agentStatus?: string;
  artifact?: Artifact;
  runStatus?: RunStatus;
  terminal?: boolean;
}

export function foldRunEvent(event: RunEvent): FoldedRunState {
  const payload = event.payload || {};
  if (event.type === "run.queued") {
    return { agentStatus: "Agent 正在排队" };
  }
  if (event.type === "run.started") {
    return { agentStatus: "Agent Runtime 已开始处理", runStatus: "running" };
  }
  if (event.type === "model.token") {
    return { agentStatus: "Agent 正在生成回复", assistantToken: event.message || "" };
  }
  if (event.type === "tool.started") {
    const command = Array.isArray(payload.command) ? payload.command.join(" ") : "";
    const skillID = typeof payload.skill_id === "string" ? payload.skill_id : "";
    return {
      agentStatus: command
        ? `正在通过系统 CLI 获取信息：${command}`
        : skillID
          ? `正在调用能力：${skillID}`
          : "Agent 正在选择可用能力",
    };
  }
  if (event.type === "tool.output") {
    return {
      agentStatus: payload.error ? "能力返回了策略或执行错误" : "能力信息已获取",
    };
  }
  if (event.type === "tool.finished") {
    return { agentStatus: "工具调用完成，Agent 正在整理回复" };
  }
  if (event.type === "artifact.created") {
    const artifact = artifactFromPayload(payload);
    const name = artifact?.name || artifact?.path;
    return {
      agentStatus: name ? `已生成产物：${name}` : "已生成新的文件产物",
      artifact: artifact || undefined,
    };
  }
  if (event.type === "approval.needed") {
    return { agentStatus: "有能力需要用户确认", runStatus: "waiting_for_approval" };
  }
  if (event.type === "run.failed") {
    return {
      agentStatus: "Run 失败",
      assistantToken: `执行失败：${event.message || "未知错误"}`,
      runStatus: "failed",
      terminal: true,
    };
  }
  if (event.type === "run.canceled") {
    return { agentStatus: "Run 已取消", runStatus: "canceled", terminal: true };
  }
  if (event.type === "run.succeeded") {
    return { agentStatus: "Agent 已完成回复", runStatus: "succeeded", terminal: true };
  }
  return {};
}

function artifactFromPayload(payload: Record<string, unknown>): Artifact | null {
  const id = stringValue(payload.id);
  const path = stringValue(payload.path);
  if (!id && !path) return null;
  return {
    id,
    run_id: stringValue(payload.run_id),
    chat_id: optionalStringValue(payload.chat_id),
    user_id: optionalStringValue(payload.user_id),
    project_id: optionalStringValue(payload.project_id),
    workspace_id: optionalStringValue(payload.workspace_id),
    path,
    name: optionalStringValue(payload.name),
    mime_type: stringValue(payload.mime_type),
    size_bytes: numberValue(payload.size_bytes),
    sha256: optionalStringValue(payload.sha256),
    storage_backend: optionalStringValue(payload.storage_backend),
    storage_key: optionalStringValue(payload.storage_key),
    created_at: optionalStringValue(payload.created_at),
    deleted_at: optionalStringValue(payload.deleted_at),
  };
}

function stringValue(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function optionalStringValue(value: unknown): string | undefined {
  return typeof value === "string" && value ? value : undefined;
}

function numberValue(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

export function statusFromRunEventType(type: RunEvent["type"]): RunStatus | null {
  if (!type.startsWith("run.")) return null;
  const next = type.replace("run.", "") as RunStatus;
  return next;
}
