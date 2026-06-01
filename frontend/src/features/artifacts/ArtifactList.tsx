import { artifactDownloadPath } from "../../api/artifacts";
import type { Artifact } from "../../domain/artifact";
import styles from "./ArtifactList.module.scss";

interface ArtifactListProps {
  artifacts: Artifact[];
  error: string;
  loading: boolean;
}

export function ArtifactList({ artifacts, error, loading }: ArtifactListProps) {
  if (!loading && !error && artifacts.length === 0) return null;

  return (
    <aside className={styles.artifacts} aria-label="Run artifacts">
      <div className={styles.header}>
        <div>
          <div className={styles.overline}>Artifacts</div>
          <h3>本次产物</h3>
        </div>
        {loading && <span className={styles.loading}>同步中</span>}
      </div>

      {error && (
        <p className={styles.error} role="alert">
          产物加载失败：{error}
        </p>
      )}

      {artifacts.length > 0 && (
        <div className={styles.list}>
          {artifacts.map((artifact) => {
            const displayName = artifact.name || artifact.path || artifact.id;
            const downloadPath = artifactDownloadPath(artifact.id);
            const isImage = isImageArtifact(artifact);
            return (
              <article className={styles.item} key={artifact.id || artifact.path}>
                <div className={styles.content}>
                  {artifact.id && isImage && (
                    <a
                      className={styles.preview}
                      href={downloadPath}
                      aria-label={`预览 ${displayName}`}
                    >
                      <img alt={displayName} loading="lazy" src={downloadPath} />
                    </a>
                  )}
                  <div className={styles.meta}>
                    <strong>{displayName}</strong>
                    <span>{artifact.path || "output"}</span>
                    {isImage && <span>图片预览</span>}
                  </div>
                </div>
                <div className={styles.actions}>
                  <span>{formatBytes(artifact.size_bytes)}</span>
                  {artifact.id && (
                    <a
                      aria-label={`下载 ${displayName}`}
                      download={displayName}
                      href={downloadPath}
                    >
                      下载
                    </a>
                  )}
                </div>
              </article>
            );
          })}
        </div>
      )}
    </aside>
  );
}

function isImageArtifact(artifact: Artifact): boolean {
  return artifact.mime_type.toLowerCase().split(";")[0].trim().startsWith("image/");
}

function formatBytes(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "大小未知";
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`;
  return `${(value / 1024 / 1024).toFixed(1)} MB`;
}
