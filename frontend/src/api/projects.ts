import { api } from "./client";
import type {
  ProjectQuotaPolicy,
  ProjectQuotaPolicyResponse,
  ProjectRuntimePolicy,
  ProjectRuntimePolicyInput,
  ProjectRuntimePolicyResponse,
  ProjectUsageResponse,
} from "../domain/project";

export function getProjectQuotaPolicy(projectID: string): Promise<ProjectQuotaPolicy> {
  return api<ProjectQuotaPolicyResponse>(
    `/api/projects/${encodeURIComponent(projectID)}/quota`,
  ).then((response) => response.policy);
}

export function getProjectRuntimePolicy(projectID: string): Promise<ProjectRuntimePolicy> {
  return api<ProjectRuntimePolicyResponse>(
    `/api/projects/${encodeURIComponent(projectID)}/runtime-policy`,
  ).then((response) => response.policy);
}

export function updateProjectRuntimePolicy(
  projectID: string,
  input: ProjectRuntimePolicyInput,
): Promise<ProjectRuntimePolicy> {
  return api<ProjectRuntimePolicyResponse>(
    `/api/projects/${encodeURIComponent(projectID)}/runtime-policy`,
    {
      method: "PATCH",
      body: JSON.stringify(input),
    },
  ).then((response) => response.policy);
}

export function getProjectUsage(
  projectID: string,
  window: "24h" | "7d" | "30d" = "24h",
): Promise<ProjectUsageResponse> {
  const params = new URLSearchParams({ window });
  return api<ProjectUsageResponse>(
    `/api/projects/${encodeURIComponent(projectID)}/usage?${params.toString()}`,
  );
}
