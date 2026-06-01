export interface Artifact {
  id: string;
  run_id: string;
  chat_id?: string;
  user_id?: string;
  project_id?: string;
  workspace_id?: string;
  path: string;
  name?: string;
  mime_type: string;
  size_bytes: number;
  sha256?: string;
  storage_backend?: string;
  storage_key?: string;
  created_at?: string;
  deleted_at?: string;
}

export interface ArtifactListResponse {
  artifacts: Artifact[];
}

export interface ArtifactTextResponse {
  artifact: Artifact;
  content: string;
  truncated: boolean;
  bytes_read: number;
}
