import { api } from "./client";
import type { Run } from "../domain/run";

export function getRun(runID: string): Promise<Run> {
  return api<Run>(`/api/runs/${encodeURIComponent(runID)}`);
}

export function cancelRun(runID: string): Promise<Run> {
  return api<Run>(`/api/runs/${encodeURIComponent(runID)}/cancel`, { method: "POST" });
}
