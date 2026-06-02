import { api } from "./client";
import type {
  ProjectRuntimePolicy,
  ProjectRuntimePolicyInput,
  ProjectRuntimePolicyResponse,
} from "../domain/project";

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
