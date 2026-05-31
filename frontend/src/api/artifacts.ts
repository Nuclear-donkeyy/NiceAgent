import { api } from "./client";
import type { Artifact, ArtifactListResponse } from "../domain/artifact";

export async function listRunArtifacts(runID: string): Promise<Artifact[]> {
  const data = await api<ArtifactListResponse>(`/api/runs/${encodeURIComponent(runID)}/artifacts`);
  return data.artifacts || [];
}

export function artifactDownloadPath(artifactID: string): string {
  return `/api/artifacts/${encodeURIComponent(artifactID)}/download`;
}
