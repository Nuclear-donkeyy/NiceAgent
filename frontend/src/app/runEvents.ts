import type { RunEvent, RunStatus } from "../domain/run";

export interface FoldedRunState {
  assistantToken?: string;
  agentStatus?: string;
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

export function statusFromRunEventType(type: RunEvent["type"]): RunStatus | null {
  if (!type.startsWith("run.")) return null;
  const next = type.replace("run.", "") as RunStatus;
  return next;
}
