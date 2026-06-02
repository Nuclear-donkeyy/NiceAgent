import { useState } from "react";

import { Empty } from "../../components/Empty";
import { SectionTitle } from "../../components/SectionTitle";
import type { InvitationEmailSuppression } from "../../domain/invitation";
import styles from "./InvitationSuppressionPanel.module.scss";

interface InvitationSuppressionPanelProps {
  suppressions: InvitationEmailSuppression[];
  loading: boolean;
  error: string;
  onDelete: (suppressionID: string) => Promise<void>;
  onRefresh: () => void;
}

export function InvitationSuppressionPanel({
  suppressions,
  loading,
  error,
  onDelete,
  onRefresh,
}: InvitationSuppressionPanelProps) {
  const [pendingID, setPendingID] = useState("");
  const [actionError, setActionError] = useState("");

  async function deleteSuppression(suppressionID: string) {
    setPendingID(suppressionID);
    setActionError("");
    try {
      await onDelete(suppressionID);
    } catch (caught) {
      setActionError(errorMessage(caught));
    } finally {
      setPendingID("");
    }
  }

  return (
    <section className={styles.panel} aria-label="邀请邮件停发管理">
      <div className={styles.header}>
        <div>
          <SectionTitle text="邮件治理" />
          <p>退信、投诉或丢弃事件会自动停发同一组织内的邀请邮箱。</p>
        </div>
        <button className={styles.button} onClick={onRefresh} type="button" disabled={loading}>
          刷新
        </button>
      </div>

      {error && (
        <p className={styles.error} role="alert">
          停发邮箱加载失败：{error}
        </p>
      )}
      {actionError && (
        <p className={styles.error} role="alert">
          解除停发失败：{actionError}
        </p>
      )}

      {loading ? (
        <Empty text="正在加载停发邮箱" />
      ) : suppressions.length === 0 ? (
        <p className={styles.empty}>暂无停发邮箱</p>
      ) : (
        <div className={styles.list}>
          {suppressions.map((suppression) => (
            <article className={styles.item} key={suppression.id}>
              <div className={styles.itemHead}>
                <strong>{suppression.email}</strong>
                <div className={styles.actions}>
                  <button
                    className={styles.button}
                    disabled={pendingID === suppression.id}
                    onClick={() => void deleteSuppression(suppression.id)}
                    type="button"
                  >
                    {pendingID === suppression.id ? "解除中" : "解除"}
                  </button>
                </div>
              </div>
              <p>{suppression.reason || "由邮件服务商事件触发停发"}</p>
              <div className={styles.meta}>
                {(suppression.provider || suppression.provider_message_id) && (
                  <span>
                    {[suppression.provider, suppression.provider_message_id]
                      .filter(Boolean)
                      .join(" / ")}
                  </span>
                )}
                <span>更新时间 {formatDateTime(suppression.updated_at)}</span>
              </div>
            </article>
          ))}
        </div>
      )}
    </section>
  );
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function formatDateTime(value?: string): string {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat("zh-CN", {
    dateStyle: "short",
    timeStyle: "short",
  }).format(date);
}
