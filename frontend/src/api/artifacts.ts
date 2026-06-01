import { api } from "./client";
import type { Artifact, ArtifactListResponse, ArtifactTextResponse } from "../domain/artifact";

export async function listRunArtifacts(runID: string): Promise<Artifact[]> {
  const data = await api<ArtifactListResponse>(`/api/runs/${encodeURIComponent(runID)}/artifacts`);
  return data.artifacts || [];
}

export function artifactDownloadPath(artifactID: string): string {
  return `/api/artifacts/${encodeURIComponent(artifactID)}/download`;
}

export function artifactPreviewPath(artifactID: string): string {
  return `${artifactDownloadPath(artifactID)}?disposition=inline`;
}

export async function readArtifactContent(
  artifactID: string,
  maxBytes = 16384,
): Promise<ArtifactTextResponse> {
  return api<ArtifactTextResponse>(
    `/api/artifacts/${encodeURIComponent(artifactID)}/content?max_bytes=${maxBytes}`,
  );
}
