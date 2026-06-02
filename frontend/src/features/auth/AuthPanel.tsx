import { SectionTitle } from "../../components/SectionTitle";
import styles from "./AuthPanel.module.scss";

interface AuthPanelProps {
  loading: boolean;
  onLogin: () => void;
  onLogout: () => void;
  onRefreshSession: () => void;
}

export function AuthPanel({ loading, onLogin, onLogout, onRefreshSession }: AuthPanelProps) {
  return (
    <section className={styles.panel} aria-label="登录会话">
      <SectionTitle text="登录会话" />
      <p className={styles.description}>
        本地 demo 默认可用；生产启用 OIDC 后，可在这里进入浏览器登录链路。
      </p>
      <div className={styles.actions}>
        <button className={styles.primaryButton} disabled={loading} onClick={onLogin} type="button">
          OIDC 登录
        </button>
        <button disabled={loading} onClick={onRefreshSession} type="button">
          刷新会话
        </button>
        <button disabled={loading} onClick={onLogout} type="button">
          退出
        </button>
      </div>
    </section>
  );
}
