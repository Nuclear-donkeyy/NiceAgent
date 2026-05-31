import type { MessageRole } from "./chat";
import type { RunEventType, RunStatus } from "./run";

export const statusText: Record<RunStatus, string> = {
  idle: "空闲",
  queued: "排队中",
  running: "运行中",
  waiting_for_approval: "等待确认",
  succeeded: "已完成",
  failed: "失败",
  canceled: "已取消",
};

export const roleText: Record<MessageRole, string> = {
  user: "用户",
  assistant: "Agent",
  system: "系统",
  tool: "工具",
};

export const runEventTypes: RunEventType[] = [
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
];
