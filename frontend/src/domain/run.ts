export type RunStatus =
  | "idle"
  | "queued"
  | "running"
  | "waiting_for_approval"
  | "succeeded"
  | "failed"
  | "canceled";

export const terminalRunStatuses = new Set<RunStatus>(["succeeded", "failed", "canceled"]);

export interface Run {
  id: string;
  chat_id: string;
  user_id: string;
  workspace_id: string;
  status: Exclude<RunStatus, "idle">;
  error?: string;
  created_at: string;
  updated_at: string;
  started_at?: string;
  finished_at?: string;
}

export type RunEventType =
  | "run.queued"
  | "run.started"
  | "model.token"
  | "tool.started"
  | "tool.output"
  | "tool.finished"
  | "approval.needed"
  | "artifact.created"
  | "run.succeeded"
  | "run.failed"
  | "run.canceled";

export interface RunEvent {
  id: string;
  run_id: string;
  chat_id: string;
  seq: number;
  type: RunEventType;
  message?: string;
  payload?: Record<string, unknown> | null;
  created_at: string;
}

export interface SendMessageResponse {
  message: import("./chat").Message;
  run: Run;
}
