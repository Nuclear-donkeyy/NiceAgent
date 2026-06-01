import { useEffect, useMemo, useState } from "react";
import {
  artifactDownloadPath,
  artifactPreviewPath,
  readArtifactContent,
} from "../../api/artifacts";
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
            const previewPath = artifactPreviewPath(artifact.id);
            const previewType = previewKind(artifact);
            return (
              <article className={styles.item} key={artifact.id || artifact.path}>
                <div className={styles.content}>
                  {artifact.id && (
                    <ArtifactPreview
                      artifact={artifact}
                      displayName={displayName}
                      previewPath={previewPath}
                      previewType={previewType}
                    />
                  )}
                  <div className={styles.meta}>
                    <strong>{displayName}</strong>
                    <span>{artifact.path || "output"}</span>
                    <span>{artifactKindLabel(artifact, previewType)}</span>
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

interface ArtifactPreviewProps {
  artifact: Artifact;
  displayName: string;
  previewPath: string;
  previewType: PreviewKind;
}

type PreviewKind = "image" | "pdf" | "audio" | "video" | "table" | "generic";

function ArtifactPreview({
  artifact,
  displayName,
  previewPath,
  previewType,
}: ArtifactPreviewProps) {
  if (previewType === "image") {
    return (
      <a className={styles.preview} href={previewPath} aria-label={`预览 ${displayName}`}>
        <img alt={displayName} loading="lazy" src={previewPath} />
      </a>
    );
  }
  if (previewType === "pdf") {
    return (
      <a
        className={`${styles.preview} ${styles.pdfPreview}`}
        href={previewPath}
        aria-label={`预览 ${displayName}`}
      >
        <iframe src={previewPath} title={displayName} loading="lazy" />
      </a>
    );
  }
  if (previewType === "audio") {
    return (
      <div
        className={`${styles.preview} ${styles.mediaPreview}`}
        aria-label={`预览 ${displayName}`}
      >
        <audio controls preload="none" src={previewPath} />
      </div>
    );
  }
  if (previewType === "video") {
    return (
      <a
        className={`${styles.preview} ${styles.mediaPreview}`}
        href={previewPath}
        aria-label={`预览 ${displayName}`}
      >
        <video muted preload="metadata" src={previewPath} />
      </a>
    );
  }
  if (previewType === "table") {
    return <TableArtifactPreview artifact={artifact} displayName={displayName} />;
  }
  return (
    <a
      className={`${styles.preview} ${styles.filePreview}`}
      href={previewPath}
      aria-label={`打开 ${displayName}`}
    >
      <span>{artifactExtension(artifact) || "FILE"}</span>
    </a>
  );
}

interface TableArtifactPreviewProps {
  artifact: Artifact;
  displayName: string;
}

function TableArtifactPreview({ artifact, displayName }: TableArtifactPreviewProps) {
  const [content, setContent] = useState("");
  const [truncated, setTruncated] = useState(false);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let canceled = false;
    setLoading(true);
    setError("");
    readArtifactContent(artifact.id, 16384)
      .then((response) => {
        if (canceled) return;
        setContent(response.content || "");
        setTruncated(Boolean(response.truncated));
      })
      .catch((err: unknown) => {
        if (canceled) return;
        setError(err instanceof Error ? err.message : "预览加载失败");
      })
      .finally(() => {
        if (!canceled) setLoading(false);
      });
    return () => {
      canceled = true;
    };
  }, [artifact.id]);

  const rows = useMemo(() => parseTableRows(content, artifact), [artifact, content]);
  const headers = rows[0] || [];
  const bodyRows = rows.slice(1, 6);

  return (
    <div className={styles.tablePreview} aria-label={`预览 ${displayName}`}>
      <div className={styles.tablePreviewHeader}>
        <span>{loading ? "表格同步中" : "表格预览"}</span>
        {truncated && <span>已截断</span>}
      </div>
      {error ? (
        <div className={styles.tablePreviewState}>{error}</div>
      ) : rows.length > 0 ? (
        <div className={styles.tableScroll}>
          <table>
            <tbody>
              {headers.length > 0 && (
                <tr>
                  {headers.slice(0, 5).map((cell, index) => (
                    <th key={`${cell}-${index}`}>{cell || "-"}</th>
                  ))}
                </tr>
              )}
              {bodyRows.map((row, rowIndex) => (
                <tr key={`row-${rowIndex}`}>
                  {row.slice(0, 5).map((cell, cellIndex) => (
                    <td key={`${rowIndex}-${cellIndex}`}>{cell || "-"}</td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <div className={styles.tablePreviewState}>{loading ? "读取中" : "无可预览内容"}</div>
      )}
    </div>
  );
}

function parseTableRows(content: string, artifact: Artifact): string[][] {
  const extension = artifactExtension(artifact).toLowerCase();
  const mimeType = normalizedMimeType(artifact);
  const delimiter = extension === "tsv" || mimeType === "text/tab-separated-values" ? "\t" : ",";
  return content
    .split(/\r?\n/)
    .filter((line) => line.trim() !== "")
    .slice(0, 7)
    .map((line) => splitDelimitedLine(line, delimiter).slice(0, 8));
}

function splitDelimitedLine(line: string, delimiter: string): string[] {
  const cells: string[] = [];
  let current = "";
  let quoted = false;
  for (let index = 0; index < line.length; index += 1) {
    const char = line[index];
    const next = line[index + 1];
    if (char === '"' && quoted && next === '"') {
      current += '"';
      index += 1;
      continue;
    }
    if (char === '"') {
      quoted = !quoted;
      continue;
    }
    if (char === delimiter && !quoted) {
      cells.push(current.trim());
      current = "";
      continue;
    }
    current += char;
  }
  cells.push(current.trim());
  return cells;
}

function previewKind(artifact: Artifact): PreviewKind {
  const mimeType = normalizedMimeType(artifact);
  if (mimeType.startsWith("image/")) return "image";
  if (mimeType === "application/pdf") return "pdf";
  if (mimeType.startsWith("audio/")) return "audio";
  if (mimeType.startsWith("video/")) return "video";
  if (isTableArtifact(artifact, mimeType)) return "table";
  return "generic";
}

function artifactKindLabel(artifact: Artifact, previewType: PreviewKind): string {
  if (previewType === "image") return "图片预览";
  if (previewType === "pdf") return "PDF 预览";
  if (previewType === "audio") return "音频预览";
  if (previewType === "video") return "视频预览";
  if (previewType === "table") return "表格文件";
  return normalizedMimeType(artifact) || "文件";
}

function normalizedMimeType(artifact: Artifact): string {
  return artifact.mime_type.toLowerCase().split(";")[0].trim();
}

function isTableArtifact(artifact: Artifact, mimeType: string): boolean {
  const extension = artifactExtension(artifact);
  return (
    ["csv", "tsv"].includes(extension.toLowerCase()) ||
    mimeType === "text/csv" ||
    mimeType === "text/tab-separated-values"
  );
}

function artifactExtension(artifact: Artifact): string {
  const name = artifact.name || artifact.path;
  const dotIndex = name.lastIndexOf(".");
  if (dotIndex < 0 || dotIndex === name.length - 1) return "";
  return name
    .slice(dotIndex + 1)
    .slice(0, 6)
    .toUpperCase();
}

function formatBytes(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "大小未知";
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`;
  return `${(value / 1024 / 1024).toFixed(1)} MB`;
}
